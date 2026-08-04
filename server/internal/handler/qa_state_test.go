package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestQAGateFromReconciledStateFailsClosedOnFailingCase is the regression for
// the Phase 2 audit requirement: a qa:pass label sitting on top of a known-
// failing test case must NOT clear the merge gate. Before ReconcileQAState,
// qaGateFromReconciledState's predecessor (gateFromLabels) only ever looked
// at the qa:pass/qa:fail LABELS — a case regression with the gate label still
// reading "pass" (e.g. a base-suite regression discovered after the qa:pass
// label was already attached) sailed straight through as "pass".
func TestQAGateFromReconciledStateFailsClosedOnFailingCase(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "qa gate reconcile — failing case", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	// A defined case for the issue.
	tc, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ProjectID:   issue.ProjectID,
		Title:       "[e2e] checkout",
		Steps:       "open cart; pay",
		Expected:    "order confirmed",
		Kind:        "automated",
		Source:      "human",
		AuthorType:  "member",
		Category:    "positive",
	})
	if err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}

	// A qa-result "pass" verdict WITH commands (survives the evidence floor)
	// attaches qa:pass.
	passContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"looks good","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, passContent, pgtype.UUID{}); verdict != "pass" || !labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want pass/true", verdict, labeled)
	}

	// Sanity: with no failing case yet, the gate reads pass.
	if status, reason := testHandler.qaGateFromReconciledState(ctx, issue); status != "pass" {
		t.Fatalf("before any run: qa gate = %q (%q), want pass", status, reason)
	}

	// Now the case's LATEST run regresses — no new label activity at all, the
	// qa:pass label from the earlier capture is still sitting there untouched.
	if _, err := testHandler.Queries.CreateTestRun(ctx, db.CreateTestRunParams{
		WorkspaceID: issue.WorkspaceID,
		TestCaseID:  tc.ID,
		IssueID:     issue.ID,
		Status:      "fail",
		Output:      "assertion failed: expected 200, got 500",
		RunSource:   "agent",
		RunByType:   "agent",
	}); err != nil {
		t.Fatalf("CreateTestRun: %v", err)
	}

	status, reason := testHandler.qaGateFromReconciledState(ctx, issue)
	if status != "fail" {
		t.Fatalf("after case regression: qa gate = %q (%q), want fail (pass_with_failing_cases must fail closed)", status, reason)
	}
	if reason == "" {
		t.Error("expected a human-readable reason for the fail-closed block")
	}

	// The reconciled state itself is the distinct pass_with_failing_cases
	// bucket, not a plain fail — the chip/lane still know a pass label is
	// present, even though the gate treats it as not-pass.
	state := testHandler.reconciledQAState(ctx, issue, true)
	if state != service.QAStatePassWithFailingCases {
		t.Fatalf("reconciledQAState = %q, want %q", state, service.QAStatePassWithFailingCases)
	}
}

// TestQAGateFromReconciledStateCleanPass confirms the gate still reads a
// plain, uncontested qa:pass (no failing case, no other state) as "pass" —
// the reconciled path must not have regressed the common green case.
func TestQAGateFromReconciledStateCleanPass(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "qa gate reconcile — clean pass", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	passContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"all green","commands":[{"cmd":"pnpm test","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, passContent, pgtype.UUID{}); verdict != "pass" || !labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want pass/true", verdict, labeled)
	}

	if status, reason := testHandler.qaGateFromReconciledState(ctx, issue); status != "pass" {
		t.Fatalf("qa gate = %q (%q), want pass", status, reason)
	}
}
