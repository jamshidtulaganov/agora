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

func createLifecycleCustomOrchestration(t *testing.T, steps []map[string]any) (IssueResponse, orchestrationRunResponse, []db.AgentTaskQueue) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Multi-stage lifecycle orchestration %d", time.Now().UnixNano()),
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
			"steps":              steps,
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
	if err != nil {
		t.Fatalf("list active orchestration tasks: %v", err)
	}
	return issue, run, tasks
}

func lifecycleTaskByStepKey(t *testing.T, run orchestrationRunResponse, tasks []db.AgentTaskQueue, key string) db.AgentTaskQueue {
	t.Helper()
	stepID := ""
	for _, step := range run.Steps {
		if step.Key == key {
			stepID = step.ID
			break
		}
	}
	for _, task := range tasks {
		if uuidToString(task.OrchestrationStepID) == stepID {
			return task
		}
	}
	t.Fatalf("active task for step %q not found", key)
	return db.AgentTaskQueue{}
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

func TestCustomOrchestrationRejectsForeignWorkspaceAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Cross-workspace orchestration guard %d", time.Now().UnixNano()),
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: %d %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issue.ID) })

	var foreignWorkspaceID, foreignRuntimeID, foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ('Foreign orchestration workspace', $1, 'FOR') RETURNING id
	`, fmt.Sprintf("foreign-orchestration-%d", time.Now().UnixNano())).Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID) })
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, 'Foreign runtime', 'cloud', 'codex', 'online', '{}'::jsonb, now()) RETURNING id
	`, foreignWorkspaceID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
		VALUES ($1, 'Foreign agent', 'cloud', '{}'::jsonb, $2, 'workspace', 1) RETURNING id
	`, foreignWorkspaceID, foreignRuntimeID).Scan(&foreignAgentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}

	autoStart := false
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         autoStart,
		"steps": []map[string]any{{
			"key": "foreign", "title": "Route across tenant", "stage": "dev", "agent_id": foreignAgentID,
		}},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("foreign route accepted: %d %s", w.Code, w.Body.String())
	}
	if unexpectedRun, err := testHandler.Queries.GetLatestOrchestrationRunForIssue(ctx, parseUUID(issue.ID)); err == nil || unexpectedRun.ID.Valid {
		t.Fatalf("foreign route created a run before validation: run=%#v err=%v", unexpectedRun, err)
	}
	var queued int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE agent_id = $1`, foreignAgentID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("foreign runtime queue was touched: count=%d err=%v", queued, err)
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

