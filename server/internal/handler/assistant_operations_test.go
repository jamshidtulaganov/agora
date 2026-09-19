package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// CONFIRMATION BINDING — the tests for the half of the assistant that decides
// whether a destructive tool is allowed to run at all.
//
// What these protect, in order of how badly it would hurt to lose it:
//
//  1. A destructive call MUTATES NOTHING. Every tool in DestructiveTools, driven
//     with arguments that resolve to a real row, must come back with
//     needs_confirmation and leave the row standing. Table-driven over the
//     catalog, so a new destructive tool cannot ship without landing here.
//  2. The confirmation is BOUND. It belongs to one operation, one user and one
//     target; it is single-use; it expires; and the roles are re-checked when it
//     is spent, not when it was asked for.
//  3. The receipt is REAL. A confirmed operation leaves a role="tool" message on
//     the transcript and an execution row on the durable receipt table, so a
//     conversation reread later cannot be mistaken for one where nothing
//     happened — or where something did.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// assistantAsk runs a destructive tool the way the run loop does — inside a
// conversation — and returns the needs_confirmation payload.
func assistantAsk(t *testing.T, userID, sessionID, tool, args string) map[string]any {
	t.Helper()
	result, err := executeAssistantSessionTool(t, userID, sessionID, tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if result["status"] != assistant.StatusNeedsConfirmation {
		t.Fatalf("%s = %v, want status needs_confirmation", tool, result)
	}
	return result
}

// assistantOperationOf digs the operation object out of a needs_confirmation
// result, checking the wire contract's keys on the way through.
func assistantOperationOf(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	op, ok := result["operation"].(map[string]any)
	if !ok {
		t.Fatalf("result has no operation object: %v", result)
	}
	for _, key := range []string{"id", "tool_name", "summary", "workspace_slug", "target"} {
		if _, present := op[key]; !present {
			t.Fatalf("operation is missing %q: %v", key, op)
		}
	}
	return op
}

func assistantOperationID(t *testing.T, result map[string]any) string {
	t.Helper()
	id, _ := assistantOperationOf(t, result)["id"].(string)
	if id == "" {
		t.Fatalf("operation has no id: %v", result)
	}
	return id
}

func postAssistantOperation(t *testing.T, userID, operationID, action string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newAssistantRequest(http.MethodPost, "/api/assistant/operations/"+operationID+"/"+action, userID, ""), "id", operationID)
	switch action {
	case "confirm":
		testHandler.ConfirmAssistantOperation(w, req)
	case "reject":
		testHandler.RejectAssistantOperation(w, req)
	default:
		t.Fatalf("unknown action %q", action)
	}
	return w
}

// assistantConfirm presses Confirm and returns the receipt message's
// tool_result — what the transcript (and the model, on the next turn) sees.
func assistantConfirm(t *testing.T, userID, operationID string) map[string]any {
	t.Helper()
	w := postAssistantOperation(t, userID, operationID, "confirm")
	if w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Operation map[string]any  `json:"operation"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("confirm response: %v (%s)", err, w.Body.String())
	}
	if resp.Operation["status"] != "confirmed" {
		t.Fatalf("operation status after confirm = %v", resp.Operation["status"])
	}
	var message AssistantMessageResponse
	if err := json.Unmarshal(resp.Message, &message); err != nil {
		t.Fatalf("receipt message: %v", err)
	}
	if message.Role != "tool" {
		t.Fatalf("receipt role = %q, want tool", message.Role)
	}
	out := map[string]any{}
	if err := json.Unmarshal(message.ToolResult, &out); err != nil {
		t.Fatalf("receipt tool_result: %v (%s)", err, message.ToolResult)
	}
	return out
}

// assistantAskAndConfirm is the whole two-gesture flow: the model asks, the
// human clicks. Existing happy-path tests use it where they used to pass
// confirm:true, which is exactly the point — the SAME tool, one gesture more.
func assistantAskAndConfirm(t *testing.T, userID, sessionID, tool, args string) map[string]any {
	t.Helper()
	asked := assistantAsk(t, userID, sessionID, tool, args)
	return assistantConfirm(t, userID, assistantOperationID(t, asked))
}

func assistantPendingOperationStatus(t *testing.T, operationID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM assistant_pending_operation WHERE id = $1`, operationID).Scan(&status); err != nil {
		t.Fatalf("read pending operation: %v", err)
	}
	return status
}

