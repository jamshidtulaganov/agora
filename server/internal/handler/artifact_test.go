package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func artifactTestStep(t *testing.T, id, kind, status, integrationStatus, base, head string) db.OrchestrationStep {
	t.Helper()
	states, _ := json.Marshal([]RepoGitStateResponse{{
		Repo: "app", Branch: "agent/app", BaseSHA: base, HeadSHA: head, MergeStatus: "clean",
	}})
	return db.OrchestrationStep{
		ID: orchestrationTestUUID(t, id), StepKey: kind, Title: kind,
		Stage: "dev", Status: status, StepKind: kind, Capability: "implementation",
		MergeStatus: "clean", IntegrationStatus: integrationStatus, GitStates: states,
		CompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: status == "completed"},
	}
}

func TestSelectCanonicalArtifactWaitsForIntegration(t *testing.T) {
	base := strings.Repeat("a", 40)
	workerHead := strings.Repeat("b", 40)
	integrationHead := strings.Repeat("c", 40)
	worker := artifactTestStep(t, "10000000-0000-4000-8000-000000000001", "task", "completed", "not_required", base, workerHead)
	pendingIntegration := artifactTestStep(t, "10000000-0000-4000-8000-000000000002", "integration", "running", "pending", base, integrationHead)
	pendingIntegration.Capability = "integration"

	if artifact, ok := selectCanonicalArtifact("run", []db.OrchestrationStep{worker, pendingIntegration}); ok {
		t.Fatalf("worker branch became canonical before integration: %+v", artifact)
	}
	pendingIntegration.Status = "completed"
	pendingIntegration.IntegrationStatus = "complete"
	if artifact, ok := selectCanonicalArtifact("run", []db.OrchestrationStep{worker, pendingIntegration}); !ok || artifact.StepID != uuidToString(pendingIntegration.ID) || !artifact.Canonical {
		t.Fatalf("completed integration was not canonical: ok=%v artifact=%+v", ok, artifact)
	}
}

func TestSelectCanonicalArtifactSupportsSoloRun(t *testing.T) {
	step := artifactTestStep(t, "20000000-0000-4000-8000-000000000001", "task", "completed", "not_required", strings.Repeat("a", 40), strings.Repeat("b", 40))
	artifact, ok := selectCanonicalArtifact("run", []db.OrchestrationStep{step})
	if !ok || artifact.StepID != uuidToString(step.ID) {
		t.Fatalf("solo development artifact not selected: ok=%v artifact=%+v", ok, artifact)
	}
}

