package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestAssistantConfirmIntentFailureDoesNotDispatch(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-intent-failure@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-intent-failure-ws", "AIF")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issue := newAssistantTestIssue(t, ws, "must remain", user, user)
	runID := newAssistantTestRun(t, session, user)
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"AIF-1"}`)
	opID := assistantOperationID(t, asked)
	// Simulate an intent record collision. The claim must roll back rather than
	// performing the deletion with an untrustworthy receipt.
	if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_operation(run_id,tool_call_id,tool_name,arguments,status) VALUES($1,$2,'delete_issue','{}','pending')`, runID, "op_"+opID); err != nil {
		t.Fatal(err)
	}
	w := postAssistantOperation(t, user, opID, "confirm")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("confirm with intent failure: %d %s", w.Code, w.Body.String())
	}
	if status := assistantPendingOperationStatus(t, opID); status != "pending" {
		t.Fatalf("claim was not rolled back: %s", status)
	}
	assertAssistantRowCount(t, "issue", issue, 1)
}

func TestAssistantConfirmationOutcomeIndependentOfProposingRun(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-terminal-run-confirm@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-terminal-run-ws", "ATR")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issue := newAssistantTestIssue(t, ws, "delete after run", user, user)
	runID := newAssistantTestRun(t, session, user)
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"ATR-1"}`)
	opID := assistantOperationID(t, asked)
	if _, err := testPool.Exec(context.Background(), `UPDATE assistant_run SET status='completed',finished_at=now(),lease_owner=NULL,lease_expires_at=NULL WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	assistantConfirm(t, user, opID)
	assertAssistantRowCount(t, "issue", issue, 0)
	w := httptest.NewRecorder()
	testHandler.GetAssistantOperation(w, withURLParam(newAssistantRequest(http.MethodGet, "/x", user, ""), "id", opID))
	if w.Code != http.StatusOK {
		t.Fatalf("get operation: %d %s", w.Code, w.Body.String())
	}
	var payload assistantOperationPayload
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "confirmed" || payload.Outcome == nil || *payload.Outcome != "succeeded" {
		t.Fatalf("outcome was lost with terminal proposing run: %+v", payload)
	}
}

func TestAssistantConfirmationReadTimeUncertainty(t *testing.T) {
	op := db.AssistantPendingOperation{Status: "confirmed"}
	op.ExecutingAt.Time = time.Now().Add(-assistantOperationOutcomeTTL - time.Second)
	op.ExecutingAt.Valid = true
	if got := assistantOperationDisplayStatus(op, time.Now()); got != "uncertain" {
		t.Fatalf("display status=%s", got)
	}
	if got := assistantExecutionOutcome(nil, context.DeadlineExceeded, true); got != "uncertain" {
		t.Fatalf("dispatched error=%s", got)
	}
	if got := assistantExecutionOutcome(nil, context.Canceled, false); got != "failed" {
		t.Fatalf("pre-dispatch refusal=%s", got)
	}
}

func TestAssistantReceiptContextSurvivesCancelledSlowRequest(t *testing.T) {
	request, cancelRequest := context.WithCancel(context.Background())
	// The request ends while a tool is running. Receipt time must begin when
	// execution returns, not when the confirm endpoint first receives it.
	time.Sleep(20 * time.Millisecond)
	cancelRequest()
	receipt, cancelReceipt := assistantReceiptContext(request)
	defer cancelReceipt()
	if err := receipt.Err(); err != nil {
		t.Fatalf("receipt inherited request cancellation: %v", err)
	}
	deadline, ok := receipt.Deadline()
	if !ok || time.Until(deadline) < assistantReceiptPersistTimeout-250*time.Millisecond {
		t.Fatalf("receipt lost its post-execution budget: %v", deadline)
	}
}

func TestAssistantConfirmationSurvivesWorkspaceDeletion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-retained-target@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-retained-target-ws", "ART")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "to inspect", user, user)
	asked := assistantAsk(t, user, session, assistant.ToolDeleteIssue, `{"workspace_id":"`+ws+`","ref":"ART-1"}`)
	opID := assistantOperationID(t, asked)
	if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, ws); err != nil {
		t.Fatal(err)
	}
	var target []byte
	var workspaceID *string
	if err := testPool.QueryRow(context.Background(), `SELECT target,workspace_id::text FROM assistant_pending_operation WHERE id=$1`, opID).Scan(&target, &workspaceID); err != nil {
		t.Fatal(err)
	}
	if workspaceID != nil || len(target) == 0 {
		t.Fatalf("target snapshot lost: workspace=%v target=%s", workspaceID, target)
	}
}
