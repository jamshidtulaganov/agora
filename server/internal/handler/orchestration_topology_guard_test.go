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

func seedTopologyRuntime(t *testing.T, suffix string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, $2, $3, 'cloud', 'codex', 'online', '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, "topology-daemon-"+suffix, "Topology runtime "+suffix).Scan(&runtimeID); err != nil {
		t.Fatalf("seed topology runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

func createTopologyIssue(t *testing.T) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": fmt.Sprintf("Artifact topology %d", time.Now().UnixNano()),
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create topology issue: %d %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode topology issue: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID) })
	return issue
}

func withTopologyWorkspaceRepo(t *testing.T) {
	t.Helper()
	var original []byte
	if err := testPool.QueryRow(context.Background(), `SELECT repos FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&original); err != nil {
		t.Fatalf("load workspace repos: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET repos = $2::jsonb WHERE id = $1`, testWorkspaceID,
		`[{"url":"https://github.com/example/agora-topology.git"}]`); err != nil {
		t.Fatalf("seed workspace repo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE workspace SET repos = $2::jsonb WHERE id = $1`, testWorkspaceID, original)
	})
}

func TestGitArtifactTopologyRejectsCrossDaemonCreation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withTopologyWorkspaceRepo(t)
	secondRuntime := seedTopologyRuntime(t, "create")
	firstAgent := createHandlerTestAgent(t, "Topology first agent", []byte("[]"))
	secondAgent := createHandlerTestAgent(t, "Topology second agent", []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, secondAgent, secondRuntime); err != nil {
		t.Fatalf("move second agent: %v", err)
	}
	issue := createTopologyIssue(t)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom", "auto_start": false,
		"steps": []map[string]any{
			{"key": "first", "title": "First", "stage": "dev", "agent_id": firstAgent},
			{"key": "second", "title": "Second", "stage": "dev", "agent_id": secondAgent, "depends_on_keys": []string{"first"}},
		},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("cross-daemon plan accepted: %d %s", w.Code, w.Body.String())
	}
	var runs int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM orchestration_run WHERE issue_id = $1`, issue.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("rejected topology left a partial run: count=%d err=%v", runs, err)
	}
}

func TestGitArtifactTopologyRejectsCrossDaemonRerouteAndRebind(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withTopologyWorkspaceRepo(t)
	foreignRuntime := seedTopologyRuntime(t, "edit")
	workerID := createHandlerTestAgent(t, "Topology pinned worker", []byte("[]"))
	controllerID := createHandlerTestAgent(t, "Topology moved controller", []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, controllerID, foreignRuntime); err != nil {
		t.Fatalf("move controller: %v", err)
	}
	issue := createTopologyIssue(t)
	metadata, _ := json.Marshal(map[string]string{"orchestrator_agent_id": controllerID})
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET metadata = $2 WHERE id = $1`, issue.ID, metadata); err != nil {
		t.Fatalf("set issue controller: %v", err)
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"execution_strategy": "custom", "auto_start": false,
		"steps": []map[string]any{{"key": "work", "title": "Work", "stage": "dev", "agent_id": workerID}},
	}), "id", issue.ID)
	testHandler.CreateIssueOrchestration(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create pinned plan: %d %s", w.Code, w.Body.String())
	}
	var run orchestrationRunResponse
	if err := json.NewDecoder(w.Body).Decode(&run); err != nil || len(run.Steps) != 1 {
		t.Fatalf("decode pinned plan: steps=%d err=%v", len(run.Steps), err)
	}

	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPatch, "/api/issues/"+issue.ID+"/orchestration", map[string]any{
		"expected_version": 1, "reason": "move work", "operation": "reroute",
		"step_id": run.Steps[0].ID, "agent_id": controllerID,
	}), "id", issue.ID)
	testHandler.EditIssueOrchestration(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("cross-daemon reroute accepted: %d %s", w.Code, w.Body.String())
	}

	// Rebinding the already-admitted worker is also caught at dispatch. The
	// pending step stays unqueued, so moving it back (or a safe reroute) can
	// recover without certifying a different checkout.
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET runtime_id = $2 WHERE id = $1`, workerID, foreignRuntime); err != nil {
		t.Fatalf("rebind worker: %v", err)
	}
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issues/"+issue.ID+"/orchestration/start", nil), "id", issue.ID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("cross-daemon rebound worker dispatched: %d %s", w.Code, w.Body.String())
	}
	var activeTasks int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status IN ('queued', 'dispatched', 'running')
	`, issue.ID).Scan(&activeTasks); err != nil || activeTasks != 0 {
		t.Fatalf("rebound topology queued work: count=%d err=%v", activeTasks, err)
	}
}
