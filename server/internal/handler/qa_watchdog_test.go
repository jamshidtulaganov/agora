package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestQAWatchdog_EscalatesSilentGate verifies the silent-failure SPOF guard end
// to end against the DB: a stale in_review issue with no qa:pass/qa:fail verdict
// and no live task is detected, escalated to qa:fail + a loud system comment,
// and then excluded from the next sweep (idempotent).
func TestQAWatchdog_EscalatesSilentGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsUUID := parseUUID(testWorkspaceID)

	id := createTestIssue(t, "watchdog-silent-gate", "in_review", "medium")
	issueUUID := parseUUID(id)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id=$1`, issueUUID)
		testPool.Exec(ctx, `DELETE FROM issue_to_label WHERE issue_id=$1`, issueUUID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issueUUID)
	})
	// Stale (past the 1-min threshold) but inside the recent 24h window.
	if _, err := testPool.Exec(ctx, `UPDATE issue SET updated_at = now() - interval '5 minutes' WHERE id=$1`, issueUUID); err != nil {
		t.Fatal(err)
	}

	params := db.ListStaleUnverifiedQAGatesParams{Column1: 1, Column2: 24}
	contains := func(gates []db.ListStaleUnverifiedQAGatesRow) bool {
		for _, g := range gates {
			if uuidToString(g.ID) == id {
				return true
			}
		}
		return false
	}

	gates, err := testHandler.Queries.ListStaleUnverifiedQAGates(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(gates) {
		t.Fatal("watchdog query must include the stale, unverified in_review gate")
	}

	testHandler.EscalateStaleQAGate(ctx, issueUUID, wsUUID, "watchdog-silent-gate")

	var labelCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM issue_to_label il JOIN issue_label l ON l.id=il.label_id WHERE il.issue_id=$1 AND l.name='qa:fail'`,
		issueUUID).Scan(&labelCount); err != nil {
		t.Fatal(err)
	}
	if labelCount != 1 {
		t.Errorf("expected qa:fail attached after escalation, got %d", labelCount)
	}

	var commentCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM comment WHERE issue_id=$1 AND author_type='system'`, issueUUID).Scan(&commentCount); err != nil {
		t.Fatal(err)
	}
	if commentCount < 1 {
		t.Error("expected a loud system comment explaining the missing verdict")
	}

	// Idempotent: the escalated gate now carries qa:fail, so it must drop out.
	gates2, err := testHandler.Queries.ListStaleUnverifiedQAGates(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if contains(gates2) {
		t.Error("an escalated gate must be excluded from the next sweep (idempotent)")
	}
}
