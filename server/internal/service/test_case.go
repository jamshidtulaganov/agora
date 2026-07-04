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

// compiledScriptsBlockRe extracts the ```scripts``` array a QA agent appends when
// it COMPILES automated cases into runnable Playwright scripts (compile_tests).
var compiledScriptsBlockRe = regexp.MustCompile("(?s)```scripts\\s*\\n(.*?)```")

type genTestRun struct {
	TestCaseID string `json:"test_case_id"`
	Status     string `json:"status"`
	Output     string `json:"output"`
	// TracePath is the local path (on the agent's runtime box) of the Playwright
	// trace .zip the compiled script captured for this case, if any. Optional —
	// only scripted cases that ran with tracing produce one. The trace-viewer
	// launch endpoint (GET /api/qa/trace/:runId) reads it to spawn `playwright
	// show-trace` on that daemon and reverse-proxy the viewer in-app.
	TracePath string `json:"trace_path"`
	// BaselineStatus is the case's result when run against the PRE-CHANGE
	// baseline (merge-base / sprint last-green). A plan-driven test discriminates
	// the change only when it FAILED before (baseline_status=fail) and PASSES now
	// (status=pass). "unknown" (the default) is neutral — an [e2e] case with no
	// baseline deploy, or a discrimination-flag-off run, never counts as evidence.
	BaselineStatus string `json:"baseline_status"`
}

type genCompiledScript struct {
	ID     string `json:"id"`
	Script string `json:"script"`
}

type genTestCase struct {
	Title    string `json:"title"`
	Steps    string `json:"steps"`
	Expected string `json:"expected"`
	Kind     string `json:"kind"`
	Category string `json:"category"` // positive | negative
	Script   string `json:"script"`   // optional compiled Playwright script (automated cases)
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
		category := "positive"
		if c.Category == "negative" {
			category = "negative"
		}
		// Only automated cases carry a compiled script — a manual case never gets a
		// stray script, mirroring the kind/category normalization above.
		script := ""
		if kind == "automated" {
			script = strings.TrimSpace(c.Script)
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
			Category:    category,
			Script:      script,
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
// the case belongs to THIS issue — or is the issue's project's standing base
// script — within the same workspace before writing (an agent can't post runs
// for another issue's case). Best-effort + detached. Exported so the HTTP
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
		// Normalize the baseline result: only pass/fail are meaningful; anything
		// else (absent, "n/a", "unknown") is neutral and never counts as
		// discriminating evidence.
		baselineStatus := strings.ToLower(strings.TrimSpace(r.BaselineStatus))
		switch baselineStatus {
		case "pass", "fail":
		default:
			baselineStatus = "unknown"
		}
		caseID, err := util.ParseUUID(r.TestCaseID)
		if err != nil {
			continue
		}
		tc, err := s.Queries.GetTestCase(ctx, db.GetTestCaseParams{ID: caseID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			continue
		}
		// Accept the issue's own cases AND the project's standing base scripts
		// (issue_id NULL, same project) — QA runs execute both suites.
		sameIssue := util.UUIDToString(tc.IssueID) == util.UUIDToString(issue.ID)
		projectBase := !tc.IssueID.Valid && tc.ProjectID.Valid && issue.ProjectID.Valid &&
			util.UUIDToString(tc.ProjectID) == util.UUIDToString(issue.ProjectID)
		if !sameIssue && !projectBase {
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
			RunByID:        agentID,
			TracePath:      strings.TrimSpace(r.TracePath),
			BaselineStatus: baselineStatus,
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

// CaptureCompiledScripts persists a compile_tests agent's ```scripts``` block —
// a JSON array [{id, script}] — onto the named cases via SetTestCaseScript. Each
// entry names a test_case_id we handed the agent; we verify the case belongs to
// THIS workspace (same defensive guard as CaptureTestRuns) before writing.
// Best-effort + detached. Exported so the HTTP comment handler can call it too
// (agents post via POST /comments). A comment with no ```scripts``` block no-ops.
func (s *TaskService) CaptureCompiledScripts(ctx context.Context, issue db.Issue, content string, agentID pgtype.UUID) {
	m := compiledScriptsBlockRe.FindStringSubmatch(content)
	if m == nil {
		return
	}
	var scripts []genCompiledScript
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &scripts); err != nil {
		return
	}

	updated := 0
	for _, sc := range scripts {
		script := strings.TrimSpace(sc.Script)
		if script == "" {
			continue
		}
		caseID, err := util.ParseUUID(sc.ID)
		if err != nil {
			continue
		}
		// Verify the case is in THIS workspace before writing (an agent can't
		// compile a script onto another workspace's case).
		if _, err := s.Queries.GetTestCase(ctx, db.GetTestCaseParams{ID: caseID, WorkspaceID: issue.WorkspaceID}); err != nil {
			continue
		}
		if err := s.Queries.SetTestCaseScript(ctx, db.SetTestCaseScriptParams{ID: caseID, WorkspaceID: issue.WorkspaceID, Script: script}); err != nil {
			slog.Warn("capture compiled scripts: update failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		updated++
	}
	if updated == 0 {
		return
	}

	s.Bus.Publish(events.Event{
		Type:        protocol.EventTestCasesChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("agent compiled scripts captured", "issue_id", util.UUIDToString(issue.ID), "count", updated)
}
