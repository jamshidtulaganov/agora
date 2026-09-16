package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// Register after user/session fixture cleanup so the detached worker is stopped
// before its rows are removed. A second service shares only the database.
func durableAssistantFixture(t *testing.T, sessionID string) (*assistant.Service, *assistant.Service) {
	t.Helper()
	t.Setenv("ZHIPU_API_KEY", "assistant-recovery-test-key")
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "zhipu")
	release := make(chan struct{})
	client := &blockingToolChat{release: release}
	newService := func() *assistant.Service {
		svc := assistant.NewService(testHandler.Queries, testHandler.Bus, func() (llm.ToolChat, string, error) {
			return client, "test-model", nil
		})
		svc.Store = testHandler.DB
		svc.TxStarter = testHandler.TxStarter
		svc.Exec = testHandler
		return svc
	}
	local, remote := newService(), newService()
	previous := testHandler.Assistant
	testHandler.Assistant = local
	t.Cleanup(func() {
		close(release)
		local.CancelSession(sessionID)
		remote.CancelSession(sessionID)
		deadline := time.Now().Add(5 * time.Second)
		for (local.HasActiveRun(sessionID) || remote.HasActiveRun(sessionID)) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		testHandler.Assistant = previous
	})
	return local, remote
}

func sendRecoveryRequest(t *testing.T, user, sessionID string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	testHandler.SendAssistantMessage(w, withURLParam(newAssistantRequest(http.MethodPost, "/x", user, string(body)), "id", sessionID))
	return w
}

func TestAssistantDurableRequestReplay(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-durable-replay@agora.dev")
	sessionID := newAssistantTestSession(t, user)
	_, remote := durableAssistantFixture(t, sessionID)
	payload := map[string]any{"content": "Summarize my work", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": nil, "timezone": "Asia/Tashkent"}}
	first := sendRecoveryRequest(t, user, sessionID, payload)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first send: %d %s", first.Code, first.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	// Replay through another process: local in-memory maps cannot deduplicate it.
	testHandler.Assistant = remote
	replay := sendRecoveryRequest(t, user, sessionID, payload)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	var repeated SendAssistantMessageResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if accepted != repeated {
		t.Fatalf("replay changed receipt: %+v vs %+v", accepted, repeated)
	}
	var messages, runs int
	if err := testPool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM assistant_message WHERE session_id=$1 AND role='user'), (SELECT count(*) FROM assistant_run WHERE session_id=$1)`, sessionID).Scan(&messages, &runs); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || runs != 1 {
		t.Fatalf("replay duplicated accepted work: messages=%d runs=%d", messages, runs)
	}
	payload["content"] = "Changed request"
	if conflict := sendRecoveryRequest(t, user, sessionID, payload); conflict.Code != http.StatusConflict {
		t.Fatalf("reused id with changed content: %d %s", conflict.Code, conflict.Body.String())
	}
	payload["content"] = "Summarize my work"
	payload["context"] = map[string]any{"workspace_id": nil, "timezone": "UTC"}
	if conflict := sendRecoveryRequest(t, user, sessionID, payload); conflict.Code != http.StatusConflict {
		t.Fatalf("reused id with changed context: %d %s", conflict.Code, conflict.Body.String())
	}
	payload["request_id"] = uuid.NewString()
	if busy := sendRecoveryRequest(t, user, sessionID, payload); busy.Code != http.StatusConflict {
		t.Fatalf("another process started overlapping work: %d %s", busy.Code, busy.Body.String())
	}
}

func TestAssistantDurableContextAndOwnership(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-durable-context@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-durable-outsider@agora.dev")
	oldWorkspace := newAssistantTestWorkspace(t, "assistant-durable-old", "ADO")
	currentWorkspace := newAssistantTestWorkspace(t, "assistant-durable-current", "ADC")
	addAssistantTestMember(t, oldWorkspace, user, "owner")
	addAssistantTestMember(t, currentWorkspace, user, "owner")
	sessionID := newAssistantTestSession(t, user)
	if _, err := testPool.Exec(context.Background(), `UPDATE assistant_session SET focus_workspace_id=$1 WHERE id=$2`, oldWorkspace, sessionID); err != nil {
		t.Fatal(err)
	}
	_, remote := durableAssistantFixture(t, sessionID)
	w := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Create a task here", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": currentWorkspace, "timezone": "Asia/Tashkent"}})
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	run, err := remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil {
		t.Fatal(err)
	}
	if run.Context.WorkspaceID == nil || *run.Context.WorkspaceID != currentWorkspace || run.Context.Timezone != "Asia/Tashkent" {
		t.Fatalf("run did not capture message context: %+v", run.Context)
	}
	if _, err := remote.GetRun(context.Background(), accepted.RunID, outsider); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign run lookup should be hidden: %v", err)
	}
	lookup := httptest.NewRecorder()
	testHandler.GetAssistantRun(lookup, withURLParam(newAssistantRequest(http.MethodGet, "/x", outsider, ""), "id", accepted.RunID))
	if lookup.Code != http.StatusNotFound {
		t.Fatalf("foreign run endpoint: %d %s", lookup.Code, lookup.Body.String())
	}
}

func TestAssistantDurableRejectsInvalidContextWithoutWriting(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-durable-invalid@agora.dev")
	workspace := newAssistantTestWorkspace(t, "assistant-durable-foreign", "ADF")
	sessionID := newAssistantTestSession(t, user)
	durableAssistantFixture(t, sessionID)
	for _, ctx := range []map[string]any{
		{"workspace_id": workspace, "timezone": "UTC"},
		{"workspace_id": nil, "timezone": "Not/A_Timezone"},
		{"workspace_id": "not-a-uuid"},
	} {
		w := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Create a task", "request_id": uuid.NewString(), "context": ctx})
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("invalid context accepted or server failed: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM assistant_message WHERE session_id=$1`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid context left %d messages behind", count)
	}
}