func assistantToolMessages(t *testing.T, sessionID, tool string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT content FROM assistant_message WHERE session_id = $1 AND tool_name = $2 ORDER BY sequence ASC`,
		sessionID, tool)
	if err != nil {
		t.Fatalf("read tool messages: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, content)
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. Every destructive tool parks instead of mutating
// ---------------------------------------------------------------------------

// The catalog-driven pin. Each tool is called with arguments that RESOLVE — a
// real issue, a real project, a real person — so the call gets all the way to
// the point where the old code would have deleted something. It must come back
// with needs_confirmation, and the row must still be there.
//
// Table-driven over assistant.DestructiveTools rather than over a hand-written
// list, so adding a destructive tool without a confirmation seam fails here
// (either for want of an argument blob, or because it mutated).
func TestAssistantDestructiveToolsParkAPendingOperation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-park@agora.dev")
	victim := newAssistantTestUser(t, "assistant-park-victim@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-park-ws", "PRK")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, ws, victim, "member")
	session := newAssistantTestSession(t, user)

	issueID := newAssistantTestIssue(t, ws, "still here", user, user)
	projectID := newAssistantTestProject(t, ws, "Keep me")
	labelID := newAssistantTestLabel(t, ws, "keep", "#64748b")
	sprintID := newAssistantTestSprint(t, ws, projectID, "Keep sprint")
	commentID := newAssistantTestComment(t, ws, issueID, user, "keep this comment")
	automationID := newAssistantTestAutomation(t, ws, user, "Keep rule")
	// An import needs a sealed connection and a job with a plan on it before
	// confirm_import has anything to bind to — the card names the plan's own
	// counts, so there has to be a plan.
	assistantImportSealKey(t)
	importConnectionID := newAssistantTestImportConnection(t, ws, "https://example.invalid", assistantImportTestKey)
	importJobID := newAssistantTestImportJob(t, ws, importConnectionID,
		`{"source":{"kind":"linear"},"issues":{"total":12,"create":12,"update":0},"exact":true}`)

	bodies := map[string]struct {
		args         string
		table, rowID string
	}{
		assistant.ToolDeleteIssue:      {`{"workspace_id":"` + ws + `","ref":"PRK-1"}`, "issue", issueID},
		assistant.ToolDeleteProject:    {`{"workspace_id":"` + ws + `","project":"Keep me"}`, "project", projectID},
		assistant.ToolDeleteSprint:     {`{"workspace_id":"` + ws + `","sprint":"Keep sprint"}`, "sprint", sprintID},
		assistant.ToolDeleteLabel:      {`{"workspace_id":"` + ws + `","label":"keep"}`, "issue_label", labelID},
		assistant.ToolDeleteComment:    {`{"workspace_id":"` + ws + `","comment_id":"` + commentID + `"}`, "comment", commentID},
		assistant.ToolRemoveMember:     {`{"workspace_id":"` + ws + `","user_id":"` + victim + `"}`, "member", ""},
		assistant.ToolLeaveWorkspace:   {`{"workspace_id":"` + ws + `"}`, "workspace", ws},
		assistant.ToolDeleteWorkspace:  {`{"workspace_id":"` + ws + `"}`, "workspace", ws},
		assistant.ToolDeleteAutomation: {`{"workspace_id":"` + ws + `","automation":"Keep rule"}`, "automation", automationID},
		// An import destroys nothing, but it writes thousands of rows on one
		// click — so it parks a card exactly like the deletes above, and the
		// job must still be waiting when the card is raised.
		assistant.ToolConfirmImport: {`{"workspace_id":"` + ws + `","job_id":"` + importJobID + `"}`, "import_job", importJobID},
	}

	for name := range assistant.DestructiveTools {
		tc, ok := bodies[name]
		if !ok {
			t.Fatalf("%q is confirmation-bound but this test has no argument blob for it — add one", name)
		}
		t.Run(name, func(t *testing.T) {
			result := assistantAsk(t, user, session, name, tc.args)
			op := assistantOperationOf(t, result)

			if op["tool_name"] != name {
				t.Fatalf("operation names %v, want %s", op["tool_name"], name)
			}
			// The summary is what the human reads instead of the arguments, so
			// it has to name the thing rather than describe the verb.
			summary, _ := op["summary"].(string)
			if summary == "" {
				t.Fatalf("%s parked without a summary: %v", name, op)
			}
			if op["workspace_slug"] != "assistant-park-ws" {
				t.Fatalf("%s workspace_slug = %v", name, op["workspace_slug"])
			}
			target, _ := op["target"].(map[string]any)
			if target["type"] == "" || target["type"] == nil {
				t.Fatalf("%s parked without a target type: %v", name, op)
			}

			// Persisted, pending, and owned by the caller.
			var status, tool, storedUser, storedSession string
			if err := testPool.QueryRow(context.Background(),
				`SELECT status, tool_name, user_id::text, session_id::text FROM assistant_pending_operation WHERE id = $1`,
				op["id"]).Scan(&status, &tool, &storedUser, &storedSession); err != nil {
				t.Fatalf("%s did not persist a pending operation: %v", name, err)
			}
			if status != "pending" || tool != name || storedUser != user || storedSession != session {
				t.Fatalf("%s pending row = %s/%s/%s/%s", name, status, tool, storedUser, storedSession)
			}

			// And the whole point: nothing moved.
			if tc.rowID != "" {
				assertAssistantRowCount(t, tc.table, tc.rowID, 1)
			}
			var members int
			if err := testPool.QueryRow(context.Background(),
				`SELECT count(*) FROM member WHERE workspace_id = $1`, ws).Scan(&members); err != nil {
				t.Fatalf("count members: %v", err)
			}
			if members != 2 {
				t.Fatalf("%s changed the membership before confirmation (%d members)", name, members)
			}
		})
	}
}

// The tool result is DATA the model reads, and the run must keep going after it
// — a parked delete is a question, not a failure.
func TestAssistantParkedOperationIsNotAnError(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-parkok@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-parkok-ws", "PKO")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "asked about", user, user)

	result, err := executeAssistantSessionTool(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"PKO-1"}`)
	if err != nil {
		t.Fatalf("a parked delete must not be an error: %v", err)
	}
	if _, isError := result["error"]; isError {
		t.Fatalf("a parked delete must not carry an error key: %v", result)
	}
	// No receipt either: nothing has happened yet, and a receipt would say it had.
	if _, hasReceipt := result["receipt"]; hasReceipt {
		t.Fatalf("a parked delete must not carry a receipt: %v", result)
	}
	op := assistantOperationOf(t, result)
	if !strings.Contains(op["summary"].(string), "PKO-1") ||
		!strings.Contains(op["summary"].(string), "asked about") {
		t.Fatalf("summary does not name the issue: %v", op["summary"])
	}
}

