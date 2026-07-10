package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
)

// seedOpenPR links an OPEN pull request with the given head sha to the issue.
func seedOpenPR(t *testing.T, issueID, headSha string) {
	t.Helper()
	ctx := context.Background()
	var prID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request (workspace_id, installation_id, repo_owner, repo_name, pr_number, title, state, html_url, pr_created_at, pr_updated_at, head_sha)
		VALUES ($1, 1, 'acme', 'repo', (SELECT COALESCE(MAX(pr_number),0)+1 FROM github_pull_request WHERE workspace_id=$1), 'stale green pr', 'open', 'https://x/pr', now(), now(), $2)
		RETURNING id`, testWorkspaceID, headSha).Scan(&prID); err != nil {
		t.Fatalf("seed pr: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO issue_pull_request (issue_id, pull_request_id) VALUES ($1, $2)`, issueID, prID); err != nil {
		t.Fatalf("link pr: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue_pull_request WHERE pull_request_id=$1`, prID)
		testPool.Exec(ctx, `DELETE FROM github_pull_request WHERE id=$1`, prID)
	})
}

// TestReconciledQAStateStaleGreenInvalidation is the Phase 3 end-to-end
// stale-green check: a captured qa:pass whose evidence commit_sha no longer
// matches the issue's OPEN PR head reconciles to STALE — the green judged a
// commit that is no longer the issue's head. When the shas match (prefix
// tolerant), the pass stands.
func TestReconciledQAStateStaleGreenInvalidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	fullSha := "deadbeef1234deadbeef1234deadbeef12341234"

	// Issue A: evidence sha == PR head → pass stands.
	freshID := createTestIssue(t, "stale green — fresh", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, freshID) })
	freshIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(freshID))
	if err != nil {
		t.Fatalf("GetIssue fresh: %v", err)
	}
	seedOpenPR(t, freshID, fullSha)
	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"green on head","commit_sha":"deadbeef1234","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if v, _ := testHandler.TaskService.CaptureQAEvidence(ctx, freshIssue, content, pgtype.UUID{}); v != "pass" {
		t.Fatalf("capture fresh: %q", v)
	}
	if got := testHandler.reconciledQAState(ctx, freshIssue, true); got != service.QAStatePass {
		t.Errorf("matching sha: reconciled = %q, want pass", got)
	}

	// Issue B: evidence sha != PR head → the green invalidates to stale.
	staleID := createTestIssue(t, "stale green — moved head", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, staleID) })
	staleIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(staleID))
	if err != nil {
		t.Fatalf("GetIssue stale: %v", err)
	}
	seedOpenPR(t, staleID, "cafebabe5678cafebabe5678cafebabe56785678")
	content2 := "```qa-result\n" +
		`{"verdict":"pass","summary":"green on an old commit","commit_sha":"deadbeef1234","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if v, _ := testHandler.TaskService.CaptureQAEvidence(ctx, staleIssue, content2, pgtype.UUID{}); v != "pass" {
		t.Fatalf("capture stale: %q", v)
	}
	if got := testHandler.reconciledQAState(ctx, staleIssue, true); got != service.QAStateStale {
		t.Errorf("moved head: reconciled = %q, want stale (stale-green invalidation)", got)
	}
	// And the merge gate refuses the outdated green.
	if status, reason := testHandler.qaGateFromReconciledState(ctx, staleIssue); status == "pass" {
		t.Errorf("merge gate must not accept an outdated green, got %q (%q)", status, reason)
	}

	// Issue C: NO open PR (sprint-branch / local-worktree mode) → head
	// unknowable → fail-open, the pass stands.
	unknownID := createTestIssue(t, "stale green — unknowable head", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, unknownID) })
	unknownIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(unknownID))
	if err != nil {
		t.Fatalf("GetIssue unknown: %v", err)
	}
	if v, _ := testHandler.TaskService.CaptureQAEvidence(ctx, unknownIssue, content2, pgtype.UUID{}); v != "pass" {
		t.Fatalf("capture unknown: %q", v)
	}
	if got := testHandler.reconciledQAState(ctx, unknownIssue, true); got != service.QAStatePass {
		t.Errorf("unknowable head: reconciled = %q, want pass (fail-open, no staleness claim)", got)
	}
}
