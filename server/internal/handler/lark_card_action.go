package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
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

// AttachLabelByNameForLark attaches a label (by name, creating it if absent) to
// an issue from a Lark card action and fires the same qa:pass -> auto_docs
// automation the HTTP attach path uses. Implements lark.IssueCardActions.
func (h *Handler) AttachLabelByNameForLark(ctx context.Context, issueID, labelName, actorUserID string) error {
	iid, err := util.ParseUUID(issueID)
	if err != nil {
		return fmt.Errorf("bad issue id %q: %w", issueID, err)
	}
	issue, err := h.Queries.GetIssue(ctx, iid)
	if err != nil {
		return fmt.Errorf("load issue: %w", err)
	}
	labels, err := h.Queries.ListLabels(ctx, issue.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list labels: %w", err)
	}
	var labelID pgtype.UUID
	found := false
	for _, l := range labels {
		if strings.EqualFold(l.Name, labelName) {
			labelID = l.ID
			found = true
			break
		}
	}
	if !found {
		created, cerr := h.Queries.CreateLabel(ctx, db.CreateLabelParams{
			WorkspaceID: issue.WorkspaceID,
			Name:        labelName,
			Color:       qaLabelColor(labelName),
		})
		if cerr != nil {
			return fmt.Errorf("create label: %w", cerr)
		}
		labelID = created.ID
	}
	if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     iid,
		LabelID:     labelID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return fmt.Errorf("attach label: %w", err)
	}
	// Same automation the HTTP AttachLabel path fires; detached + best-effort.
	go h.maybeAutoDocsOnLabel(context.Background(), issue, labelName, actorUserID)
	return nil
}

// AssignIssueToMemberForLark assigns an issue to the tapping member from a Lark
// card action, publishing the assignee_changed EventIssueUpdated the HTTP path
// uses. Implements lark.IssueCardActions.
func (h *Handler) AssignIssueToMemberForLark(ctx context.Context, issueID, memberUserID string) error {
	iid, err := util.ParseUUID(issueID)
	if err != nil {
		return fmt.Errorf("bad issue id %q: %w", issueID, err)
	}
	uid, err := util.ParseUUID(memberUserID)
	if err != nil {
		return fmt.Errorf("bad member id %q: %w", memberUserID, err)
	}
	issue, err := h.Queries.GetIssue(ctx, iid)
	if err != nil {
		return fmt.Errorf("load issue: %w", err)
	}
	if issue.AssigneeType.String == "member" && issue.AssigneeID == uid {
		return nil // already assigned to this member
	}
	prevType, prevID := issue.AssigneeType, issue.AssigneeID
	updated, err := h.Queries.UpdateIssueAssignee(ctx, db.UpdateIssueAssigneeParams{
		ID:           iid,
		AssigneeType: pgtype.Text{String: "member", Valid: true},
		AssigneeID:   uid,
		WorkspaceID:  issue.WorkspaceID,
	})
	if err != nil {
		return fmt.Errorf("update assignee: %w", err)
	}
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	resp := issueToResponse(updated, prefix)
	h.publish(protocol.EventIssueUpdated, uuidToString(issue.WorkspaceID), "member", memberUserID, map[string]any{
		"issue":              resp,
		"assignee_changed":   true,
		"prev_assignee_type": textToPtr(prevType),
		"prev_assignee_id":   uuidToPtr(prevID),
		"creator_type":       issue.CreatorType,
		"creator_id":         uuidToString(issue.CreatorID),
		"source":             "lark_card",
	})
	return nil
}

// qaLabelColor picks a label color for an auto-created QA label.
func qaLabelColor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "qa:pass":
		return "#22c55e"
	case "qa:fail":
		return "#ef4444"
	default:
		return "#6b7280"
	}
}
