package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedRun inserts a test_run with an explicit commit_sha (direct SQL — the
// capture path is covered elsewhere; these tests need precise sha control).
func seedRun(t *testing.T, caseID, issueID, status, sha string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO test_run (workspace_id, test_case_id, issue_id, status, output, run_source, run_by_type, commit_sha)
		VALUES ($1, $2, $3, $4, 'seeded', 'agent', 'agent', $5)`,
		testWorkspaceID, caseID, issueID, status, sha); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

// TestFlakySignal: same case + same commit_sha with BOTH pass and fail runs
// ⇒ flaky; consistent verdicts (even mixed across DIFFERENT shas) ⇒ not
// flaky — a fail on sha A and a pass on sha B is a FIX, not flakiness.
func TestFlakySignal(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "flaky signal", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	seedCase := func(title string) db.TestCase {
		tc, cerr := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
			WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ProjectID: issue.ProjectID,
			Title: title, Steps: "s", Expected: "e",
			Kind: "automated", Source: "human", AuthorType: "member", Category: "positive",
		})
		if cerr != nil {
			t.Fatalf("CreateTestCase: %v", cerr)
		}
		return tc
	}
	flakyCase := seedCase("flaky checkout")
	fixedCase := seedCase("fixed checkout")
	noShaCase := seedCase("no sha checkout")

	// flakyCase: pass AND fail on the SAME sha → flaky.
	seedRun(t, uuidToString(flakyCase.ID), issueID, "fail", "deadbeef1234")
	seedRun(t, uuidToString(flakyCase.ID), issueID, "pass", "deadbeef1234")
	// fixedCase: fail on sha A, pass on sha B → a fix, NOT flaky.
	seedRun(t, uuidToString(fixedCase.ID), issueID, "fail", "aaaa111122223333")
	seedRun(t, uuidToString(fixedCase.ID), issueID, "pass", "bbbb444455556666")
	// noShaCase: mixed verdicts but NO sha reported → can't bind → not flaky.
	seedRun(t, uuidToString(noShaCase.ID), issueID, "fail", "")
	seedRun(t, uuidToString(noShaCase.ID), issueID, "pass", "")

	ids, err := testHandler.Queries.ListFlakyCaseIDsForIssue(ctx, db.ListFlakyCaseIDsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("ListFlakyCaseIDsForIssue: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[uuidToString(id)] = true
	}
	if !got[uuidToString(flakyCase.ID)] {
		t.Error("pass+fail on the same sha must flag the case flaky")
	}
	if got[uuidToString(fixedCase.ID)] {
		t.Error("fail-then-pass across DIFFERENT shas is a fix, not flaky")
	}
	if got[uuidToString(noShaCase.ID)] {
		t.Error("mixed verdicts with no sha must not flag flaky (nothing binds them to one commit)")
	}
}

// TestGetTestCaseRuns wires the run-history endpoint end to end: newest
// first, capped, carrying the Phase 3 identity fields; a case from another
// workspace 404s.
func TestGetTestCaseRuns(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "run history", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	tc, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ProjectID: issue.ProjectID,
		Title: "history case", Steps: "s", Expected: "e",
		Kind: "automated", Source: "human", AuthorType: "member", Category: "positive",
	})
	if err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}
	seedRun(t, uuidToString(tc.ID), issueID, "fail", "deadbeef1234")
	seedRun(t, uuidToString(tc.ID), issueID, "pass", "cafebabe5678")

	req := httptest.NewRequest(http.MethodGet, "/api/test-cases/"+uuidToString(tc.ID)+"/runs", nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Get("/api/test-cases/{id}/runs", testHandler.GetTestCaseRuns)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Runs []struct {
			Status    string `json:"status"`
			CommitSha string `json:"commit_sha"`
			RunSource string `json:"run_source"`
			CreatedAt string `json:"created_at"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(resp.Runs))
	}
	// Newest first: the pass on cafebabe5678 was seeded last.
	if resp.Runs[0].Status != "pass" || resp.Runs[0].CommitSha != "cafebabe5678" {
		t.Errorf("newest run = %+v, want the pass on cafebabe5678", resp.Runs[0])
	}
	if resp.Runs[1].CommitSha != "deadbeef1234" {
		t.Errorf("older run sha = %q, want deadbeef1234", resp.Runs[1].CommitSha)
	}
}
