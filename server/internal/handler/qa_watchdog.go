package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// qaGateNoVerdictNote is the loud comment the watchdog posts when a QA gate
// produced no verdict. It is explicit that this is NOT a test failure — it is a
// gate that did not RUN — so the issue is marked qa:stale (gate didn't run),
// which blocks like a missing verdict should, WITHOUT reading as a real test
// failure in the cockpit. The audit found the previous qa:fail minting was the
// main inflater of the "need fix" lane (33/33 watchdog comments vs 3 real
// verdicts) and its "then this clears" promise was false — qa:stale IS cleared
// by the fresh-cycle label sweep on the next in_review re-entry and replaced
// by whatever verdict the re-run produces.
const qaGateNoVerdictNote = "⚠️ QA gate has NO verdict. The run_qa gate fired on in_review but never produced a " +
	"qa:pass / qa:fail result (the agent failed, hit a usage limit, or was never dispatched). This is NOT a pass — a " +
	"missing verdict must block, not read as green. Marking qa:stale so this surfaces as \"gate didn't run\" (not a " +
	"test failure); re-run QA (Re-run on the QA review page) to get a real verdict."

// ensureLabel resolves a label id by name, creating it if missing. Used by the
// watchdog so qa:fail exists even in a workspace that has never run QA.
func (h *Handler) ensureLabel(ctx context.Context, wsID pgtype.UUID, name, color string) (pgtype.UUID, error) {
	l, err := h.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{WorkspaceID: wsID, Name: name})
	if err == nil {
		return l.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	created, err := h.Queries.CreateLabel(ctx, db.CreateLabelParams{WorkspaceID: wsID, Name: name, Color: color})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}

// EscalateStaleQAGate converts a silently-dead QA gate into a LOUD blocked state:
// it attaches qa:stale and posts an explanatory system comment. Idempotent by
// construction — the watchdog query excludes issues that already carry a gate
// label. qa:stale (NOT qa:fail): a gate that never ran is an infrastructure
// problem, and minting it as a test failure both lies to the cockpit and — with
// verdict labels now replace-on-write — could never be told apart from a real
// regression.
func (h *Handler) EscalateStaleQAGate(ctx context.Context, issueID, workspaceID pgtype.UUID, title string) {
	labelID, err := h.ensureLabel(ctx, workspaceID, "qa:stale", "#f59e0b")
	if err != nil {
		slog.Warn("qa watchdog: ensure qa:stale label failed", "error", err, "issue_id", util.UUIDToString(issueID))
		return
	}
	if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     issueID,
		LabelID:     labelID,
		WorkspaceID: workspaceID,
	}); err != nil {
		slog.Warn("qa watchdog: attach qa:stale failed", "error", err, "issue_id", util.UUIDToString(issueID))
		return
	}
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: workspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     qaGateNoVerdictNote,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err == nil {
		h.publish(protocol.EventCommentCreated, util.UUIDToString(workspaceID), "system", "", map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(comment.ID),
				"issue_id":    util.UUIDToString(issueID),
				"author_type": "system",
				"content":     comment.Content,
				"type":        comment.Type,
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
		})
	}
	h.publish(protocol.EventIssueLabelsChanged, util.UUIDToString(workspaceID), "system", "", map[string]any{
		"issue_id": util.UUIDToString(issueID),
	})
	slog.Info("qa watchdog: escalated silent gate to qa:stale", "issue_id", util.UUIDToString(issueID), "title", title)
}
