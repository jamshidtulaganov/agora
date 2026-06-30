package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// qaGateNoVerdictNote is the loud comment the watchdog posts when a QA gate
// produced no verdict. It is explicit that this is NOT a test failure — it is a
// gate that did not RUN — so the issue blocks (qa:fail) instead of reading green.
const qaGateNoVerdictNote = "⚠️ QA gate has NO verdict. The run_qa gate fired on in_review but never produced a " +
	"qa:pass / qa:fail result (the agent failed, hit a usage limit, or was never dispatched). This is NOT a pass — a " +
	"missing verdict must block, not read as green. Marking qa:fail so this surfaces in the QA queue; re-run QA " +
	"(Re-run on the QA review page) to get a real verdict, then this clears."

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
// it attaches qa:fail and posts an explanatory system comment. Idempotent by
// construction — the watchdog query excludes issues that already carry qa:fail,
// so a re-run of run_qa (which the note asks for) is what clears it.
func (h *Handler) EscalateStaleQAGate(ctx context.Context, issueID, workspaceID pgtype.UUID, title string) {
	labelID, err := h.ensureLabel(ctx, workspaceID, "qa:fail", "#ef4444")
	if err != nil {
		slog.Warn("qa watchdog: ensure qa:fail label failed", "error", err, "issue_id", util.UUIDToString(issueID))
		return
	}
	if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     issueID,
		LabelID:     labelID,
		WorkspaceID: workspaceID,
	}); err != nil {
		slog.Warn("qa watchdog: attach qa:fail failed", "error", err, "issue_id", util.UUIDToString(issueID))
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
	slog.Info("qa watchdog: escalated silent gate to qa:fail", "issue_id", util.UUIDToString(issueID), "title", title)
}
