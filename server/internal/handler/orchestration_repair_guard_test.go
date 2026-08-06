package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func createManualRepairOrchestration(t *testing.T, agentID string) (IssueResponse, orchestrationRunResponse, db.AgentTaskQueue) {
	t.Helper()
	ctx := context.Background()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Manual repair orchestration %d", time.Now().UnixNano()),
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
			"progression_policy": "manual",
			"auto_start":         true,
			"steps": []map[string]any{
				{
					"key": "plan", "title": "Plan", "stage": "plan",
					"agent_id": agentID, "max_attempts": 2,
				},
				{
					"key": "dev", "title": "Implement", "stage": "dev",
					"agent_id": agentID, "depends_on_keys": []string{"plan"}, "max_attempts": 2,
				},
			},
		}),
		"id",
		issue.ID,
	)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create manual orchestration: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var run orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&run); err != nil {
		t.Fatalf("decode manual orchestration: %v", err)
	}
	if run.ProgressionPolicy != "manual" || run.Status != "running" {
		t.Fatalf("manual orchestration precondition: policy=%q status=%q", run.ProgressionPolicy, run.Status)
	}

	tasks, err := testHandler.Queries.ListActiveTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("initial manual batch: tasks=%d err=%v", len(tasks), err)
	}
	return issue, run, tasks[0]
}

func createProjectedStatusRepairOrchestration(t *testing.T, status string) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	stamp := time.Now().UnixNano()
	waitingAgentID := createHandlerTestAgent(t, fmt.Sprintf("Projected repair waiting %s %d", status, stamp), []byte("[]"))
	predecessorAgentID := createHandlerTestAgent(t, fmt.Sprintf("Projected repair predecessor %s %d", status, stamp), []byte("[]"))
	dependentAgentID := createHandlerTestAgent(t, fmt.Sprintf("Projected repair dependent %s %d", status, stamp), []byte("[]"))
	_, run, tasks := createLifecycleCustomOrchestration(t, []map[string]any{
		{
			"key": "waiting", "title": "Waiting sibling", "stage": "plan",
			"agent_id": waitingAgentID, "max_attempts": 2,
		},
		{
			"key": "predecessor", "title": "Completed predecessor", "stage": "dev",
			"agent_id": predecessorAgentID, "max_attempts": 2,
		},
		{
			"key": "dependent", "title": "Ready dependent", "stage": "dev",
			"agent_id": dependentAgentID, "depends_on_keys": []string{"predecessor"}, "max_attempts": 2,
		},
	})
	if len(tasks) != 2 {
		t.Fatalf("initial projected-state branches: tasks=%d, want 2", len(tasks))
	}
	waitingTask := lifecycleTaskByStepKey(t, run, tasks, "waiting")
	predecessorTask := lifecycleTaskByStepKey(t, run, tasks, "predecessor")
	dependentStepID := pgtype.UUID{}
	for _, step := range run.Steps {
		if step.Key == "dependent" {
			dependentStepID = parseUUID(step.ID)
			break
		}
	}
	if !dependentStepID.Valid {
		t.Fatal("dependent step was not persisted")
	}

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', result = '{"output":"persisted before dispatch"}'::jsonb, completed_at = now()
		WHERE id IN ($1, $2)
	`, waitingTask.ID, predecessorTask.ID); err != nil {
		t.Fatalf("seed projected-state terminal tasks: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_step
		SET status = $2, output = jsonb_build_object('outcome', $2::text), completed_at = NULL, updated_at = now()
		WHERE id = $1
	`, waitingTask.OrchestrationStepID, status); err != nil {
		t.Fatalf("seed %s sibling: %v", status, err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_step
		SET status = 'completed', output = '{"outcome":"completed"}'::jsonb,
		    completed_at = now(), updated_at = now()
		WHERE id = $1
	`, predecessorTask.OrchestrationStepID); err != nil {
		t.Fatalf("seed completed predecessor: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_run SET status = $2, updated_at = now() WHERE id = $1
	`, parseUUID(run.ID), status); err != nil {
		t.Fatalf("project run status %s: %v", status, err)
	}

	return parseUUID(run.ID), dependentStepID
}

func TestProjectedActiveRunRepairScansDispatchReadyDependencies(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	for _, scan := range []string{"runnable", "completed"} {
		for _, status := range []string{"waiting_input", "blocked", "waiting_approval"} {
			t.Run(scan+"/"+status, func(t *testing.T) {
				runID, dependentStepID := createProjectedStatusRepairOrchestration(t, status)
				ctx := context.Background()
				persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, runID)
				if err != nil || persistedRun.Status != status {
					t.Fatalf("projected run precondition: status=%q err=%v", persistedRun.Status, err)
				}

				repaired := 0
				if scan == "runnable" {
					repaired = testHandler.TaskService.ReconcileRunnableOrchestrationRuns(ctx)
				} else {
					repaired = testHandler.TaskService.ReconcileTerminalOrchestrationTasks(ctx)
				}
				if repaired != 1 {
					t.Fatalf("%s repair count in %s run=%d, want 1", scan, status, repaired)
				}
				dependent, err := testHandler.Queries.GetOrchestrationStep(ctx, dependentStepID)
				if err != nil || dependent.Status != "queued" || !dependent.TaskID.Valid {
					t.Fatalf("%s scan did not dispatch ready dependent from %s run: step=%#v err=%v", scan, status, dependent, err)
				}
			})
		}
	}
}

