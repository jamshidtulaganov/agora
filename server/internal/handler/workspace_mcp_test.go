package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createMcpTestWorkspace inserts a throwaway workspace with testUserID as a
// member of the given role, returning the workspace id. Cleanup drops the
// workspace (member rows cascade).
func createMcpTestWorkspace(t *testing.T, ctx context.Context, slug, role string) string {
	t.Helper()

	_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)

	var wsID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description)
VALUES ($1, $2, 'workspace default mcp config test')
RETURNING id
`, "MCP Test "+slug, slug).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1, $2, $3)
`, wsID, testUserID, role); err != nil {
		t.Fatalf("create %s member: %v", role, err)
	}

	return wsID
}

func putDefaultMcpConfig(t *testing.T, wsID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/"+wsID+"/default-mcp-config", body)
	req = withURLParam(req, "id", wsID)
	testHandler.UpdateWorkspaceDefaultMcpConfig(w, req)
	return w
}

func TestWorkspaceDefaultMcpConfig_UpdateGetAndAudit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-default-mcp", "owner")

	cfg := map[string]any{
		"mcp_config": map[string]any{
			"mcpServers": map[string]any{
				"zoho-crm": map[string]any{"type": "http", "url": "https://mcp.example/zoho"},
			},
		},
	}
	if w := putDefaultMcpConfig(t, wsID, cfg); w.Code != http.StatusOK {
		t.Fatalf("PUT default-mcp-config: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// GET round-trips the stored config.
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+wsID+"/default-mcp-config", nil)
	req = withURLParam(req, "id", wsID)
	testHandler.GetWorkspaceDefaultMcpConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET default-mcp-config: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got WorkspaceDefaultMcpConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(string(got.McpConfig), "zoho-crm") {
		t.Fatalf("expected stored config to contain zoho-crm, got: %s", got.McpConfig)
	}

	// The update wrote an audit row naming the server, never its definition.
	var details string
	if err := testPool.QueryRow(ctx, `
SELECT details::text FROM activity_log
WHERE workspace_id = $1 AND action = 'workspace_default_mcp_updated'
ORDER BY created_at DESC LIMIT 1
`, wsID).Scan(&details); err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	if !strings.Contains(details, "zoho-crm") {
		t.Fatalf("audit details missing server name: %s", details)
	}
	if strings.Contains(details, "mcp.example") {
		t.Fatalf("audit details leaked server definition: %s", details)
	}

	// Clearing via null empties the column.
	if w := putDefaultMcpConfig(t, wsID, map[string]any{"mcp_config": nil}); w.Code != http.StatusOK {
		t.Fatalf("PUT clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var raw []byte
	if err := testPool.QueryRow(ctx, `SELECT default_mcp_config FROM workspace WHERE id = $1`, wsID).Scan(&raw); err != nil {
		t.Fatalf("load cleared config: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected cleared config, got: %s", raw)
	}
}

func TestWorkspaceDefaultMcpConfig_MemberForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-default-mcp-member", "member")

	body := map[string]any{"mcp_config": map[string]any{"mcpServers": map[string]any{"x": map[string]any{}}}}
	if w := putDefaultMcpConfig(t, wsID, body); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for plain member PUT, got %d: %s", w.Code, w.Body.String())
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+wsID+"/default-mcp-config", nil)
	req = withURLParam(req, "id", wsID)
	testHandler.GetWorkspaceDefaultMcpConfig(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for plain member GET, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceDefaultMcpConfig_AgentActorForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-default-mcp-agent", "owner")

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+wsID+"/default-mcp-config", nil)
	req = withURLParam(req, "id", wsID)
	// Simulate the task-token auth path: middleware sets X-Actor-Source and
	// forces X-Agent-ID from the token row. Even though the backing user is
	// a workspace owner, the agent actor must be rejected.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	testHandler.GetWorkspaceDefaultMcpConfig(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for agent actor, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceDefaultMcpConfig_RejectsMalformedShapes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-default-mcp-shape", "owner")

	cases := []struct {
		name string
		body any
	}{
		{"not an object", map[string]any{"mcp_config": []any{"x"}}},
		{"missing mcpServers", map[string]any{"mcp_config": map[string]any{"servers": map[string]any{}}}},
		{"mcpServers not object", map[string]any{"mcp_config": map[string]any{"mcpServers": "zoho"}}},
		{"server def not object", map[string]any{"mcp_config": map[string]any{"mcpServers": map[string]any{"zoho": "https://x"}}}},
	}
	for _, tc := range cases {
		if w := putDefaultMcpConfig(t, wsID, tc.body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", tc.name, w.Code, w.Body.String())
		}
	}
}

