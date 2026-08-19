package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/service"
)

// TestComposeReviewVerdictGroupNotifyText covers the Telegram body: the blocker
// count appears only when the reviewer recorded blockers, every dynamic field is
// HTML-escaped (a title with `<` must not break Telegram's HTML parse mode), and
// empty optional fields leave no dangling labels.
func TestComposeReviewVerdictGroupNotifyText(t *testing.T) {
	pass := composeReviewVerdictGroupNotifyText("SD-25", "Add totals", "pass", "no blocking issues", "opening the merge request", 0, "SD Reviewer")
	for _, want := range []string{"Code review passed", "SD-25", "Add totals", "no blocking issues", "opening the merge request", "SD Reviewer"} {
		if !strings.Contains(pass, want) {
			t.Errorf("pass notice missing %q: %s", want, pass)
		}
	}
	if strings.Contains(pass, "blocker") {
		t.Errorf("a passing verdict must not mention blockers: %s", pass)
	}

	fail := composeReviewVerdictGroupNotifyText("SD-26", "Fix <script> handling", "fail", "SQL injection in report filter", "returned to To Do for the developer", 2, "Dev Squad")
	for _, want := range []string{"Code review failed", "2 blocker(s)", "SD-26", "returned to To Do", "SQL injection"} {
		if !strings.Contains(fail, want) {
			t.Errorf("fail notice missing %q: %s", want, fail)
		}
	}
	if strings.Contains(fail, "<script>") {
		t.Errorf("title must be HTML-escaped, got raw tag: %s", fail)
	}
	if !strings.Contains(fail, "&lt;script&gt;") {
		t.Errorf("escaped title missing: %s", fail)
	}

	// No summary / no owner → no empty labels left behind.
	bare := composeReviewVerdictGroupNotifyText("SD-27", "", "fail", "", "", 0, "")
	for _, unwanted := range []string{"📝", "👤", "➡️"} {
		if strings.Contains(bare, unwanted) {
			t.Errorf("bare notice must omit empty sections, found %q: %s", unwanted, bare)
		}
	}
}

// TestMaybeOpenPROnReviewPassDisabled: opening a merge request is an outward-facing
// write on the team's repo, so it must never happen on a default-configured project.
func TestMaybeOpenPROnReviewPassDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_PASS_OPEN_PR_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET metadata = jsonb_build_object('bitrix_task_id','9001') WHERE id=$1::uuid`, issueID); err != nil {
		t.Fatalf("stamp task id: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeOpenPROnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("disabled open_pr must not dispatch: %d -> %d", before, after)
	}
}

// TestMaybeOpenPROnReviewPassNoBranch: enabled, but nothing names a branch (no
// linked PR, no Bitrix task id) → there is nothing to open a request from, so the
// dispatch is skipped rather than sent to an agent to improvise.
func TestMaybeOpenPROnReviewPassNoBranch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_PASS_OPEN_PR_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeOpenPROnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("no branch must not dispatch open_pr: %d -> %d", before, after)
	}
}

// TestMaybeOpenPROnReviewPassStandingFail: the verdict pair can be mid-replace, so
// a review:pass arriving while review:fail still stands must not open a request for
// a diff the reviewer rejected.
func TestMaybeOpenPROnReviewPassStandingFail(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_PASS_OPEN_PR_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	attachLabelDirect(t, ctx, issueID, service.ReviewLabelFail)
	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET metadata = jsonb_build_object('bitrix_task_id','9002') WHERE id=$1::uuid`, issueID); err != nil {
		t.Fatalf("stamp task id: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeOpenPROnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("standing review:fail must block open_pr: %d -> %d", before, after)
	}
}

// TestMaybeRouteToDevOnReviewFailDisabled: the return-to-todo routing changes an
// issue's status automatically, so it stays opt-in.
func TestMaybeRouteToDevOnReviewFailDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_FAIL_AUTOROUTE_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	before := commentCount(t, ctx, issueID)
	testHandler.maybeRouteToDevOnReviewFail(ctx, issue, service.ReviewLabelFail, testUserID)
	if after := commentCount(t, ctx, issueID); after != before {
		t.Errorf("disabled review-fail autoroute must not dispatch: %d -> %d", before, after)
	}
	reloaded, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status == "todo" {
		t.Error("disabled review-fail autoroute must not reset the status")
	}
}