func TestOrchestrationQuestionWaitsForDurableHumanResponse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Question handoff agent", []byte("[]"))
	issue, run, task := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET stage = 'plan', capability = 'coordination' WHERE id = $1`, task.OrchestrationStepID); err != nil {
		t.Fatalf("prepare planning task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("start planning task: %v", err)
	}
	output := "Need a product decision.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"plan","outcome":"waiting_input","summary":"API version is ambiguous","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Should the endpoint use v1 or v2?","target":"human","blocking":true}}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, task.ID, result, "", ""); err != nil {
		t.Fatalf("complete planning task: %v", err)
	}
	step, err := testHandler.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
	if err != nil || step.Status != "waiting_input" {
		t.Fatalf("question must pause step: status=%q err=%v", step.Status, err)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "waiting_input" {
		t.Fatalf("question must pause run: status=%q err=%v", persistedRun.Status, err)
	}
	activeRun, err := testHandler.Queries.GetActiveOrchestrationRunForIssue(ctx, parseUUID(issue.ID))
	if err != nil || activeRun.ID != persistedRun.ID {
		t.Fatalf("waiting-input run must remain the issue's active orchestration: run=%#v err=%v", activeRun, err)
	}
	question, err := testHandler.Queries.GetLatestOpenOrchestrationQuestion(ctx, step.ID)
	if err != nil || !question.ExpectsReply {
		t.Fatalf("durable question missing: %#v err=%v", question, err)
	}
	firstQuestionID := question.ID

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue.ID+"/orchestration", nil), "id", issue.ID)
	testHandler.GetIssueOrchestration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get waiting orchestration: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var waitingResponse orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&waitingResponse); err != nil {
		t.Fatalf("decode waiting orchestration: %v", err)
	}
	if len(waitingResponse.Steps) != 1 || waitingResponse.Steps[0].QuestionID != uuidToString(firstQuestionID) {
		t.Fatalf("waiting step question identity missing: %#v", waitingResponse.Steps)
	}

	// A client must identify the exact rendered question. Missing identity fails
	// closed before any durable answer or step transition is written.
	w = httptest.NewRecorder()
	req = withURLParams(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
			"message": "Use v2 and keep v1 compatibility.",
		}),
		"id", issue.ID, "stepId", uuidToString(step.ID),
	)
	testHandler.RespondToOrchestrationStep(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing question identity: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withURLParams(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
			"question_id": uuidToString(firstQuestionID),
			"message":     "Use v2 and keep v1 compatibility.",
		}),
		"id", issue.ID, "stepId", uuidToString(step.ID),
	)
	testHandler.RespondToOrchestrationStep(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("respond: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	question, err = testHandler.Queries.GetLatestOpenOrchestrationQuestion(ctx, step.ID)
	if err == nil || question.ID.Valid {
		t.Fatalf("question must be resolved, got %#v err=%v", question, err)
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 2 || messages[1].Kind != "answer" {
		t.Fatalf("question/answer log mismatch: %#v err=%v", messages, err)
	}

	// A lost HTTP response is safe to replay: the same answer should retry
	// downstream dispatch without creating a second coordination message.
	w = httptest.NewRecorder()
	req = withURLParams(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
			"question_id": uuidToString(firstQuestionID),
			"message":     "Use v2 and keep v1 compatibility.",
		}),
		"id", issue.ID, "stepId", uuidToString(step.ID),
	)
	testHandler.RespondToOrchestrationStep(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("replay response: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	messages, err = testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("replayed answer must remain idempotent: %#v err=%v", messages, err)
	}
	step, err = testHandler.Queries.GetOrchestrationStep(ctx, step.ID)
	if err != nil || step.Status != "queued" {
		t.Fatalf("answered step must dispatch a continuation: status=%q err=%v", step.Status, err)
	}

	// The continuation can ask a second question on the same persisted step.
	// A delayed retry carrying Q1's identity must remain an accepted no-op and
	// must never resolve or answer Q2.
	continuation, err := testHandler.Queries.GetLatestTaskForOrchestrationStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("load clarification continuation: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, continuation.ID); err != nil {
		t.Fatalf("start clarification continuation: %v", err)
	}
	secondOutput := "Need one more product decision.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"plan","outcome":"waiting_input","summary":"Migration scope is ambiguous","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Should v1 data be migrated?","target":"human","blocking":true}}` +
		"\n```"
	secondResult, _ := json.Marshal(map[string]string{"output": secondOutput})
	if _, err := testHandler.TaskService.CompleteTask(ctx, continuation.ID, secondResult, "clarification-session-2", ""); err != nil {
		t.Fatalf("complete clarification continuation: %v", err)
	}
	secondQuestion, err := testHandler.Queries.GetLatestOpenOrchestrationQuestion(ctx, step.ID)
	if err != nil || secondQuestion.ID == firstQuestionID {
		t.Fatalf("second durable question missing: %#v err=%v", secondQuestion, err)
	}

	w = httptest.NewRecorder()
	req = withURLParams(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
			"question_id": uuidToString(firstQuestionID),
			"message":     "Use v2 and keep v1 compatibility.",
		}),
		"id", issue.ID, "stepId", uuidToString(step.ID),
	)
	testHandler.RespondToOrchestrationStep(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("stale exact replay: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	stillOpen, err := testHandler.Queries.GetLatestOpenOrchestrationQuestion(ctx, step.ID)
	if err != nil || stillOpen.ID != secondQuestion.ID {
		t.Fatalf("stale Q1 replay resolved Q2: open=%#v err=%v", stillOpen, err)
	}
	messages, err = testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 3 || messages[2].ID != secondQuestion.ID {
		t.Fatalf("stale replay changed question/answer log: %#v err=%v", messages, err)
	}

	w = httptest.NewRecorder()
	req = withURLParams(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
			"question_id": uuidToString(firstQuestionID),
			"message":     "Use v1 instead.",
		}),
		"id", issue.ID, "stepId", uuidToString(step.ID),
	)
	testHandler.RespondToOrchestrationStep(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale changed Q1 answer: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMentionedWaitingAgentContinuesSameOrchestrationStep(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Mentioned clarification agent", []byte("[]"))
	otherAgentID := createHandlerTestAgent(t, "Unrelated mentioned agent", []byte("[]"))
	issue, _, originalTask := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET stage = 'plan', capability = 'coordination' WHERE id = $1`, originalTask.OrchestrationStepID); err != nil {
		t.Fatalf("prepare planning task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, originalTask.ID); err != nil {
		t.Fatalf("start planning task: %v", err)
	}
	output := "Need a product decision.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"plan","outcome":"waiting_input","summary":"API version is ambiguous","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Should the endpoint use v1 or v2?","target":"human","blocking":true}}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, originalTask.ID, result, "tagged-clarification-session", "/tmp/tagged-clarification-workdir"); err != nil {
		t.Fatalf("complete planning task: %v", err)
	}
	step, err := testHandler.Queries.GetOrchestrationStep(ctx, originalTask.OrchestrationStepID)
	if err != nil || step.Status != "waiting_input" {
		t.Fatalf("question must pause step: status=%q err=%v", step.Status, err)
	}
	persistedIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	answerContent := "[@Clarifier](mention://agent/" + agentID + ") Use v2 and preserve the v1 adapter."
	triggers := testHandler.computeCommentAgentTriggers(ctx, persistedIssue, answerContent, nil, "member", testUserID)
	if len(triggers) != 1 || triggers[0].Source != commentTriggerSourceOrchestrationAnswer || triggers[0].StepID != step.ID {
		t.Fatalf("waiting agent mention did not resolve to the paused step: %#v", triggers)
	}
	if got := testHandler.computeCommentAgentTriggers(ctx, persistedIssue,
		"[@Other](mention://agent/"+otherAgentID+") take a look", nil, "member", testUserID); len(got) != 0 {
		t.Fatalf("unrelated active-run mention escaped orchestration suppression: %#v", got)
	}
	if got := testHandler.computeCommentAgentTriggers(ctx, persistedIssue, answerContent, nil, "agent", agentID); len(got) != 0 {
		t.Fatalf("agent-authored mention could create a clarification loop: %#v", got)
	}

	// A preview chip the member deliberately de-selects must not be recovered
	// later merely because the stored Markdown still contains the mention. The
	// outbox marker is created only after suppress_agent_ids filtering.
	w := httptest.NewRecorder()
	req := withURLParam(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{
			"content": answerContent, "suppress_agent_ids": []string{agentID},
		}),
		"id", issue.ID,
	)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create suppressed answer comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	step, err = testHandler.Queries.GetOrchestrationStep(ctx, step.ID)
	if err != nil || step.Status != "waiting_input" {
		t.Fatalf("suppressed answer changed paused step: status=%q err=%v", step.Status, err)
	}
	if repaired := testHandler.TaskService.ReconcileOrchestrationAnswerComments(ctx); repaired != 0 {
		t.Fatalf("suppressed answer was resurrected by repair: count=%d", repaired)
	}

	w = httptest.NewRecorder()
	req = withURLParam(
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"content": answerContent}),
		"id", issue.ID,
	)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create answer comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var comment CommentResponse
	if err := json.NewDecoder(w.Body).Decode(&comment); err != nil {
		t.Fatalf("decode answer comment: %v", err)
	}

	step, err = testHandler.Queries.GetOrchestrationStep(ctx, step.ID)
	if err != nil || step.Status != "queued" {
		t.Fatalf("mentioned answer did not dispatch continuation: status=%q err=%v", step.Status, err)
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 2 || messages[0].Kind != "question" || messages[1].Kind != "answer" {
		t.Fatalf("durable mentioned answer log mismatch: %#v err=%v", messages, err)
	}
	var answerBody map[string]any
	if err := json.Unmarshal(messages[1].Body, &answerBody); err != nil || answerBody["comment_id"] != comment.ID {
		t.Fatalf("answer did not retain comment causality: body=%s err=%v", messages[1].Body, err)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(tasks) != 2 {
		t.Fatalf("clarification continuation tasks: len=%d err=%v", len(tasks), err)
	}
	var continuation db.AgentTaskQueue
	for _, task := range tasks {
		if task.ID != originalTask.ID {
			continuation = task
		}
	}
	if !continuation.ID.Valid || continuation.OrchestrationStepID != originalTask.OrchestrationStepID || continuation.TriggerCommentID.Valid || continuation.ForceFreshSession {
		t.Fatalf("comment answer escaped same-step session lineage: %#v", continuation)
	}
	originalTask, err = testHandler.Queries.GetAgentTask(ctx, originalTask.ID)
	if err != nil || !originalTask.SessionID.Valid || originalTask.SessionID.String != "tagged-clarification-session" {
		t.Fatalf("provider session lineage was not retained on prior step task: task=%#v err=%v", originalTask, err)
	}

	// Replaying the same persisted comment is idempotent and cannot enqueue a
	// second continuation or duplicate the answer/event log.
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load clarification agent: %v", err)
	}
	if err := testHandler.respondToOrchestrationQuestionFromComment(ctx, persistedIssue, commentAgentTrigger{
		Agent: agent, Source: commentTriggerSourceOrchestrationAnswer,
		StepID: step.ID, QuestionID: messages[0].ID,
	}, parseUUID(comment.ID)); err != nil {
		t.Fatalf("replay mentioned answer: %v", err)
	}
	messages, err = testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	tasks, tasksErr := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || tasksErr != nil || len(messages) != 2 || len(tasks) != 2 {
		t.Fatalf("mentioned answer replay was not idempotent: messages=%d tasks=%d message_err=%v task_err=%v", len(messages), len(tasks), err, tasksErr)
	}
}

func TestClarificationHandoffsBypassSuccessOnlyGitGates(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	t.Run("development no-op can ask before editing", func(t *testing.T) {
		agentID := createHandlerTestAgent(t, "Dev clarification agent", []byte("[]"))
		_, run, task := createActiveCustomOrchestration(t, agentID)
		if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, task.ID); err != nil {
			t.Fatalf("start dev task: %v", err)
		}
		output := "Need scope confirmation.\n```agora-handoff\n" +
			`{"schema_version":1,"stage":"dev","outcome":"waiting_input","summary":"The API scope is ambiguous","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Should this change include the legacy endpoint?","target":"human","blocking":true}}` +
			"\n```"
		body := map[string]any{
			"output": output, "branch_name": "agent/dev", "base_sha": "abc", "head_sha": "abc", "merge_status": "clean",
			"git_states": []map[string]any{{"repo": "app", "branch": "agent/dev", "base_sha": "abc", "head_sha": "abc", "merge_status": "clean"}},
		}
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+uuidToString(task.ID)+"/complete", body, testWorkspaceID, "dev-clarification")
		req = withURLParam(req, "taskId", uuidToString(task.ID))
		w := httptest.NewRecorder()
		testHandler.CompleteTask(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("dev clarification rejected by success gate: %d %s", w.Code, w.Body.String())
		}
		step, err := testHandler.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
		if err != nil || step.Status != "waiting_input" {
			t.Fatalf("dev clarification not persisted: status=%q err=%v", step.Status, err)
		}
		persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
		if err != nil || persistedRun.Status != "waiting_input" {
			t.Fatalf("dev clarification did not pause run: status=%q err=%v", persistedRun.Status, err)
		}
	})

	t.Run("integration blocker can report missing heads", func(t *testing.T) {
		agentID := createHandlerTestAgent(t, "Integration clarification agent", []byte("[]"))
		_, _, task := createActiveCustomOrchestration(t, agentID)
		if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET step_kind = 'integration' WHERE id = $1`, task.OrchestrationStepID); err != nil {
			t.Fatalf("prepare integration step: %v", err)
		}
		if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, task.ID); err != nil {
			t.Fatalf("start integration task: %v", err)
		}
		output := "Dependency head is unavailable.\n```agora-handoff\n" +
			`{"schema_version":1,"stage":"dev","outcome":"blocked","summary":"Cannot fetch the dependency commit","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":["Dependency commit is unavailable"],"next_actions":[]}` +
			"\n```"
		body := map[string]any{
			"output": output, "merge_status": "conflicts", "integration_status": "conflicts", "missing_head_shas": []string{"missing-head"},
		}
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+uuidToString(task.ID)+"/complete", body, testWorkspaceID, "integration-clarification")
		req = withURLParam(req, "taskId", uuidToString(task.ID))
		w := httptest.NewRecorder()
		testHandler.CompleteTask(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("integration blocker rejected by success gate: %d %s", w.Code, w.Body.String())
		}
		step, err := testHandler.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
		if err != nil || step.Status != "blocked" || step.IntegrationStatus != "conflicts" {
			t.Fatalf("integration blocker not persisted with diagnostics: step=%#v err=%v", step, err)
		}
	})
}