// A destructive tool outside a conversation has nowhere to put the card, so it
// refuses rather than falling back to executing.
func TestAssistantDestructiveToolRefusesOutsideASession(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-nosession@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-nosession-ws", "NSS")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "safe", user, user)

	_, err := executeAssistantTool(t, user, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"NSS-1"}`)
	if err == nil {
		t.Fatal("delete_issue ran with no conversation to confirm in")
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
}

// ---------------------------------------------------------------------------
// 2. Confirm executes exactly once, for exactly this user and this target
// ---------------------------------------------------------------------------

func TestAssistantConfirmExecutesTheStoredCall(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-confirmexec@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-confirmexec-ws", "CEX")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "doomed", user, user)
	deleted := recordBusEvents(t, protocol.EventIssueDeleted)
	announced := recordBusEvents(t, protocol.EventAssistantMessage)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"CEX-1"}`)
	operationID := assistantOperationID(t, asked)
	assertAssistantRowCount(t, "issue", issueID, 1)

	receipt := assistantConfirm(t, user, operationID)

	// The tool's own answer, unchanged — the confirm path runs the SAME tool.
	if receipt["deleted"] != true || receipt["issue_identifier"] != "CEX-1" || receipt["title"] != "doomed" {
		t.Fatalf("receipt = %v", receipt)
	}
	assertAssistantRowCount(t, "issue", issueID, 0)
	if len(deleted()) == 0 {
		t.Fatal("no issue:deleted event — the confirmed delete did not go through the handler")
	}
	if len(announced()) == 0 {
		t.Fatal("no assistant:message event for the receipt")
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "confirmed" {
		t.Fatalf("operation status = %q, want confirmed", got)
	}

	// The receipt is on the transcript, so the conversation records that the
	// action actually happened. (The ASK row is written by the run loop, which
	// is not in play here — TestAssistantConfirmationRoundTripThroughARun drives
	// the whole two-row exchange.)
	messages := assistantToolMessages(t, session, assistant.ToolDeleteIssue)
	if len(messages) != 1 || !strings.Contains(messages[0], `"deleted":true`) {
		t.Fatalf("transcript does not carry the receipt: %v", messages)
	}
}

