package handler

import (
	"context"
	"testing"
)

// mkSpecCase inserts an automated test case (optionally carrying a compiled
// script) plus, when runStatus is non-empty, one recorded run for it. Returns
// the case id.
func mkSpecCase(t *testing.T, ctx context.Context, issueID, title, script, runStatus string) string {
	t.Helper()
	var cid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO test_case (workspace_id, issue_id, title, steps, expected, kind, source, author_type, category, script)
		 VALUES ($1::uuid,$2::uuid,$3,'1. open','ok','automated','agent','agent','positive',$4) RETURNING id::text`,
		testWorkspaceID, issueID, title, script).Scan(&cid); err != nil {
		t.Fatalf("insert test_case: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM test_case WHERE id=$1::uuid`, cid) })
	if runStatus != "" {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO test_run (workspace_id, test_case_id, issue_id, status, output, run_source, run_by_type)
			 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'','agent','agent')`,
			testWorkspaceID, cid, issueID, runStatus); err != nil {
			t.Fatalf("insert test_run: %v", err)
		}
	}
	return cid
}

// TestGreenScriptedCasesForIssue is the entry bar for committing a spec into the
// repository: a compiled script AND a latest run that PASSED. The three excluded
// shapes are the ones that would plant a red or unproven test in the repo and
// block every future pipeline on the branch.
func TestGreenScriptedCasesForIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	wantID := mkSpecCase(t, ctx, issueID, "green with script", "import x from 'y'", "pass")
	mkSpecCase(t, ctx, issueID, "green without script", "", "pass")
	mkSpecCase(t, ctx, issueID, "red with script", "import x from 'y'", "fail")
	mkSpecCase(t, ctx, issueID, "never run with script", "import x from 'y'", "")

	green := testHandler.greenScriptedCasesForIssue(ctx, issue)
	if len(green) != 1 {
		var titles []string
		for _, c := range green {
			titles = append(titles, c.Title)
		}
		t.Fatalf("greenScriptedCasesForIssue = %d cases %v, want exactly 1 (green with script)", len(green), titles)
	}
	if uuidToString(green[0].ID) != wantID {
		t.Errorf("green case = %s, want %s", uuidToString(green[0].ID), wantID)
	}
}

// TestGreenScriptedCasesLatestRunWins: a case that failed and was then re-run
// green is committable, and a case that passed and then regressed is NOT — only
// the LATEST run decides.
func TestGreenScriptedCasesLatestRunWins(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	fixed := mkSpecCase(t, ctx, issueID, "failed then fixed", "script", "fail")
	regressed := mkSpecCase(t, ctx, issueID, "passed then regressed", "script", "pass")
	// Second run per case, later than the first (created_at DESC picks it).
	for _, c := range []struct{ id, status string }{{fixed, "pass"}, {regressed, "fail"}} {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO test_run (workspace_id, test_case_id, issue_id, status, output, run_source, run_by_type, created_at)
			 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'','agent','agent', now() + interval '1 minute')`,
			testWorkspaceID, c.id, issueID, c.status); err != nil {
			t.Fatalf("insert later run: %v", err)
		}
	}

	green := testHandler.greenScriptedCasesForIssue(ctx, issue)
	if len(green) != 1 || uuidToString(green[0].ID) != fixed {
		t.Fatalf("green = %d cases, want exactly the re-run-green one (%s)", len(green), fixed)
	}
}

// TestIssueReviewBranchBitrixFallback: a GitLab MR linked from a comment URL
// carries no branch (the URL has none), so the branch falls back to the
// btx-<taskId> convention the dev agent was told to use.
func TestIssueReviewBranchBitrixFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")

	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET metadata = jsonb_build_object('bitrix_task_id','4821') WHERE id=$1::uuid`, issueID); err != nil {
		t.Fatalf("stamp bitrix_task_id: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if got := testHandler.issueReviewBranch(ctx, issue); got != "btx-4821" {
		t.Errorf("issueReviewBranch = %q, want %q", got, "btx-4821")
	}
}

// TestIssueReviewBranchNoneResolves: with neither a linked PR branch nor a
// Bitrix task id there is nothing to push to, and the resolver must say so
// rather than hand back a guess (which would commit onto the default branch).
func TestIssueReviewBranchNoneResolves(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if got := testHandler.issueReviewBranch(ctx, issue); got != "" {
		t.Errorf("issueReviewBranch = %q, want \"\"", got)
	}
}

// TestSliceActionCommitSpecsContextRefusesEmpty: no branch or no specs → ok=false,
// so the caller never dispatches an agent with nothing to do.
func TestSliceActionCommitSpecsContextRefusesEmpty(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if _, ok := testHandler.sliceActionCommitSpecsContext(ctx, issue, "", nil); ok {
		t.Error("no branch + no specs must not produce an instruction")
	}
	if _, ok := testHandler.sliceActionCommitSpecsContext(ctx, issue, "btx-1", nil); ok {
		t.Error("a branch with no passing specs must not produce an instruction")
	}
}

// TestMaybeCommitSpecsOnQAPassDisabled: with AGORA_COMMIT_SPECS_ENABLED unset
// the spec-commit dispatch is inert, so the behavior is strictly opt-in — an
// agent must never push to a team's branch because a default flipped.
func TestMaybeCommitSpecsOnQAPassDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_COMMIT_SPECS_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	mkSpecCase(t, ctx, issueID, "green with script", "script", "pass")

	var before, after int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1::uuid`, issueID).Scan(&before)
	testHandler.maybeCommitSpecsOnQAPass(ctx, issue, "qa:pass", testUserID)
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1::uuid`, issueID).Scan(&after)
	if after != before {
		t.Errorf("disabled spec-commit must not post a comment: %d -> %d", before, after)
	}
}

// TestMaybeCommitSpecsOnQAPassWrongLabel: only qa:pass may open the spec commit.
// A qa:fail (or any other label) landing must not push specs from a red run.
func TestMaybeCommitSpecsOnQAPassWrongLabel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_COMMIT_SPECS_ENABLED", "true")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	mkSpecCase(t, ctx, issueID, "green with script", "script", "pass")

	var before, after int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1::uuid`, issueID).Scan(&before)
	testHandler.maybeCommitSpecsOnQAPass(ctx, issue, "qa:fail", testUserID)
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1::uuid`, issueID).Scan(&after)
	if after != before {
		t.Errorf("qa:fail must not fire the spec commit: %d -> %d", before, after)
	}
}
