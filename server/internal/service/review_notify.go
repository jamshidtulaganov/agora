package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Typed REVIEW inbox notifications (Review stage v2). Mirrors qa_notify.go —
// the dispatch lives in the service layer next to CaptureReviewEvidence, NOT
// in cmd/server/notification_listeners.go (which carries unrelated in-flight
// work and stays untouched).

// NotifyReviewVerdict creates typed inbox items for a landed review verdict:
//
//   - verdict "fail" → review_failed (action_required) — a blocker finding
//     needs the responsible humans NOW.
//   - verdict "pass" with every deterministic gate green → merge_ready
//     (action_required) — the chain is done and a HUMAN's Approve & merge is
//     the only remaining step ("awaiting your approval").
//   - verdict "pass" otherwise → review_passed (info) — the review is green
//     but another gate is still red/pending, so nothing awaits the human yet.
//
// "Gates green" is deliberately aligned with what the review-decision approve
// endpoint itself checks (qa:pass present, no qa:fail) plus a ci:fail veto —
// NOT the full merge-readiness pending logic, because a never-dispatched ci
// gate must not silently suppress the one notification that tells the human
// their approval is awaited. Recipients match NotifyQAVerdict: the assignee
// (an agent through its OWNER), the human creator, and human subscribers,
// deduped, minus the acting human. Best-effort throughout.
func (s *TaskService) NotifyReviewVerdict(ctx context.Context, issue db.Issue, verdict string, actorType string, actorID pgtype.UUID, summary string) {
	itemType, severity := "", ""
	switch verdict {
	case "fail":
		itemType, severity = "review_failed", "action_required"
	case "pass":
		if s.reviewGatesGreen(ctx, issue) {
			itemType, severity = "merge_ready", "action_required"
		} else {
			itemType, severity = "review_passed", "info"
		}
	default:
		return
	}

	recipients := map[string]pgtype.UUID{}
	add := func(id pgtype.UUID) {
		if id.Valid {
			recipients[util.UUIDToString(id)] = id
		}
	}
	if issue.AssigneeType.Valid && issue.AssigneeID.Valid {
		switch issue.AssigneeType.String {
		case "member":
			add(issue.AssigneeID)
		case "agent":
			if agent, err := s.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
			}); err == nil {
				add(agent.OwnerID)
			}
		}
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid {
		add(issue.CreatorID)
	}
	if subs, err := s.Queries.ListIssueSubscribers(ctx, issue.ID); err == nil {
		for _, sub := range subs {
			if sub.UserType == "member" && sub.UserID.Valid {
				add(sub.UserID)
			}
		}
	}
	// The acting human already knows — they just made the decision.
	if actorType == "member" && actorID.Valid {
		delete(recipients, util.UUIDToString(actorID))
	}
	if len(recipients) == 0 {
		return
	}

	prefix := s.getIssuePrefix(issue.WorkspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	details, _ := json.Marshal(map[string]any{
		"issue_id":   util.UUIDToString(issue.ID),
		"identifier": identifier,
		"verdict":    verdict,
	})

	body := pgtype.Text{}
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		if runes := []rune(trimmed); len(runes) > qaNotifySummaryMaxRunes {
			trimmed = string(runes[:qaNotifySummaryMaxRunes-1]) + "…"
		}
		body = pgtype.Text{String: trimmed, Valid: true}
	}

	actorText := pgtype.Text{}
	if actorType != "" {
		actorText = pgtype.Text{String: actorType, Valid: true}
	}
	for _, uid := range recipients {
		item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   issue.WorkspaceID,
			RecipientType: "member",
			RecipientID:   uid,
			Type:          itemType,
			Severity:      severity,
			IssueID:       issue.ID,
			Title:         issue.Title,
			Body:          body,
			ActorType:     actorText,
			ActorID:       actorID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("review notify: inbox write failed", "error", err, "type", itemType, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		s.publishQuickCreateInbox(item, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(actorID), issue.Status)
	}
	slog.Info("review verdict inbox notified", "type", itemType, "issue_id", util.UUIDToString(issue.ID), "recipients", len(recipients))
}

// reviewGatesGreen reports whether the OTHER deterministic gates are green at
// the moment a review:pass lands — qa:pass present, no qa:fail, no ci:fail —
// i.e. the same state in which the review-decision approve endpoint would
// accept an Approve & merge. Label-based on purpose (deterministic, no agent
// reasoning); a query error reports false (fail to the quieter info type).
func (s *TaskService) reviewGatesGreen(ctx context.Context, issue db.Issue) bool {
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return false
	}
	has := make(map[string]bool, len(labels))
	for _, l := range labels {
		has[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}
	return has["qa:pass"] && !has["qa:fail"] && !has["ci:fail"]
}
