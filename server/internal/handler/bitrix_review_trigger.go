package handler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Bitrix-driven review trigger: the dev team drags a task into its kanban's
// CODE REVIEW column and that move — not an Agora status edit — starts the
// automated pipeline (review → E2E/regression).
//
// Why the stage and not the status: MapStage collapses Code Review, Ready for
// testing, Testing, Need Merge and Ready for release ALL into Agora's single
// in_review column, so a move from "Testing" to "Code Review" changes NO Agora
// status and a status-change hook would never see it. The raw column name is
// persisted per issue in metadata (`bitrix_stage`, setBitrixIssueMetadata), so
// the previous column is available to diff against the incoming one.
//
// Why the sync path needs its own hook at all: syncBitrixTaskWithState writes
// status with a RAW, bus-free Queries.UpdateIssueStatus (deliberately — the
// outbound mirror listens on the bus and would echo the change straight back to
// Bitrix), which means NONE of the status-change automations in issue.go run for
// a Bitrix-driven move. Before this hook, a task entering Code Review on the
// portal produced a status write and nothing else.

// bitrixStageMetaKey is the issue-metadata key holding the raw Bitrix kanban
// column name (written by setBitrixIssueMetadata on every sync).
const bitrixStageMetaKey = "bitrix_stage"

// bitrixStageFromMetadata reads the last-synced Bitrix column name off an
// issue's metadata. "" when the issue has never carried a resolvable stage.
func bitrixStageFromMetadata(raw []byte) string {
	return strings.TrimSpace(metaString(raw, bitrixStageMetaKey))
}

// bitrixCodeReviewEntered reports whether this sync is the moment the task
// ENTERED the code-review column: the incoming column is a review column and
// the previously synced one was not.
//
// Entry-only is what makes the trigger idempotent against the poll loop, which
// re-reads every active task on a fixed interval: without the prev-column diff,
// each poll cycle would re-fire a review for a task simply parked in Code
// Review. An unknown previous column (empty metadata — first sync, or an issue
// synced before the stage was recorded) counts as "was not in review", so a
// task already sitting in Code Review when this ships gets exactly one review.
func bitrixCodeReviewEntered(prevStage, newStage string) bool {
	return bitrix.StageIsCodeReview(newStage) && !bitrix.StageIsCodeReview(prevStage)
}

// onBitrixStageChanged is the sync-path automation hook. It runs after the raw
// status/assignee reconcile, receives the column the issue carried BEFORE this
// sync (captured before setBitrixIssueMetadata overwrites it), and fires the
// review-first chain on entry into the code-review column.
//
// The dispatch is detached (safeGo): the sync loop holds a per-task advisory
// lock and must not wait on reviewer resolution, comment creation and task
// triggering. Everything downstream is best-effort and self-gated, so a miss
// costs a review, never a sync.
func (h *Handler) onBitrixStageChanged(ctx context.Context, issue db.Issue, prevStage, newStage string) {
	// User-defined automations see EVERY column move (a rule may care about
	// "Ready for release" or a board this code knows nothing about), so this is
	// emitted before the code-review-specific gate below.
	if strings.TrimSpace(newStage) != "" && !strings.EqualFold(strings.TrimSpace(prevStage), strings.TrimSpace(newStage)) {
		h.emitAutomationEvent(ctx, AutomationEvent{
			Trigger: automationTriggerStageChanged, Issue: issue,
			Stage: newStage, PrevStage: prevStage,
			ActorType: "system", ActorID: "",
		})
	}
	if !bitrixCodeReviewEntered(prevStage, newStage) {
		return
	}
	issueID := uuidToString(issue.ID)
	safeGo("bitrixCodeReviewStage:"+issueID, func() {
		bg := context.Background()
		// Re-read the issue: the caller's row predates this sync's status write
		// and metadata stamps (pr_number among them, which the review gate keys
		// on), and the dispatch decisions must see the current row.
		fresh, err := h.Queries.GetIssue(bg, issue.ID)
		if err != nil {
			slog.Warn("bitrix code-review trigger: reload issue failed", "issue_id", issueID, "error", err)
			return
		}
		switch fresh.Status {
		case "done", "cancelled":
			// The portal moved it into review, but Agora considers the task
			// finished — do not resurrect a closed task's pipeline.
			return
		}
		// A fresh review cycle: the diff about to be reviewed is NOT the one the
		// previous cycle judged, so the previous cycle's QA verdict, review
		// verdict and human approval are all stale. Without this, a task that
		// failed review, got fixed, and came BACK to Code Review would still
		// carry review:fail — and dispatchRunReview refuses to dispatch while a
		// verdict stands, so the second review would never happen.
		h.clearStaleQAGateLabels(bg, fresh)

		slog.Info("bitrix code-review stage entered",
			"issue_id", issueID, "prev_stage", prevStage, "stage", newStage)
		h.maybeRunReviewOnCodeReviewStage(bg, fresh, "member", uuidToString(fresh.CreatorID))
	})
}
