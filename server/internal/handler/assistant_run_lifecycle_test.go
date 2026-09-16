package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAssistantRunRemoteCancelRetainsSlotUntilWorkerStops(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-remote-cancel@agora.dev")
	sessionID := newAssistantTestSession(t, user)
	local, remote := durableAssistantFixture(t, sessionID)
	w := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Summarize my work", "request_id": uuid.NewString()})
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	ok, err := remote.RequestCancel(context.Background(), accepted.RunID, user)
	if err != nil || !ok {
		t.Fatalf("remote cancel: %v %v", ok, err)
	}
	var status string
	var requested bool
	if err := testPool.QueryRow(context.Background(), `SELECT status,cancel_requested FROM assistant_run WHERE id=$1`, accepted.RunID).Scan(&status, &requested); err != nil {
		t.Fatal(err)
	}
	if status != "running" || !requested {
		t.Fatalf("cancel prematurely freed active slot: status=%s requested=%v", status, requested)
	}
	busy := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Next request", "request_id": uuid.NewString()})
	if busy.Code != http.StatusConflict {
		t.Fatalf("overlap after remote cancel: %d %s", busy.Code, busy.Body.String())
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		run, err := remote.GetRun(context.Background(), accepted.RunID, user)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "cancelled" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	local.CancelSession(sessionID)
	t.Fatal("worker did not observe remote cancellation")
}

func TestAssistantRunExpiredLeaseInterruptsPendingWrite(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-expired-lease@agora.dev")
	sessionID := newAssistantTestSession(t, user)
	_, remote := durableAssistantFixture(t, sessionID)
	w := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Summarize my work", "request_id": uuid.NewString()})
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_operation(run_id,tool_call_id,tool_name,arguments,status) VALUES($1,'call-1','create_issue','{}','pending')`, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE assistant_run SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	run, err := remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "interrupted" || run.FinishedAt == nil {
		t.Fatalf("expired run: %+v", run)
	}
	var operationStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM assistant_operation WHERE run_id=$1 AND tool_call_id='call-1'`, accepted.RunID).Scan(&operationStatus); err != nil {
		t.Fatal(err)
	}
	if operationStatus != "uncertain" {
		t.Fatalf("pending write was not marked uncertain: %s", operationStatus)
	}
}
