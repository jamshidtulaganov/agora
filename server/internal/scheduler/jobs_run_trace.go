package scheduler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/runtrace"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNameBackfillRunTraceOutcomes is the canonical audit name. Stable across
// releases — do not rename without a migration.
const JobNameBackfillRunTraceOutcomes = "backfill_run_trace_outcomes"

// runTraceSettleWindow is how long a run must sit before its outcome is judged,
// giving humans time to react, correct, or reopen before we label it.
const runTraceSettleWindow = 30 * time.Minute

// runTraceBackfillBatch caps traces labeled per tick.
const runTraceBackfillBatch = 200

// BackfillRunTraceOutcomesJob sweeps settled, still-pending agent_run_trace rows
// and labels each with a derived outcome (accepted/corrected/rejected) — the
// preference data for the fine-tuning dataset. Idempotent: only rows with
// outcome_label='pending' are touched, so a stale re-entry relabels nothing
// already finalized.
func BackfillRunTraceOutcomesJob(pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameBackfillRunTraceOutcomes,
		Cadence:           5 * time.Minute,
		ScheduleDelay:     1 * time.Minute,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{1 * time.Minute, 5 * time.Minute, 15 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeBackfillRunTraceOutcomesHandler(pool),
	}
}

func makeBackfillRunTraceOutcomesHandler(pool *pgxpool.Pool) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		cutoff := time.Now().UTC().Add(-runTraceSettleWindow)
		n, err := runtrace.BackfillOnce(ctx, db.New(pool), cutoff, runTraceBackfillBatch)
		if err != nil {
			return HandlerResult{}, err
		}
		if in.Heartbeat != nil {
			_ = in.Heartbeat(ctx)
		}
		return HandlerResult{
			RowsAffected: n,
			Result:       map[string]any{"settle_window": runTraceSettleWindow.String()},
		}, nil
	}
}
