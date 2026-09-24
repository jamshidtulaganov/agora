package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mcpCall posts one JSON-RPC message to the proxy with task-token actor
// headers (simulating what the auth middleware sets for a mat_ token).
func mcpCall(t *testing.T, wsID, userID, taskID string, msg map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/mcp/zoho", msg)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Workspace-ID", wsID)
	req.Header.Set("X-User-ID", userID)
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	testHandler.ZohoMcpProxy(w, req)
	return w
}

func rpc(id int, method string, params map[string]any) map[string]any {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	return m
}

// toolText extracts the first text content of a tools/call result.
func toolText(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, body)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("empty tool content: %s", body)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func TestZohoMcpProxy_ProtocolHandshake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-proto", "owner")

	w := mcpCall(t, wsID, testUserID, "", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18"}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize: %d %s", w.Code, w.Body.String())
	}
	nw := httptest.NewRecorder()
	nreq := newRequest("POST", "/mcp/zoho", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	nreq.Header.Set("X-Actor-Source", "task_token")
	testHandler.ZohoMcpProxy(nw, nreq)
	if nw.Code != http.StatusAccepted {
		t.Fatalf("notification: expected 202, got %d", nw.Code)
	}
	// tools/list carries the CRM and Desk read surface and nothing that writes.
	lw := mcpCall(t, wsID, testUserID, "", rpc(2, "tools/list", nil))
	for _, tool := range []string{"zoho_whoami", "zoho_crm_modules", "zoho_crm_fields", "zoho_crm_search", "zoho_crm_get_record",
		"zoho_desk_departments", "zoho_desk_list_tickets", "zoho_desk_get_ticket", "zoho_desk_ticket_conversation"} {
		if !strings.Contains(lw.Body.String(), `"`+tool+`"`) {
			t.Fatalf("tools/list missing %s: %s", tool, lw.Body.String())
		}
	}
	for _, word := range []string{"create", "update", "delete"} {
		if strings.Contains(lw.Body.String(), `"zoho_crm_`+word) || strings.Contains(lw.Body.String(), `"zoho_desk_`+word) {
			t.Fatalf("tools/list offers a %s tool: %s", word, lw.Body.String())
		}
	}
	gw := httptest.NewRecorder()
	testHandler.ZohoMcpProxy(gw, httptest.NewRequest("GET", "/mcp/zoho", nil))
	if gw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: expected 405, got %d", gw.Code)
	}
}

func TestZohoMcpProxy_RequiresTaskToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	req := newRequest("POST", "/mcp/zoho", rpc(1, "tools/list", nil))
	req.Header.Del("X-Actor-Source")
	testHandler.ZohoMcpProxy(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without task token, got %d: %s", w.Code, w.Body.String())
	}
}

// seedZohoTask inserts a queued agent task with the given initiator (empty
// for none) and returns its id — the only input the identity rule reads here.
func seedZohoTask(t *testing.T, initiatorID string) string {
	t.Helper()
	agentID := createHandlerTestAgent(t, fmt.Sprintf("zoho-identity-agent-%d", time.Now().UnixNano()), nil)
	var initiator any
	if initiatorID != "" {
		initiator = initiatorID
	}
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, initiator_user_id)
		VALUES ($1, $2, 'queued', 2, $3)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), initiator).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

// callZohoTool runs one tools/call as the task and decodes the JSON payload.
func callZohoTool(t *testing.T, wsID, taskID, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	w := mcpCall(t, wsID, testUserID, taskID, rpc(9, "tools/call", map[string]any{"name": name, "arguments": args}))
	text, isErr := toolText(t, w.Body.Bytes())
	var payload map[string]any
	if !isErr {
		_ = json.Unmarshal([]byte(text), &payload)
	}
	return payload, text, isErr
}

func countOf(payload map[string]any, key string) int {
	list, _ := payload[key].([]any)
	return len(list)
}