func TestTerminalFinalStepReplayRepairsRunningRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Terminal repair agent", []byte("[]"))
	_, run, task := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()

	// Reproduce a crash after the task and final step committed but before the
	// run-level reducer advanced the still-running orchestration.
	result := []byte(`{"output":"completed before callback"}`)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', result = $2, completed_at = now()
		WHERE id = $1
	`, task.ID, result); err != nil {
		t.Fatalf("seed terminal task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_step
		SET status = 'completed', output = $2, completed_at = now(), updated_at = now()
		WHERE id = $1
	`, task.OrchestrationStepID, []byte(`{"outcome":"completed"}`)); err != nil {
		t.Fatalf("seed completed final step: %v", err)
	}

	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "running" {
		t.Fatalf("repair precondition: status=%q err=%v", persistedRun.Status, err)
	}
	if repaired := testHandler.TaskService.ReconcileTerminalOrchestrationTasks(ctx); repaired != 1 {
		t.Fatalf("terminal repair count=%d, want 1", repaired)
	}
	persistedRun, err = testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "completed" {
		t.Fatalf("terminal replay did not finish run: status=%q err=%v", persistedRun.Status, err)
	}
	if repaired := testHandler.TaskService.ReconcileTerminalOrchestrationTasks(ctx); repaired != 0 {
		t.Fatalf("completed run remained in terminal repair scan: count=%d", repaired)
	}
}

func TestManualRunnableRepairRequiresDurableAuthorization(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Manual batch repair agent", []byte("[]"))
	issue, run, firstTask := createManualRepairOrchestration(t, agentID)
	ctx := context.Background()

	// Reproduce the post-batch durable state without invoking the terminal
	// callback: the predecessor is complete and its successor is now runnable.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', result = '{"output":"done"}'::jsonb, completed_at = now()
		WHERE id = $1
	`, firstTask.ID); err != nil {
		t.Fatalf("complete first manual task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_step
		SET status = 'completed', output = '{"outcome":"completed"}'::jsonb,
		    completed_at = now(), updated_at = now()
		WHERE id = $1
	`, firstTask.OrchestrationStepID); err != nil {
		t.Fatalf("complete first manual step: %v", err)
	}
	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil {
		t.Fatalf("load manual run: %v", err)
	}
	if _, err = setManualOrchestrationDispatchAuthorization(ctx, testHandler.Queries, persistedRun, false); err != nil {
		t.Fatalf("close manual batch boundary: %v", err)
	}

	if repaired := testHandler.TaskService.ReconcileTerminalOrchestrationTasks(ctx); repaired != 0 {
		t.Fatalf("unauthorized manual completion was replayed: count=%d", repaired)
	}
	if repaired := testHandler.TaskService.ReconcileRunnableOrchestrationRuns(ctx); repaired != 0 {
		t.Fatalf("unauthorized manual batch was repaired: count=%d", repaired)
	}
	steps, err := testHandler.Queries.ListOrchestrationSteps(ctx, parseUUID(run.ID))
	if err != nil || len(steps) != 2 {
		t.Fatalf("load manual steps: len=%d err=%v", len(steps), err)
	}
	if steps[0].Status != "completed" || steps[1].Status != "pending" || steps[1].TaskID.Valid {
		t.Fatalf("sweeper crossed closed manual boundary: first=%q second=%#v", steps[0].Status, steps[1])
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("closed manual boundary enqueued work: tasks=%d err=%v", len(tasks), err)
	}

	persistedRun, err = testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil {
		t.Fatalf("reload manual run: %v", err)
	}
	if _, err = setManualOrchestrationDispatchAuthorization(ctx, testHandler.Queries, persistedRun, true); err != nil {
		t.Fatalf("authorize manual dispatch repair: %v", err)
	}
	if repaired := testHandler.TaskService.ReconcileRunnableOrchestrationRuns(ctx); repaired != 1 {
		t.Fatalf("authorized manual repair count=%d, want 1", repaired)
	}
	steps, err = testHandler.Queries.ListOrchestrationSteps(ctx, parseUUID(run.ID))
	if err != nil || len(steps) != 2 {
		t.Fatalf("reload manual steps: len=%d err=%v", len(steps), err)
	}
	if steps[1].Status != "queued" || !steps[1].TaskID.Valid {
		t.Fatalf("authorized manual successor was not dispatched: %#v", steps[1])
	}
	tasks, err = testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(tasks) != 2 {
		t.Fatalf("authorized repair task lineage: tasks=%d err=%v", len(tasks), err)
	}
}

