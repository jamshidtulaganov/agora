package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestCaptureQAEvidenceAttachesDesignVerdictLabel exercises
// TaskService.captureDesignVerdictLabel (server/internal/service/qa_evidence.go),
// which mirrors CaptureQAEvidence's qa:pass/qa:fail attach for the ADVISORY
// design-compare verdict nested at result_json.design.verdict (see
// sliceActionDesignCompareContext, design_action.go). Same replace-on-write
// semantics: attaching one design:* label detaches the other, the label is
// auto-created per workspace if missing, and a "skipped" verdict (Figma
// unreachable) touches nothing. See docs/design-stage-research.md §2 Phase 1.
func TestCaptureQAEvidenceAttachesDesignVerdictLabel(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "Design verdict label test", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	labelNames := func() map[string]bool {
		t.Helper()
		labels, err := testHandler.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
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

	// 1. A design-compare pass attaches design:pass.
	passContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"functional pass","design":{"verdict":"pass","reference_node":"1:2"}}` +
		"\n```"
	if verdict, _ := testHandler.TaskService.CaptureQAEvidence(ctx, issue, passContent); verdict != "pass" {
		t.Fatalf("CaptureQAEvidence: verdict = %q, want pass", verdict)
	}
	if names := labelNames(); !names["design:pass"] || names["design:fail"] {
		t.Fatalf("after pass: expected {design:pass} only, got %v", names)
	}

	// 2. A later design-compare fail REPLACES the label (opposite detached) —
	// independent of the top-level qa verdict, which stays "pass" here.
	failContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"functional still pass","design":{"verdict":"fail",` +
		`"mismatches":[{"kind":"color","selector":"button.primary","expected":"#2563EB","actual":"#000000"}]}}` +
		"\n```"
	if verdict, _ := testHandler.TaskService.CaptureQAEvidence(ctx, issue, failContent); verdict != "pass" {
		t.Fatalf("CaptureQAEvidence: verdict = %q, want pass (top-level verdict unrelated to design)", verdict)
	}
	if names := labelNames(); names["design:pass"] || !names["design:fail"] {
		t.Fatalf("after fail: expected {design:fail} only, got %v", names)
	}

	// 3. A "skipped" design verdict (Figma unreachable) must NOT touch labels
	// — never fail an issue for an infra reason. The prior design:fail stays.
	skippedContent := "```qa-result\n" +
		`{"verdict":"pass","summary":"figma unreachable","design":{"verdict":"skipped"}}` +
		"\n```"
	if verdict, _ := testHandler.TaskService.CaptureQAEvidence(ctx, issue, skippedContent); verdict != "pass" {
		t.Fatalf("CaptureQAEvidence: verdict = %q, want pass", verdict)
	}
	if names := labelNames(); names["design:pass"] || !names["design:fail"] {
		t.Fatalf("after skipped: expected design:fail to remain untouched, got %v", names)
	}

	// 4. A qa-result with no design object at all is likewise a no-op for
	// design labels (e.g. an issue with no Figma refs never got the appendix).
	noDesignContent := "```qa-result\n" + `{"verdict":"fail","summary":"unrelated functional failure"}` + "\n```"
	if verdict, _ := testHandler.TaskService.CaptureQAEvidence(ctx, issue, noDesignContent); verdict != "fail" {
		t.Fatalf("CaptureQAEvidence: verdict = %q, want fail", verdict)
	}
	if names := labelNames(); names["design:pass"] || !names["design:fail"] {
		t.Fatalf("with no design object: expected design:fail to remain untouched, got %v", names)
	}
}
