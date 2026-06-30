package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sprintEndInterval is how often the scheduler polls for sprints whose window
// has closed. Sprints start on arbitrary dates, so a fixed cron can't express
// "2 weeks after THIS sprint's start" — we poll instead. Hourly is plenty: the
// sprint-end regression is heavy and the close is a human-coordinated event, so
// a few minutes of latency on the dispatch is irrelevant.
const sprintEndInterval = time.Hour

// runSprintEndScheduler polls for due sprints (status='active' AND
// end_date<=now()) and, for each, deploys the sprint branch to its project's QA
// box, dispatches the sprint-end regression autopilot, and marks the sprint
// completed so it stops matching on the next tick.
func runSprintEndScheduler(ctx context.Context, queries *db.Queries, h *handler.Handler) {
	ticker := time.NewTicker(sprintEndInterval)
	defer ticker.Stop()

	// Fire once on boot so a sprint that ended while the server was down is not
	// stuck waiting a full interval.
	tickSprintEnd(ctx, queries, h)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickSprintEnd(ctx, queries, h)
		}
	}
}

// tickSprintEnd processes every due sprint exactly once. The completion flip is
// done FIRST (and is status-guarded, so a concurrent tick can't double-fire);
// only the tick that wins the UPDATE goes on to deploy + dispatch.
func tickSprintEnd(ctx context.Context, queries *db.Queries, h *handler.Handler) {
	sprints, err := queries.ListDueSprints(ctx)
	if err != nil {
		slog.Warn("sprint-end scheduler: failed to list due sprints", "error", err)
		return
	}
	if len(sprints) == 0 {
		return
	}
	slog.Info("sprint-end scheduler: due sprints", "count", len(sprints))

	for _, sprint := range sprints {
		// Win the completion race FIRST. MarkSprintCompleted is guarded by
		// status='active', so only the first caller across concurrent ticks /
		// instances matches a row; a no-rows result means another tick already
		// claimed this sprint — skip it (no re-dispatch, no repeated regression).
		completed, err := queries.MarkSprintCompleted(ctx, db.MarkSprintCompletedParams{
			ID:          sprint.ID,
			WorkspaceID: sprint.WorkspaceID,
		})
		if err != nil {
			// pgx.ErrNoRows here means a concurrent tick already completed it.
			slog.Info("sprint-end scheduler: sprint already claimed, skipping",
				"sprint_id", util.UUIDToString(sprint.ID))
			continue
		}

		dispatchSprintEnd(ctx, h, completed)
	}
}

// dispatchSprintEnd dispatches the project's sprint-end regression
// (scope=regression / baseline=sprint-root) via the same shared path a human
// re-run from the QA review page uses (handler.Handler.DispatchSprintRegression)
// — source="schedule" is the only thing that distinguishes this call from a
// manual one. Best-effort: a missing box or a missing autopilot logs a warning
// but does not undo the sprint's completion — the window is genuinely over,
// and the human can re-run QA manually.
func dispatchSprintEnd(ctx context.Context, h *handler.Handler, sprint db.Sprint) {
	if _, err := h.DispatchSprintRegression(ctx, sprint.ID, sprint.WorkspaceID, "schedule"); err != nil {
		slog.Warn("sprint-end scheduler: dispatch regression failed",
			"sprint_id", util.UUIDToString(sprint.ID), "error", err)
		return
	}
	slog.Info("sprint-end scheduler: dispatched regression",
		"sprint_id", util.UUIDToString(sprint.ID),
		"branch", handler.SprintBranchFor(sprint))
}
