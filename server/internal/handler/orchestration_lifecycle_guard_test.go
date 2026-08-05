package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func createActiveCustomOrchestration(t *testing.T, agentID string) (IssueResponse, orchestrationRunResponse, db.AgentTaskQueue) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Lifecycle-guard orchestration %d", time.Now().UnixNano()),
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID) })

	w = httptest.NewRecorder()
	req = withURLParam(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
			"execution_strategy": "custom",
			"auto_start":         true,
			"steps": []map[string]any{{
				"key": "dev", "title": "Implement", "stage": "dev",
				"agent_id": agentID, "max_attempts": 2,
			}},
		}),
		"id",
		issue.ID,
	)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create orchestration: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var run orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&run); err != nil {
		t.Fatalf("decode orchestration: %v", err)
	}
	tasks, err := testHandler.Queries.ListActiveTasksByIssue(context.Background(), parseUUID(issue.ID))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("active orchestration task: len=%d err=%v", len(tasks), err)
	}
	return issue, run, tasks[0]
}

func TestActiveOrchestrationRemainsSoleDispatcherAcrossIssueMutations(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	firstAgentID := createHandlerTestAgent(t, "Sole dispatcher first", []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Sole dispatcher second", []byte("[]"))
	issue, run, originalTask := createActiveCustomOrchestration(t, firstAgentID)

	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+issue.ID, map[string]any{
			"assignee_type": "agent",
			"assignee_id":   secondAgentID,
		}),
		"id",
		issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reassign issue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(context.Background(), parseUUID(issue.ID))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reassignment created duplicate work: len=%d err=%v", len(tasks), err)
	}
	if tasks[0].ID != originalTask.ID || !tasks[0].OrchestrationStepID.Valid || tasks[0].Status == "cancelled" {
		t.Fatalf("orchestration task lost ownership after reassignment: %#v", tasks[0])
	}

	w = httptest.NewRecorder()
	req = withURLParam(
		newRequest(http.MethodPut, "/api/issues/"+issue.ID, map[string]any{"status": "cancelled"}),
		"id",
		issue.ID,
	)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel issue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(context.Background(), parseUUID(run.ID))
	if err != nil || persistedRun.Status != "cancelled" {
		t.Fatalf("issue cancellation left run active: status=%q err=%v", persistedRun.Status, err)
	}
	steps, err := testHandler.Queries.ListOrchestrationSteps(context.Background(), persistedRun.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != "cancelled" {
		t.Fatalf("issue cancellation left step active: steps=%#v err=%v", steps, err)
	}
	persistedTask, err := testHandler.Queries.GetAgentTask(context.Background(), originalTask.ID)
	if err != nil || persistedTask.Status != "cancelled" {
		t.Fatalf("issue cancellation left task active: status=%q err=%v", persistedTask.Status, err)
	}
}

func TestStaleOrchestrationFailureRetriesInsidePersistedStep(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Stale orchestration recovery", []byte("[]"))
	issue, run, originalTask := createActiveCustomOrchestration(t, agentID)

	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'failed', error = 'runtime disappeared', failure_reason = 'runtime_recovery', completed_at = now()
		WHERE id = $1
	`, originalTask.ID); err != nil {
		t.Fatalf("fail task as sweeper would: %v", err)
	}
	failedTask, err := testHandler.Queries.GetAgentTask(context.Background(), originalTask.ID)
	if err != nil {
		t.Fatalf("load failed task: %v", err)
	}
	if retried := testHandler.TaskService.HandleFailedTasks(context.Background(), []db.AgentTaskQueue{failedTask}); retried != 0 {
		t.Fatalf("generic retry count = %d, want 0 for orchestration-owned task", retried)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(context.Background(), parseUUID(issue.ID))
	if err != nil || len(tasks) != 2 {
		t.Fatalf("orchestration recovery tasks: len=%d err=%v", len(tasks), err)
	}
	var replacement db.AgentTaskQueue
	for _, task := range tasks {
		if task.ID != originalTask.ID {
			replacement = task
		}
	}
	if !replacement.ID.Valid || !replacement.OrchestrationStepID.Valid || replacement.ParentTaskID.Valid {
		t.Fatalf("replacement escaped persisted step identity: %#v", replacement)
	}
	if replacement.OrchestrationStepID != originalTask.OrchestrationStepID {
		t.Fatalf("replacement step id changed: got %v want %v", replacement.OrchestrationStepID, originalTask.OrchestrationStepID)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(context.Background(), parseUUID(run.ID))
	if err != nil || persistedRun.Status != "running" {
		t.Fatalf("recovered run status=%q err=%v", persistedRun.Status, err)
	}
}