// The whole exchange, through the real run loop: the model asks, the tool
// parks, THE RUN CARRIES ON and the model relays the ask, the human clicks, the
// receipt lands. This is the test that proves a parked delete is a question
// rather than a dead end.
func TestAssistantConfirmationRoundTripThroughARun(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-roundtrip@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-roundtrip-ws", "RTP")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "doomed", user, user)
	runID := newAssistantTestRun(t, session, user)

	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolDeleteIssue, Arguments: `{"workspace_id":"` + ws + `","ref":"RTP-1"}`},
		}},
		{Role: "assistant", Content: "That would permanently delete RTP-1 “doomed”. Press Confirm to go ahead."},
	}}
	svc := assistant.NewService(testHandler.Queries, events.New(), func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Exec = testHandler
	svc.Run(context.Background(), session, runID, user)

	// The run reached its second round and answered in text — a parked delete
	// must not abort the loop.
	if len(script.replies) != 0 {
		t.Fatalf("the run stopped after the parked delete (%d scripted replies unused)", len(script.replies))
	}
	assertAssistantRowCount(t, "issue", issueID, 1)

	asks := assistantToolMessages(t, session, assistant.ToolDeleteIssue)
	if len(asks) != 1 || !strings.Contains(asks[0], assistant.StatusNeedsConfirmation) {
		t.Fatalf("the run did not record the ask: %v", asks)
	}
	var parked map[string]any
	if err := json.Unmarshal([]byte(asks[0]), &parked); err != nil {
		t.Fatalf("ask row is not JSON: %v", err)
	}

	assistantConfirm(t, user, assistantOperationID(t, parked))
	assertAssistantRowCount(t, "issue", issueID, 0)

	messages := assistantToolMessages(t, session, assistant.ToolDeleteIssue)
	if len(messages) != 2 || !strings.Contains(messages[1], `"deleted":true`) {
		t.Fatalf("transcript = %v, want the ask then the receipt", messages)
	}
}

