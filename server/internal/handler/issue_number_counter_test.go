package handler

import (
	"context"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// TestIncrementIssueCounterHealsLag reproduces the sd-main incident: issues land
// with numbers the workspace counter never advanced past (e.g. a bulk data load
// / DB restore that preserved external Bitrix numbering), leaving issue_counter
// behind max(issue.number). Before the GREATEST fix in IncrementIssueCounter,
// the next CreateIssue reused an already-taken number and failed on
// uq_issue_workspace_number (SQLSTATE 23505). The counter must self-heal so a
// manual create never collides.
func TestIncrementIssueCounterHealsLag(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Isolated workspace so we don't perturb the shared fixture's counter.
	const slug = "counter-heal-test"
	testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	var wsID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix, issue_counter)
		VALUES ('Counter Heal', $1, '', 'CHL', 179)
		RETURNING id
	`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = $1`, slug) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, wsID, testUserID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Insert an issue carrying a high explicit number while the counter lags at
	// 179 — the exact drift observed in sd-main (counter 179 vs max number 319).
	const highNumber = 319
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			creator_type, creator_id, position, number
		)
		VALUES ($1, 'Imported task', NULL, 'todo', 'none', 'member', $2, 0, $3)
	`, wsID, testUserID, highNumber); err != nil {
		t.Fatalf("insert high-numbered issue: %v", err)
	}

	// A manual create must succeed and skip past the lagging counter rather than
	// reuse 180..319 and collide.
	res, err := testHandler.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    util.MustParseUUID(wsID),
		Title:          "Manual issue after import",
		Status:         "todo",
		Priority:       "none",
		CreatorType:    "member",
		CreatorID:      util.MustParseUUID(testUserID),
		AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: testUserID})
	if err != nil {
		t.Fatalf("create manual issue: %v", err)
	}
	if res.Issue.Number != highNumber+1 {
		t.Fatalf("expected healed number %d, got %d", highNumber+1, res.Issue.Number)
	}

	// And the workspace counter is now parked above the imported max, so further
	// creates keep climbing without collision.
	var counter int32
	if err := testPool.QueryRow(ctx, `SELECT issue_counter FROM workspace WHERE id = $1`, wsID).Scan(&counter); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if counter != highNumber+1 {
		t.Fatalf("expected counter %d, got %d", highNumber+1, counter)
	}
}
