package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// commentCount is the observable used across the review-chain gate tests: every
// auto-dispatch in the chain works by posting an @mention comment, so "no new
// comment" is exactly "did not dispatch".
func commentCount(t *testing.T, ctx context.Context, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}

// TestMaybeRunReviewOnCodeReviewStageDisabled: with AGORA_AUTO_REVIEW_ENABLED
// unset, a tracker move into Code Review dispatches nothing — the review-first
// trigger is opt-in exactly like the qa:pass trigger.
func TestMaybeRunReviewOnCodeReviewStageDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunReviewOnCodeReviewStage(ctx, issue, "member", testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("disabled auto-review must not post a comment: %d -> %d", before, after)
	}
}

// TestMaybeRunReviewOnCodeReviewStageNoPR: enabled, but the platform knows no
// pull/merge request for the issue → there is no diff to review, so the trigger
// stays silent instead of summoning a reviewer with nothing to read.
func TestMaybeRunReviewOnCodeReviewStageNoPR(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunReviewOnCodeReviewStage(ctx, issue, "member", testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("no known PR must not dispatch a review: %d -> %d", before, after)
	}
}

// TestMaybeRunReviewOnCodeReviewStageVerdictStands: a review verdict from this
// cycle already exists → the cycle is judged and the trigger must not summon a
// second reviewer. (A genuinely fresh cycle clears the labels first; that is
// what onBitrixStageChanged does before dispatching.)
func TestMaybeRunReviewOnCodeReviewStageVerdictStands(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	attachLabelDirect(t, ctx, issueID, service.ReviewLabelPass)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunReviewOnCodeReviewStage(ctx, issue, "member", testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("standing review verdict must not re-dispatch: %d -> %d", before, after)
	}
}

// TestMaybeRunTestsOnReviewPassDisabled: the review → E2E chain is gated on
// AGORA_AUTO_QA_ENABLED, so a review:pass on a project that never opted into
// automatic QA authors and runs nothing.
func TestMaybeRunTestsOnReviewPassDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunTestsOnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("disabled auto-QA must not open the E2E stage: %d -> %d", before, after)
	}
}

// TestMaybeRunTestsOnReviewPassWrongLabel: only review:pass opens the E2E stage.
// A review:fail must never start an E2E pass on a diff the reviewer rejected.
func TestMaybeRunTestsOnReviewPassWrongLabel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunTestsOnReviewPass(ctx, issue, service.ReviewLabelFail, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("review:fail must not open the E2E stage: %d -> %d", before, after)
	}
}

// TestMaybeRunTestsOnReviewPassStandingFailLabel: the pass label arrives, but a
// review:fail is ALSO on the issue (verdict pair mid-replace, or a stale fail).
// Fail wins — the E2E stage stays shut.
func TestMaybeRunTestsOnReviewPassStandingFailLabel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	attachLabelDirect(t, ctx, issueID, service.ReviewLabelFail)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRunTestsOnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("standing review:fail must block the E2E stage: %d -> %d", before, after)
	}
}

// TestMaybeRunReviewOnCodeReviewStageDispatches is the review-first happy path:
// the tracker moved the task into Code Review, the platform knows the MR, and a
// reviewer distinct from the author resolves → exactly ONE dispatch, addressed to
// the reviewer. The re-fire assertion is the poll-loop guard at the dispatch
// layer: even if the stage-entry diff let a second call through, the in-flight
// marker keeps it at one reviewer per cycle.
func TestMaybeRunReviewOnCodeReviewStageDispatches(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
	ctx := context.Background()

	dispatches := func(issue db.Issue) []db.Comment {
		var out []db.Comment
		for _, c := range issueComments(t, issue) {
			if strings.Contains(c.Content, reviewDispatchMarker) {
				out = append(out, c)
			}
		}
		return out
	}

	leaderID, authorID := seedReviewSquad(t, "Code Review Stage Squad")
	issue := seedReviewDecisionIssue(t, "code review stage dispatch", "in_review", authorID, `{"pr_number": 11}`)

	testHandler.maybeRunReviewOnCodeReviewStage(ctx, issue, "member", testUserID)
	got := dispatches(issue)
	if len(got) != 1 {
		t.Fatalf("dispatch comments = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "mention://agent/"+leaderID) {
		t.Errorf("dispatch must @mention the reviewer (squad leader), got: %.200s", got[0].Content)
	}
	if strings.Contains(got[0].Content, "mention://agent/"+authorID) {
		t.Error("dispatch must NOT target the author agent")
	}
	if !strings.Contains(got[0].Content, agentProtocolMarker(sliceActionRunReview)) {
		t.Error("dispatch must carry the run_review protocol marker")
	}

	testHandler.maybeRunReviewOnCodeReviewStage(ctx, issue, "member", testUserID)
	if n := len(dispatches(issue)); n != 1 {
		t.Fatalf("dispatch comments after re-fire = %d, want still 1 (in-flight dedup)", n)
	}
}

// TestMaybeRunReviewOnInReviewDispatches: work driven from AGORA (an issue dragged
// into in_review) must summon the reviewer exactly like a tracker's Code Review
// column does — same guards, same one-dispatch-per-cycle behavior.
func TestMaybeRunReviewOnInReviewDispatches(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_REVIEW_ENABLED", "1")
	ctx := context.Background()

	leaderID, authorID := seedReviewSquad(t, "In Review Entry Squad")
	// No pr_number: the reviewer must fall back to the branch diff.
	issue := seedReviewDecisionIssue(t, "in_review entry dispatch", "in_review", authorID, `{"bitrix_task_id":"9200"}`)

	dispatches := func() []db.Comment {
		var out []db.Comment
		for _, c := range issueComments(t, issue) {
			if strings.Contains(c.Content, reviewDispatchMarker) {
				out = append(out, c)
			}
		}
		return out
	}

	testHandler.maybeRunReviewOnInReview(ctx, issue, "member", testUserID)
	got := dispatches()
	if len(got) != 1 {
		t.Fatalf("dispatch comments = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Content, "mention://agent/"+leaderID) {
		t.Errorf("dispatch must @mention the reviewer, got: %.200s", got[0].Content)
	}
	// Branch-only review context: no MR exists yet, so the reviewer is pointed at
	// the branch diff instead of a PR number.
	if !strings.Contains(got[0].Content, "btx-9200") ||
		!strings.Contains(got[0].Content, "NO PULL/MERGE REQUEST EXISTS YET") {
		t.Errorf("dispatch must carry the branch-diff context, got: %.400s", got[0].Content)
	}
	testHandler.maybeRunReviewOnInReview(ctx, issue, "member", testUserID)
	if n := len(dispatches()); n != 1 {
		t.Fatalf("dispatches after re-fire = %d, want still 1", n)
	}
}
