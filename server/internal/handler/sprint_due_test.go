package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestListDueSprintsAndMarkCompleted covers the sprint-end dispatch primitives:
// ListDueSprints must surface ONLY sprints that are status='active' with an
// end_date in the past (planned / completed / future-end sprints are excluded),
// and MarkSprintCompleted must be status-guarded so two concurrent scheduler
// ticks can't both dispatch the same sprint (the second UPDATE matches no row).
func TestListDueSprintsAndMarkCompleted(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	q := testHandler.Queries

	wsUUID := parseUUID(testWorkspaceID)

	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "Due-Sprint Test Project").Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projUUID := parseUUID(projectID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM sprint WHERE project_id = $1`, projectID)
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	now := time.Now().UTC()
	past := pgtype.Timestamptz{Time: now.Add(-24 * time.Hour), Valid: true}
	future := pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true}

	mk := func(name, status string, end pgtype.Timestamptz) db.Sprint {
		s, err := q.CreateSprint(ctx, db.CreateSprintParams{
			WorkspaceID: wsUUID,
			ProjectID:   projUUID,
			Name:        name,
			Goal:        "",
			Status:      status,
			StartDate:   pgtype.Timestamptz{Time: now.Add(-14 * 24 * time.Hour), Valid: true},
			EndDate:     end,
		})
		if err != nil {
			t.Fatalf("create sprint %q: %v", name, err)
		}
		return s
	}

	dueActive := mk("active-past", "active", past)
	mk("active-future", "active", future)              // not due yet
	mk("planned-past", "planned", past)                // not active
	mk("completed-past", "completed", past)            // already done
	mk("active-noend", "active", pgtype.Timestamptz{}) // no end_date → never due

	due, err := q.ListDueSprints(ctx)
	if err != nil {
		t.Fatalf("ListDueSprints: %v", err)
	}
	// Filter to this project (the table may hold other test rows).
	var got []db.Sprint
	for _, s := range due {
		if s.ProjectID.Bytes == projUUID.Bytes {
			got = append(got, s)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 due sprint for project, got %d", len(got))
	}
	if got[0].ID.Bytes != dueActive.ID.Bytes {
		t.Fatalf("wrong sprint surfaced as due: %v", got[0].Name)
	}

	// First completion wins.
	completed, err := q.MarkSprintCompleted(ctx, db.MarkSprintCompletedParams{
		ID:          dueActive.ID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		t.Fatalf("MarkSprintCompleted (first): %v", err)
	}
	if completed.Status != "completed" {
		t.Fatalf("expected status completed, got %q", completed.Status)
	}

	// Second completion (concurrent tick) matches no active row → ErrNoRows.
	if _, err := q.MarkSprintCompleted(ctx, db.MarkSprintCompletedParams{
		ID:          dueActive.ID,
		WorkspaceID: wsUUID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second MarkSprintCompleted should report no rows, got: %v", err)
	}

	// After completion it no longer appears as due.
	due2, err := q.ListDueSprints(ctx)
	if err != nil {
		t.Fatalf("ListDueSprints (after): %v", err)
	}
	for _, s := range due2 {
		if s.ID.Bytes == dueActive.ID.Bytes {
			t.Fatalf("completed sprint should not be due anymore")
		}
	}
}
