package service

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

type runtimeClaimFixture struct {
	pool       *pgxpool.Pool
	service    *TaskService
	workspace  pgtype.UUID
	runtimeOne pgtype.UUID
	runtimeTwo pgtype.UUID
	agent      pgtype.UUID
}

func newRuntimeClaimFixture(t *testing.T, maxConcurrent int) runtimeClaimFixture {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://agora:agora@localhost:5432/agora?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("task claim integration tests require Postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("task claim integration tests require Postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()[:8]
	var workspaceID string
	if err := pool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, '', 'TCQ')
RETURNING id
`, "Task Claim Test", "task-claim-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("delete task claim workspace: %v", err)
		}
	})

	createRuntime := func(name string) pgtype.UUID {
		var runtimeID string
		if err := pool.QueryRow(ctx, `
INSERT INTO agent_runtime (
workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at
) VALUES ($1, $2, 'cloud', 'task-claim-test', 'online', '{}'::jsonb, now())
RETURNING id
`, workspaceID, name).Scan(&runtimeID); err != nil {
			t.Fatalf("create runtime %q: %v", name, err)
		}
		return util.MustParseUUID(runtimeID)
	}
	runtimeOne := createRuntime("Task claim runtime one " + suffix)
	runtimeTwo := createRuntime("Task claim runtime two " + suffix)

	var agentID string
	if err := pool.QueryRow(ctx, `
INSERT INTO agent (
workspace_id, name, runtime_mode, runtime_config, runtime_id,
visibility, max_concurrent_tasks
) VALUES ($1, $2, 'cloud', '{}'::jsonb, $3, 'workspace', $4)
RETURNING id
`, workspaceID, "Task claim agent "+suffix, util.UUIDToString(runtimeOne), maxConcurrent).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	queries := db.New(pool)
	return runtimeClaimFixture{
		pool:       pool,
		service:    NewTaskService(queries, pool, nil, events.New()),
		workspace:  util.MustParseUUID(workspaceID),
		runtimeOne: runtimeOne,
		runtimeTwo: runtimeTwo,
		agent:      util.MustParseUUID(agentID),
	}
}

func (f runtimeClaimFixture) queueIssueTask(t *testing.T, runtimeID pgtype.UUID, number, priority int) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var issueID string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO issue (
workspace_id, title, status, priority, creator_type, creator_id, number, position
) VALUES ($1, $2, 'todo', 'none', 'agent', $3, $4, 0)
RETURNING id
`, util.UUIDToString(f.workspace), "Task claim issue", util.UUIDToString(f.agent), number).Scan(&issueID); err != nil {
		t.Fatalf("create issue %d: %v", number, err)
	}

	var taskID string
	if err := f.pool.QueryRow(ctx, `
INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
VALUES ($1, $2, $3, 'queued', $4)
RETURNING id
`, util.UUIDToString(f.agent), util.UUIDToString(runtimeID), issueID, priority).Scan(&taskID); err != nil {
		t.Fatalf("queue issue task %d: %v", number, err)
	}
	return util.MustParseUUID(taskID)
}

func TestClaimTaskForRuntimeDoesNotDispatchAnotherRuntimeTask(t *testing.T) {
	fixture := newRuntimeClaimFixture(t, 2)
	ctx := context.Background()

	// The foreign-runtime task deliberately has higher priority. The old
	// runtime-candidate -> agent-global claim path selected this row, dispatched
	// it, and then discarded it only after noticing the runtime mismatch.
	foreignTask := fixture.queueIssueTask(t, fixture.runtimeTwo, 1, 100)
	ownTask := fixture.queueIssueTask(t, fixture.runtimeOne, 2, 1)

	claimed, err := fixture.service.ClaimTaskForRuntime(ctx, fixture.runtimeOne)
	if err != nil {
		t.Fatalf("claim runtime-one task: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected runtime-one task, got nil")
	}
	if claimed.ID != ownTask || claimed.RuntimeID != fixture.runtimeOne {
		t.Fatalf("claimed task = %s runtime=%s, want task=%s runtime=%s",
			util.UUIDToString(claimed.ID), util.UUIDToString(claimed.RuntimeID),
			util.UUIDToString(ownTask), util.UUIDToString(fixture.runtimeOne))
	}

	var foreignStatus string
	if err := fixture.pool.QueryRow(ctx,
		`SELECT status FROM agent_task_queue WHERE id = $1`,
		util.UUIDToString(foreignTask),
	).Scan(&foreignStatus); err != nil {
		t.Fatalf("read foreign task: %v", err)
	}
	if foreignStatus != "queued" {
		t.Fatalf("foreign-runtime task status = %q, want queued", foreignStatus)
	}
}

func TestClaimTaskForRuntimeConcurrentCapacityIsAtomic(t *testing.T) {
	fixture := newRuntimeClaimFixture(t, 1)
	ctx := context.Background()
	firstTask := fixture.queueIssueTask(t, fixture.runtimeOne, 1, 100)
	secondTask := fixture.queueIssueTask(t, fixture.runtimeOne, 2, 90)

	// Keep the first claim uncommitted so the competing poll overlaps it
	// deterministically. ClaimAgentTaskForRuntime locks the shared agent row;
	// the competing statement must skip that agent instead of dispatching its
	// second task from a stale capacity count.
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first claim transaction: %v", err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = tx.Rollback(context.Background())
		}
	})

	transactionalService := NewTaskService(db.New(tx), nil, nil, events.New())
	first, err := transactionalService.ClaimTaskForRuntime(ctx, fixture.runtimeOne)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first == nil || first.ID != firstTask {
		t.Fatalf("first claim = %#v, want task %s", first, util.UUIDToString(firstTask))
	}

	competing, err := fixture.service.ClaimTaskForRuntime(ctx, fixture.runtimeOne)
	if err != nil {
		t.Fatalf("competing claim: %v", err)
	}
	if competing != nil {
		t.Fatalf("competing claim exceeded max concurrency with task %s", util.UUIDToString(competing.ID))
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit first claim: %v", err)
	}
	finished = true

	// Capacity must remain closed after the agent-row lock is released because
	// the committed dispatched row now participates in the same SQL predicate.
	afterCommit, err := fixture.service.ClaimTaskForRuntime(ctx, fixture.runtimeOne)
	if err != nil {
		t.Fatalf("post-commit capacity check: %v", err)
	}
	if afterCommit != nil {
		t.Fatalf("post-commit claim exceeded max concurrency with task %s", util.UUIDToString(afterCommit.ID))
	}

	var dispatched, queued int
	if err := fixture.pool.QueryRow(ctx, `
SELECT
count(*) FILTER (WHERE status = 'dispatched'),
count(*) FILTER (WHERE status = 'queued')
FROM agent_task_queue
WHERE id = ANY($1::uuid[])
`, []string{util.UUIDToString(firstTask), util.UUIDToString(secondTask)}).Scan(&dispatched, &queued); err != nil {
		t.Fatalf("count task statuses: %v", err)
	}
	if dispatched != 1 || queued != 1 {
		t.Fatalf("task statuses: dispatched=%d queued=%d, want 1/1", dispatched, queued)
	}
}
