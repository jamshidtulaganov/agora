package handler

import (
	"context"
	"fmt"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// UpdateIssueStatusForLark applies a status change triggered by a Lark card
// action, attributed to the bound member. It publishes the SAME
// EventIssueUpdated the HTTP UpdateIssue and GitHub PR-merge paths use (issue,
// status_changed, prev_status, source) so notify-out, Bitrix sync, and the WS
// broadcaster all fire identically — the card action is just another transport
// onto the canonical status transition. Implements lark.IssueStatusUpdater.
func (h *Handler) UpdateIssueStatusForLark(ctx context.Context, issueID, newStatus, actorUserID string) error {
	iid, err := util.ParseUUID(issueID)
	if err != nil {
		return fmt.Errorf("bad issue id %q: %w", issueID, err)
	}
	issue, err := h.Queries.GetIssue(ctx, iid)
	if err != nil {
		return fmt.Errorf("load issue: %w", err)
	}
	if issue.Status == newStatus {
		return nil // idempotent: re-tapping the same status is a no-op
	}
	updated, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:          iid,
		Status:      newStatus,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	resp := issueToResponse(updated, prefix)
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), "member", actorUserID, map[string]any{
		"issue":          resp,
		"status_changed": true,
		"prev_status":    issue.Status,
		"creator_type":   issue.CreatorType,
		"creator_id":     uuidToString(issue.CreatorID),
		"source":         "lark_card",
	})
	return nil
}