// TestWorkspaceDefaultMcpConfig_HiddenFromGenericWorkspaceAPI locks the
// isolation contract: the generic workspace resource never exposes
// default_mcp_config, and a whole-blob settings update cannot clobber it.
func TestWorkspaceDefaultMcpConfig_HiddenFromGenericWorkspaceAPI(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-default-mcp-hidden", "owner")

	if _, err := testPool.Exec(ctx, `
UPDATE workspace SET default_mcp_config = '{"mcpServers":{"secret-server":{"type":"http","url":"https://mcp.example/token123"}}}'::jsonb
WHERE id = $1
`, wsID); err != nil {
		t.Fatalf("seed default_mcp_config: %v", err)
	}

	// GET /workspaces/{id} must not leak it.
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces/"+wsID, nil)
	req = withURLParam(req, "id", wsID)
	testHandler.GetWorkspace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetWorkspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-server") || strings.Contains(w.Body.String(), "default_mcp_config") {
		t.Fatalf("generic workspace response leaked default_mcp_config: %s", w.Body.String())
	}

	// A whole-settings PUT must leave the config untouched.
	w2 := httptest.NewRecorder()
	req2 := newRequest("PUT", "/api/workspaces/"+wsID, map[string]any{"settings": map[string]any{"theme": "dark"}})
	req2 = withURLParam(req2, "id", wsID)
	testHandler.UpdateWorkspace(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("UpdateWorkspace: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var raw []byte
	if err := testPool.QueryRow(ctx, `SELECT default_mcp_config FROM workspace WHERE id = $1`, wsID).Scan(&raw); err != nil {
		t.Fatalf("load config after settings update: %v", err)
	}
	if !strings.Contains(string(raw), "secret-server") {
		t.Fatalf("settings update clobbered default_mcp_config: %s", raw)
	}
}

// TestClaimTaskByRuntime_MergesWorkspaceDefaultMcpServers verifies the
// claim-time merge: workspace defaults appear in the claimed task's agent
// mcp_config, and an agent-level entry with the same name wins.
func TestClaimTaskByRuntime_MergesWorkspaceDefaultMcpServers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, `
UPDATE workspace SET default_mcp_config = '{"mcpServers":{"zoho-crm":{"type":"http","url":"https://mcp.example/default-zoho"},"zapier":{"type":"http","url":"https://mcp.example/zapier"}}}'::jsonb
WHERE id = $1
`, testWorkspaceID); err != nil {
		t.Fatalf("seed workspace default mcp config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE workspace SET default_mcp_config = NULL WHERE id = $1`, testWorkspaceID)
	})

	runtimeID := createClaimReclaimRuntime(t, ctx, "Default MCP merge runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Default MCP merge agent")
	if _, err := testPool.Exec(ctx, `
UPDATE agent SET mcp_config = '{"mcpServers":{"zoho-crm":{"type":"http","url":"https://mcp.example/agent-zoho"}}}'::jsonb
WHERE id = $1
`, agentID); err != nil {
		t.Fatalf("seed agent mcp config: %v", err)
	}
	createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "default-mcp-merge")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			Agent *struct {
				McpConfig json.RawMessage `json:"mcp_config"`
			} `json:"agent"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil || resp.Task.Agent == nil {
		t.Fatalf("expected claimed task with agent data, got: %s", w.Body.String())
	}

	servers := mcpServersOf(resp.Task.Agent.McpConfig)
	if _, ok := servers["zapier"]; !ok {
		t.Fatalf("workspace default server missing from claimed config: %s", resp.Task.Agent.McpConfig)
	}
	zoho, ok := servers["zoho-crm"].(map[string]any)
	if !ok {
		t.Fatalf("zoho-crm entry missing: %s", resp.Task.Agent.McpConfig)
	}
	if zoho["url"] != "https://mcp.example/agent-zoho" {
		t.Fatalf("agent-level entry must win on name collision, got url=%v", zoho["url"])
	}
}

// TestClaimTaskByRuntime_NoDefaultLeavesAgentConfigUntouched pins the
// no-default path: an agent with a nil mcp_config and no workspace default
// claims with a nil config (no spurious empty object).
func TestClaimTaskByRuntime_NoDefaultLeavesAgentConfigUntouched(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "No default MCP runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "No default MCP agent")
	createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "no-default-mcp")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Task *struct {
			Agent *struct {
				McpConfig json.RawMessage `json:"mcp_config"`
			} `json:"agent"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if resp.Task == nil || resp.Task.Agent == nil {
		t.Fatalf("expected claimed task with agent data, got: %s", w.Body.String())
	}
	if trimmed := strings.TrimSpace(string(resp.Task.Agent.McpConfig)); trimmed != "" && trimmed != "null" {
		t.Fatalf("expected nil mcp_config without workspace default, got: %s", trimmed)
	}
}