func TestTaggedAnswerCommentRepairAfterCommentCommit(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Tagged answer repair agent", []byte("[]"))
	issue, run, originalTask := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET stage = 'plan', capability = 'coordination' WHERE id = $1`, originalTask.OrchestrationStepID); err != nil {
		t.Fatalf("prepare planning step: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, originalTask.ID); err != nil {
		t.Fatalf("start planning task: %v", err)
	}
	output := "Need a durable answer.\n```agora-handoff\n" +
		`{"schema_version":1,"stage":"plan","outcome":"waiting_input","summary":"A contract choice is required","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Which contract should be used?","target":"human","blocking":true}}` +
		"\n```"
	result, _ := json.Marshal(map[string]string{"output": output})
	if _, err := testHandler.TaskService.CompleteTask(ctx, originalTask.ID, result, "repair-answer-session", ""); err != nil {
		t.Fatalf("persist orchestration question: %v", err)
	}
	step, err := testHandler.Queries.GetOrchestrationStep(ctx, originalTask.OrchestrationStepID)
	if err != nil || step.Status != "waiting_input" {
		t.Fatalf("repair precondition step: status=%q err=%v", step.Status, err)
	}

	// Reproduce a process exit after CreateComment committed but before
	// respondToOrchestrationQuestionFromComment began its transaction. The
	// comment and delivery marker are the atomic durable outbox record the
	// periodic repair consumes.
	answerContent := "[@Repair agent](mention://agent/" + agentID + ") Use the versioned contract."
	persistedIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("load answer issue: %v", err)
	}
	triggers := testHandler.computeOrchestrationAnswerCommentTrigger(ctx, persistedIssue, answerContent, "member", testUserID)
	if len(triggers) != 1 || triggers[0].StepID != step.ID {
		t.Fatalf("answer outbox trigger mismatch: %#v", triggers)
	}
	comment, err := testHandler.createCommentWithOrchestrationAnswerOutbox(ctx, db.CreateCommentParams{
		IssueID: parseUUID(issue.ID), WorkspaceID: parseUUID(testWorkspaceID),
		AuthorType: "member", AuthorID: parseUUID(testUserID), Content: answerContent, Type: "comment",
	}, &triggers[0])
	if err != nil {
		t.Fatalf("commit answer comment before simulated failure: %v", err)
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 1 || messages[0].Kind != "question" {
		t.Fatalf("answer unexpectedly persisted before repair: messages=%#v err=%v", messages, err)
	}
	events, err := testHandler.Queries.ListOrchestrationEvents(ctx, parseUUID(run.ID))
	if err != nil {
		t.Fatalf("load answer outbox event: %v", err)
	}
	var outboxQuestionID string
	for _, event := range events {
		if event.Kind != orchestrationCommentAnswerOutboxEventKind {
			continue
		}
		var details map[string]any
		if json.Unmarshal(event.Details, &details) == nil && details["comment_id"] == uuidToString(comment.ID) {
			outboxQuestionID, _ = details["question_id"].(string)
		}
	}
	if outboxQuestionID != uuidToString(messages[0].ID) {
		t.Fatalf("answer outbox question_id=%q, want %q", outboxQuestionID, uuidToString(messages[0].ID))
	}

	if repaired := testHandler.TaskService.ReconcileOrchestrationAnswerComments(ctx); repaired != 1 {
		t.Fatalf("tagged answer repair count=%d, want 1", repaired)
	}
	answer, err := testHandler.Queries.GetOrchestrationMessageByIdempotencyKey(ctx, db.GetOrchestrationMessageByIdempotencyKeyParams{
		RunID: parseUUID(run.ID), IdempotencyKey: "comment-answer:" + uuidToString(comment.ID),
	})
	if err != nil || answer.StepID != step.ID || answer.ActorID != comment.AuthorID {
		t.Fatalf("durable repaired answer mismatch: answer=%#v err=%v", answer, err)
	}
	step, err = testHandler.Queries.GetOrchestrationStep(ctx, step.ID)
	if err != nil || step.Status != "queued" {
		t.Fatalf("repaired answer did not resume the same step: status=%q err=%v", step.Status, err)
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil || len(tasks) != 2 {
		t.Fatalf("repaired answer continuation tasks=%d err=%v", len(tasks), err)
	}

	if repaired := testHandler.TaskService.ReconcileOrchestrationAnswerComments(ctx); repaired != 0 {
		t.Fatalf("idempotent answer was repaired twice: count=%d", repaired)
	}
	messages, err = testHandler.Queries.ListOrchestrationStepMessages(ctx, step.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("idempotent repair duplicated messages: messages=%d err=%v", len(messages), err)
	}
}

func TestClarificationLimitBlocksFourthQuestion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Clarification limit agent", []byte("[]"))
	issue, run, task := createActiveCustomOrchestration(t, agentID)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE orchestration_step
		SET stage = 'plan', capability = 'coordination'
		WHERE id = $1
	`, task.OrchestrationStepID); err != nil {
		t.Fatalf("prepare planning step: %v", err)
	}

	for round := 1; round <= maxOrchestrationClarificationRounds+1; round++ {
		if _, err := testPool.Exec(ctx, `
			UPDATE agent_task_queue SET status = 'running', started_at = COALESCE(started_at, now())
			WHERE id = $1 AND status = 'queued'
		`, task.ID); err != nil {
			t.Fatalf("start clarification round %d: %v", round, err)
		}
		output := "Need another decision.\n```agora-handoff\n" +
			fmt.Sprintf(`{"schema_version":1,"stage":"plan","outcome":"waiting_input","summary":"Round %d remains ambiguous","decisions":[],"contracts":[],"artifacts":[],"verification":[],"findings":[],"risks":[],"blockers":[],"next_actions":[],"question":{"prompt":"Clarify round %d?","target":"human","blocking":true}}`, round, round) +
			"\n```"
		result, _ := json.Marshal(map[string]string{"output": output})
		if _, err := testHandler.TaskService.CompleteTask(ctx, task.ID, result, fmt.Sprintf("clarification-session-%d", round), ""); err != nil {
			t.Fatalf("complete clarification round %d: %v", round, err)
		}

		step, err := testHandler.Queries.GetOrchestrationStep(ctx, task.OrchestrationStepID)
		if err != nil {
			t.Fatalf("load step after clarification round %d: %v", round, err)
		}
		questionCount, err := testHandler.Queries.CountOrchestrationStepQuestions(ctx, step.ID)
		if err != nil {
			t.Fatalf("count questions after round %d: %v", round, err)
		}

		if round == maxOrchestrationClarificationRounds+1 {
			if step.Status != "blocked" {
				t.Fatalf("fourth clarification status=%q, want blocked", step.Status)
			}
			if questionCount != int64(maxOrchestrationClarificationRounds) {
				t.Fatalf("persisted question count=%d, want capped at %d", questionCount, maxOrchestrationClarificationRounds)
			}
			if !strings.Contains(string(step.Output), "Clarification limit reached") || strings.Contains(string(step.Output), `"question":{`) {
				t.Fatalf("blocked handoff did not remove the fourth question: %s", step.Output)
			}
			break
		}

		if step.Status != "waiting_input" || questionCount != int64(round) {
			t.Fatalf("clarification round %d: status=%q questions=%d", round, step.Status, questionCount)
		}
		question, err := testHandler.Queries.GetLatestOpenOrchestrationQuestion(ctx, step.ID)
		if err != nil {
			t.Fatalf("load clarification question %d: %v", round, err)
		}
		w := httptest.NewRecorder()
		req := withURLParams(
			newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/steps/"+uuidToString(step.ID)+"/respond", map[string]any{
				"question_id": uuidToString(question.ID),
				"message":     fmt.Sprintf("Answer for round %d", round),
			}),
			"id", issue.ID, "stepId", uuidToString(step.ID),
		)
		testHandler.RespondToOrchestrationStep(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("respond to clarification round %d: expected 202, got %d: %s", round, w.Code, w.Body.String())
		}
		nextTask, err := testHandler.Queries.GetLatestTaskForOrchestrationStep(ctx, step.ID)
		if err != nil || nextTask.ID == task.ID || nextTask.Status != "queued" {
			t.Fatalf("clarification round %d continuation: task=%#v err=%v", round, nextTask, err)
		}
		task = nextTask
	}

	persistedRun, err := testHandler.Queries.GetOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil || persistedRun.Status != "blocked" {
		t.Fatalf("clarification limit did not block run: status=%q err=%v", persistedRun.Status, err)
	}
	messages, err := testHandler.Queries.ListOrchestrationStepMessages(ctx, task.OrchestrationStepID)
	if err != nil {
		t.Fatalf("list bounded clarification messages: %v", err)
	}
	questions, answers, blockers := 0, 0, 0
	for _, message := range messages {
		switch message.Kind {
		case "question":
			questions++
		case "answer":
			answers++
		case "blocker":
			blockers++
		}
	}
	if questions != maxOrchestrationClarificationRounds || answers != maxOrchestrationClarificationRounds || blockers != 1 {
		t.Fatalf("clarification audit log: questions=%d answers=%d blockers=%d messages=%#v", questions, answers, blockers, messages)
	}
}
