package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---- pure tests ------------------------------------------------------------

func TestRequiredGatesWithReview(t *testing.T) {
	full := reviewTierForLabels(map[string]bool{})
	light := reviewTierForLabels(map[string]bool{"tier:light": true})
	trivial := reviewTierForLabels(map[string]bool{"tier:trivial": true})

	tests := []struct {
		name          string
		tier          reviewTier
		reviewRequire bool
		want          []string
	}{
		{"full tier with review required appends review", full, true, []string{"ci", "qa", "review"}},
		{"full tier without review required omits review", full, false, []string{"ci", "qa"}},
		{"light tier never appends review", light, false, []string{"ci"}},
		{"trivial tier never appends review", trivial, false, []string{"ci"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiredGatesWithReview(tt.tier, tt.reviewRequire); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("requiredGatesWithReview(%s, %v) = %v, want %v", tt.tier.name, tt.reviewRequire, got, tt.want)
			}
		})
	}

	// The append must never mutate the tier's own required slice.
	if !reflect.DeepEqual(full.required, []string{"ci", "qa"}) {
		t.Errorf("full.required mutated to %v", full.required)
	}
}

// TestReviewGateRequired covers the coupling fix: the review gate is required
// only for a full-tier issue that has a diff to review AND an active review
// (auto-review enabled OR a manual verdict landed). Flag off + no manual
// verdict ⇒ advisory, never a silent blocker.
func TestReviewGateRequired(t *testing.T) {
	full := reviewTierForLabels(map[string]bool{})
	light := reviewTierForLabels(map[string]bool{"tier:light": true})

	// The 4th arg is the resolved (project-scoped) auto-review flag, passed
	// explicitly now that reviewGateRequired is pure (the caller resolves it).
	t.Run("auto-review off, no verdict, with PR: not required (advisory)", func(t *testing.T) {
		if reviewGateRequired(full, true, map[string]bool{}, false) {
			t.Error("review gate must be advisory when auto-review is off and no manual verdict exists")
		}
	})
	t.Run("auto-review on, with PR: required", func(t *testing.T) {
		if !reviewGateRequired(full, true, map[string]bool{}, true) {
			t.Error("review gate must be required when auto-review is enabled on a full-tier PR issue")
		}
	})
	t.Run("auto-review off but manual verdict present: required", func(t *testing.T) {
		if !reviewGateRequired(full, true, map[string]bool{"review:fail": true}, false) {
			t.Error("a landed review verdict must make the gate required even with auto-review off")
		}
	})
	t.Run("manual verdict present, no PR detected: still required", func(t *testing.T) {
		if !reviewGateRequired(full, false, map[string]bool{"review:pass": true}, false) {
			t.Error("a landed verdict proves a review happened even without a detectable PR")
		}
	})
	t.Run("no PR and no verdict: not required", func(t *testing.T) {
		if reviewGateRequired(full, false, map[string]bool{}, true) {
			t.Error("no diff to review (no PR, no verdict) ⇒ gate not required")
		}
	})
	t.Run("light tier never required", func(t *testing.T) {
		if reviewGateRequired(light, true, map[string]bool{"review:fail": true}, true) {
			t.Error("non-full tiers never require the review gate")
		}
	})
}

func TestIssuePRNumberFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"number", `{"pr_number": 42}`, 42},
		{"numeric string", `{"pr_number": "17"}`, 17},
		{"absent", `{"bitrix_task_id": "9"}`, 0},
		{"garbage string", `{"pr_number": "not-a-number"}`, 0},
		{"empty metadata", ``, 0},
		{"malformed json", `{oops`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := issuePRNumberFromMetadata([]byte(tt.raw)); got != tt.want {
				t.Errorf("issuePRNumberFromMetadata(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// ---- DB-backed fixtures ------------------------------------------------------

// seedReviewDecisionIssue creates an issue in the shared test workspace,
// optionally metadata-stamped and agent-assigned.
func seedReviewDecisionIssue(t *testing.T, title, status, assigneeAgentID, metadata string) db.Issue {
	t.Helper()
	ctx := context.Background()
	if metadata == "" {
		metadata = "{}"
	}
	var issueID string
	if assigneeAgentID == "" {
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,number,metadata)
			VALUES ($1,$2,$3,'member',$4,(SELECT COALESCE(MAX(number),0)+1 FROM issue WHERE workspace_id=$1),$5::jsonb)
			RETURNING id`,
			testWorkspaceID, title, status, testUserID, metadata).Scan(&issueID); err != nil {
			t.Fatalf("seed issue: %v", err)
		}
	} else {
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,assignee_type,assignee_id,number,metadata)
			VALUES ($1,$2,$3,'member',$4,'agent',$5,(SELECT COALESCE(MAX(number),0)+1 FROM issue WHERE workspace_id=$1),$6::jsonb)
			RETURNING id`,
			testWorkspaceID, title, status, testUserID, assigneeAgentID, metadata).Scan(&issueID); err != nil {
			t.Fatalf("seed issue: %v", err)
		}
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1::uuid`, issueID)
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue
}

// seedReviewSquad creates a dev squad whose leader differs from the author
// agent, with both as agent members. Returns (leaderID, authorID).
func seedReviewSquad(t *testing.T, name string) (string, string) {
	t.Helper()
	ctx := context.Background()
	leaderID := createHandlerTestAgent(t, name+" Leader", nil)
	authorID := createHandlerTestAgent(t, name+" Author", nil)
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4) RETURNING id`,
		testWorkspaceID, name, leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1::uuid`, squadID)
	})
	for _, aid := range []string{leaderID, authorID} {
		if _, err := testPool.Exec(ctx, `INSERT INTO squad_member (squad_id, member_type, member_id) VALUES ($1,'agent',$2)`, squadID, aid); err != nil {
			t.Fatalf("add squad member: %v", err)
		}
	}
	return leaderID, authorID
}

func issueComments(t *testing.T, issue db.Issue) []db.Comment {
	t.Helper()
	comments, err := testHandler.Queries.ListCommentsForIssue(context.Background(), db.ListCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 200,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	return comments
}

type reviewCorrectionFixture struct {
	RunID      string
	WorkStepID string
	BaseStepID string
	ReleaseID  string
}

// seedReviewCorrectionRun models the moment at which a human is reviewing an
// exact integrated artifact: development, integration, QA, and review are
// complete; release is waiting for the human gate.
func seedReviewCorrectionRun(t *testing.T, issue db.Issue, agentID string) reviewCorrectionFixture {
	t.Helper()
	ctx := context.Background()
	fixture := reviewCorrectionFixture{}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, issue_id, status, created_by, execution_strategy,
			progression_policy, owner_type, owner_id, controller_agent_id
		)
		VALUES ($1, $2, 'waiting_approval', $3, 'solo', 'automatic', 'agent', $4, $4)
		RETURNING id::text
	`, testWorkspaceID, uuidToString(issue.ID), testUserID, agentID).Scan(&fixture.RunID); err != nil {
		t.Fatalf("seed review orchestration run: %v", err)
	}
	baseSHA := strings.Repeat("a", 40)
	workSHA := strings.Repeat("b", 40)
	integrationSHA := strings.Repeat("c", 40)
	workGit, _ := json.Marshal([]RepoGitStateResponse{{
		Repo: "app", Branch: "agent/work", BaseSHA: baseSHA, HeadSHA: workSHA, MergeStatus: "clean",
	}})
	integrationGit, _ := json.Marshal([]RepoGitStateResponse{{
		Repo: "app", Branch: "agent/integration", BaseSHA: baseSHA, HeadSHA: integrationSHA, MergeStatus: "clean",
	}})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_step (
			run_id, step_key, title, stage, status, position, agent_id,
			step_kind, capability, merge_status, git_states, base_sha, head_sha, completed_at
		)
		VALUES ($1, 'work', 'Implement original change', 'dev', 'completed', 0, $2,
			'task', 'implementation', 'clean', $3, $4, $5, now())
		RETURNING id::text
	`, fixture.RunID, agentID, workGit, baseSHA, workSHA).Scan(&fixture.WorkStepID); err != nil {
		t.Fatalf("seed completed work step: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_step (
			run_id, step_key, title, stage, status, position, agent_id,
			depends_on_step_id, step_kind, capability, merge_status,
			integration_status, git_states, base_sha, head_sha, completed_at
		)
		VALUES ($1, 'integrate', 'Integrate original change', 'dev', 'completed', 1, $2,
			$3, 'integration', 'integration', 'clean', 'complete', $4, $5, $6, now())
		RETURNING id::text
	`, fixture.RunID, agentID, fixture.WorkStepID, integrationGit, baseSHA, integrationSHA).Scan(&fixture.BaseStepID); err != nil {
		t.Fatalf("seed completed integration step: %v", err)
	}
	stepID := func(key, title, stage, status string, position int, dependency string, approval bool) string {
		var id string
		var routedAgent any = agentID
		capability := stage
		if stage == "release" {
			routedAgent = nil
		}
		if err := testPool.QueryRow(ctx, `
			INSERT INTO orchestration_step (
				run_id, step_key, title, stage, status, position, agent_id,
				depends_on_step_id, approval_required, step_kind, capability,
				controller_agent_id, completed_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'task', $10, $11,
				CASE WHEN $5 = 'completed' THEN now() ELSE NULL END)
			RETURNING id::text
		`, fixture.RunID, key, title, stage, status, position, routedAgent, dependency, approval, capability, agentID).Scan(&id); err != nil {
			t.Fatalf("seed %s step: %v", stage, err)
		}
		return id
	}
	qaID := stepID("qa", "Verify original integration", "qa", "completed", 2, fixture.BaseStepID, false)
	reviewID := stepID("review", "Review original integration", "review", "completed", 3, fixture.BaseStepID, false)
	fixture.ReleaseID = stepID("release", "Approve original release", "release", "waiting_approval", 4, qaID, true)
	for _, dependency := range [][2]string{
		{fixture.BaseStepID, fixture.WorkStepID},
		{qaID, fixture.BaseStepID},
		{reviewID, fixture.BaseStepID},
		{fixture.ReleaseID, qaID},
		{fixture.ReleaseID, reviewID},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO orchestration_step_dependency (step_id, depends_on_step_id)
			VALUES ($1, $2)
		`, dependency[0], dependency[1]); err != nil {
			t.Fatalf("seed step dependency: %v", err)
		}
	}
	return fixture
}

// ---- review-decision endpoint ------------------------------------------------

// TestCreateReviewDecisionMachineActorForbidden asserts the route-level
// human-only guard: a task-token actor gets 403 before the handler runs.
func TestCreateReviewDecisionMachineActorForbidden(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issue := seedReviewDecisionIssue(t, "review 403 issue", "in_review", "", "")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/review-decision", map[string]any{
		"action": "approve",
	})
	req = withURLParam(req, "id", uuidToString(issue.ID))
	req.Header.Set("X-Actor-Source", "task_token")
	RequireHumanActor(http.HandlerFunc(testHandler.CreateReviewDecision)).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("machine actor: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateReviewDecisionApproveGateViolations(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	seedRelease := func(t *testing.T, title, metadata string) db.Issue {
		t.Helper()
		agentID := createHandlerTestAgent(t, title+" release worker", nil)
		issue := seedReviewDecisionIssue(t, title, "in_review", agentID, metadata)
		seedReviewCorrectionRun(t, issue, agentID)
		return issue
	}

	post := func(issue db.Issue, body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/review-decision", body)
		req = withURLParam(req, "id", uuidToString(issue.ID))
		testHandler.CreateReviewDecision(w, req)
		return w
	}

	t.Run("unknown action is 400", func(t *testing.T) {
		issue := seedReviewDecisionIssue(t, "review 400 action", "in_review", "", "")
		if w := post(issue, map[string]any{"action": "merge"}); w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("approve with no gates passing is 409", func(t *testing.T) {
		issue := seedRelease(t, "review 409 no gates", "")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "merge_gates_not_satisfied") {
			t.Fatalf("expected 409 merge_gates_not_satisfied, got %d: %s", w.Code, w.Body.String())
		}
	})

	// ci:fail must block approve — the spine requires ci at every tier, which
	// the old bespoke qa/review-only check missed.
	t.Run("approve with ci:fail is 409", func(t *testing.T) {
		issue := seedRelease(t, "review 409 ci fail", "")
		attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
		attachTestLabel(t, uuidToString(issue.ID), "ci:fail")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ci") {
			t.Fatalf("expected 409 mentioning ci, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("approve with review:fail is 409", func(t *testing.T) {
		issue := seedRelease(t, "review 409 fail", "")
		attachTestLabel(t, uuidToString(issue.ID), "ci:pass")
		attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
		attachTestLabel(t, uuidToString(issue.ID), "review:fail")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "review") {
			t.Fatalf("expected 409 mentioning review, got %d: %s", w.Code, w.Body.String())
		}
	})

	// The review gate applies (auto-review on + a PR) but no verdict has landed
	// yet → approve must 409 rather than merge past a pending review.
	t.Run("approve with review gate applying but no verdict is 409", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		issue := seedRelease(t, "review 409 pending", `{"pr_number": 11}`)
		attachTestLabel(t, uuidToString(issue.ID), "ci:pass")
		attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "review") {
			t.Fatalf("expected 409 for a pending review gate, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("merge:override bypasses the gates", func(t *testing.T) {
		issue := seedReviewDecisionIssue(t, "review override bypass", "in_review", "", "")
		agentID := createHandlerTestAgent(t, "Review override release", nil)
		seedReviewCorrectionRun(t, issue, agentID)
		attachTestLabel(t, uuidToString(issue.ID), "merge:override")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 with merge:override, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestCreateReviewDecisionApproveDispatch: a green-gated compatibility approve
// stamps merge:approved and queues the persisted release worker without a
// mention-triggered second scheduler.
func TestCreateReviewDecisionApproveDispatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, authorID := seedReviewSquad(t, "Review Approve Squad")
	issue := seedReviewDecisionIssue(t, "review approve dispatch", "in_review", authorID, "")
	fixture := seedReviewCorrectionRun(t, issue, authorID)
	// Green the full readiness spine: ci + qa + review (review:pass makes the
	// review gate apply for this full-tier issue).
	attachTestLabel(t, uuidToString(issue.ID), "ci:pass")
	attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
	attachTestLabel(t, uuidToString(issue.ID), "review:pass")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/review-decision", map[string]any{
		"action": "approve", "note": "ship it",
	})
	req = withURLParam(req, "id", uuidToString(issue.ID))
	testHandler.CreateReviewDecision(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Action         string `json:"action"`
		MergedDispatch bool   `json:"merged_dispatch"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Action != "approve" || !resp.MergedDispatch {
		t.Fatalf("response = %+v, want approve + merged_dispatch=true", resp)
	}

	if !testHandler.issueHasLabel(context.Background(), issue, "merge:approved") {
		t.Error("merge:approved label not attached")
	}

	for _, c := range issueComments(t, issue) {
		if strings.Contains(c.Content, "mention://agent/") {
			t.Fatalf("release approval created a mention side scheduler: %s", c.Content)
		}
	}
	var releaseStatus, releaseAgentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, agent_id::text FROM orchestration_step WHERE id=$1
	`, fixture.ReleaseID).Scan(&releaseStatus, &releaseAgentID); err != nil {
		t.Fatal(err)
	}
	if releaseStatus != "queued" || releaseAgentID != authorID {
		t.Fatalf("release = %s/%s, want queued/%s", releaseStatus, releaseAgentID, authorID)
	}
	var queued int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_task_queue
		WHERE orchestration_step_id=$1 AND agent_id=$2 AND status IN ('queued','dispatched','running')
	`, fixture.ReleaseID, authorID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("persisted release tasks = %d, want 1", queued)
	}
}