// TestMaybeRouteToDevOnReviewFailReturnsToTodo is the happy path: the issue goes
// back to todo, gets reassigned to the orchestrator, carries a retry brief, and
// bumps the loop counter. Then the loop cap is asserted at the boundary — a change
// that keeps failing must stop bouncing and wait for a human.
func TestMaybeRouteToDevOnReviewFailReturnsToTodo(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_FAIL_AUTOROUTE_ENABLED", "true")
	ctx := context.Background()

	leaderID, authorID := seedReviewSquad(t, "Review Fail Route Squad")
	issue := seedReviewDecisionIssue(t, "review fail routes back", "in_review", authorID, "")
	issueID := uuidToString(issue.ID)

	before := commentCount(t, ctx, issueID)
	testHandler.maybeRouteToDevOnReviewFail(ctx, issue, service.ReviewLabelFail, testUserID)
	if after := commentCount(t, ctx, issueID); after != before+1 {
		t.Fatalf("comment count = %d, want %d (one retry brief)", after, before+1)
	}
	reloaded, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != "todo" {
		t.Errorf("status = %q, want todo", reloaded.Status)
	}
	if !reloaded.AssigneeID.Valid || uuidToString(reloaded.AssigneeID) != leaderID {
		t.Errorf("assignee = %q, want the orchestrator %q", uuidToString(reloaded.AssigneeID), leaderID)
	}
	if n := issueMetadataInt(reloaded.Metadata, reviewFailAutorouteCountKey); n != 1 {
		t.Errorf("%s = %d, want 1", reviewFailAutorouteCountKey, n)
	}

	// At the cap: no further routing, no further comment.
	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET metadata = jsonb_set(COALESCE(metadata,'{}'::jsonb), '{`+reviewFailAutorouteCountKey+`}', to_jsonb($2::int)) WHERE id = $1`,
		issue.ID, reviewFailAutorouteMaxAttempts); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	capped, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("reload capped: %v", err)
	}
	atCap := commentCount(t, ctx, issueID)
	testHandler.maybeRouteToDevOnReviewFail(ctx, capped, service.ReviewLabelFail, testUserID)
	if after := commentCount(t, ctx, issueID); after != atCap {
		t.Errorf("at the attempt cap the autoroute must stop: %d -> %d", atCap, after)
	}
}

// TestClearReviewFailAutorouteBudget: a passing review resets the loop budget so a
// later regression starts fresh instead of inheriting a spent one.
func TestClearReviewFailAutorouteBudget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET metadata = jsonb_build_object('`+reviewFailAutorouteCountKey+`', 3) WHERE id=$1::uuid`, issueID); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	// The wrong label leaves it alone.
	testHandler.clearReviewFailAutorouteBudget(ctx, issue, service.ReviewLabelFail)
	mid, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if n := issueMetadataInt(mid.Metadata, reviewFailAutorouteCountKey); n != 3 {
		t.Fatalf("review:fail must not clear the budget, got %d", n)
	}
	testHandler.clearReviewFailAutorouteBudget(ctx, issue, service.ReviewLabelPass)
	after, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := issueMetadataInt(after.Metadata, reviewFailAutorouteCountKey); n != 0 {
		t.Errorf("%s = %d after review:pass, want 0", reviewFailAutorouteCountKey, n)
	}
}

// TestMaybeOpenPROnReviewPassDispatches is the review-first happy path's last
// step: a clean verdict, a resolvable branch, no request yet → exactly ONE open_pr
// dispatch, addressed to the AUTHOR side (the orchestrator), never the reviewer.
// The re-fire assertion covers the duplicate-MR hazard: two openers racing the
// same branch push would open two merge requests.
func TestMaybeOpenPROnReviewPassDispatches(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_REVIEW_PASS_OPEN_PR_ENABLED", "true")
	ctx := context.Background()

	leaderID, authorID := seedReviewSquad(t, "Open PR Squad")
	issue := seedReviewDecisionIssue(t, "open pr on clean review", "in_review", authorID, `{"bitrix_task_id":"9100"}`)

	dispatches := func() []string {
		var out []string
		for _, c := range issueComments(t, issue) {
			if strings.Contains(c.Content, openPRMarker) {
				out = append(out, c.Content)
			}
		}
		return out
	}

	testHandler.maybeOpenPROnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	got := dispatches()
	if len(got) != 1 {
		t.Fatalf("open_pr dispatches = %d, want 1", len(got))
	}
	if !strings.Contains(got[0], "mention://agent/"+leaderID) {
		t.Errorf("open_pr must be addressed to the orchestrator, got: %.200s", got[0])
	}
	if !strings.Contains(got[0], "btx-9100") {
		t.Errorf("open_pr must name the branch resolved from the Bitrix task id, got: %.300s", got[0])
	}
	if !strings.Contains(got[0], agentProtocolMarker(sliceActionOpenPR)) {
		t.Error("open_pr dispatch must carry its protocol marker")
	}

	testHandler.maybeOpenPROnReviewPass(ctx, issue, service.ReviewLabelPass, testUserID)
	if n := len(dispatches()); n != 1 {
		t.Fatalf("open_pr dispatches after re-fire = %d, want still 1 (no duplicate merge request)", n)
	}
}
