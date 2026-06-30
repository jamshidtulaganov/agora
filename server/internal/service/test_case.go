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