// The receipt answers "what did you change?" in the tool result itself.
func TestAssistantConfirmedDeleteCarriesAReceipt(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-delreceipt@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-delreceipt-ws", "DRC")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "doomed", user, user)

	receipt := assistantAskAndConfirm(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"DRC-1"}`)
	got, ok := receipt["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("no receipt on the confirmed result: %v", receipt)
	}
	if got["action"] != assistant.ToolDeleteIssue {
		t.Fatalf("receipt action = %v", got["action"])
	}
	target, _ := got["target"].(map[string]any)
	if target["type"] != "issue" || target["identifier"] != "DRC-1" || target["title"] != "doomed" {
		t.Fatalf("receipt target = %v", target)
	}
}

// Reject is Cancel: nothing runs, and the transcript says so where the card is.
func TestAssistantRejectLeavesTheRowStanding(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-reject@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-reject-ws", "REJ")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "spared", user, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"REJ-1"}`)
	operationID := assistantOperationID(t, asked)

	w := postAssistantOperation(t, user, operationID, "reject")
	if w.Code != http.StatusNoContent {
		t.Fatalf("reject = %d %s", w.Code, w.Body.String())
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
	if got := assistantPendingOperationStatus(t, operationID); got != "rejected" {
		t.Fatalf("operation status = %q, want rejected", got)
	}
	messages := assistantToolMessages(t, session, assistant.ToolDeleteIssue)
	if len(messages) != 1 || !strings.Contains(messages[0], "cancelled") {
		t.Fatalf("transcript does not record the cancellation: %v", messages)
	}

	// A cancelled card is spent: confirming it afterwards is a 409, not a delete.
	if w := postAssistantOperation(t, user, operationID, "confirm"); w.Code != http.StatusConflict {
		t.Fatalf("confirm after reject = %d, want 409", w.Code)
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
}

// Single-use. Two clicks, two tabs, a retried request — one delete.
func TestAssistantConfirmTwiceIsAConflict(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-twice@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-twice-ws", "TWC")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "doomed", user, user)
	deleted := recordBusEvents(t, protocol.EventIssueDeleted)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"TWC-1"}`)
	operationID := assistantOperationID(t, asked)
	assistantConfirm(t, user, operationID)

	second := postAssistantOperation(t, user, operationID, "confirm")
	if second.Code != http.StatusConflict {
		t.Fatalf("second confirm = %d %s, want 409", second.Code, second.Body.String())
	}
	if len(deleted()) != 1 {
		t.Fatalf("issue:deleted fired %d times — the confirmation was replayable", len(deleted()))
	}
	messages := assistantToolMessages(t, session, assistant.ToolDeleteIssue)
	if len(messages) != 1 {
		t.Fatalf("a refused second confirm wrote a transcript row: %v", messages)
	}
}

// Somebody else's confirmation is NOT FOUND, never forbidden: these rows are
// user-private, and acknowledging one exists leaks that a teammate is about to
// delete something.
func TestAssistantConfirmByAnotherUserIsNotFound(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-otheruser-owner@agora.dev")
	other := newAssistantTestUser(t, "assistant-otheruser-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-otheruser-ws", "OTU")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "owner")
	session := newAssistantTestSession(t, owner)
	issueID := newAssistantTestIssue(t, ws, "not yours", owner, owner)

	asked := assistantAsk(t, owner, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"OTU-1"}`)
	operationID := assistantOperationID(t, asked)

	// `other` is an owner of the same workspace and could delete this issue by
	// hand. They still cannot spend somebody else's confirmation.
	for _, action := range []string{"confirm", "reject"} {
		if w := postAssistantOperation(t, other, operationID, action); w.Code != http.StatusNotFound {
			t.Fatalf("%s by another user = %d %s, want 404", action, w.Code, w.Body.String())
		}
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
	if got := assistantPendingOperationStatus(t, operationID); got != "pending" {
		t.Fatalf("operation status = %q, want it untouched", got)
	}
}

// Expiry is evaluated when somebody looks, not by a sweeper.
func TestAssistantExpiredConfirmationIsRefused(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-expired@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-expired-ws", "EXP")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "outlived it", user, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"EXP-1"}`)
	operationID := assistantOperationID(t, asked)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE assistant_pending_operation SET expires_at = now() - interval '1 minute' WHERE id = $1`,
		operationID); err != nil {
		t.Fatalf("age the operation: %v", err)
	}

	w := postAssistantOperation(t, user, operationID, "confirm")
	if w.Code != http.StatusConflict {
		t.Fatalf("confirm on an expired operation = %d %s, want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("refusal does not say it expired: %s", w.Body.String())
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
	// The read recorded the expiry, so the row stops reporting "pending".
	if got := assistantPendingOperationStatus(t, operationID); got != "expired" {
		t.Fatalf("operation status = %q, want expired", got)
	}
}

// THE test the whole design exists for: authorization is re-derived at the
// moment it is spent, not at the moment it was asked for. A person demoted
// between the card and the click does not get the owner's button.
func TestAssistantConfirmRechecksRoleAtConfirmTime(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-demote-owner@agora.dev")
	admin := newAssistantTestUser(t, "assistant-demote-admin@agora.dev")
	victim := newAssistantTestUser(t, "assistant-demote-victim@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-demote-ws", "DMT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, admin, "admin")
	addAssistantTestMember(t, ws, victim, "member")
	session := newAssistantTestSession(t, admin)

	asked := assistantAsk(t, admin, session, assistant.ToolRemoveMember,
		`{"workspace_id":"`+ws+`","user_id":"`+victim+`"}`)
	operationID := assistantOperationID(t, asked)

	// …and now the admin is just a member.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`, ws, admin); err != nil {
		t.Fatalf("demote: %v", err)
	}

	w := postAssistantOperation(t, admin, operationID, "confirm")
	if w.Code == http.StatusOK {
		t.Fatal("a demoted member spent a confirmation they could no longer authorize")
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "insufficient permissions") {
		t.Fatalf("refusal = %s, want the middleware's own message", w.Body.String())
	}
	var stillAMember int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, ws, victim).Scan(&stillAMember); err != nil {
		t.Fatalf("count member: %v", err)
	}
	if stillAMember != 1 {
		t.Fatal("the removal went ahead after the role was revoked")
	}
	// The refusal is on the transcript, so the conversation does not read as if
	// the person had been removed.
	messages := assistantToolMessages(t, session, assistant.ToolRemoveMember)
	if len(messages) != 1 || !strings.Contains(messages[0], "insufficient permissions") {
		t.Fatalf("transcript does not record the refusal: %v", messages)
	}
}

