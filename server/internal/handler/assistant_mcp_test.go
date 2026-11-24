package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpCall sends one JSON-RPC message to the Assistant MCP endpoint as userID
// and returns the HTTP status and the decoded result.
func assistantMCPCall(t *testing.T, h http.Handler, userID, method, params string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	req.Header.Set(ConnectorClientHeader, "Claude")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Result map[string]any `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Result
}

func assistantMCPText(t *testing.T, result map[string]any) (string, bool) {
	t.Helper()
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func TestAssistantMCP(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_APP_URL", "https://app.example")
	h := testHandler.AssistantMCP("test")

	if code, _ := assistantMCPCall(t, h, "", "tools/list", "{}"); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", code)
	}

	_, listed := assistantMCPCall(t, h, testUserID, "tools/list", "{}")
	tools := map[string]map[string]any{}
	for _, raw := range listed["tools"].([]any) {
		tool := raw.(map[string]any)
		tools[tool["name"].(string)] = tool
	}
	for _, want := range []string{"list_workspaces", "create_issue", "create_agent", "update_agent", "create_project", "invite_member", "delete_issue", "create_automation", "check_confirmation"} {
		if tools[want] == nil {
			t.Errorf("missing tool %s", want)
		}
	}
	for _, chatOnly := range []string{"create_artifact", "update_artifact", "propose_plan"} {
		if tools[chatOnly] != nil {
			t.Errorf("chat-only tool %s exposed", chatOnly)
		}
	}
	if ann := tools["delete_issue"]["annotations"].(map[string]any); ann["destructiveHint"] != true || ann["readOnlyHint"] == true {
		t.Errorf("delete_issue annotations: %v", ann)
	}
	if ann := tools["list_workspaces"]["annotations"].(map[string]any); ann["readOnlyHint"] != true {
		t.Errorf("list_workspaces must be read-only: %v", ann)
	}

	_, res := assistantMCPCall(t, h, testUserID, "tools/call", `{"name":"list_workspaces","arguments":{}}`)
	if text, isErr := assistantMCPText(t, res); isErr || !strings.Contains(text, testWorkspaceID) {
		t.Fatalf("list_workspaces: %s", text)
	}

	// A delete is parked for a human click in Agora, never executed here.
	// The Assistant may only delete what it created, so create it through MCP.
	_, res = assistantMCPCall(t, h, testUserID, "tools/call", `{"name":"create_issue","arguments":{"workspace_id":"`+testWorkspaceID+`","title":"MCP delete probe"}}`)
	created, isErr := assistantMCPText(t, res)
	var made struct {
		Issue struct {
			Identifier string `json:"identifier"`
		} `json:"issue"`
	}
	_ = json.Unmarshal([]byte(created), &made)
	ref := made.Issue.Identifier
	if isErr || ref == "" {
		t.Fatalf("create_issue through MCP: %s", created)
	}
	var issueID string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM issue WHERE workspace_id = $1 AND title = 'MCP delete probe' ORDER BY created_at DESC LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&issueID); err != nil {
		t.Fatalf("created issue not found: %v", err)
	}
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	_, res = assistantMCPCall(t, h, testUserID, "tools/call", `{"name":"delete_issue","arguments":{"workspace_id":"`+testWorkspaceID+`","ref":"`+ref+`"}}`)
	text, isErr := assistantMCPText(t, res)
	_ = isErr
	var parked struct {
		Status     string `json:"status"`
		ConfirmURL string `json:"confirm_url"`
		Operation  struct {
			ID string `json:"id"`
		} `json:"operation"`
	}
	if isErr || json.Unmarshal([]byte(text), &parked) != nil || parked.Status != "needs_confirmation" || !strings.HasPrefix(parked.ConfirmURL, "https://app.example/oauth/confirm?op=") {
		t.Fatalf("delete must wait for confirmation: %s", text)
	}
	if _, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issueID)); err != nil {
		t.Fatalf("issue deleted without confirmation: %v", err)
	}
	_, res = assistantMCPCall(t, h, testUserID, "tools/call", `{"name":"check_confirmation","arguments":{"operation_id":"`+parked.Operation.ID+`"}}`)
	if text, isErr := assistantMCPText(t, res); isErr || !strings.Contains(text, `"status":"pending"`) {
		t.Fatalf("check_confirmation: %s", text)
	}
	stranger := "00000000-0000-4000-8000-0000000000aa"
	_, res = assistantMCPCall(t, h, stranger, "tools/call", `{"name":"check_confirmation","arguments":{"operation_id":"`+parked.Operation.ID+`"}}`)
	if _, isErr := assistantMCPText(t, res); !isErr {
		t.Fatal("another member could read this operation")
	}
	var title string
	if err := testPool.QueryRow(context.Background(), `SELECT s.title FROM assistant_pending_operation o JOIN assistant_session s ON s.id = o.session_id WHERE o.id = $1`, parseUUID(parked.Operation.ID)).Scan(&title); err != nil || title != "Claude connector" {
		t.Fatalf("operation should live in the Claude connector conversation: %q %v", title, err)
	}
}
