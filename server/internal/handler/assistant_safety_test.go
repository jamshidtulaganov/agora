package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// Legacy confirmation tests need a deletable fixture. Keep ordinary issue
// fixtures unmarked, so ownership alone never masquerades as provenance.
func newAssistantCreatedTestIssue(t *testing.T, ws, title, creator, assignee string) string {
	t.Helper()
	id := newAssistantTestIssue(t, ws, title, creator, assignee)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_issue_creation(issue_id,user_id) VALUES($1,$2)`, id, creator); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAssistantDeletionSafety(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-safety@agora.dev")
	other := newAssistantTestUser(t, "assistant-safety-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-safety", "SAFE")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, ws, other, "member")
	session := newAssistantTestSession(t, user)
	ordinary := newAssistantTestIssue(t, ws, "human task", user, user)
	// A mutable attribution badge (including one stamped by an assistant edit)
	// must not grant permission to delete a human-created issue.
	testHandler.stampAssistantAttribution(context.Background(), parseUUID(ordinary), parseUUID(ws))
	foreign := newAssistantCreatedTestIssue(t, ws, "other person's assistant task", other, other)
	parent := newAssistantCreatedTestIssue(t, ws, "parent", user, user)
	child := newAssistantTestIssue(t, ws, "archived child", other, other)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET parent_issue_id=$1, archived_at=now() WHERE id=$2`, parent, child); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ordinary, foreign, parent, uuid.NewString()} {
		_, err := executeAssistantSessionTool(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"`+id+`"}`)
		if err == nil {
			t.Fatalf("protected issue %s admitted deletion", id)
		}
	}
	var pending int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM assistant_pending_operation WHERE session_id=$1`, session).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("unsafe confirmations: %d, %v", pending, err)
	}
	assertAssistantRowCount(t, "issue", ordinary, 1)
	assertAssistantRowCount(t, "issue", parent, 1)
	assertAssistantRowCount(t, "issue", child, 1)
}

func TestAssistantDeletionRechecksChildrenOnConfirmation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-child-race@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-child-race", "ACR")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	parent := newAssistantCreatedTestIssue(t, ws, "parent", user, user)
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"`+parent+`"}`)
	child := newAssistantTestIssue(t, ws, "new child", user, user)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET parent_issue_id=$1 WHERE id=$2`, parent, child); err != nil {
		t.Fatal(err)
	}
	resp := postAssistantOperation(t, user, assistantOperationID(t, asked), "confirm")
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "subtasks") {
		t.Fatalf("confirm: %d %s", resp.Code, resp.Body.String())
	}
	assertAssistantRowCount(t, "issue", parent, 1)
	var parentID string
	if err := testPool.QueryRow(context.Background(), `SELECT parent_issue_id::text FROM issue WHERE id=$1`, child).Scan(&parentID); err != nil || parentID != parent {
		t.Fatalf("child detached: %s, %v", parentID, err)
	}
}

func TestAssistantCreationProvenanceAndDeletion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-provenance@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-provenance", "ACP")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	created, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateIssue, `{"workspace_id":"`+ws+`","title":"Created by Assistant","status":"backlog"}`)
	if err != nil || created["created"] != true {
		t.Fatalf("create: %v, %v", created, err)
	}
	var id string
	if err := testPool.QueryRow(context.Background(), `SELECT c.issue_id::text FROM assistant_issue_creation c JOIN issue i ON i.id=c.issue_id WHERE c.user_id=$1 AND i.workspace_id=$2`, user, ws).Scan(&id); err != nil {
		t.Fatal(err)
	}
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"`+id+`"}`)
	assertAssistantRowCount(t, "issue", id, 1)
	if result := assistantConfirm(t, user, assistantOperationID(t, asked)); result["deleted"] != true {
		t.Fatalf("delete: %v", result)
	}
	assertAssistantRowCount(t, "issue", id, 0)
}

func TestAssistantWorkspaceDeletionCannotCascadeProtectedTasks(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-workspace-safety@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-workspace-safety", "AWS")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	// A card can be offered only while no tasks remain.
	asked := assistantAsk(t, user, session, assistant.ToolDeleteWorkspace, `{"workspace_id":"`+ws+`"}`)
	id := newAssistantTestIssue(t, ws, "protected archived task", user, user)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET archived_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	resp := postAssistantOperation(t, user, assistantOperationID(t, asked), "confirm")
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "containing tasks") {
		t.Fatalf("workspace confirm: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolDeleteWorkspace, `{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("workspace containing tasks admitted a new confirmation")
	}
	// Exercise the final row-locked guard independently of the adapter.
	ctx := withAssistantExecution(context.Background(), &assistantExecution{tool: assistant.ToolDeleteWorkspace, authorized: true, destructive: true})
	status, body := testHandler.assistantInvokeAs(ctx, testHandler.DeleteWorkspace, http.MethodDelete, "/x", user, ws, "", map[string]string{"id": ws}, "owner")
	if status != http.StatusForbidden || !strings.Contains(string(body), "containing tasks") {
		t.Fatalf("workspace final guard: %d %s", status, body)
	}
	assertAssistantRowCount(t, "issue", id, 1)
	assertAssistantRowCount(t, "workspace", ws, 1)
}

