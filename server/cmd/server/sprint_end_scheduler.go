package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sprintEndInterval is how often the scheduler polls for sprints whose window
// has closed. Sprints start on arbitrary dates, so a fixed cron can't express
// "2 weeks after THIS sprint's start" — we poll instead. Hourly is plenty: the
// sprint-end regression is heavy and the close is a human-coordinated event, so
// a few minutes of latency on the dispatch is irrelevant.
const sprintEndInterval = time.Hour

// sprintEndPayload is the per-run trigger payload the sprint-end autopilot's
// agent receives (rendered into its prompt via the schedule-source path added
// in buildIssueDescription / the daemon run_only prompt). It carries the QA
// directive: a whole-branch regression of the sprint branch against sprint-root.
type sprintEndPayload struct {
	Scope    string `json:"scope"`
	Branch   string `json:"branch"`
	Baseline string `json:"baseline"`
	SprintID string `json:"sprint_id"`
}

// runSprintEndScheduler polls for due sprints (status='active' AND
// end_date<=now()) and, for each, deploys the sprint branch to its project's QA
// box, dispatches the sprint-end regression autopilot, and marks the sprint
// completed so it stops matching on the next tick.
func runSprintEndScheduler(ctx context.Context, queries *db.Queries, svc *service.AutopilotService, h *handler.Handler) {
	ticker := time.NewTicker(sprintEndInterval)
	defer ticker.Stop()

	// Fire once on boot so a sprint that ended while the server was down is not
	// stuck waiting a full interval.
	tickSprintEnd(ctx, queries, svc, h)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickSprintEnd(ctx, queries, svc, h)
		}
	}
}

// tickSprintEnd processes every due sprint exactly once. The completion flip is
// done FIRST (and is status-guarded, so a concurrent tick can't double-fire);
// only the tick that wins the UPDATE goes on to deploy + dispatch.
func tickSprintEnd(ctx context.Context, queries *db.Queries, svc *service.AutopilotService, h *handler.Handler) {
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

		dispatchSprintEnd(ctx, queries, svc, h, completed)
	}
}

// dispatchSprintEnd deploys the sprint branch to the project's QA box, then
// dispatches the project's sprint-end regression autopilot with a
// scope=regression / baseline=sprint-root payload. Best-effort: a missing box
// or a missing autopilot logs a warning but does not undo the completion — the
// sprint window is genuinely over, and the human can re-run QA manually.
func dispatchSprintEnd(ctx context.Context, queries *db.Queries, svc *service.AutopilotService, h *handler.Handler, sprint db.Sprint) {
	branch := handler.SprintBranchFor(sprint)

	// Deploy the sprint branch to the bound QA box FIRST so the box serves the
	// sprint's accumulated change before the regression runs. A deploy failure
	// is non-fatal: the autopilot still runs (it may QA on a non-box runtime),
	// and the failure is recorded on the box row by performBoxSync.
	if _, ok, err := h.DeploySprintBranch(ctx, sprint.ID, sprint.WorkspaceID); err != nil {
		slog.Warn("sprint-end scheduler: deploy sprint branch failed",
			"sprint_id", util.UUIDToString(sprint.ID), "error", err)
	} else if !ok {
		slog.Warn("sprint-end scheduler: sprint branch sync reported failure",
			"sprint_id", util.UUIDToString(sprint.ID), "branch", branch)
	}

	autopilots, err := queries.ListActiveRunOnlyAutopilotsForProject(ctx, db.ListActiveRunOnlyAutopilotsForProjectParams{
		WorkspaceID: sprint.WorkspaceID,
		ProjectID:   sprint.ProjectID,
	})
	if err != nil {
		slog.Warn("sprint-end scheduler: list sprint-end autopilots failed",
			"sprint_id", util.UUIDToString(sprint.ID), "error", err)
		return
	}
	if len(autopilots) == 0 {
		slog.Info("sprint-end scheduler: no sprint-end autopilot bound to project; deployed branch only",
			"sprint_id", util.UUIDToString(sprint.ID),
			"project_id", util.UUIDToString(sprint.ProjectID))
		return
	}

	payload, err := json.Marshal(sprintEndPayload{
		Scope:    "regression",
		Branch:   branch,
		Baseline: "sprint-root",
		SprintID: util.UUIDToString(sprint.ID),
	})
	if err != nil {
		slog.Warn("sprint-end scheduler: marshal payload failed",
			"sprint_id", util.UUIDToString(sprint.ID), "error", err)
		return
	}

	// The oldest active run-only autopilot bound to the project is the sprint-end
	// QA runner. No trigger row exists for a sprint-end dispatch (the poll IS the
	// trigger), so triggerID is the zero/invalid UUID; trigger_id is nullable.
	ap := autopilots[0]
	if _, err := svc.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "schedule", payload); err != nil {
		slog.Warn("sprint-end scheduler: dispatch failed",
			"sprint_id", util.UUIDToString(sprint.ID),
			"autopilot_id", util.UUIDToString(ap.ID),
			"error", err)
		return
	}
	slog.Info("sprint-end scheduler: dispatched regression",
		"sprint_id", util.UUIDToString(sprint.ID),
		"autopilot_id", util.UUIDToString(ap.ID),
		"branch", branch)
}
