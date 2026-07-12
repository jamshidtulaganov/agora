package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestHumanRunStepFailure covers the pure step-results fence parse: the
// prior-human context names the exact failed step + the human's note.
func TestHumanRunStepFailure(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			"checklist walk with a noted failure",
			"Manual step run — 1/3 passed, failed at step 2\n```step-results\n[{\"step\":1,\"status\":\"pass\"},{\"step\":2,\"status\":\"fail\",\"note\":\"toast never appears\"},{\"step\":3,\"status\":\"skip\"}]\n```",
			"failed at step 2 — toast never appears",
		},
		{
			"two failing steps",
			"```step-results\n[{\"step\":1,\"status\":\"fail\"},{\"step\":2,\"status\":\"fail\",\"note\":\"500\"}]\n```",
			"failed at step 1; failed at step 2 — 500",
		},
		{"no fence — free text", "just a note the human typed", ""},
		{"fence with no failures", "```step-results\n[{\"step\":1,\"status\":\"pass\"}]\n```", ""},
		{"malformed fence json", "```step-results\n{not json}\n```", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanRunStepFailure(tt.output); got != tt.want {
				t.Errorf("humanRunStepFailure(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// TestSliceActionPriorHumanResultsContext seeds one case with a HUMAN run
// (checklist walk, failed step + note) and one case with only an AGENT run:
// the injected context must carry the human-run case (with the failed-step
// detail) and NOT the agent-only case — human results are the ground truth
// being injected, agent runs are not.
func TestSliceActionPriorHumanResultsContext(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	issueID := createTestIssue(t, "prior human results context", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	seedCase := func(title string) db.TestCase {
		tc, cerr := testHandler.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
			WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ProjectID: issue.ProjectID,
			Title: title, Steps: "1. open\n2. save", Expected: "saved",
			Kind: "manual", Source: "human", AuthorType: "member", Category: "positive",
		})
		if cerr != nil {
			t.Fatalf("CreateTestCase %s: %v", title, cerr)
		}
		return tc
	}
	humanCase := seedCase("checkout saves the order")
	agentCase := seedCase("api returns 200")

	// The human's checklist walk — failed at step 2 with a note.
	if _, err := testHandler.Queries.CreateTestRun(ctx, db.CreateTestRunParams{
		WorkspaceID: issue.WorkspaceID, TestCaseID: humanCase.ID, IssueID: issue.ID,
		Status: "fail",
		Output: "Manual step run — 1/2 passed, failed at step 2\n```step-results\n[{\"step\":1,\"status\":\"pass\"},{\"step\":2,\"status\":\"fail\",\"note\":\"save button stays disabled\"}]\n```",
		RunSource: "human", RunByType: "member",
	}); err != nil {
		t.Fatalf("CreateTestRun human: %v", err)
	}
	// An agent run on the other case — must NOT appear.
	if _, err := testHandler.Queries.CreateTestRun(ctx, db.CreateTestRunParams{
		WorkspaceID: issue.WorkspaceID, TestCaseID: agentCase.ID, IssueID: issue.ID,
		Status: "pass", Output: "200 OK", RunSource: "agent", RunByType: "agent",
	}); err != nil {
		t.Fatalf("CreateTestRun agent: %v", err)
	}

	got := testHandler.sliceActionPriorHumanResultsContext(ctx, issue)
	if got == "" {
		t.Fatal("expected a non-empty prior-human-results context")
	}
	if !strings.Contains(got, "PRIOR HUMAN RESULTS") {
		t.Errorf("missing the PRIOR HUMAN RESULTS framing: %q", got)
	}
	if !strings.Contains(got, uuidToString(humanCase.ID)) || !strings.Contains(got, "failed at step 2 — save button stays disabled") {
		t.Errorf("missing the human case's failed-step detail: %q", got)
	}
	if !strings.Contains(got, "CONFIRM AND LOCALIZE") {
		t.Errorf("missing the confirm-and-localize directive: %q", got)
	}
	if strings.Contains(got, uuidToString(agentCase.ID)) {
		t.Errorf("agent-only case must NOT appear in the human-results context: %q", got)
	}
}

// TestSliceActionPriorHumanResultsContextEmpty: no human runs → "" (the
// injector adds nothing to the prompt).
func TestSliceActionPriorHumanResultsContextEmpty(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := createTestIssue(t, "prior human results — none", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got := testHandler.sliceActionPriorHumanResultsContext(ctx, issue); got != "" {
		t.Errorf("expected empty context with no human runs, got %q", got)
	}
}