func TestApproveOrchestrationReleaseRechecksReadiness(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Release readiness agent", nil)
	issue := seedReviewDecisionIssue(t, "release readiness", "in_review", agentID, "")
	fixture := seedReviewCorrectionRun(t, issue, agentID)
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/orchestration/steps/"+fixture.ReleaseID+"/approve", nil)
		req = withURLParams(req, "id", uuidToString(issue.ID), "stepId", fixture.ReleaseID)
		testHandler.ApproveOrchestrationStep(w, req)
		return w
	}
	if w := post(); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "merge_gates_not_satisfied") {
		t.Fatalf("unready release: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	attachTestLabel(t, uuidToString(issue.ID), "ci:pass")
	attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
	attachTestLabel(t, uuidToString(issue.ID), "review:pass")
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("ready release: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateReviewDecisionRequestChanges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	authorID := createHandlerTestAgent(t, "Review RC Author", nil)
	issue := seedReviewDecisionIssue(t, "review request changes", "in_review", authorID, "")
	fixture := seedReviewCorrectionRun(t, issue, authorID)
	attachTestLabel(t, uuidToString(issue.ID), "review:fail")

	post := func(body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/review-decision", body)
		req = withURLParam(req, "id", uuidToString(issue.ID))
		testHandler.CreateReviewDecision(w, req)
		return w
	}

	// Empty note → 400.
	if w := post(map[string]any{"action": "request_changes", "note": "  "}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty note: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w := post(map[string]any{
		"action": "request_changes", "note": "fix the nil deref in the handler",
		"expected_version": 1, "target_step_id": fixture.WorkStepID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Action           string `json:"action"`
		PlanVersion      int32  `json:"plan_version"`
		RevisionID       string `json:"revision_id"`
		CorrectionStepID string `json:"correction_step_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Action != "request_changes" || response.PlanVersion != 2 || response.RevisionID == "" || response.CorrectionStepID == "" {
		t.Fatalf("unexpected revision response: %+v", response)
	}

	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1::uuid`, uuidToString(issue.ID)).Scan(&status)
	if status != "in_progress" {
		t.Errorf("issue status = %q, want in_progress", status)
	}

	foundAudit := false
	for _, c := range issueComments(t, issue) {
		if strings.Contains(c.Content, "mention://agent/") {
			t.Fatalf("request changes created a second mention scheduler: %s", c.Content)
		}
		if strings.Contains(c.Content, "Plan revised to v2") && strings.Contains(c.Content, "fix the nil deref") {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Error("no inert plan-revision audit comment was written")
	}

	var runVersion int32
	if err := testPool.QueryRow(context.Background(), `SELECT plan_version FROM orchestration_run WHERE id=$1`, fixture.RunID).Scan(&runVersion); err != nil {
		t.Fatal(err)
	}
	if runVersion != 2 {
		t.Fatalf("run plan_version = %d, want 2", runVersion)
	}
	var retiredStatus string
	var retiredVersion int32
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, retired_in_version FROM orchestration_step WHERE id=$1
	`, fixture.ReleaseID).Scan(&retiredStatus, &retiredVersion); err != nil {
		t.Fatal(err)
	}
	if retiredStatus != "skipped" || retiredVersion != 2 {
		t.Fatalf("old release = %s/v%d, want skipped/v2", retiredStatus, retiredVersion)
	}
	rows, err := testPool.Query(context.Background(), `
		SELECT step_key, stage, status, introduced_in_version
		FROM orchestration_step WHERE run_id=$1 AND introduced_in_version=2 ORDER BY position
	`, fixture.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var revised []string
	for rows.Next() {
		var key, stage, stepStatus string
		var version int32
		if err := rows.Scan(&key, &stage, &stepStatus, &version); err != nil {
			t.Fatal(err)
		}
		revised = append(revised, key+":"+stage+":"+stepStatus)
	}
	if want := []string{"changes-v2:dev:queued", "integrate-v2:dev:pending", "qa-v2:qa:pending", "review-v2:review:pending", "release-v2:release:pending"}; !reflect.DeepEqual(revised, want) {
		t.Fatalf("revision DAG = %#v, want %#v", revised, want)
	}
	var baseDependency string
	if err := testPool.QueryRow(context.Background(), `
		SELECT depends_on_step_id::text FROM orchestration_step_dependency WHERE step_id=$1
	`, response.CorrectionStepID).Scan(&baseDependency); err != nil {
		t.Fatal(err)
	}
	if baseDependency != fixture.BaseStepID {
		t.Fatalf("correction base = %s, want exact integration %s", baseDependency, fixture.BaseStepID)
	}

	// review:fail is deliberately KEPT until a re-review replaces it.
	if !testHandler.issueHasLabel(context.Background(), issue, "review:fail") {
		t.Error("review:fail must survive request_changes")
	}
}

// TestRequestChangesNeutralizesNoteMentions covers finding 9: a mention link
// smuggled into the human note must NOT survive as a live trigger in the
// request-changes comment (which is itself a mention-trigger comment).
func TestRequestChangesNeutralizesNoteMentions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	authorID := createHandlerTestAgent(t, "RC Note Author", nil)
	evilID := createHandlerTestAgent(t, "RC Note Evil", nil)
	issue := seedReviewDecisionIssue(t, "review rc note mention", "in_review", authorID, "")
	seedReviewCorrectionRun(t, issue, authorID)

	note := "please fix, also [@evil](mention://agent/" + evilID + ") must not be pinged"
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/review-decision", map[string]any{
		"action": "request_changes", "note": note,
	})
	req = withURLParam(req, "id", uuidToString(issue.ID))
	testHandler.CreateReviewDecision(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	for _, c := range issueComments(t, issue) {
		if strings.Contains(c.Content, "](mention://agent/"+evilID) {
			t.Fatalf("the note's smuggled mention survived as a live trigger: %.200s", c.Content)
		}
	}
}

func TestBuildReviewCorrectionPlanRejectsActiveWork(t *testing.T) {
	run := db.OrchestrationRun{ControllerAgentID: parseUUID("11111111-1111-4111-8111-111111111111")}
	steps := []db.OrchestrationStep{{Status: "running", Stage: "dev", StepKind: "task"}}
	if _, err := buildReviewCorrectionPlan(run, steps, ""); err == nil || !strings.Contains(err.Error(), "active orchestration work") {
		t.Fatalf("running work must block a revision, got %v", err)
	}
}

// TestResolveSliceActionAgentRunReviewNeverAuthor covers finding 12: a manual
// run_review must never resolve to the AUTHOR agent — it resolves to a distinct
// reviewer or refuses (409), consistent with the capture-time self-review
// rejection.
func TestResolveSliceActionAgentRunReviewNeverAuthor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	// A one-agent squad where the author IS the leader.
	authorID := createHandlerTestAgent(t, "Solo RR Author", nil)
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'Solo RR Squad', '', $2, $3) RETURNING id`,
		testWorkspaceID, authorID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1::uuid`, squadID) })
	if _, err := testPool.Exec(ctx, `INSERT INTO squad_member (squad_id, member_type, member_id) VALUES ($1,'agent',$2)`, squadID, authorID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	issue := seedReviewDecisionIssue(t, "run_review no reviewer", "in_review", authorID, "")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+uuidToString(issue.ID)+"/slice-actions", nil), "id", uuidToString(issue.ID))
	agent, ok := testHandler.resolveSliceActionAgent(w, req, issue, testUserID, "", sliceActionRunReview)
	if ok {
		// If a distinct reviewer resolved (e.g. a QA leader in the shared
		// workspace), it must NOT be the author.
		if uuidToString(agent.ID) == authorID {
			t.Fatal("run_review must never resolve to the author agent")
		}
		return
	}
	// Otherwise it must refuse cleanly with a 409, not fall through to the
	// author via the own-agent path.
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 when no distinct reviewer resolves, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- auto-dispatch guards ------------------------------------------------------

func TestMaybeRunReviewOnQAPassGuards(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	dispatchComments := func(issue db.Issue) []db.Comment {
		var out []db.Comment
		for _, c := range issueComments(t, issue) {
			if strings.Contains(c.Content, reviewDispatchMarker) {
				out = append(out, c)
			}
		}
		return out
	}
	prMeta := `{"pr_number": 7}`

	t.Run("flag off (default) never dispatches", func(t *testing.T) {
		_, authorID := seedReviewSquad(t, "Auto Review Off Squad")
		issue := seedReviewDecisionIssue(t, "auto review flag off", "in_review", authorID, prMeta)
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:pass", testUserID)
		if n := len(dispatchComments(issue)); n != 0 {
			t.Fatalf("dispatch comments = %d, want 0 with the flag unset", n)
		}
	})

	t.Run("no PR skips", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		_, authorID := seedReviewSquad(t, "Auto Review NoPR Squad")
		issue := seedReviewDecisionIssue(t, "auto review no pr", "in_review", authorID, "")
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:pass", testUserID)
		if n := len(dispatchComments(issue)); n != 0 {
			t.Fatalf("dispatch comments = %d, want 0 without a PR", n)
		}
	})

	t.Run("existing review label skips", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		_, authorID := seedReviewSquad(t, "Auto Review Labeled Squad")
		issue := seedReviewDecisionIssue(t, "auto review already labeled", "in_review", authorID, prMeta)
		attachTestLabel(t, uuidToString(issue.ID), "review:pass")
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:pass", testUserID)
		if n := len(dispatchComments(issue)); n != 0 {
			t.Fatalf("dispatch comments = %d, want 0 when a review verdict already stands", n)
		}
	})

	t.Run("non-qa:pass label never dispatches", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		_, authorID := seedReviewSquad(t, "Auto Review Wrong Label Squad")
		issue := seedReviewDecisionIssue(t, "auto review wrong label", "in_review", authorID, prMeta)
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:fail", testUserID)
		if n := len(dispatchComments(issue)); n != 0 {
			t.Fatalf("dispatch comments = %d, want 0 for qa:fail", n)
		}
	})

	t.Run("dispatches to a reviewer that is not the author", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		leaderID, authorID := seedReviewSquad(t, "Auto Review Go Squad")
		issue := seedReviewDecisionIssue(t, "auto review dispatch", "in_review", authorID, prMeta)
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:pass", testUserID)
		got := dispatchComments(issue)
		if len(got) != 1 {
			t.Fatalf("dispatch comments = %d, want 1", len(got))
		}
		if !strings.Contains(got[0].Content, "mention://agent/"+leaderID) {
			t.Errorf("dispatch must @mention the squad leader (reviewer), got: %.200s", got[0].Content)
		}
		if strings.Contains(got[0].Content, "mention://agent/"+authorID) {
			t.Error("dispatch must NOT target the author agent")
		}
		// A second qa:pass in the same cycle sees the in-flight dispatch and skips.
		testHandler.maybeRunReviewOnQAPass(ctx, issue, "qa:pass", testUserID)
		if n := len(dispatchComments(issue)); n != 1 {
			t.Fatalf("dispatch comments after re-fire = %d, want still 1 (in-flight dedup)", n)
		}
	})

	t.Run("reviewer resolution skips when the author is the only agent", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
		// A one-agent squad where the author IS the leader and no QA squad
		// leader differs: no reviewer resolves → skip.
		ctxb := context.Background()
		authorID := createHandlerTestAgent(t, "Solo Author Leader", nil)
		var squadID string
		if err := testPool.QueryRow(ctxb, `
			INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
			VALUES ($1, 'Solo Author Squad', '', $2, $3) RETURNING id`,
			testWorkspaceID, authorID, testUserID).Scan(&squadID); err != nil {
			t.Fatalf("create squad: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1::uuid`, squadID) })
		if _, err := testPool.Exec(ctxb, `INSERT INTO squad_member (squad_id, member_type, member_id) VALUES ($1,'agent',$2)`, squadID, authorID); err != nil {
			t.Fatalf("add member: %v", err)
		}
		issue := seedReviewDecisionIssue(t, "auto review solo author", "in_review", authorID, prMeta)
		testHandler.maybeRunReviewOnQAPass(ctxb, issue, "qa:pass", testUserID)
		for _, c := range dispatchComments(issue) {
			if strings.Contains(c.Content, "mention://agent/"+authorID) {
				t.Fatal("the author agent must never be dispatched as its own reviewer")
			}
		}
	})
}
