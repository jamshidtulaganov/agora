package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ShippedIssue is one issue in a sprint's release changelog: the human-facing
// identifier (PREFIX-number), its title, and its rolled-up QA verdict. The
// release-integrations dispatcher turns these into release notes for the
// configured connectors.
type ShippedIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Verdict    string `json:"verdict"`
}

// BuildSprintChangelog returns the shipped issues of a sprint, reusing the
// SprintReadinessRows query + the exact verdict rollup the QA cockpit's Sprint
// tab uses (sprint_readiness.go): fail if qa:fail or any failing run, pass if
// qa:pass and no failing run, else pending. This is the changelog source Thread
// B feeds to release connectors — one definition of "what shipped" shared by
// the readiness view and the outbound webhooks.
//
// Best-effort: a query error returns the error so the caller can log + skip;
// an empty sprint returns an empty (non-nil) slice.
func (s *TaskService) BuildSprintChangelog(ctx context.Context, sprintID, workspaceID pgtype.UUID) ([]ShippedIssue, error) {
	rows, err := s.Queries.SprintReadinessRows(ctx, db.SprintReadinessRowsParams{
		SprintID:    sprintID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, err
	}
	prefix := s.getIssuePrefix(workspaceID)
	out := make([]ShippedIssue, 0, len(rows))
	for _, row := range rows {
		out = append(out, ShippedIssue{
			ID:         util.UUIDToString(row.ID),
			Identifier: fmt.Sprintf("%s-%d", prefix, row.Number),
			Title:      row.Title,
			Verdict:    sprintRowVerdict(row.QaFail, row.QaPass, row.RunsFail),
		})
	}
	return out, nil
}

// sprintRowVerdict rolls a sprint-readiness row up to a single verdict. Kept as
// a standalone helper so BuildSprintChangelog and any future consumer agree
// with the handler's inline rollup (sprint_readiness.go).
func sprintRowVerdict(qaFail, qaPass bool, runsFail int64) string {
	switch {
	case qaFail || runsFail > 0:
		return "fail"
	case qaPass && runsFail == 0:
		return "pass"
	default:
		return "pending"
	}
}
