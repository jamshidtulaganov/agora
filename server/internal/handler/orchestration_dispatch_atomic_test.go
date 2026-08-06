package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

type orchestrationDispatchWakeupRecorder struct {
	calls atomic.Int32
	hook  func(runtimeID, taskID string)
}

func (r *orchestrationDispatchWakeupRecorder) NotifyTaskAvailable(runtimeID, taskID string) {
	r.calls.Add(1)
	if r.hook != nil {
		r.hook(runtimeID, taskID)
	}
}

func createRunnableAtomicDispatchFixture(
	t *testing.T,
	agentID string,
) (db.Issue, db.OrchestrationRun, db.OrchestrationStep) {
	t.Helper()
	ctx := context.Background()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Atomic orchestration dispatch %d", time.Now().UnixNano()),
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issueResponse IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issueResponse); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueResponse.ID)
	})

	autoStart := false
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueResponse.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         autoStart,
		"steps": []map[string]any{{
			"key": "dev", "title": "Implement", "stage": "dev",
			"agent_id": agentID, "max_attempts": 2,
		}},
	}), "id", issueResponse.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create draft orchestration: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var response orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil || len(response.Steps) != 1 {
		t.Fatalf("decode draft orchestration: steps=%d err=%v", len(response.Steps), err)
	}

	run, err := testHandler.Queries.StartOrchestrationRun(ctx, parseUUID(response.ID))
	if err != nil {
		t.Fatalf("make orchestration runnable: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueResponse.ID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	step, err := testHandler.Queries.GetOrchestrationStep(ctx, parseUUID(response.Steps[0].ID))
	if err != nil {
		t.Fatalf("load orchestration step: %v", err)
	}
	return issue, run, step
}

func addAtomicDispatchStep(t *testing.T, runID, agentID string, position int32, key string) db.OrchestrationStep {
	t.Helper()
	step, err := testHandler.Queries.CreateOrchestrationStep(context.Background(), db.CreateOrchestrationStepParams{
		RunID:               parseUUID(runID),
		StepKey:             key,
		Title:               "Implement " + key,
		Stage:               "dev",
		Position:            position,
		AgentID:             parseUUID(agentID),
		MaxAttempts:         2,
		IntroducedInVersion: 1,
		StepKind:            "task",
	})
	if err != nil {
		t.Fatalf("add atomic dispatch step %q: %v", key, err)
	}
	return step
}

func TestAtomicOrchestrationDispatchRollsBackPostInsertFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Atomic dispatch rollback", []byte("[]"))
	issue, run, step := createRunnableAtomicDispatchFixture(t, agentID)

	recorder := &orchestrationDispatchWakeupRecorder{}
	previousWakeup := testHandler.TaskService.Wakeup
	testHandler.TaskService.Wakeup = recorder
	t.Cleanup(func() { testHandler.TaskService.Wakeup = previousWakeup })

	// A deliberately wrong pinned location fails after QueueOrchestrationStep
	// and CreateAgentTask have both executed inside the transaction.
	_, _, err := testHandler.queueOrchestrationStepAtomically(
		context.Background(), run.ID, issue, step,
		"runtime:00000000-0000-0000-0000-000000000001",
		3,
	)
	if !errors.Is(err, errOrchestrationArtifactMoved) {
		t.Fatalf("post-insert dispatch error=%v, want artifact-location mismatch", err)
	}

	persisted, err := testHandler.Queries.GetOrchestrationStep(context.Background(), step.ID)
	if err != nil {
		t.Fatalf("reload rolled-back step: %v", err)
	}
	if persisted.Status != "pending" || persisted.Attempt != 0 || persisted.TaskID.Valid || persisted.StartedAt.Valid {
		t.Fatalf("post-insert failure leaked charged step state: %#v", persisted)
	}
	var taskCount, eventCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue WHERE orchestration_step_id = $1
	`, step.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count rolled-back tasks: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM orchestration_event WHERE step_id = $1 AND kind = 'step_queued'
	`, step.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count rolled-back events: %v", err)
	}
	if taskCount != 0 || eventCount != 0 {
		t.Fatalf("post-insert failure leaked task/event rows: tasks=%d events=%d", taskCount, eventCount)
	}
	if got := recorder.calls.Load(); got != 0 {
		t.Fatalf("rolled-back task woke a daemon %d times", got)
	}
}