func TestAssistantIssueDeletionFinalHandlerGuard(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-delete-final@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-delete-final", "ADF")
	addAssistantTestMember(t, ws, user, "owner")
	ordinary := newAssistantTestIssue(t, ws, "ordinary task", user, user)
	parent := newAssistantCreatedTestIssue(t, ws, "Assistant parent", user, user)
	child := newAssistantTestIssue(t, ws, "child", user, user)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET parent_issue_id=$1 WHERE id=$2`, parent, child); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ordinary, parent} {
		ctx := withAssistantExecution(context.Background(), &assistantExecution{tool: assistant.ToolDeleteIssue, authorized: true, destructive: true})
		status, body := testHandler.assistantInvoke(ctx, testHandler.DeleteIssue, http.MethodDelete, "/x", user, ws, "", map[string]string{"id": id})
		if status != http.StatusForbidden {
			t.Fatalf("issue final guard: %d %s", status, body)
		}
		assertAssistantRowCount(t, "issue", id, 1)
	}
	assertAssistantRowCount(t, "issue", child, 1)
}

func TestAssistantSprintDeletionCannotDeleteProtectedTasks(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-sprint-safety@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-sprint-safety", "ASS")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	project := newAssistantTestProject(t, ws, "Safety project")
	sprint := newAssistantTestSprint(t, ws, project, "Empty until confirmed")
	asked := assistantAsk(t, user, session, assistant.ToolDeleteSprint, `{"workspace_id":"`+ws+`","sprint":"`+sprint+`"}`)
	id := newAssistantTestIssue(t, ws, "protected archived task", user, user)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO issue_to_sprint(issue_id,sprint_id) VALUES($1,$2)`, id, sprint); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET archived_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	resp := postAssistantOperation(t, user, assistantOperationID(t, asked), "confirm")
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "containing tasks") {
		t.Fatalf("sprint confirm: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolDeleteSprint, `{"workspace_id":"`+ws+`","sprint":"`+sprint+`"}`); err == nil {
		t.Fatal("sprint containing tasks admitted a new confirmation")
	}
	ctx := withAssistantExecution(context.Background(), &assistantExecution{tool: assistant.ToolDeleteSprint, authorized: true, destructive: true})
	status, body := testHandler.assistantInvoke(ctx, testHandler.DeleteSprint, http.MethodDelete, "/x", user, ws, "", map[string]string{"id": sprint})
	if status != http.StatusForbidden || !strings.Contains(string(body), "containing tasks") {
		t.Fatalf("sprint final guard: %d %s", status, body)
	}
	assertAssistantRowCount(t, "issue", id, 1)
	assertAssistantRowCount(t, "sprint", sprint, 1)
}