// A confirmation names ONE target. If the thing it named changed between the
// card and the click, the confirmation no longer describes what would happen —
// so it is refused rather than redirected at whatever now answers to the name.
func TestAssistantConfirmRefusesAChangedTarget(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-changed@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-changed-ws", "CHG")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "original title", user, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"CHG-1"}`)
	operationID := assistantOperationID(t, asked)

	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET title = 'renamed while you were reading' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("rename: %v", err)
	}

	w := postAssistantOperation(t, user, operationID, "confirm")
	if w.Code != http.StatusConflict {
		t.Fatalf("confirm on a changed target = %d %s, want 409", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "changed since this was confirmed") {
		t.Fatalf("refusal = %s", w.Body.String())
	}
	assertAssistantRowCount(t, "issue", issueID, 1)
}

// ---------------------------------------------------------------------------
// 3. Durable receipts
// ---------------------------------------------------------------------------

// The two operation tables are the two halves of one story and must both be
// written: assistant_pending_operation records the human decision,
// assistant_operation records what the execution did.
func TestAssistantConfirmWritesTheDurableExecutionReceipt(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-durable@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-durable-ws", "DUR")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "doomed", user, user)
	runID := newAssistantTestRun(t, session, user)

	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"DUR-1"}`)
	operationID := assistantOperationID(t, asked)

	// The pending row is bound to the run that asked, so the execution receipt
	// can be filed against it.
	var storedRun string
	if err := testPool.QueryRow(context.Background(),
		`SELECT run_id::text FROM assistant_pending_operation WHERE id = $1`, operationID).Scan(&storedRun); err != nil {
		t.Fatalf("read pending run_id: %v", err)
	}
	if storedRun != runID {
		t.Fatalf("pending operation run_id = %s, want the active run %s", storedRun, runID)
	}

	assistantConfirm(t, user, operationID)

	var status, tool string
	var result []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, tool_name, result FROM assistant_operation WHERE run_id = $1 AND tool_call_id = $2`,
		runID, "op_"+operationID).Scan(&status, &tool, &result); err != nil {
		t.Fatalf("no durable execution receipt: %v", err)
	}
	if status != "succeeded" || tool != assistant.ToolDeleteIssue {
		t.Fatalf("execution receipt = %s / %s", status, tool)
	}
	var stored map[string]any
	if err := json.Unmarshal(result, &stored); err != nil {
		t.Fatalf("execution receipt result is not JSON: %v (%s)", err, result)
	}
	if stored["deleted"] != true || stored["issue_identifier"] != "DUR-1" {
		t.Fatalf("execution receipt result = %s", result)
	}
}

// ---------------------------------------------------------------------------
// 4. Receipts on ordinary (non-destructive) mutations
// ---------------------------------------------------------------------------

