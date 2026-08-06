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

func createOrchestrationAccessIssue(t *testing.T, userID string, extra map[string]any) IssueResponse {
	t.Helper()
	body := map[string]any{
		"title": fmt.Sprintf("Orchestration access guard %d", time.Now().UnixNano()),
	}
	for key, value := range extra {
		body[key] = value
	}
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, body)
	if userID != "" {
		req = newRequestAs(userID, http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, body)
	}
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID) })
	return issue
}

func TestCreateOrchestrationRejectsPrivateEffectiveRoute(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	privateAgentID, _, memberID := privateAgentTestFixture(t)
	issue := createOrchestrationAccessIssue(t, memberID, nil)

	w := httptest.NewRecorder()
	req := withURLParam(newRequestAs(memberID, http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         false,
		"steps": []map[string]any{{
			"key": "dev", "title": "Private implementation", "stage": "dev", "agent_id": privateAgentID,
		}},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("private orchestration route: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if run, err := testHandler.Queries.GetLatestOrchestrationRunForIssue(context.Background(), parseUUID(issue.ID)); err == nil || run.ID.Valid {
		t.Fatalf("private route created a run: run=%#v err=%v", run, err)
	}
}

func TestEditAndStartOrchestrationRejectPrivateRouteForUnrelatedMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	privateAgentID, _, memberID := privateAgentTestFixture(t)
	publicAgentID := createHandlerTestAgent(t, "Orchestration public access route", []byte("[]"))

	// A plain member cannot inject a private target into somebody else's draft.
	issue := createOrchestrationAccessIssue(t, "", nil)
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         false,
		"steps": []map[string]any{{
			"key": "dev", "title": "Public implementation", "stage": "dev", "agent_id": publicAgentID,
		}},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public draft: %d %s", w.Code, w.Body.String())
	}
	var publicRun orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&publicRun); err != nil || len(publicRun.Steps) != 1 {
		t.Fatalf("decode public draft: run=%#v err=%v", publicRun, err)
	}
	w = httptest.NewRecorder()
	req = withURLParam(newRequestAs(memberID, http.MethodPatch, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"expected_version": publicRun.PlanVersion,
		"reason":           "try private worker",
		"operation":        "reroute",
		"step_id":          publicRun.Steps[0].ID,
		"agent_id":         privateAgentID,
	}), "id", issue.ID)
	testHandler.EditIssueOrchestration(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("private reroute: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	persisted, err := testHandler.Queries.GetOrchestrationStep(context.Background(), parseUUID(publicRun.Steps[0].ID))
	if err != nil || uuidToString(persisted.AgentID) != publicAgentID {
		t.Fatalf("rejected reroute mutated step: step=%#v err=%v", persisted, err)
	}

	// Even when an owner legitimately created a private draft, another member
	// cannot turn it into executable work through Continue/Start.
	privateIssue := createOrchestrationAccessIssue(t, "", nil)
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issues/"+privateIssue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         false,
		"steps": []map[string]any{{
			"key": "dev", "title": "Authorized private draft", "stage": "dev", "agent_id": privateAgentID,
		}},
	}), "id", privateIssue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("owner create private draft: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req = withURLParam(newRequestAs(memberID, http.MethodPost, "/api/issues/"+privateIssue.ID+"/orchestration/start", nil), "id", privateIssue.ID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("private draft start: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	tasks, err := testHandler.Queries.ListActiveTasksByIssue(context.Background(), parseUUID(privateIssue.ID))
	if err != nil || len(tasks) != 0 {
		t.Fatalf("rejected private start queued work: tasks=%#v err=%v", tasks, err)
	}
}

func TestCreateOrchestrationEnforcesProjectSquadWorkforce(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	leaderID := createHandlerTestAgent(t, "Bound project squad leader", []byte("[]"))
	outsideAgentID := createHandlerTestAgent(t, "Outside bound project squad", []byte("[]"))
	var squadID, projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Bound orchestration squad %d", time.Now().UnixNano()), leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create bound squad: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority, squad_id)
		VALUES ($1, $2, 'in_progress', 'none', $3) RETURNING id
	`, testWorkspaceID, fmt.Sprintf("Bound orchestration project %d", time.Now().UnixNano()), squadID).Scan(&projectID); err != nil {
		t.Fatalf("create bound project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID)
	})
	issue := createOrchestrationAccessIssue(t, "", map[string]any{"project_id": projectID})
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom",
		"auto_start":         false,
		"steps": []map[string]any{{
			"key": "dev", "title": "Outside workforce", "stage": "dev", "agent_id": outsideAgentID,
		}},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("outside project squad route: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if run, err := testHandler.Queries.GetLatestOrchestrationRunForIssue(ctx, parseUUID(issue.ID)); err == nil || run.ID.Valid {
		t.Fatalf("outside workforce route created a run: run=%#v err=%v", run, err)
	}
}
