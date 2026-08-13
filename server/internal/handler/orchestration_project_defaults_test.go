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

func TestCreateIssueOrchestrationInheritsProjectDefaults(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Project-default solo agent", []byte("[]"))

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority, settings)
		VALUES ($1, $2, 'in_progress', 'none', $3::jsonb)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Orchestration defaults %d", time.Now().UnixNano()), `{
		"orchestration": {
			"execution_level": "controlled",
			"execution_strategy": "solo",
			"progression_policy": "gated",
			"max_concurrency": 5,
			"review_plan_first": true,
			"model_routing_mode": "balanced"
		}
	}`).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         fmt.Sprintf("Inherited orchestration %d", time.Now().UnixNano()),
		"project_id":    projectID,
		"assignee_type": "agent",
		"assignee_id":   agentID,
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
	if run.ExecutionStrategy != "solo" || run.ProgressionPolicy != "gated" {
		t.Fatalf("project defaults not inherited: strategy=%q progression=%q", run.ExecutionStrategy, run.ProgressionPolicy)
	}
	if run.Status != "draft" {
		t.Fatalf("review_plan_first should create a draft, got %q", run.Status)
	}
	var policy map[string]any
	if err := json.Unmarshal(run.Policy, &policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if got := policy["max_concurrency"]; got != float64(5) {
		t.Fatalf("max_concurrency = %#v, want 5", got)
	}
	taskLevel, ok := policy["task_level"].(map[string]any)
	if !ok || taskLevel["requested"] != "controlled" || taskLevel["resolved"] != "controlled" {
		t.Fatalf("execution level defaults not inherited: %#v", policy["task_level"])
	}
	modelRouting, ok := policy["model_routing"].(map[string]any)
	if !ok || modelRouting["mode"] != modelRoutingBalanced || modelRouting["router_version"] != float64(orchestrationModelRouterVersion) {
		t.Fatalf("model routing defaults not inherited: %#v", policy["model_routing"])
	}
}