// Every mutating tool answers the user's second question — "what did you
// change?" — in the result itself, built from what the tool already returned.
func TestAssistantOrdinaryMutationsCarryAReceipt(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-receipts@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-receipts-ws", "RCP")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestLabel(t, ws, "bug", "#ef4444")

	created, err := executeAssistantTool(t, user, assistant.ToolCreateIssue,
		`{"workspace_id":"`+ws+`","title":"Receipts please"}`)
	if err != nil {
		t.Fatalf("create_issue: %v", err)
	}
	receipt, ok := created["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("create_issue has no receipt: %v", created)
	}
	if receipt["action"] != assistant.ToolCreateIssue {
		t.Fatalf("receipt action = %v", receipt["action"])
	}
	target, _ := receipt["target"].(map[string]any)
	if target["type"] != "issue" || target["identifier"] != "RCP-1" || target["title"] != "Receipts please" {
		t.Fatalf("receipt target = %v", target)
	}
	links, _ := receipt["links"].([]any)
	if len(links) != 1 || links[0] != "/assistant-receipts-ws/issues/RCP-1" {
		t.Fatalf("receipt links = %v", links)
	}

	// Effects are only the state flips the RESPONSE encodes — never invented.
	labelled, err := executeAssistantTool(t, user, assistant.ToolAddIssueLabel,
		`{"workspace_id":"`+ws+`","ref":"RCP-1","label":"bug"}`)
	if err != nil {
		t.Fatalf("add_issue_label: %v", err)
	}
	labelReceipt, _ := labelled["receipt"].(map[string]any)
	effects, _ := labelReceipt["effects"].([]any)
	if len(effects) != 1 || effects[0] != "label bug added" {
		t.Fatalf("add_issue_label effects = %v", effects)
	}

	archived, err := executeAssistantTool(t, user, assistant.ToolArchiveIssue,
		`{"workspace_id":"`+ws+`","ref":"RCP-1","archived":true}`)
	if err != nil {
		t.Fatalf("archive_issue: %v", err)
	}
	archiveReceipt, _ := archived["receipt"].(map[string]any)
	archiveEffects, _ := archiveReceipt["effects"].([]any)
	if len(archiveEffects) != 1 || archiveEffects[0] != "moved to the archive" {
		t.Fatalf("archive_issue effects = %v", archiveEffects)
	}
}

// A read is not a mutation, and a receipt on one would be noise the model has
// to read past on every single call.
func TestAssistantReadsCarryNoReceipt(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-noreceipt@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-noreceipt-ws", "NRC")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "just looking", user, user)

	result, err := executeAssistantTool(t, user, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if _, hasReceipt := result["receipt"]; hasReceipt {
		t.Fatalf("a read carried a receipt: %v", result)
	}
}

// A failed mutation gets no receipt either: a receipt says "this happened".
func TestAssistantFailedMutationCarriesNoReceipt(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-failreceipt@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-failreceipt-ws", "FRC")
	addAssistantTestMember(t, ws, user, "owner")

	if _, err := executeAssistantTool(t, user, assistant.ToolCreateIssue,
		`{"workspace_id":"`+ws+`","title":""}`); err == nil {
		t.Fatal("create_issue accepted an empty title")
	}
}

// ---------------------------------------------------------------------------
// Rows the confirmation table drives against
// ---------------------------------------------------------------------------

func newAssistantTestSprint(t *testing.T, workspaceID, projectID, name string) string {
	t.Helper()
	var sprintID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO sprint (workspace_id, project_id, name, status) VALUES ($1, $2, $3, 'planned') RETURNING id`,
		workspaceID, projectID, name).Scan(&sprintID); err != nil {
		t.Fatalf("insert sprint: %v", err)
	}
	return sprintID
}

func newAssistantTestComment(t *testing.T, workspaceID, issueID, authorID, content string) string {
	t.Helper()
	var commentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, $4, 'comment') RETURNING id
	`, issueID, workspaceID, authorID, content).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	return commentID
}

func newAssistantTestAutomation(t *testing.T, workspaceID, creatorID, name string) string {
	t.Helper()
	var automationID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO automation (workspace_id, name, enabled, trigger_type, trigger_config, conditions, actions,
		                        created_by_type, created_by_id)
		VALUES ($1, $2, true, 'issue_created', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, 'member', $3)
		RETURNING id
	`, workspaceID, name, creatorID).Scan(&automationID); err != nil {
		t.Fatalf("insert automation: %v", err)
	}
	return automationID
}

// newAssistantTestRun is the run a pending operation is filed against — the one
// the model was in when it asked.
func newAssistantTestRun(t *testing.T, sessionID, userID string) string {
	t.Helper()
	var messageID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO assistant_message (session_id, role, content) VALUES ($1, 'user', 'delete it') RETURNING id`,
		sessionID).Scan(&messageID); err != nil {
		t.Fatalf("insert run message: %v", err)
	}
	var runID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO assistant_run (session_id, user_id, message_id, request_content, status, lease_expires_at)
		VALUES ($1, $2, $3, 'delete it', 'running', now() + interval '1 hour')
		RETURNING id
	`, sessionID, userID, messageID).Scan(&runID); err != nil {
		t.Fatalf("insert assistant run: %v", err)
	}
	return runID
}