func TestFailedVerificationHandoffBlocksReleaseProgression(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Negative QA handoff agent", []byte("[]"))
	_, run, task := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET stage = 'qa', capability = 'qa' WHERE id = $1`, task.OrchestrationStepID); err != nil {
		t.Fatalf("prepare QA task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("start QA task: %v", err)
	}
	output := "QA found a regression.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"qa","outcome":"completed","verdict":"fail","summary":"Login regression","decisions":[],"contracts":[],"artifacts":[],"verification":[{"name":"login test","status":"failed"}],"findings":["Login returns 500"],"risks":[],"blockers":[],"next_actions":[]}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, task.ID, result, "", ""); err != nil {
		t.Fatalf("complete QA task: %v", err)
	}
	step, err := testHandler.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
	if err != nil || step.Status != "blocked" {
		t.Fatalf("failed QA verdict must block step: status=%q err=%v", step.Status, err)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "blocked" {
		t.Fatalf("failed QA verdict must block run: status=%q err=%v", persistedRun.Status, err)
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 1 || messages[0].Kind != "blocker" {
		t.Fatalf("blocker handoff missing: %#v err=%v", messages, err)
	}
}

func TestParallelBlockerPrecedenceSurvivesQuestionAndCancellation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	blockerAgentID := createHandlerTestAgent(t, "Parallel blocker agent", []byte("[]"))
	questionAgentID := createHandlerTestAgent(t, "Parallel question agent", []byte("[]"))
	issue, run, tasks := createLifecycleCustomOrchestration(t, []map[string]any{
		{
			"key": "blocker", "title": "Find blocker", "stage": "plan",
			"agent_id": blockerAgentID, "max_attempts": 2,
		},
		{
			"key": "question", "title": "Need input", "stage": "dev",
			"agent_id": questionAgentID, "max_attempts": 2,
		},
	})
	if len(tasks) != 2 {
		t.Fatalf("parallel plan active tasks=%d, want 2", len(tasks))
	}
	blockerTask := lifecycleTaskByStepKey(t, run, tasks, "blocker")
	questionTask := lifecycleTaskByStepKey(t, run, tasks, "question")
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id IN ($1, $2)
`, blockerTask.ID, questionTask.ID); err != nil {
		t.Fatalf("start parallel tasks: %v", err)
	}

	blockedOutput := "Cannot implement safely.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"plan","outcome":"blocked","summary":"Required schema is missing","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":["No source-of-truth schema"],"next_actions":[]}` +
		"\n```"
	blockedResult, _ := json.Marshal(map[string]string{"output": blockedOutput})
	if _, err := testHandler.TaskService.CompleteTask(ctx, blockerTask.ID, blockedResult, "", ""); err != nil {
		t.Fatalf("complete blocker task: %v", err)
	}

	questionOutput := "Need a decision.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"dev","outcome":"waiting_input","summary":"Two API shapes are possible","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Which API shape is canonical?","target":"human","blocking":true}}` +
		"\n```"
	questionResult, _ := json.Marshal(map[string]string{"output": questionOutput})
	if _, err := testHandler.TaskService.CompleteTask(ctx, questionTask.ID, questionResult, "", ""); err != nil {
		t.Fatalf("complete question task: %v", err)
	}

	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "blocked" {
		t.Fatalf("question overwrote stronger blocker: status=%q err=%v", persistedRun.Status, err)
	}
	activeRun, err := testHandler.Queries.GetActiveOrchestrationRunForIssue(ctx, parseUUID(issue.ID))
	if err != nil || activeRun.ID != persistedRun.ID || !testHandler.orchestrationOwnsIssuePipeline(ctx, parseUUID(issue.ID)) {
		t.Fatalf("blocked run lost active pipeline ownership: run=%#v err=%v", activeRun, err)
	}
	if !testHandler.cancelActiveOrchestrationForIssue(ctx, parseUUID(issue.ID), "member", parseUUID(testUserID)) {
		t.Fatal("blocked/input run was not cancellable")
	}
	persistedRun, err = testHandler.Queries.GetOrchestrationRun(ctx, persistedRun.ID)
	if err != nil || persistedRun.Status != "cancelled" {
		t.Fatalf("cancelled run status=%q err=%v", persistedRun.Status, err)
	}
	steps, err := testHandler.Queries.ListOrchestrationSteps(ctx, persistedRun.ID)
	if err != nil || len(steps) != 2 {
		t.Fatalf("load cancelled parallel steps: len=%d err=%v", len(steps), err)
	}
	for _, step := range steps {
		if step.Status != "cancelled" {
			t.Fatalf("paused step %q survived cancellation with status %q", step.StepKey, step.Status)
		}
	}
}