func TestAssistantReviewExecutorBlocksAllWorkspaceWrites(t *testing.T) {
	ctx := assistant.WithRunMode(context.Background(), assistant.RunModeReview)
	for name := range assistant.MutatingTools {
		if name == assistant.ToolCreateArtifact || name == assistant.ToolUpdateArtifact {
			continue
		}
		_, err := testHandler.Execute(ctx, uuid.NewString(), uuid.NewString(), name, json.RawMessage(`{}`))
		if err != assistant.ErrReviewReadOnly {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range []string{assistant.ToolDryRunImport, "unknown_tool"} {
		if _, err := testHandler.Execute(ctx, uuid.NewString(), "", name, nil); err != assistant.ErrReviewReadOnly {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestAssistantReviewCannotUpdatePublishedArtifacts(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-review-artifact@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-review-artifact", "ARA")
	addAssistantTestMember(t, ws, user, "owner")
	project := newAssistantTestProject(t, ws, "Review project")
	session := newAssistantTestSession(t, user)
	id := createTestArtifact(t, user, session, "Private review", "markdown", "Original")
	ctx := assistant.WithRunMode(context.Background(), assistant.RunModeReview)
	if _, err := testHandler.Execute(ctx, user, session, assistant.ToolUpdateArtifact, json.RawMessage(`{"artifact_id":"`+id+`","content":"Private recommendations"}`)); err != nil {
		t.Fatalf("private review update: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_artifact_pin(artifact_id,workspace_id,project_id,pinned_by) VALUES($1,$2,$3,$4)`, id, ws, project, user); err != nil {
		t.Fatal(err)
	}
	if _, err := testHandler.Execute(ctx, user, session, assistant.ToolUpdateArtifact, json.RawMessage(`{"artifact_id":"`+id+`","content":"Unwanted published change"}`)); err == nil || !strings.Contains(err.Error(), "published report") {
		t.Fatalf("published review update: %v", err)
	}
	var content string
	var version int
	if err := testPool.QueryRow(context.Background(), `SELECT content,version FROM assistant_artifact WHERE id=$1`, id).Scan(&content, &version); err != nil || content != "Private recommendations" || version != 2 {
		t.Fatalf("published report changed: content=%s version=%d error=%v", content, version, err)
	}
}

func TestAssistantReviewModeIsDurableAndCannotBeDowngraded(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-review-mode@agora.dev")
	session := newAssistantTestSession(t, user)
	_, remote := durableAssistantFixture(t, session)
	payload := map[string]any{"content": "/review inspect this feature", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": nil, "mode": "assist"}}
	resp := sendRecoveryRequest(t, user, session, payload)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", resp.Code, resp.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	run, err := remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil || run.Context.Mode != assistant.RunModeReview {
		t.Fatalf("policy lost: %+v, %v", run.Context, err)
	}
	payload["context"] = map[string]any{"workspace_id": nil, "mode": "review"}
	if changed := sendRecoveryRequest(t, user, session, payload); changed.Code != http.StatusConflict {
		t.Fatalf("changed retry: %d %s", changed.Code, changed.Body.String())
	}
	payload["context"] = map[string]any{"workspace_id": nil, "mode": "invalid"}
	if invalid := sendRecoveryRequest(t, user, session, payload); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestAssistantReviewEndpointEnforcesModeAndRejectsEditableReplay(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-review-endpoint@agora.dev")
	session := newAssistantTestSession(t, user)
	_, remote := durableAssistantFixture(t, session)
	body := `{"content":"inspect this feature","request_id":"` + uuid.NewString() + `","context":{"workspace_id":null,"mode":"assist"}}`
	resp := httptest.NewRecorder()
	testHandler.SendAssistantReviewMessage(resp, withURLParam(newAssistantRequest(http.MethodPost, "/x", user, body), "id", session))
	if resp.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", resp.Code, resp.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	run, err := remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil || run.Context.Mode != assistant.RunModeReview {
		t.Fatalf("endpoint policy lost: %+v, %v", run.Context, err)
	}
	// Identical JSON sent to the editable endpoint is a different policy,
	// not an idempotent retry. The immutable receipt cannot be downgraded.
	replay := httptest.NewRecorder()
	testHandler.SendAssistantMessage(replay, withURLParam(newAssistantRequest(http.MethodPost, "/x", user, body), "id", session))
	if replay.Code != http.StatusConflict {
		t.Fatalf("editable replay: %d %s", replay.Code, replay.Body.String())
	}
	storedSession, err := testHandler.Queries.GetAssistantSession(context.Background(), parseUUID(session))
	if err != nil {
		t.Fatal(err)
	}
	var payload SendAssistantMessageRequest
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	rawContext, _ := json.Marshal(payload.Context)
	_, err = remote.AcceptRun(context.Background(), storedSession, user, payload.Content, &payload.RequestID, string(rawContext), assistant.RunContext{Mode: assistant.RunModeAssist}, nil)
	if err != assistant.ErrRequestConflict {
		t.Fatalf("store-level editable replay: %v", err)
	}
}

type reviewPolicyChat struct {
	scriptedToolChat
	offered []llm.Tool
}

func (c *reviewPolicyChat) CompleteWithTools(ctx context.Context, model string, messages []llm.Message, tools []llm.Tool) (llm.Message, error) {
	c.mu.Lock()
	c.offered = append(c.offered, tools...)
	c.mu.Unlock()
	return c.scriptedToolChat.CompleteWithTools(ctx, model, messages, tools)
}

func TestAssistantReviewRunRejectsModelWritesButProducesReport(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-review-model@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-review-model", "ARM")
	addAssistantTestMember(t, ws, user, "owner")
	sessionID := newAssistantTestSession(t, user)
	id := newAssistantTestIssue(t, ws, "protected task", user, user)
	client := &reviewPolicyChat{scriptedToolChat: scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "create", Name: assistant.ToolCreateIssue, Arguments: `{"workspace_id":"` + ws + `","title":"unwanted task"}`},
			{ID: "delete", Name: assistant.ToolDeleteIssue, Arguments: `{"workspace_id":"` + ws + `","ref":"` + id + `"}`},
			{ID: "plan", Name: assistant.ToolProposePlan, Arguments: `{"items":[]}`},
			{ID: "report", Name: assistant.ToolCreateArtifact, Arguments: `{"title":"Review","kind":"markdown","content":"Recommendations only"}`},
		}},
		{Role: "assistant", Content: "Here are my recommendations."},
	}}}
	svc := assistant.NewService(testHandler.Queries, testHandler.Bus, func() (llm.ToolChat, string, error) { return client, "test-model", nil })
	svc.Store, svc.TxStarter, svc.Exec = testHandler.DB, testHandler.TxStarter, testHandler
	svc.RunExtras = func(context.Context, string) ([]llm.Tool, string) {
		return []llm.Tool{{Name: "unknown_extra_tool"}, {Name: assistant.ToolSearchKnowledge}}, ""
	}
	session, err := testHandler.Queries.GetAssistantSession(context.Background(), parseUUID(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := svc.AcceptRun(context.Background(), session, user, "create a task anyway", nil, "", assistant.RunContext{WorkspaceID: &ws, Mode: assistant.RunModeReview}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.CancelSession(sessionID) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, err := svc.GetRun(context.Background(), accepted.Run.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" {
			break
		}
		if run.Status != "running" || time.Now().After(deadline) {
			t.Fatalf("review run: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, tool := range client.offered {
		if !assistant.ReviewToolAllowed(tool.Name) {
			t.Fatalf("model was offered %s", tool.Name)
		}
	}
	var issues, pending, artifacts int
	if err := testPool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM issue WHERE workspace_id=$1), (SELECT count(*) FROM assistant_pending_operation WHERE session_id=$2), (SELECT count(*) FROM assistant_artifact WHERE session_id=$2)`, ws, sessionID).Scan(&issues, &pending, &artifacts); err != nil {
		t.Fatal(err)
	}
	if issues != 1 || pending != 0 || artifacts != 1 {
		t.Fatalf("review effects: issues=%d, pending=%d, artifacts=%d", issues, pending, artifacts)
	}
}

func TestAssistantReviewOriginBlocksPendingConfirmation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-review-card@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-review-card", "ARC")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	id := newAssistantCreatedTestIssue(t, ws, "protected by review", user, user)
	run := newAssistantTestRun(t, session, user)
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"`+id+`"}`)
	if _, err := testPool.Exec(context.Background(), `UPDATE assistant_run SET context_snapshot='{"mode":"review"}' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	op := assistantOperationID(t, asked)
	resp := postAssistantOperation(t, user, op, "confirm")
	if resp.Code != http.StatusForbidden {
		t.Fatalf("review confirmation: %d %s", resp.Code, resp.Body.String())
	}
	if assistantPendingOperationStatus(t, op) != "pending" {
		t.Fatal("review confirmation was consumed")
	}
	assertAssistantRowCount(t, "issue", id, 1)
}
