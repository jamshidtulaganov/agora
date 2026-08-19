package handler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Engine stress: many events, many issues, several rules, all concurrent. What
// must hold under load is not speed but the GUARDS — per-issue serialization, the
// cooldown, the hourly cap, and an audit row for every evaluation. A stress test
// that only measured latency would miss the one failure mode that matters: a
// guard racing itself into double application.

// stressIssue creates one bare issue row directly (the HTTP fixture is too slow
// to build fifty of them per run).
func stressIssue(t *testing.T, ctx context.Context, n int) db.Issue {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1::uuid, $2, 'in_progress', 'medium', 'member', $3::uuid,
		        (SELECT COALESCE(MAX(number),0)+1 FROM issue WHERE workspace_id = $1::uuid))
		RETURNING id::text`,
		testWorkspaceID, fmt.Sprintf("stress issue %d", n), testUserID).Scan(&id); err != nil {
		t.Fatalf("seed issue %d: %v", n, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM automation_run WHERE issue_id = $1::uuid`, id)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1::uuid`, id)
	})
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(id))
	if err != nil {
		t.Fatalf("load issue %d: %v", n, err)
	}
	return issue
}

// TestAutomationEngineStress fires 20 concurrent label events at each of 30
// issues (600 evaluations) against a rule with a long cooldown, plus a second
// rule whose conditions never match. Assertions:
//
//   - the cooldown admits EXACTLY ONE application per issue — the rest skip;
//   - the never-matching rule applies zero times and skips 600 times;
//   - every evaluation left an audit row (guards read the trail, so a lost row
//     would silently loosen them);
//   - the issue mutated exactly once (one label attach), proving the per-issue
//     lock serialized the writers.
func TestAutomationEngineStress(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testing.Short() {
		t.Skip("stress test skipped in -short")
	}
	ctx := context.Background()

	const issues = 30
	const eventsPerIssue = 20

	applies := seedAutomation(t, ctx, "stress: applies once per issue", automationTriggerLabelAttached,
		[]automationCondition{{Field: "label", Op: automationOpEq, Value: "stress:go"}},
		[]automationAction{{Type: automationActionAddLabel, Config: map[string]string{"name": "stress-touched"}}},
		pgtype.UUID{}, `{"min_interval_seconds": 3600, "max_per_hour": 1000}`)
	neverMatches := seedAutomation(t, ctx, "stress: never matches", automationTriggerLabelAttached,
		[]automationCondition{{Field: "label", Op: automationOpEq, Value: "stress:other"}},
		[]automationAction{{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}}},
		pgtype.UUID{}, "")

	rows := make([]db.Issue, 0, issues)
	for i := 0; i < issues; i++ {
		rows = append(rows, stressIssue(t, ctx, i))
	}

	start := time.Now()
	var wg sync.WaitGroup
	for _, issue := range rows {
		for e := 0; e < eventsPerIssue; e++ {
			wg.Add(1)
			go func(issue db.Issue) {
				defer wg.Done()
				testHandler.runAutomationsForEvent(ctx, AutomationEvent{
					Trigger: automationTriggerLabelAttached, Issue: issue,
					Label: "stress:go", ActorType: "member", ActorID: testUserID,
				})
			}(issue)
		}
	}
	wg.Wait()
	elapsed := time.Since(start)
	total := issues * eventsPerIssue
	t.Logf("stress: %d evaluations × 2 rules in %s (%.1f evals/s)",
		total, elapsed, float64(total*2)/elapsed.Seconds())

	type counts struct{ applied, skipped, failed int }
	countRuns := func(automationID pgtype.UUID) counts {
		var c counts
		result, err := testPool.Query(ctx,
			`SELECT status, count(*) FROM automation_run WHERE automation_id = $1 GROUP BY status`, automationID)
		if err != nil {
			t.Fatalf("count runs: %v", err)
		}
		defer result.Close()
		for result.Next() {
			var status string
			var n int
			if err := result.Scan(&status, &n); err != nil {
				t.Fatalf("scan: %v", err)
			}
			switch status {
			case "applied":
				c.applied = n
			case "skipped":
				c.skipped = n
			case "failed":
				c.failed = n
			}
		}
		return c
	}

	got := countRuns(applies.ID)
	if got.applied != issues {
		t.Errorf("cooldown breached: applied = %d, want exactly %d (one per issue)", got.applied, issues)
	}
	if got.applied+got.skipped+got.failed != total {
		t.Errorf("audit rows lost: %d+%d+%d != %d — the guards read the trail, so this loosens them",
			got.applied, got.skipped, got.failed, total)
	}
	if got.failed != 0 {
		t.Errorf("failed evaluations under stress: %d", got.failed)
	}

	never := countRuns(neverMatches.ID)
	if never.applied != 0 {
		t.Errorf("the never-matching rule applied %d times", never.applied)
	}
	if never.skipped != total {
		t.Errorf("never-matching rule skips = %d, want %d", never.skipped, total)
	}

	// Per-issue integrity: the applied rule left its label on every issue.
	for _, issue := range rows {
		if !testHandler.issueHasLabelNameHandler(ctx, issue, "stress-touched") {
			t.Errorf("issue %s: the applied rule left no label", uuidToString(issue.ID))
		}
	}
}

// TestAutomationEngineStressHourlyCap hammers ONE issue with 40 sequential
// events on a rule with no cooldown and a cap of 5 — exactly 5 must apply.
// Sequential on purpose: the cap reads committed rows, so it is a budget, not a
// mutex; sequential events are the contract it must hold exactly.
func TestAutomationEngineStressHourlyCap(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if testing.Short() {
		t.Skip("stress test skipped in -short")
	}
	ctx := context.Background()

	rule := seedAutomation(t, ctx, "stress: capped at five", automationTriggerLabelAttached, nil,
		[]automationAction{{Type: automationActionPostComment, Config: map[string]string{"body": "cap check {{issue}}"}}},
		pgtype.UUID{}, `{"min_interval_seconds": 0, "max_per_hour": 5}`)
	issue := stressIssue(t, ctx, 9999)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issue.ID)
	})

	for i := 0; i < 40; i++ {
		testHandler.runAutomationsForEvent(ctx, AutomationEvent{
			Trigger: automationTriggerLabelAttached, Issue: issue,
			Label: fmt.Sprintf("cap:%d", i), ActorType: "member", ActorID: testUserID,
		})
	}

	var applied int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM automation_run WHERE automation_id = $1 AND status = 'applied'`, rule.ID).Scan(&applied); err != nil {
		t.Fatalf("count: %v", err)
	}
	if applied != 5 {
		t.Errorf("hourly cap: applied = %d, want exactly 5", applied)
	}
	var comments int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND content LIKE 'cap check%'`, issue.ID).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if comments != 5 {
		t.Errorf("comments posted = %d, want exactly 5 (one per admitted run)", comments)
	}
}