func TestArtifactCapabilityIsSignedPurposeBoundAndExpiring(t *testing.T) {
	token, err := mintArtifactCapability(artifactCapabilityRecord{
		ArtifactID: "artifact", Purpose: "changes", WorkspaceID: "workspace",
		SourceRoot: "/private/path", Repos: []artifactRepoRef{{Repo: "app", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), MergeStatus: "clean"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, ok := lookupArtifactCapability(token)
	if !ok || record.Purpose != "changes" || record.SourceRoot != "/private/path" {
		t.Fatalf("minted capability did not round-trip: ok=%v record=%+v", ok, record)
	}
	// Tamper a middle character: every one of its base64 bits is significant,
	// so the decoded payload always changes. The FINAL character has unused
	// low bits — flipping it can decode to identical bytes and verify.
	mid := len(token) / 2
	replacement := byte('0')
	if token[mid] == replacement {
		replacement = '1'
	}
	tampered := token[:mid] + string(replacement) + token[mid+1:]
	if _, ok := lookupArtifactCapability(tampered); ok {
		t.Fatal("tampered capability verified")
	}

	expiredToken, err := sealArtifactCapability(artifactCapabilityRecord{
		ID: "expired", ArtifactID: "artifact", Purpose: "changes", WorkspaceID: "workspace",
		SourceRoot: "/private/path", ExpiresAt: time.Now().Add(-time.Second),
		Repos: []artifactRepoRef{{Repo: "app", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), MergeStatus: "clean"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupArtifactCapability(expiredToken); ok {
		t.Fatal("expired capability verified")
	}
}

func TestVerifyArtifactCapabilityBindsDaemonIdentity(t *testing.T) {
	token, err := mintArtifactCapability(artifactCapabilityRecord{
		ArtifactID: "artifact", Purpose: "file", WorkspaceID: testWorkspaceID,
		DaemonID: "daemon-a", SourceRoot: "/private/path",
		Repos: []artifactRepoRef{{Repo: "app", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), MergeStatus: "clean"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(daemonID, purpose string) *http.Request {
		req := newRequest(http.MethodPost, "/api/daemon/artifact-capabilities/verify", map[string]string{"token": token, "purpose": purpose})
		return req.WithContext(middleware.WithDaemonContext(req.Context(), testWorkspaceID, daemonID))
	}

	w := httptest.NewRecorder()
	testHandler.VerifyArtifactCapability(w, request("daemon-a", "file"))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), token) {
		t.Fatalf("bound daemon verification failed: status=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	testHandler.VerifyArtifactCapability(w, request("daemon-b", "file"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong daemon status=%d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	testHandler.VerifyArtifactCapability(w, request("daemon-a", "changes"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong purpose status=%d, want 403", w.Code)
	}
}

func TestGetIssueArtifactDoesNotExposeWorkdir(t *testing.T) {
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Artifact integration agent", nil)
	var issueID, runID, stepID, taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id)
		VALUES ($1, 'artifact API boundary', 'member', $2)
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_run (workspace_id, issue_id, status, created_by)
		VALUES ($1, $2, 'running', $3) RETURNING id::text
	`, testWorkspaceID, issueID, testUserID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	gitStates, _ := json.Marshal([]RepoGitStateResponse{{Repo: "app", Branch: "agent/integration", BaseSHA: base, HeadSHA: head, MergeStatus: "clean"}})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_step
			(run_id, step_key, title, stage, status, position, agent_id, step_kind, capability,
			 merge_status, integration_status, git_states, base_sha, head_sha, completed_at)
		VALUES ($1, 'integrate', 'Integrate', 'dev', 'completed', 0, $2, 'integration', 'integration',
		        'clean', 'complete', $3, $4, $5, now())
		RETURNING id::text
	`, runID, agentID, gitStates, base, head).Scan(&stepID); err != nil {
		t.Fatal(err)
	}
	const secretWorkdir = "/tmp/agora-canonical-secret-workdir"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue
			(agent_id, runtime_id, issue_id, status, work_dir, orchestration_step_id, completed_at)
		VALUES ($1, $2, $3, 'completed', $4, $5, now())
		RETURNING id::text
	`, agentID, handlerTestRuntimeID(t), issueID, secretWorkdir, stepID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_step SET task_id = $1 WHERE id = $2`, taskID, stepID); err != nil {
		t.Fatal(err)
	}

	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/artifact", nil), "id", issueID)
	w := httptest.NewRecorder()
	testHandler.GetIssueArtifact(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("artifact endpoint status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), secretWorkdir) {
		t.Fatalf("artifact response leaked raw workdir: %s", w.Body.String())
	}
	var response issueArtifactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Ready || response.Artifact == nil || response.Artifact.ID == "" || len(response.Capabilities) != 4 {
		t.Fatalf("artifact response incomplete: %+v", response)
	}
	for purpose, token := range response.Capabilities {
		record, ok := lookupArtifactCapability(token)
		if !ok || record.Purpose != purpose || record.SourceRoot != secretWorkdir || record.ArtifactID != response.Artifact.ID {
			t.Fatalf("capability %q is not bound to the exact hidden artifact: ok=%v record=%+v", purpose, ok, record)
		}
	}
}

// TestGetIssueArtifactLiveLocalFallback: an issue whose project has a
// local_directory on an ONLINE daemon and no orchestration artifact serves a
// LIVE artifact (working tree) with a preview grant carrying the dev-server URL.
func TestGetIssueArtifactLiveLocalFallback(t *testing.T) {
	ctx := context.Background()
	const daemonID = "d-live-local-test"
	const localPath = "/Users/dev/my-project"
	const previewURL = "http://localhost:5173"

	// Online runtime for the daemon that owns the folder.
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, daemon_id, metadata, last_seen_at)
		VALUES ($1, 'Live Local RT', 'local', 'claude', 'online', $2, '{"editor_port":"7080"}'::jsonb, now())
		RETURNING id::text`, testWorkspaceID, daemonID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	// Project + local_directory resource pinned to that daemon, with a preview URL.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, 'Live Local') RETURNING id::text
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	refJSON, _ := json.Marshal(map[string]any{"local_path": localPath, "daemon_id": daemonID, "preview_url": previewURL})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref)
		VALUES ($1, $2, 'local_directory', $3)`, projectID, testWorkspaceID, refJSON); err != nil {
		t.Fatal(err)
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_type, creator_id, project_id)
		VALUES ($1, 'live local issue', 'member', $2, $3) RETURNING id::text
	`, testWorkspaceID, testUserID, projectID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/artifact", nil), "id", issueID)
	w := httptest.NewRecorder()
	testHandler.GetIssueArtifact(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), localPath) {
		t.Fatalf("live-local response leaked raw local_path: %s", w.Body.String())
	}
	var response issueArtifactResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Ready || response.RunStatus != "live" || response.Artifact == nil || response.Artifact.Kind != "local_directory" {
		t.Fatalf("expected a ready live artifact, got %+v", response)
	}
	if len(response.Capabilities) != 4 {
		t.Fatalf("expected 4 capabilities, got %d", len(response.Capabilities))
	}
	rec, ok := lookupArtifactCapability(response.Capabilities["preview"])
	if !ok {
		t.Fatal("preview capability did not verify")
	}
	if !rec.Live || rec.SourceRoot != localPath || rec.PreviewURL != previewURL || rec.DaemonID != daemonID {
		t.Fatalf("preview grant not a live-local grant: %+v", rec)
	}
}

// Offline daemon => no live surface; the endpoint falls through to the
// standard "no orchestration run" response.
func TestGetIssueArtifactLiveLocalOfflineDaemonFallsThrough(t *testing.T) {
	ctx := context.Background()
	var projectID, issueID string
	if err := testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'Offline Local') RETURNING id::text`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	refJSON, _ := json.Marshal(map[string]any{"local_path": "/Users/dev/off", "daemon_id": "d-offline-none"})
	testPool.Exec(ctx, `INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref) VALUES ($1, $2, 'local_directory', $3)`, projectID, testWorkspaceID, refJSON)
	if err := testPool.QueryRow(ctx, `INSERT INTO issue (workspace_id, title, creator_type, creator_id, project_id) VALUES ($1, 'offline local', 'member', $2, $3) RETURNING id::text`, testWorkspaceID, testUserID, projectID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/artifact", nil), "id", issueID)
	w := httptest.NewRecorder()
	testHandler.GetIssueArtifact(w, req)
	var response issueArtifactResponse
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.Ready || response.Reason != "no orchestration run" {
		t.Fatalf("offline daemon should fall through, got %+v", response)
	}
}
