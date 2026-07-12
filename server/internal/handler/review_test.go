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
		name  string
		tier  reviewTier
		hasPR bool
		want  []string
	}{
		{"full tier with PR requires review", full, true, []string{"ci", "qa", "review"}},
		{"full tier without PR omits review entirely", full, false, []string{"ci", "qa"}},
		{"light tier never requires review", light, true, []string{"ci"}},
		{"trivial tier never requires review", trivial, true, []string{"ci"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiredGatesWithReview(tt.tier, tt.hasPR); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("requiredGatesWithReview(%s, %v) = %v, want %v", tt.tier.name, tt.hasPR, got, tt.want)
			}
		})
	}

	// The append must never mutate the tier's own required slice.
	if !reflect.DeepEqual(full.required, []string{"ci", "qa"}) {
		t.Errorf("full.required mutated to %v", full.required)
	}
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

	t.Run("approve without qa:pass is 409", func(t *testing.T) {
		issue := seedReviewDecisionIssue(t, "review 409 no qa", "in_review", "", "")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "qa_gate_not_passed") {
			t.Fatalf("expected 409 qa_gate_not_passed, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("approve with review:fail is 409", func(t *testing.T) {
		issue := seedReviewDecisionIssue(t, "review 409 fail", "in_review", "", "")
		attachTestLabel(t, uuidToString(issue.ID), "qa:pass")
		attachTestLabel(t, uuidToString(issue.ID), "review:fail")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "review_failed") {
			t.Fatalf("expected 409 review_failed, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("merge:override bypasses the gates", func(t *testing.T) {
		issue := seedReviewDecisionIssue(t, "review override bypass", "in_review", "", "")
		attachTestLabel(t, uuidToString(issue.ID), "merge:override")
		w := post(issue, map[string]any{"action": "approve"})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 with merge:override, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestCreateReviewDecisionApproveDispatch: a green-gated approve stamps
// merge:approved and writes the member-authored merge order @mentioning the
// dev squad leader.
func TestCreateReviewDecisionApproveDispatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	leaderID, authorID := seedReviewSquad(t, "Review Approve Squad")
	issue := seedReviewDecisionIssue(t, "review approve dispatch", "in_review", authorID, "")
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

	foundOrder := false
	for _, c := range issueComments(t, issue) {
		if strings.Contains(c.Content, "mention://agent/"+leaderID) && strings.Contains(c.Content, "gh pr merge") {
			foundOrder = true
			if c.AuthorType != "member" {
				t.Errorf("merge order author_type = %s, want member", c.AuthorType)
			}
		}
	}
	if !foundOrder {
		t.Error("no member-authored merge order @mentioning the squad leader was written")
	}
}

func TestCreateReviewDecisionRequestChanges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	authorID := createHandlerTestAgent(t, "Review RC Author", nil)
	issue := seedReviewDecisionIssue(t, "review request changes", "in_review", authorID, "")
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

	w := post(map[string]any{"action": "request_changes", "note": "fix the nil deref in the handler"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1::uuid`, uuidToString(issue.ID)).Scan(&status)
	if status != "in_progress" {
		t.Errorf("issue status = %q, want in_progress", status)
	}

	found := false
	for _, c := range issueComments(t, issue) {
		if strings.Contains(c.Content, "mention://agent/"+authorID) && strings.Contains(c.Content, "fix the nil deref") {
			found = true
		}
	}
	if !found {
		t.Error("no @mention comment routing the note to the author agent was written")
	}

	// review:fail is deliberately KEPT until a re-review replaces it.
	if !testHandler.issueHasLabel(context.Background(), issue, "review:fail") {
		t.Error("review:fail must survive request_changes")
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