// The core promise: two people, same agent tools, each sees only what their
// own Zoho role allows.
func TestZohoMcpProxy_EachPersonSeesOnlyTheirOwnZoho(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-perm", "owner")
	manager := newZohoTestUser(t, "manager")
	agent := newZohoTestUser(t, "agent")
	connectZohoAs(t, fake, manager, "dilnoza")
	connectZohoAs(t, fake, agent, "shohruh")
	managerTask := seedZohoTask(t, manager)
	agentTask := seedZohoTask(t, agent)

	who, _, isErr := callZohoTool(t, wsID, agentTask, "zoho_whoami", nil)
	if isErr || who["crm_role"] != "Collections Agent" || who["acting_for"] != "the person who started this conversation" {
		t.Fatalf("whoami: %v", who)
	}

	deals := "SELECT Deal_Name, Stage, Amount FROM Deals LIMIT 50"
	if p, text, isErr := callZohoTool(t, wsID, managerTask, "zoho_crm_search", map[string]any{"coql": deals}); isErr || countOf(p, "rows") != 5 {
		t.Fatalf("manager deals: %s", text)
	}
	p, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_crm_search", map[string]any{"coql": deals})
	if isErr || countOf(p, "rows") != 2 {
		t.Fatalf("agent deals: %s", text)
	}
	if strings.Contains(text, "Write-off review") {
		t.Fatalf("agent saw a deal he doesn't own: %s", text)
	}
	if _, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_crm_get_record", map[string]any{"module": "Deals", "id": "3003"}); !isErr || !strings.Contains(text, "doesn't have access") {
		t.Fatalf("agent reading the manager's deal: isErr=%v %s", isErr, text)
	}

	if p, text, isErr := callZohoTool(t, wsID, managerTask, "zoho_desk_list_tickets", nil); isErr || countOf(p, "tickets") != 5 {
		t.Fatalf("manager tickets: %s", text)
	}
	if p, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_desk_list_tickets", nil); isErr || countOf(p, "tickets") != 3 {
		t.Fatalf("agent tickets (Collections only): %s", text)
	}
	if p, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_desk_list_tickets", map[string]any{"mine": true}); isErr || countOf(p, "tickets") != 2 {
		t.Fatalf("agent's own tickets: %s", text)
	}
	// A Billing ticket: invisible by number, refused by id.
	if p, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_desk_get_ticket", map[string]any{"ticket_number": "4825"}); isErr || p["ticket"] != nil {
		t.Fatalf("agent finding a Billing ticket by number: %s", text)
	}
	if _, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_desk_get_ticket", map[string]any{"ticket_id": "8105"}); !isErr || !strings.Contains(text, "doesn't have access") {
		t.Fatalf("agent reading a Billing ticket by id: isErr=%v %s", isErr, text)
	}
	if p, text, isErr := callZohoTool(t, wsID, agentTask, "zoho_desk_get_ticket", map[string]any{"ticket_number": "#4821"}); isErr {
		t.Fatalf("agent's own ticket: %s", text)
	} else if ticket, _ := p["ticket"].(map[string]any); ticket["subject"] != "Dispute on fuel card charge" {
		t.Fatalf("ticket 4821: %v", p)
	}

	// Every read is audited with a count — never the contents.
	var calls, rows int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*), coalesce(sum(record_count), 0) FROM zoho_call_log WHERE user_id = $1 AND source = 'agent' AND tool = 'zoho_crm_search'`, agent,
	).Scan(&calls, &rows); err != nil || calls != 1 || rows != 2 {
		t.Fatalf("audit log: calls=%d rows=%d err=%v", calls, rows, err)
	}

	// Every API call reached Zoho with the right person's token.
	for _, c := range fake.Calls {
		if strings.Contains(c, "/crm/v8/coql") && !strings.HasSuffix(c, " dilnoza") && !strings.HasSuffix(c, " shohruh") {
			t.Fatalf("unexpected caller: %s", c)
		}
	}
}

func TestZohoMcpProxy_ReadOnlyAndCOQLGuard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-ro", "owner")
	person := newZohoTestUser(t, "readonly")
	connectZohoAs(t, fake, person, "dilnoza")
	taskID := seedZohoTask(t, person)

	for _, q := range []string{"DELETE FROM Deals", "SELECT id FROM Deals; SELECT id FROM Leads", "  "} {
		if _, text, isErr := callZohoTool(t, wsID, taskID, "zoho_crm_search", map[string]any{"coql": q}); !isErr {
			t.Fatalf("coql %q was accepted: %s", q, text)
		}
	}
	w := mcpCall(t, wsID, testUserID, taskID, rpc(10, "tools/call", map[string]any{
		"name": "zoho_crm_create_record", "arguments": map[string]any{"module": "Deals", "data": map[string]any{"Deal_Name": "x"}},
	}))
	if !strings.Contains(w.Body.String(), "unknown tool") {
		t.Fatalf("create_record: %s", w.Body.String())
	}
	for _, c := range fake.Calls {
		if strings.HasPrefix(c, "PUT ") || strings.HasPrefix(c, "DELETE ") || (strings.HasPrefix(c, "POST ") && !strings.Contains(c, "/crm/v8/coql")) {
			t.Fatalf("a write reached Zoho: %s", c)
		}
	}
}

// The runtime owner's own Zoho must not stand in for a task that can't be
// attributed to a person.
func TestZohoMcpProxy_NoFallbackToRuntimeOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-owner", "owner")
	connectZohoAs(t, fake, testUserID, "dilnoza") // the runtime owner has Zoho
	taskID := seedZohoTask(t, "")                 // but nobody can be named for the task

	_, text, isErr := callZohoTool(t, wsID, taskID, "zoho_crm_modules", nil)
	if !isErr || !strings.Contains(text, "none could be identified") {
		t.Fatalf("expected no-access error, got isErr=%v %s", isErr, text)
	}
}

// Neither the workspace's org-level connection nor anyone else's account
// stands in for a person who hasn't connected Zoho.
func TestZohoMcpProxy_PersonWithoutZohoGetsNothing(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-none", "owner")
	connectZohoAs(t, fake, testUserID, "dilnoza")
	person := newZohoTestUser(t, "unconnected")
	taskID := seedZohoTask(t, person)

	_, text, isErr := callZohoTool(t, wsID, taskID, "zoho_whoami", nil)
	if !isErr || !strings.Contains(text, "hasn't connected their Zoho account") {
		t.Fatalf("expected not-connected error, got isErr=%v %s", isErr, text)
	}
}

func TestZohoMcpProxy_RevokedGrantAsksToReconnect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	wsID := createMcpTestWorkspace(t, context.Background(), "handler-tests-zoho-mcp-revoked", "owner")
	person := newZohoTestUser(t, "revoked")
	connectZohoAs(t, fake, person, "shohruh")
	taskID := seedZohoTask(t, person)

	fake.Revoke("rt-shohruh")
	zohoClientCache.Delete(person) // force a fresh token mint
	_, text, isErr := callZohoTool(t, wsID, taskID, "zoho_crm_modules", nil)
	if !isErr || !strings.Contains(text, "reconnect") {
		t.Fatalf("expected reconnect error, got isErr=%v %s", isErr, text)
	}
	if got := getMyZoho(t, person); got.Status != "reconnect" {
		t.Fatalf("account status = %q", got.Status)
	}
}

func TestInjectZohoMcpProxy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	fake := startFakeZoho(t)
	person := newZohoTestUser(t, "inject")
	taskID := parseUUID(seedZohoTask(t, person))

	// The task's person hasn't connected Zoho: no entry.
	if got := testHandler.injectZohoMcpProxy(ctx, taskID, nil, "mat_tok"); got != nil {
		t.Fatalf("expected no-op without a connected person, got %s", got)
	}

	connectZohoAs(t, fake, person, "shohruh")
	got := testHandler.injectZohoMcpProxy(ctx, taskID, nil, "mat_tok")
	entry, ok := mcpServersOf(got)["zoho"].(map[string]any)
	if !ok {
		t.Fatalf("zoho entry missing: %s", got)
	}
	if entry["url"] != "https://api.example.test/mcp/zoho" {
		t.Fatalf("url = %v", entry["url"])
	}
	if headers, _ := entry["headers"].(map[string]any); headers["Authorization"] != "Bearer mat_tok" {
		t.Fatalf("auth header = %v", headers["Authorization"])
	}

	// An operator-defined zoho server wins untouched.
	agentCfg := json.RawMessage(`{"mcpServers":{"zoho":{"type":"http","url":"https://custom"}}}`)
	if kept, _ := mcpServersOf(testHandler.injectZohoMcpProxy(ctx, taskID, agentCfg, "mat_tok"))["zoho"].(map[string]any); kept["url"] != "https://custom" {
		t.Fatalf("operator entry clobbered: %v", kept)
	}
	// Without a token: no-op.
	if got := testHandler.injectZohoMcpProxy(ctx, taskID, nil, ""); got != nil {
		t.Fatalf("expected no-op without token, got %s", got)
	}
}
