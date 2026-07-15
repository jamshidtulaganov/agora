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
)

// Squad execution is a run-level choice: the issue can remain assigned to an
// individual while the run explicitly selects a capability roster. Before this
// regression, the API accepted "squad" without a squad id and silently built a
// single-worker serial plan from the issue assignee.
func TestCreateIssueOrchestrationUsesExplicitSquadRoster(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "Orchestration squad leader", []byte("[]"))
	backendID := createHandlerTestAgent(t, "Backend engineer", []byte("[]"))
	frontendID := createHandlerTestAgent(t, "Frontend engineer", []byte("[]"))
	qaID := createHandlerTestAgent(t, "QA engineer", []byte("[]"))
	reviewerID := createHandlerTestAgent(t, "Security reviewer", []byte("[]"))
	var offlineRuntimeID, offlineAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at, owner_id
		)
		VALUES ($1, NULL, $2, 'cloud', 'codex', 'offline', $3, '{}'::jsonb, now(), $4)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Offline orchestration runtime %d", time.Now().UnixNano()), "Offline test runtime", testUserID).Scan(&offlineRuntimeID); err != nil {
		t.Fatalf("create offline runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, $2, 'Mobile engineer', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Offline mobile engineer %d", time.Now().UnixNano()), offlineRuntimeID, testUserID).Scan(&offlineAgentID); err != nil {
		t.Fatalf("create offline squad agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, offlineAgentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, offlineRuntimeID)
	})

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, 'Capability-aware orchestration test roster', $3, $4)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Orchestration squad %d", time.Now().UnixNano()), leaderID, testUserID).Scan(&squadID); err != nil {
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
		{reviewerID, "Security reviewer"},
		{offlineAgentID, "Mobile engineer"},
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
		"title": fmt.Sprintf("Explicit squad orchestration %d", time.Now().UnixNano()),
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
			"execution_strategy": "squad",
			"squad_id":           squadID,
			"auto_start":         false,
		}),
		"id",
		issue.ID,
	)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create squad orchestration: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var run orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&run); err != nil {
		t.Fatalf("decode orchestration: %v", err)
	}
	if run.OwnerType != "squad" || run.OwnerID != squadID || run.ControllerAgentID != leaderID {
		t.Fatalf("explicit squad routing was not persisted: owner=%s/%s controller=%s", run.OwnerType, run.OwnerID, run.ControllerAgentID)
	}
	if len(run.Steps) != 7 {
		t.Fatalf("capability-aware plan has %d steps, want 7: %#v", len(run.Steps), run.Steps)
	}
	byKey := make(map[string]orchestrationStepResponse, len(run.Steps))
	for _, step := range run.Steps {
		byKey[step.Key] = step
	}
	if byKey["dev-backend"].AgentID != backendID || byKey["dev-frontend"].AgentID != frontendID {
		t.Fatalf("specialist branches were not routed from selected squad: backend=%#v frontend=%#v", byKey["dev-backend"], byKey["dev-frontend"])
	}
	if _, exists := byKey["dev-mobile"]; exists {
		t.Fatalf("offline squad member was scheduled into the plan: %#v", byKey["dev-mobile"])
	}
	if len(byKey["integrate"].DependsOnStepIDs) != 2 {
		t.Fatalf("integration did not join both development branches: %#v", byKey["integrate"])
	}
	if run.Status != "draft" || len(run.Events) != 1 || run.Events[0].Kind != "plan_proposed" {
		t.Fatalf("review-first run must persist a draft proposal event: status=%s events=%#v", run.Status, run.Events)
	}

	patchPlan := func(body map[string]any) (int, orchestrationRunResponse, string) {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(
			newRequest(http.MethodPatch, "/api/issues/"+issue.ID+"/orchestration", body),
			"id",
			issue.ID,
		)
		testHandler.EditIssueOrchestration(w, req)
		var edited orchestrationRunResponse
		if w.Code == http.StatusOK {
			if err := json.NewDecoder(w.Body).Decode(&edited); err != nil {
				t.Fatalf("decode edited orchestration: %v", err)
			}
		}
		return w.Code, edited, w.Body.String()
	}

	// Membership alone is not enough: a frontend-only worker cannot take a
	// backend branch. Rejected proposals must not consume a plan version.
	code, _, body := patchPlan(map[string]any{
		"expected_version": 1,
		"reason":           "Move API work to the web specialist",
		"operation":        "reroute",
		"step_id":          byKey["dev-backend"].ID,
		"agent_id":         frontendID,
	})
	if code != http.StatusBadRequest || !strings.Contains(body, "not compatible with backend") {
		t.Fatalf("incompatible reroute: expected 400, got %d: %s", code, body)
	}

	// The controller is the explicit recovery route and may accept any pending
	// capability. The accepted reroute is a durable v2 revision.
	code, run, body = patchPlan(map[string]any{
		"expected_version": 1,
		"reason":           "Leader will recover the API branch",
		"operation":        "reroute",
		"step_id":          byKey["dev-backend"].ID,
		"agent_id":         leaderID,
	})
	if code != http.StatusOK {
		t.Fatalf("controller reroute: expected 200, got %d: %s", code, body)
	}
	byKey = make(map[string]orchestrationStepResponse, len(run.Steps))
	for _, step := range run.Steps {
		byKey[step.Key] = step
	}
	if run.PlanVersion != 2 || byKey["dev-backend"].AgentID != leaderID || len(run.Revisions) != 1 || run.Revisions[0].ActorType != "member" {
		t.Fatalf("accepted reroute was not versioned: version=%d step=%#v revisions=%#v", run.PlanVersion, byKey["dev-backend"], run.Revisions)
	}

	// A structural proposal edit inserts new development work before the join
	// and makes integration depend on it. It cannot become an orphan branch.
	code, run, body = patchPlan(map[string]any{
		"expected_version": 2,
		"reason":           "Add deployment configuration to the proposal",
		"operation":        "add_child",
		"step_id":          byKey["plan"].ID,
		"child": map[string]any{
			"key":        "dev-infrastructure",
			"title":      "Prepare deployment configuration",
			"stage":      "dev",
			"kind":       "task",
			"capability": "infrastructure",
			"agent_id":   leaderID,
		},
	})
	if code != http.StatusOK {
		t.Fatalf("add proposal child: expected 200, got %d: %s", code, body)
	}
	byKey = make(map[string]orchestrationStepResponse, len(run.Steps))
	for _, step := range run.Steps {
		byKey[step.Key] = step
	}
	child := byKey["dev-infrastructure"]
	join := byKey["integrate"]
	if run.PlanVersion != 3 || child.ID == "" || child.Position >= join.Position || len(join.DependsOnStepIDs) != 3 {
		t.Fatalf("new proposal branch must be ordered before and joined by integration: version=%d child=%#v join=%#v", run.PlanVersion, child, join)
	}

	// Optimistic versioning prevents a stale proposal editor from overwriting
	// the accepted v3 graph.
	code, _, body = patchPlan(map[string]any{
		"expected_version": 2,
		"reason":           "Stale reroute",
		"operation":        "reroute",
		"step_id":          byKey["dev-frontend"].ID,
		"agent_id":         leaderID,
	})
	if code != http.StatusConflict || !strings.Contains(body, "plan version changed") {
		t.Fatalf("stale proposal edit: expected 409, got %d: %s", code, body)
	}
}