func TestAtomicOrchestrationDispatchSerializesConcurrentCallers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Atomic dispatch concurrency", []byte("[]"))
	issue, run, step := createRunnableAtomicDispatchFixture(t, agentID)

	var durableBeforeWake atomic.Bool
	recorder := &orchestrationDispatchWakeupRecorder{hook: func(_, taskID string) {
		var committed bool
		err := testPool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				FROM orchestration_step step
				JOIN agent_task_queue task ON task.id = step.task_id
				JOIN orchestration_event event
				  ON event.step_id = step.id AND event.kind = 'step_queued'
				WHERE step.id = $1 AND task.id = $2
			)
		`, step.ID, taskID).Scan(&committed)
		durableBeforeWake.Store(err == nil && committed)
	}}
	previousWakeup := testHandler.TaskService.Wakeup
	testHandler.TaskService.Wakeup = recorder
	t.Cleanup(func() { testHandler.TaskService.Wakeup = previousWakeup })

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			errs <- testHandler.dispatchNextOrchestrationStep(context.Background(), run.ID, issue)
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent dispatcher failed: %v", err)
		}
	}

	persisted, err := testHandler.Queries.GetOrchestrationStep(context.Background(), step.ID)
	if err != nil {
		t.Fatalf("reload dispatched step: %v", err)
	}
	if persisted.Status != "queued" || persisted.Attempt != 1 || !persisted.TaskID.Valid {
		t.Fatalf("concurrent dispatch did not commit exactly one generation: %#v", persisted)
	}
	var taskCount, linkedTaskCount, eventCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE id = $2)
		FROM agent_task_queue WHERE orchestration_step_id = $1
	`, step.ID, persisted.TaskID).Scan(&taskCount, &linkedTaskCount); err != nil {
		t.Fatalf("count dispatched task generations: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM orchestration_event WHERE step_id = $1 AND kind = 'step_queued'
	`, step.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count committed queue events: %v", err)
	}
	if taskCount != 1 || linkedTaskCount != 1 || eventCount != 1 {
		t.Fatalf("concurrent dispatch duplicated durable state: tasks=%d linked=%d events=%d", taskCount, linkedTaskCount, eventCount)
	}
	if got := recorder.calls.Load(); got != 1 {
		t.Fatalf("committed task wakeups=%d, want exactly 1", got)
	}
	if !durableBeforeWake.Load() {
		t.Fatal("daemon wakeup observed before the step, linked task, and queue event committed")
	}
}

func TestAtomicOrchestrationDispatchRechecksCapacityUnderRunLock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	firstAgentID := createHandlerTestAgent(t, "Atomic capacity first", []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Atomic capacity second", []byte("[]"))
	issue, run, first := createRunnableAtomicDispatchFixture(t, firstAgentID)
	second := addAtomicDispatchStep(t, uuidToString(run.ID), secondAgentID, 1, "second")

	if _, _, err := testHandler.queueOrchestrationStepAtomically(
		context.Background(), run.ID, issue, first, "", 1,
	); err != nil {
		t.Fatalf("queue first capacity slot: %v", err)
	}
	if _, _, err := testHandler.queueOrchestrationStepAtomically(
		context.Background(), run.ID, issue, second, "", 1,
	); !errors.Is(err, errOrchestrationDispatchAtCapacity) {
		t.Fatalf("second stale candidate error=%v, want at-capacity", err)
	}

	persisted, err := testHandler.Queries.GetOrchestrationStep(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("reload capacity-rejected step: %v", err)
	}
	if persisted.Status != "pending" || persisted.Attempt != 0 || persisted.TaskID.Valid {
		t.Fatalf("capacity rejection charged or linked the second step: %#v", persisted)
	}
	var active int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM orchestration_step
		WHERE run_id = $1 AND status IN ('queued', 'running')
	`, run.ID).Scan(&active); err != nil {
		t.Fatalf("count active capacity slots: %v", err)
	}
	if active != 1 {
		t.Fatalf("max_concurrency=1 admitted %d active steps", active)
	}
}

func TestAtomicOrchestrationDispatchRechecksActiveAgentUnderRunLock(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Atomic active-agent guard", []byte("[]"))
	issue, run, first := createRunnableAtomicDispatchFixture(t, agentID)
	second := addAtomicDispatchStep(t, uuidToString(run.ID), agentID, 1, "same-agent")

	firstTask, _, err := testHandler.queueOrchestrationStepAtomically(
		context.Background(), run.ID, issue, first, "", 2,
	)
	if err != nil {
		t.Fatalf("queue first same-agent step: %v", err)
	}
	// Simulate the daemon claiming and starting the first task after a second
	// dispatcher took its stale outer scheduling snapshot. Running tasks are
	// intentionally outside HasPendingTaskForIssueAndAgent, so only the
	// transaction's current orchestration-step scan can reject this candidate.
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'running', dispatched_at = now(), started_at = now()
		WHERE id = $1
	`, firstTask.ID); err != nil {
		t.Fatalf("mark first task running: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE orchestration_step SET status = 'running', updated_at = now() WHERE id = $1
	`, first.ID); err != nil {
		t.Fatalf("mark first step running: %v", err)
	}

	if _, _, err := testHandler.queueOrchestrationStepAtomically(
		context.Background(), run.ID, issue, second, "", 2,
	); !errors.Is(err, errOrchestrationDispatchBusy) {
		t.Fatalf("second same-agent candidate error=%v, want agent-busy", err)
	}
	persisted, err := testHandler.Queries.GetOrchestrationStep(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("reload same-agent-rejected step: %v", err)
	}
	if persisted.Status != "pending" || persisted.Attempt != 0 || persisted.TaskID.Valid {
		t.Fatalf("same-agent rejection charged or linked the second step: %#v", persisted)
	}
	var generations int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue WHERE orchestration_step_id IN ($1, $2)
	`, first.ID, second.ID).Scan(&generations); err != nil {
		t.Fatalf("count same-agent task generations: %v", err)
	}
	if generations != 1 {
		t.Fatalf("same agent received %d active generations, want 1", generations)
	}
}
