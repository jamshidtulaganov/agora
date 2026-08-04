package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// callQAOverride drives OverrideQAVerdict through a chi router so the
// {id} URL param resolves, authenticated as the fixture user.
func callQAOverride(t *testing.T, issueID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/qa-override", bytes.NewReader(raw))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/api/issues/{id}/qa-override", testHandler.OverrideQAVerdict)
	r.ServeHTTP(rec, req)
	return rec
}

// TestOverrideQAVerdict_ProvenanceEndToEnd is the Phase 2 override contract:
// an override flips the label, REPLACES the current evidence row with a
// human-sourced one (reason as summary, actor stamped into result_json,
// prior agent result preserved), and posts a timeline comment. The
// reconciled state in the response reflects the new verdict.
func TestOverrideQAVerdict_ProvenanceEndToEnd(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "qa override — provenance", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	// Seed the AGENT verdict first (pass, with a command so the floor clears)
	// — the override must replace it while preserving its command table.
	agentContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"agent says fine","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if v, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, agentContent, pgtype.UUID{}); v != "pass" || !labeled {
		t.Fatalf("seed agent evidence: verdict=%q labeled=%v", v, labeled)
	}

	rec := callQAOverride(t, issueID, map[string]any{"verdict": "fail", "reason": "step 2 renders blank on staging"})
	if rec.Code != http.StatusOK {
		t.Fatalf("override status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp QAEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Verdict != "fail" || resp.Source != "human" {
		t.Errorf("response verdict/source = %q/%q, want fail/human", resp.Verdict, resp.Source)
	}
	if resp.Summary != "step 2 renders blank on staging" {
		t.Errorf("summary = %q, want the reason", resp.Summary)
	}
	if resp.ReconciledState != "fail" {
		t.Errorf("reconciled_state = %q, want fail", resp.ReconciledState)
	}

	// The label flipped: qa:fail on, qa:pass off.
	names := labelNamesForIssue(t, issueID)
	if !names["qa:fail"] || names["qa:pass"] {
		t.Errorf("labels after override = %v, want {qa:fail} only", names)
	}

	// ONE current evidence row: human-sourced, actor stamped, prior agent
	// result preserved under it.
	ev, err := testHandler.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("GetLatestQAEvidenceForIssue: %v", err)
	}
	if ev.Source != "human" || ev.Verdict != "fail" {
		t.Errorf("evidence row source/verdict = %q/%q, want human/fail", ev.Source, ev.Verdict)
	}
	var result struct {
		Commands []json.RawMessage `json:"commands"`
		Override *qaOverrideStamp  `json:"override"`
	}
	if err := json.Unmarshal(ev.ResultJson, &result); err != nil {
		t.Fatalf("result_json unmarshal: %v", err)
	}
	if len(result.Commands) != 1 {
		t.Errorf("prior agent command table not preserved: commands len = %d, want 1", len(result.Commands))
	}
	if result.Override == nil || result.Override.ByUserID != testUserID || result.Override.Reason == "" {
		t.Errorf("override stamp missing/wrong: %+v", result.Override)
	}

	// Timeline comment posted, attributed, carrying the reason.
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListCommentsForIssue: %v", err)
	}
	found := false
	for _, c := range comments {
		if c.AuthorType == "member" &&
			bytes.Contains([]byte(c.Content), []byte("overridden to FAIL")) &&
			bytes.Contains([]byte(c.Content), []byte("step 2 renders blank")) {
			found = true
		}
	}
	if !found {
		t.Error("expected an attributed timeline comment recording the override + reason")
	}
}

// TestOverrideQAVerdict_PassWithFailingCasesMatrix pins the documented
// override matrix: overriding to PASS while a defined case's latest run is
// failing yields pass_with_failing_cases — the override asserts the human's
// verdict but does NOT erase a known-red case, so the merge gate stays
// closed and the chip shows "Pass · N failing".
func TestOverrideQAVerdict_PassWithFailingCasesMatrix(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "qa override — pass with failing case", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	tc, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ProjectID: issue.ProjectID,
		Title: "[e2e] checkout", Steps: "pay", Expected: "ok",
		Kind: "automated", Source: "human", AuthorType: "member", Category: "positive",
	})
	if err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}
	if _, err := testHandler.Queries.CreateTestRun(ctx, db.CreateTestRunParams{
		WorkspaceID: issue.WorkspaceID, TestCaseID: tc.ID, IssueID: issue.ID,
		Status: "fail", Output: "500 on submit", RunSource: "agent", RunByType: "agent",
	}); err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}

	rec := callQAOverride(t, issueID, map[string]any{"verdict": "pass", "reason": "verified by hand, known flake"})
	if rec.Code != http.StatusOK {
		t.Fatalf("override status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp QAEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ReconciledState != "pass_with_failing_cases" {
		t.Errorf("reconciled_state = %q, want pass_with_failing_cases (override must not erase a known-red case)", resp.ReconciledState)
	}
	// And the merge gate stays closed.
	if status, _ := testHandler.qaGateFromReconciledState(ctx, issue); status == "pass" {
		t.Error("merge gate must NOT read pass when a case is still failing, even after a human override to pass")
	}
}

// TestOverrideQAVerdict_RejectsBadVerdict guards the input contract.
func TestOverrideQAVerdict_RejectsBadVerdict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := createTestIssue(t, "qa override — bad verdict", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	rec := callQAOverride(t, issueID, map[string]any{"verdict": "maybe"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for verdict=maybe", rec.Code)
	}
}
