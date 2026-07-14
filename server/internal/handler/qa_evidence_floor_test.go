package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// labelNamesForIssue is a small shared helper for reading an issue's current
// label set by name — used by both the evidence-floor and design-verdict
// tests in this package.
func labelNamesForIssue(t *testing.T, issueID string) map[string]bool {
	t.Helper()
	labels, err := testHandler.Queries.ListLabelsByIssue(context.Background(), db.ListLabelsByIssueParams{
		IssueID:     parseUUID(issueID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("ListLabelsByIssue: %v", err)
	}
	names := make(map[string]bool, len(labels))
	for _, l := range labels {
		names[l.Name] = true
	}
	return names
}

// TestQAEvidenceFloorDowngradesZeroCommandPass is the exact regression the
// audit specified: a fabricated {"verdict":"pass","commands":[]} verdict must
// mint qa:stale, not qa:pass — an agent asserting "pass" without a single
// command result to back it is not evidence.
func TestQAEvidenceFloorDowngradesZeroCommandPass(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "evidence floor — zero commands", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	content := "```qa-result\n" + `{"verdict":"pass","commands":[]}` + "\n```"
	verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, content, pgtype.UUID{})
	if verdict != "" || labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want \"\"/false (verdict not applied)", verdict, labeled)
	}

	names := labelNamesForIssue(t, issueID)
	if names["qa:pass"] {
		t.Error("qa:pass must NOT be attached for a zero-command pass")
	}
	if !names["qa:stale"] {
		t.Error("qa:stale must be attached for a zero-command pass")
	}

	// No qa_evidence row either — an under-evidenced pass must not sit in the
	// evidence table reading green.
	if _, err := testHandler.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		t.Error("expected no qa_evidence row for a floor-downgraded verdict")
	}

	// A loud, explanatory system comment was posted.
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListCommentsForIssue: %v", err)
	}
	found := false
	for _, c := range comments {
		if c.AuthorType == "system" {
			found = true
		}
	}
	if !found {
		t.Error("expected a system comment explaining the insufficient-evidence downgrade")
	}
}

// TestQAEvidenceFloorAllowsPassWithCommands confirms the floor doesn't
// over-fire: a "pass" WITH at least one command clears it and applies qa:pass
// normally (the common case must not regress).
func TestQAEvidenceFloorAllowsPassWithCommands(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "evidence floor — has commands", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"all green","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, content, pgtype.UUID{})
	if verdict != "pass" || !labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want pass/true", verdict, labeled)
	}
	if names := labelNamesForIssue(t, issueID); !names["qa:pass"] || names["qa:stale"] {
		t.Fatalf("expected {qa:pass} only, got %v", names)
	}
}

// attachLabelToTestIssue ensures a label exists in the test workspace (get-or-
// create — several tests share label names like qa:fail) and attaches it to
// the issue — used to exercise the blast-radius-scoped floor + stage guards.
func attachLabelToTestIssue(t *testing.T, issueID, name string) {
	t.Helper()
	ctx := context.Background()
	label, err := testHandler.Queries.CreateLabel(ctx, db.CreateLabelParams{
		WorkspaceID: parseUUID(testWorkspaceID), Name: name, Color: "#888888",
	})
	if err != nil {
		label, err = testHandler.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{
			WorkspaceID: parseUUID(testWorkspaceID), Name: name,
		})
		if err != nil {
			t.Fatalf("CreateLabel/GetLabelByName %s: %v", name, err)
		}
	}
	if err := testHandler.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: parseUUID(issueID), LabelID: label.ID, WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("AttachLabelToIssue %s: %v", name, err)
	}
}

// TestQAEvidenceFloorRequiresVisualEvidenceForGuardedUICase covers the
// blast-radius-scoped half of the floor: a HIGH-RISK change (risk:guarded) with
// a UI-modality test case needs VISUAL evidence (a screenshot, or a captured
// trace) — command exit codes alone don't prove the rendered UI was checked.
// The bar is intentionally reserved for high blast radius; the sibling test
// below pins that an unknown/low-risk UI case is NOT held to it (the fix for
// the sprint-mode no-PR path that dead-ended light QA as qa:stale).
func TestQAEvidenceFloorRequiresVisualEvidenceForGuardedUICase(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "evidence floor — guarded ui case no trace", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	attachLabelToTestIssue(t, issueID, "risk:guarded")
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	if _, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ProjectID:   issue.ProjectID,
		Title:       "[e2e] login renders",
		Steps:       "open /login",
		Expected:    "form visible",
		Kind:        "automated",
		Source:      "human",
		AuthorType:  "member",
		Category:    "positive",
		Modality:    "ui",
	}); err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}

	// Has a command, but NO screenshot and NO trace — insufficient for a GUARDED
	// UI-modality issue even though it clears the base zero-commands check.
	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"looked fine","commands":[{"cmd":"curl -s https://box/login","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, content, pgtype.UUID{})
	if verdict != "" || labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want \"\"/false (insufficient visual evidence for a guarded UI case)", verdict, labeled)
	}
	if names := labelNamesForIssue(t, issueID); !names["qa:stale"] || names["qa:pass"] {
		t.Fatalf("expected {qa:stale} only, got %v", names)
	}
}

// TestQAEvidenceFloorAllowsUnknownScopeUICaseWithoutTrace pins the inverted
// default: an UNKNOWN-scope UI case (no risk label, no PR — the common
// sprint-mode / no-per-task-PR path) with a real command but NO screenshot and
// NO trace must PASS. Forcing a trace by default here is exactly what
// dead-ended fast deterministic light QA as qa:stale; the deterministic command
// is the proportionate floor, and the zero-commands check still backstops it.
func TestQAEvidenceFloorAllowsUnknownScopeUICaseWithoutTrace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "evidence floor — unknown ui case no trace", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	if _, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ProjectID:   issue.ProjectID,
		Title:       "[e2e] greet button label",
		Steps:       "open /",
		Expected:    "button reads Greet",
		Kind:        "automated",
		Source:      "gen",
		AuthorType:  "agent",
		Category:    "positive",
		Modality:    "ui",
	}); err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}

	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"button text asserted","commands":[{"cmd":"node smoke.mjs","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, content, pgtype.UUID{})
	if verdict != "pass" || !labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want pass/true (unknown-scope UI case, deterministic command suffices)", verdict, labeled)
	}
	if names := labelNamesForIssue(t, issueID); !names["qa:pass"] || names["qa:stale"] {
		t.Fatalf("expected {qa:pass} only, got %v", names)
	}
}

// TestQAEvidenceFloorAllowsUICaseWithScreenshot confirms a screenshot alone
// (no captured trace needed) satisfies the UI-modality visual-evidence check.
func TestQAEvidenceFloorAllowsUICaseWithScreenshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "evidence floor — ui case with screenshot", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	if _, err := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ProjectID:   issue.ProjectID,
		Title:       "[e2e] login renders",
		Steps:       "open /login",
		Expected:    "form visible",
		Kind:        "automated",
		Source:      "human",
		AuthorType:  "member",
		Category:    "positive",
		Modality:    "ui",
	}); err != nil {
		t.Fatalf("CreateTestCase: %v", err)
	}

	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"looked fine","commands":[{"cmd":"node run.mjs","branch_exit":0,"kind":"pass"}],"screenshots":["/tmp/login.png"]}` +
		"\n```"
	verdict, labeled := testHandler.TaskService.CaptureQAEvidence(ctx, issue, content, pgtype.UUID{})
	if verdict != "pass" || !labeled {
		t.Fatalf("CaptureQAEvidence: verdict=%q labeled=%v, want pass/true", verdict, labeled)
	}
}
