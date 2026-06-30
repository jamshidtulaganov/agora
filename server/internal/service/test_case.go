package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// testCasesBlockRe extracts the ```test-cases``` fenced JSON array a QA-Squad
// agent appends when authoring cases (the gen_test_cases slice action).
var testCasesBlockRe = regexp.MustCompile("(?s)```test-cases\\s*\\n(.*?)```")

// testRunsBlockRe extracts the ```test-runs``` array a QA agent appends when it
// RUNS the automated cases (the run_test_cases slice action).
var testRunsBlockRe = regexp.MustCompile("(?s)```test-runs\\s*\\n(.*?)```")

type genTestRun struct {
	TestCaseID string `json:"test_case_id"`
	Status     string `json:"status"`
	Output     string `json:"output"`
}

type genTestCase struct {
	Title    string `json:"title"`
	Steps    string `json:"steps"`
	Expected string `json:"expected"`
	Kind     string `json:"kind"`
}

// captureTestCases persists a gen_test_cases agent comment's ```test-cases```
// block as test_case rows (source=agent), so the QA team's Test-cases panel reads
// them. Best-effort + detached: a miss (no block, malformed JSON, empty) no-ops.
// parseTestCasesBlock extracts the ```test-cases``` JSON array a gen_test_cases
// agent appends. Returns the cases + ok=false on no block / malformed JSON.
func parseTestCasesBlock(content string) (cases []genTestCase, ok bool) {
	m := testCasesBlockRe.FindStringSubmatch(content)
	if m == nil {
		return nil, false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &cases); err != nil {
		return nil, false
	}
	return cases, true
}

// CaptureTestCases is exported so the HTTP comment handler can call it too:
// real agents (daemon/CLI) post their cases via POST /comments, not the internal
// createAgentComment path.
func (s *TaskService) CaptureTestCases(ctx context.Context, issue db.Issue, content string, agentID pgtype.UUID) {
	cases, ok := parseTestCasesBlock(content)
	if !ok {
		return
	}

	inserted := 0
	for _, c := range cases {
		title := strings.TrimSpace(c.Title)
		if title == "" {
			continue
		}
		kind := "manual"
		if c.Kind == "automated" {
			kind = "automated"
		}
		if _, err := s.Queries.CreateTestCase(ctx, db.CreateTestCaseParams{
			WorkspaceID: issue.WorkspaceID,
			IssueID:     issue.ID,
			ProjectID:   issue.ProjectID,
			Title:       title,
			Steps:       strings.TrimSpace(c.Steps),
			Expected:    strings.TrimSpace(c.Expected),
			Kind:        kind,
			Source:      "agent",
			AuthorType:  "agent",
			AuthorID:    agentID,
		}); err != nil {
			slog.Warn("capture test cases: insert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		inserted++
	}
	if inserted == 0 {
		return
	}

	s.Bus.Publish(events.Event{
		Type:        protocol.EventTestCasesChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("agent test cases captured", "issue_id", util.UUIDToString(issue.ID), "count", inserted)
}

// CaptureTestRuns persists a run_test_cases agent's ```test-runs``` block as
// test_run rows. Each entry names a test_case_id we handed the agent; we verify
// the case belongs to THIS issue+workspace before writing (an agent can't post
// runs for another issue's case). Best-effort + detached. Exported so the HTTP
// comment handler can call it too (agents post via POST /comments).
func (s *TaskService) CaptureTestRuns(ctx context.Context, issue db.Issue, content string, agentID pgtype.UUID) {
	m := testRunsBlockRe.FindStringSubmatch(content)
	if m == nil {
		return
	}
	var runs []genTestRun
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &runs); err != nil {
		return
	}

	inserted := 0
	for _, r := range runs {
		status := r.Status
		switch status {
		case "pass", "fail", "skip", "blocked":
		default:
			continue
		}
		caseID, err := util.ParseUUID(r.TestCaseID)
		if err != nil {
			continue
		}
		tc, err := s.Queries.GetTestCase(ctx, db.GetTestCaseParams{ID: caseID, WorkspaceID: issue.WorkspaceID})
		if err != nil || util.UUIDToString(tc.IssueID) != util.UUIDToString(issue.ID) {
			continue
		}
		if _, err := s.Queries.CreateTestRun(ctx, db.CreateTestRunParams{
			WorkspaceID: issue.WorkspaceID,
			TestCaseID:  tc.ID,
			IssueID:     issue.ID,
			Status:      status,
			Output:      strings.TrimSpace(r.Output),
			RunSource:   "agent",
			RunByType:   "agent",
			RunByID:     agentID,
		}); err != nil {
			slog.Warn("capture test runs: insert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		inserted++
	}
	if inserted == 0 {
		return
	}

	s.Bus.Publish(events.Event{
		Type:        protocol.EventTestCasesChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("agent test runs captured", "issue_id", util.UUIDToString(issue.ID), "count", inserted)
}
