package main

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestIssueHasRunQADispatchMarker is the regression for the manual-Re-run
// stale backstop (audit finding: "manual Re-run QA has no stale backstop").
// The watchdog loop now always runs; when AGORA_AUTO_QA_ENABLED is off it only
// escalates a candidate issue that ACTUALLY had a run_qa dispatched — detected
// by this query finding the agent-protocol marker comment
// (`<!--agent-protocol:run_qa-->`) buildSliceInstruction's caller stamps at
// the front of every run_qa dispatch comment (slice_action.go,
// agentProtocolMarker). This isolates that detection query directly against
// Postgres: an issue with the marker comment reports dispatched=true; an
// issue with unrelated comments (or none) reports false.
func TestIssueHasRunQADispatchMarker(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	q := db.New(testPool)

	// number has no DB-side sequence (it's assigned by the application on the
	// normal create path) — compute the next free one per insert so two seeds
	// in the same workspace don't collide on uq_issue_workspace_number.
	var dispatchedIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		SELECT $1, 'watchdog-dispatch-marker', 'in_review', 'medium', 'member', m.user_id,
		       COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1
		FROM member m WHERE m.workspace_id = $1 LIMIT 1
		RETURNING id`, testWorkspaceID).Scan(&dispatchedIssueID); err != nil {
		t.Fatalf("seed dispatched issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, dispatchedIssueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, dispatchedIssueID)
	})

	var quietIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		SELECT $1, 'watchdog-no-marker', 'in_review', 'medium', 'member', m.user_id,
		       COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1
		FROM member m WHERE m.workspace_id = $1 LIMIT 1
		RETURNING id`, testWorkspaceID).Scan(&quietIssueID); err != nil {
		t.Fatalf("seed quiet issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, quietIssueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, quietIssueID)
	})

	// A real run_qa dispatch comment — the marker sits at the very front,
	// followed by the @mention link and the instruction body (see
	// agentProtocolMarker + buildSliceInstruction, slice_action.go).
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		SELECT $1, $2, 'member', m.user_id, $3, 'comment'
		FROM member m WHERE m.workspace_id = $2 LIMIT 1`,
		dispatchedIssueID, testWorkspaceID,
		"<!--agent-protocol:run_qa-->\n[@QA Tester](mention://agent/00000000-0000-0000-0000-000000000000) Run the QA gate.",
	); err != nil {
		t.Fatalf("seed dispatch comment: %v", err)
	}
	// An unrelated comment on the quiet issue — must NOT be mistaken for a
	// dispatch marker.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		SELECT $1, $2, 'member', m.user_id, 'just a note, no gate ever fired here', 'comment'
		FROM member m WHERE m.workspace_id = $2 LIMIT 1`,
		quietIssueID, testWorkspaceID,
	); err != nil {
		t.Fatalf("seed unrelated comment: %v", err)
	}

	dispatched, err := q.IssueHasRunQADispatchMarker(ctx, util.MustParseUUID(dispatchedIssueID))
	if err != nil {
		t.Fatalf("IssueHasRunQADispatchMarker(dispatched): %v", err)
	}
	if !dispatched {
		t.Error("expected the issue with a run_qa dispatch marker comment to report dispatched=true")
	}

	quiet, err := q.IssueHasRunQADispatchMarker(ctx, util.MustParseUUID(quietIssueID))
	if err != nil {
		t.Fatalf("IssueHasRunQADispatchMarker(quiet): %v", err)
	}
	if quiet {
		t.Error("expected the issue with no dispatch marker to report dispatched=false")
	}
}
