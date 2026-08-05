package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// aggRow answers a GetIssuePullRequestCloseAggregate :one scan (two int64s:
// open_count, merged_with_close_intent_count).
type aggRow struct{ open, merged int64 }

func (r *aggRow) Scan(dest ...any) error {
	if len(dest) >= 1 {
		if p, ok := dest[0].(*int64); ok {
			*p = r.open
		}
	}
	if len(dest) >= 2 {
		if p, ok := dest[1].(*int64); ok {
			*p = r.merged
		}
	}
	return nil
}

// mockPRAggDBTX answers only the open-PR aggregate query; everything else is
// ErrNoRows. MaybeRetryFailedTask receives the parent task as a parameter, so
// the open-PR check is the only DB read it performs before the guard returns.
type mockPRAggDBTX struct{ open int64 }

func (m *mockPRAggDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (m *mockPRAggDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (m *mockPRAggDBTX) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	if strings.Contains(sql, "merged_with_close_intent_count") {
		return &aggRow{open: m.open}
	}
	return &mockRow{err: pgx.ErrNoRows}
}

// An infra-shaped failure (runtime_recovery) is normally auto-retried, but once
// the issue carries an in-flight PR the agent already delivered — re-running
// would fork a duplicate branch. The guard must suppress the retry.
func TestMaybeRetryFailedTask_SkipsWhenOpenPR(t *testing.T) {
	parent := db.AgentTaskQueue{
		ID:            testUUID(1),
		AgentID:       testUUID(2),
		IssueID:       testUUID(3),
		Status:        "failed",
		FailureReason: pgtype.Text{String: "runtime_recovery", Valid: true},
		Attempt:       0,
		MaxAttempts:   3,
	}
	svc := &TaskService{Queries: db.New(&mockPRAggDBTX{open: 1}), Bus: events.New()}

	child, err := svc.MaybeRetryFailedTask(context.Background(), parent)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if child != nil {
		t.Fatal("expected nil child (retry suppressed by an in-flight PR), got a retry task")
	}
}

func TestGenericTaskRecoverySkipsOrchestrationOwnedTasks(t *testing.T) {
	parent := db.AgentTaskQueue{
		Status:              "failed",
		FailureReason:       pgtype.Text{String: "runtime_recovery", Valid: true},
		Attempt:             0,
		MaxAttempts:         3,
		OrchestrationStepID: testUUID(9),
	}
	svc := &TaskService{}

	child, err := svc.MaybeRetryFailedTask(context.Background(), parent)
	if err != nil || child != nil {
		t.Fatalf("generic retry must skip orchestration task: child=%#v err=%v", child, err)
	}
	child, err = svc.maybeFailoverToFallbackRuntime(context.Background(), parent, "agent_provider_quota_limit")
	if err != nil || child != nil {
		t.Fatalf("generic failover must skip orchestration task: child=%#v err=%v", child, err)
	}
}
