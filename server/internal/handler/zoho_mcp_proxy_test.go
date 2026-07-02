package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-mcp-proto", "owner")

	// initialize
	w := mcpCall(t, wsID, testUserID, "", rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18"}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize: %d %s", w.Code, w.Body.String())
	}
	// notification → 202
	nw := httptest.NewRecorder()
	nreq := newRequest("POST", "/mcp/zoho", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	nreq.Header.Set("X-Actor-Source", "task_token")
	testHandler.ZohoMcpProxy(nw, nreq)
	if nw.Code != http.StatusAccepted {
		t.Fatalf("notification: expected 202, got %d", nw.Code)
	}
	// tools/list carries the full surface
	lw := mcpCall(t, wsID, testUserID, "", rpc(2, "tools/list", nil))
	for _, tool := range []string{"zoho_whoami", "zoho_crm_modules", "zoho_crm_fields", "zoho_crm_search", "zoho_crm_get_record", "zoho_crm_create_record", "zoho_crm_update_record"} {
		if !strings.Contains(lw.Body.String(), tool) {
			t.Fatalf("tools/list missing %s: %s", tool, lw.Body.String())
		}
	}
	// GET → 405 (no server-initiated streams)
	gw := httptest.NewRecorder()
	greq := httptest.NewRequest("GET", "/mcp/zoho", nil)
	testHandler.ZohoMcpProxy(gw, greq)
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
	// No X-Actor-Source — a plain member/PAT caller.
	req.Header.Del("X-Actor-Source")
	testHandler.ZohoMcpProxy(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without task token, got %d: %s", w.Code, w.Body.String())
	}
}

func TestZohoMcpProxy_ToolCallActsAsBoundUser(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-mcp-act", "owner")
	seedZohoConnection(t, wsID)
	if w := putZohoBinding(t, wsID, "1000.grantcode.abc"); w.Code != http.StatusOK {
		t.Fatalf("seed binding: %d %s", w.Code, w.Body.String())
	}

	// whoami resolves through the runtime owner's binding.
	w := mcpCall(t, wsID, testUserID, "", rpc(3, "tools/call", map[string]any{
		"name": "zoho_whoami", "arguments": map[string]any{},
	}))
	text, isErr := toolText(t, w.Body.Bytes())
	if isErr || !strings.Contains(text, "person@octanefuel.com") || !strings.Contains(text, "runtime owner binding") {
		t.Fatalf("whoami: isErr=%v text=%s", isErr, text)
	}

	// modules ride the same identity.
	mw := mcpCall(t, wsID, testUserID, "", rpc(4, "tools/call", map[string]any{
		"name": "zoho_crm_modules", "arguments": map[string]any{},
	}))
	mtext, misErr := toolText(t, mw.Body.Bytes())
	if misErr || !strings.Contains(mtext, "CustomModule34") {
		t.Fatalf("modules: isErr=%v text=%s", misErr, mtext)
	}

	// Module name validation fails closed.
	bw := mcpCall(t, wsID, testUserID, "", rpc(5, "tools/call", map[string]any{
		"name": "zoho_crm_fields", "arguments": map[string]any{"module": "Bad;DROP"},
	}))
	if _, bisErr := toolText(t, bw.Body.Bytes()); !bisErr {
		t.Fatal("expected isError for invalid module name")
	}
}

func TestZohoMcpProxy_FallsBackToWorkspaceConnection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-mcp-fb", "owner")
	seedZohoConnection(t, wsID)
	// No user binding — org-level fallback.

	w := mcpCall(t, wsID, testUserID, "", rpc(6, "tools/call", map[string]any{
		"name": "zoho_whoami", "arguments": map[string]any{},
	}))
	text, isErr := toolText(t, w.Body.Bytes())
	if isErr || !strings.Contains(text, "workspace connection (org-level)") {
		t.Fatalf("fallback identity wrong: isErr=%v text=%s", isErr, text)
	}
}

func TestZohoMcpProxy_NoIdentityIsToolError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-mcp-none", "owner")
	// No connection, no binding.

	w := mcpCall(t, wsID, testUserID, "", rpc(7, "tools/call", map[string]any{
		"name": "zoho_crm_modules", "arguments": map[string]any{},
	}))
	text, isErr := toolText(t, w.Body.Bytes())
	if !isErr || !strings.Contains(text, "no Zoho identity") {
		t.Fatalf("expected identity tool-error, got isErr=%v text=%s", isErr, text)
	}
}

func TestInjectZohoMcpProxy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	stub := zohoBindingStub(t, false)
	configureZohoConnEnv(t, stub.URL)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-zoho-mcp-inject", "owner")
	wsUUID := parseUUID(wsID)

	origPublicURL := testHandler.cfg.PublicURL
	testHandler.cfg.PublicURL = "https://api.example.test"
	t.Cleanup(func() { testHandler.cfg.PublicURL = origPublicURL })

	// Without a connection: no-op.
	if got := testHandler.injectZohoMcpProxy(ctx, wsUUID, nil, "mat_tok"); got != nil {
		t.Fatalf("expected no-op without connection, got %s", got)
	}

	seedZohoConnection(t, wsID)

	// With a connection: entry provisioned with the task token, URL derived
	// from PublicURL.
	got := testHandler.injectZohoMcpProxy(ctx, wsUUID, nil, "mat_tok")
	servers := mcpServersOf(got)
	entry, ok := servers["zoho"].(map[string]any)
	if !ok {
		t.Fatalf("zoho entry missing: %s", got)
	}
	if entry["url"] != "https://api.example.test/mcp/zoho" {
		t.Fatalf("url = %v", entry["url"])
	}
	headers, _ := entry["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer mat_tok" {
		t.Fatalf("auth header = %v", headers["Authorization"])
	}

	// Operator-defined zoho server wins untouched.
	agentCfg := json.RawMessage(`{"mcpServers":{"zoho":{"type":"http","url":"https://custom"}}}`)
	kept := testHandler.injectZohoMcpProxy(ctx, wsUUID, agentCfg, "mat_tok")
	keptServers := mcpServersOf(kept)
	keptEntry, _ := keptServers["zoho"].(map[string]any)
	if keptEntry["url"] != "https://custom" {
		t.Fatalf("operator entry clobbered: %s", kept)
	}

	// Without a token: no-op.
	if got := testHandler.injectZohoMcpProxy(ctx, wsUUID, nil, ""); got != nil {
		t.Fatalf("expected no-op without token, got %s", got)
	}
}