func TestLateParallelCompletionIsRecordedWithoutReopeningFailedRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	failingAgentID := createHandlerTestAgent(t, "Exhausted parallel agent", []byte("[]"))
	lateAgentID := createHandlerTestAgent(t, "Late parallel agent", []byte("[]"))
	_, run, tasks := createLifecycleCustomOrchestration(t, []map[string]any{
		{
			"key": "fails", "title": "Exhaust retries", "stage": "plan",
			"agent_id": failingAgentID, "max_attempts": 1,
		},
		{
			"key": "late", "title": "Finish after failure", "stage": "dev",
			"agent_id": lateAgentID, "max_attempts": 1,
		},
	})
	if len(tasks) != 2 {
		t.Fatalf("parallel active tasks=%d, want 2", len(tasks))
	}
	failingTask := lifecycleTaskByStepKey(t, run, tasks, "fails")
	lateTask := lifecycleTaskByStepKey(t, run, tasks, "late")
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id IN ($1, $2)
`, failingTask.ID, lateTask.ID); err != nil {
		t.Fatalf("start parallel tasks: %v", err)
	}

	if _, err := testHandler.TaskService.FailTask(ctx, failingTask.ID, "implementation failed", "", "", "agent_error"); err != nil {
		t.Fatalf("fail exhausted branch: %v", err)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "failed" {
		t.Fatalf("exhausted branch did not fail run: status=%q err=%v", persistedRun.Status, err)
	}

	output := "Late work finished.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"dev","outcome":"completed","verdict":"not_applicable","summary":"Late branch result","decisions":[],"contracts":[],"artifacts":[{"kind":"commit","reference":"abc123"}],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[]}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, lateTask.ID, result, "", ""); err != nil {
		t.Fatalf("record late completion: %v", err)
	}

	persistedRun, err = testHandler.Queries.GetOrchestrationRun(ctx, persistedRun.ID)
	if err != nil || persistedRun.Status != "failed" {
		t.Fatalf("late completion reopened terminal run: status=%q err=%v", persistedRun.Status, err)
	}
	steps, err := testHandler.Queries.ListOrchestrationSteps(ctx, persistedRun.ID)
	if err != nil || len(steps) != 2 {
		t.Fatalf("load terminal steps: len=%d err=%v", len(steps), err)
	}
	statuses := map[string]string{}
	var lateStep db.OrchestrationStep
	for _, step := range steps {
		statuses[step.StepKey] = step.Status
		if step.StepKey == "late" {
			lateStep = step
		}
	}
	if statuses["fails"] != "failed" || statuses["late"] != "completed" {
		t.Fatalf("terminal sibling states are not truthful: %v", statuses)
	}
	if len(lateStep.Output) == 0 {
		t.Fatal("late sibling handoff was discarded")
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, lateStep.ID)
	if err != nil || len(messages) != 1 || messages[0].Kind != "handoff" {
		t.Fatalf("late handoff message missing: %#v err=%v", messages, err)
	}
}

func TestMissingQAVerdictCannotAdvanceRelease(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	qaAgentID := createHandlerTestAgent(t, "Missing verdict QA agent", []byte("[]"))
	_, run, tasks := createLifecycleCustomOrchestration(t, []map[string]any{
		{
			"key": "qa", "title": "Verify", "stage": "qa",
			"agent_id": qaAgentID, "max_attempts": 2,
		},
		{
			"key": "release", "title": "Release", "stage": "release",
			"depends_on_keys": []string{"qa"}, "approval_required": true,
			"max_attempts": 2,
		},
	})
	if len(tasks) != 1 {
		t.Fatalf("precondition: active tasks=%d, want only QA", len(tasks))
	}
	qaTask := lifecycleTaskByStepKey(t, run, tasks, "qa")
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, qaTask.ID); err != nil {
		t.Fatalf("start QA task: %v", err)
	}
	output := "Checks finished without a verdict.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"qa","outcome":"completed","summary":"Checks were inconclusive","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[]}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, qaTask.ID, result, "", ""); err != nil {
		t.Fatalf("complete QA task without verdict: %v", err)
	}

	steps, err := testHandler.Queries.ListOrchestrationSteps(ctx, parseUUID(run.ID))
	if err != nil || len(steps) != 2 {
		t.Fatalf("load QA/release steps: len=%d err=%v", len(steps), err)
	}
	statuses := map[string]string{}
	for _, step := range steps {
		statuses[step.StepKey] = step.Status
	}
	if statuses["qa"] != "blocked" || statuses["release"] != "pending" {
		t.Fatalf("missing verdict advanced release: statuses=%v", statuses)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "blocked" {
		t.Fatalf("missing verdict run status=%q err=%v", persistedRun.Status, err)
	}
}