type delayedAssistantMutation struct {
	started chan struct{}
	release chan struct{}
	call    llm.ToolCall
}

func (m *delayedAssistantMutation) CompleteWithTools(ctx context.Context, _ string, _ []llm.Message, _ []llm.Tool) (llm.Message, error) {
	select {
	case m.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return llm.Message{}, ctx.Err()
	case <-m.release:
		return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{m.call}}, nil
	}
}

func TestAssistantDurableExpiredWorkerCannotWrite(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-durable-expired@agora.dev")
	workspace := newAssistantTestWorkspace(t, "assistant-durable-expired", "ADE")
	addAssistantTestMember(t, workspace, user, "owner")
	sessionID := newAssistantTestSession(t, user)
	local, remote := durableAssistantFixture(t, sessionID)
	args, _ := json.Marshal(map[string]string{"workspace_id": workspace, "title": "Expired worker must not create this"})
	model := &delayedAssistantMutation{started: make(chan struct{}, 1), release: make(chan struct{}), call: llm.ToolCall{ID: "late-write", Name: "create_issue", Arguments: string(args)}}
	release := sync.OnceFunc(func() { close(model.release) })
	t.Cleanup(release)
	local.Client = func() (llm.ToolChat, string, error) { return model, "test-model", nil }
	w := sendRecoveryRequest(t, user, sessionID, map[string]any{"content": "Create a task", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": workspace}})
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(5 * time.Second):
		t.Fatal("model call did not start")
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_operation(run_id,tool_call_id,tool_name,arguments,status) VALUES($1,'before-crash','update_issue','{}','pending')`, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE assistant_run SET lease_expires_at=now()-interval '1 minute' WHERE id=$1`, accepted.RunID); err != nil {
		t.Fatal(err)
	}
	run, err := remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil || run.Status != "interrupted" {
		t.Fatalf("expired run recovery: %+v %v", run, err)
	}
	var operationStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM assistant_operation WHERE run_id=$1 AND tool_call_id='before-crash'`, accepted.RunID).Scan(&operationStatus); err != nil || operationStatus != "uncertain" {
		t.Fatalf("pending operation recovery: %q %v", operationStatus, err)
	}
	release()
	deadline := time.Now().Add(5 * time.Second)
	for local.HasActiveRun(sessionID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if local.HasActiveRun(sessionID) {
		t.Fatal("expired worker did not stop")
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id=$1`, workspace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired worker wrote an issue: count=%d err=%v", count, err)
	}
	run, err = remote.GetRun(context.Background(), accepted.RunID, user)
	if err != nil || run.Status != "interrupted" {
		t.Fatalf("late worker changed terminal status: %+v %v", run, err)
	}
}
