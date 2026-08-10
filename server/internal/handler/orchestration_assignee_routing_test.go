package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Agent-assigned issues self-orchestrate (solo). Membership in a squad must
// not pull that squad's multi-model roster into the run — that path is for
// squad assignees (and explicit Customize → squad + squad_id).
func TestCreateIssueOrchestrationAgentAssigneeStaysSolo(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "Solo-path squad leader", []byte("[]"))
	workerID := createHandlerTestAgent(t, "Solo-path squad worker", []byte("[]"))
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET model = $2, thinking_level = $3, max_concurrent_tasks = 2 WHERE id = $1
	`, workerID, "claude-sonnet-5", "high"); err != nil {
		t.Fatalf("configure worker model: %v", err)
	}

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, 'Must not be used for agent-assigned solo runs', $3, $4)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Solo-path squad %d", time.Now().UnixNano()), leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID) })
	for _, memberID := range []string{leaderID, workerID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO squad_member (squad_id, member_type, member_id, role)
			VALUES ($1, 'agent', $2, 'Backend engineer')
		`, squadID, memberID); err != nil {
			t.Fatalf("add squad member: %v", err)
		}
	}

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority, settings, squad_id)
		VALUES ($1, $2, 'in_progress', 'none', $3::jsonb, $4)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Solo assignee project %d", time.Now().UnixNano()), `{
		"orchestration": {
			"execution_strategy": "squad",
			"progression_policy": "automatic",
			"max_concurrency": 4,
			"review_plan_first": true
		}
	}`, squadID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         fmt.Sprintf("Agent-assigned orchestration %d", time.Now().UnixNano()),
		"project_id":    projectID,
		"assignee_type": "agent",
		"assignee_id":   workerID,
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
		newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{}),
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
	if run.ExecutionStrategy != "solo" {
		t.Fatalf("agent assignee must resolve to solo, got %q", run.ExecutionStrategy)
	}
	if run.ControllerAgentID != workerID || run.OwnerType != "agent" || run.OwnerID != workerID {
		t.Fatalf("solo run must stay with the assignee agent: owner=%s/%s controller=%s", run.OwnerType, run.OwnerID, run.ControllerAgentID)
	}
	if len(run.Steps) != 2 || run.Steps[0].Key != "work" || run.Steps[0].AgentID != workerID {
		t.Fatalf("solo plan must be work→release on the assignee: %#v", run.Steps)
	}
	if run.Steps[0].Model != "claude-sonnet-5" || run.Steps[0].ThinkingLevel == nil || *run.Steps[0].ThinkingLevel != "high" {
		t.Fatalf("solo work must pin the assignee model+thinking: %#v", run.Steps[0])
	}
	if run.Steps[1].ThinkingLevel == nil || *run.Steps[1].ThinkingLevel != "high" {
		t.Fatalf("parked release must snapshot its future controller thinking: %#v", run.Steps[1])
	}
	var policy map[string]any
	if err := json.Unmarshal(run.Policy, &policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if _, hasRoster := policy["squad_roster"]; hasRoster {
		t.Fatalf("solo agent path must not attach a squad multi-model roster: %#v", policy)
	}

	// Mutating the shared agent after plan creation must not change the queued
	// execution contract.
	if _, err := testPool.Exec(ctx, `UPDATE agent SET model = 'changed-later', thinking_level = 'low' WHERE id = $1`, workerID); err != nil {
		t.Fatalf("mutate agent after plan creation: %v", err)
	}
	if err := testHandler.TaskService.CancelTasksForIssue(ctx, parseUUID(issue.ID)); err != nil {
		t.Fatalf("clear ordinary assignment task before direct scheduler fixture: %v", err)
	}
	dbRun, err := testHandler.Queries.StartOrchestrationRun(ctx, parseUUID(run.ID))
	if err != nil {
		t.Fatalf("start solo run: %v", err)
	}
	dbIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("load solo issue: %v", err)
	}
	dbStep, err := testHandler.Queries.GetOrchestrationStep(ctx, parseUUID(run.Steps[0].ID))
	if err != nil {
		t.Fatalf("load solo work step: %v", err)
	}
	task, _, err := testHandler.queueOrchestrationStepAtomically(ctx, dbRun.ID, dbIssue, dbStep, "", 1)
	if err != nil {
		t.Fatalf("queue solo work: %v", err)
	}
	if !task.ModelOverride.Valid || task.ModelOverride.String != "claude-sonnet-5" ||
		!task.ThinkingLevelOverride.Valid || task.ThinkingLevelOverride.String != "high" {
		t.Fatalf("queued task lost the step execution snapshot: %#v", task)
	}
}

// Squad-assigned issues fan out across that squad's ready agents, pinning each
// agent's model and recording role / think mode / concurrency on the run.
func TestCreateIssueOrchestrationSquadAssigneeUsesMultiModelRoster(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "Multi-model squad leader", []byte("[]"))
	backendID := createHandlerTestAgent(t, "Multi-model backend", []byte("[]"))
	frontendID := createHandlerTestAgent(t, "Multi-model frontend", []byte("[]"))
	qaID := createHandlerTestAgent(t, "Multi-model qa", []byte("[]"))

	for _, cfg := range []struct {
		id       string
		model    string
		thinking string
		slots    int
	}{
		{leaderID, "claude-opus-4", "high", 1},
		{backendID, "claude-sonnet-5", "low", 1},
		{frontendID, "gpt-5", "medium", 2},
		{qaID, "claude-haiku", "", 1},
	} {
		if _, err := testPool.Exec(ctx, `
			UPDATE agent
			SET model = $2, thinking_level = NULLIF($3, ''), max_concurrent_tasks = $4
			WHERE id = $1
		`, cfg.id, cfg.model, cfg.thinking, cfg.slots); err != nil {
			t.Fatalf("configure agent %s: %v", cfg.id, err)
		}
	}

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, 'Multi-model orchestration roster', $3, $4)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Multi-model squad %d", time.Now().UnixNano()), leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID) })

	for _, member := range []struct {
		id   string
		role string
	}{
		{leaderID, "leader"},
		{backendID, "Backend engineer"},
		{frontendID, "Frontend engineer"},
		{qaID, "QA engineer"},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO squad_member (squad_id, member_type, member_id, role)
			VALUES ($1, 'agent', $2, $3)
		`, squadID, member.id, member.role); err != nil {
			t.Fatalf("add squad member %s: %v", member.role, err)
		}
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         fmt.Sprintf("Squad-assigned orchestration %d", time.Now().UnixNano()),
		"assignee_type": "squad",
		"assignee_id":   squadID,
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
			"auto_start": false,
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
	if run.ExecutionStrategy != "squad" || run.OwnerType != "squad" || run.OwnerID != squadID {
		t.Fatalf("squad assignee routing wrong: strategy=%s owner=%s/%s", run.ExecutionStrategy, run.OwnerType, run.OwnerID)
	}

	byKey := make(map[string]orchestrationStepResponse, len(run.Steps))
	for _, step := range run.Steps {
		byKey[step.Key] = step
	}
	if byKey["dev-backend"].AgentID != backendID || byKey["dev-backend"].Model != "claude-sonnet-5" {
		t.Fatalf("backend branch must pin role+model: %#v", byKey["dev-backend"])
	}
	if byKey["dev-frontend"].AgentID != frontendID || byKey["dev-frontend"].Model != "gpt-5" {
		t.Fatalf("frontend branch must pin role+model: %#v", byKey["dev-frontend"])
	}
	if byKey["qa"].AgentID != qaID || byKey["qa"].Model != "claude-haiku" {
		t.Fatalf("qa branch must pin role+model: %#v", byKey["qa"])
	}
	if byKey["plan"].Model != "claude-opus-4" {
		t.Fatalf("plan step must pin leader model, got %q", byKey["plan"].Model)
	}
	for key, want := range map[string]string{
		"plan": "high", "dev-backend": "low", "dev-frontend": "medium", "qa": "",
	} {
		if byKey[key].ThinkingLevel == nil || *byKey[key].ThinkingLevel != want {
			t.Fatalf("step %s thinking pin = %#v, want %q", key, byKey[key].ThinkingLevel, want)
		}
	}

	var policy map[string]any
	if err := json.Unmarshal(run.Policy, &policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	rosterRaw, ok := policy["squad_roster"].([]any)
	if !ok || len(rosterRaw) < 4 {
		t.Fatalf("expected squad_roster with ready members, got %#v", policy["squad_roster"])
	}
	byAgent := map[string]map[string]any{}
	for _, entry := range rosterRaw {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["agent_id"].(string)
		byAgent[id] = row
	}
	frontend := byAgent[frontendID]
	if frontend["model"] != "gpt-5" || frontend["thinking_level"] != "medium" || frontend["max_concurrent_tasks"] != float64(2) {
		t.Fatalf("roster lost frontend multi-model contract: %#v", frontend)
	}
	if frontend["role"] != "Frontend engineer" || frontend["capability"] != "frontend" {
		t.Fatalf("roster lost frontend role/capability: %#v", frontend)
	}
	if got := policy["max_concurrency"]; got != float64(2) {
		// Global per-agent capacity does not create extra same-issue slots.
		t.Fatalf("max_concurrency = %#v, want 2 distinct parallel agents", got)
	}
	if got := policy["parallel_workers"]; got != float64(2) {
		t.Fatalf("parallel_workers = %#v, want 2 development specialists", got)
	}
}
