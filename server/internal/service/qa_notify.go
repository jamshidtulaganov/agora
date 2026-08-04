package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Typed QA inbox notifications (Phase 2, item 2 — "make the human first-
// class"). A qa:fail verdict — and a qa:pass that RECOVERS from a fail —
// used to notify nobody: the humans found out by polling the /qa queue.
// This mirrors the design stage's notifyDesignProposal pattern (inbox item
// + inbox:new publish per recipient), deliberately living HERE in the
// service layer next to CaptureQAEvidence rather than in the
// cmd/server/notification_listeners.go event fan-out, so the dispatch is
// part of the verdict capture itself (and that file — which carries
// unrelated in-flight work — stays untouched).

// qaNotifySummaryMaxRunes caps the inbox body at a notification-sized note.
const qaNotifySummaryMaxRunes = 200

// NotifyQAVerdict creates typed inbox items for a landed QA verdict:
//
//   - verdict "fail"                → qa_failed  (action_required)
//   - verdict "pass" with recovery  → qa_passed  (info — the fail it
//     displaced was already action_required; this closes that loop)
//   - verdict "pass" without a prior fail → no-op (a routine green gate is
//     queue-visible; notifying every pass would train people to ignore the
//     inbox)
//
// Recipients: the issue's assignee — a member directly, an agent through
// its OWNER (the human responsible for the agent's work) — plus the human
// creator and human subscribers, deduped. The acting human (an override)
// is excluded — you don't need a notification about your own decision.
// Best-effort throughout: a notify failure never fails the capture.
func (s *TaskService) NotifyQAVerdict(ctx context.Context, issue db.Issue, verdict string, recovery bool, actorType string, actorID pgtype.UUID, summary string) {
	itemType, severity := "", ""
	switch {
	case verdict == "fail":
		itemType, severity = "qa_failed", "action_required"
	case verdict == "pass" && recovery:
		itemType, severity = "qa_passed", "info"
	default:
		return
	}

	recipients := map[string]pgtype.UUID{}
	add := func(id pgtype.UUID) {
		if id.Valid {
			recipients[util.UUIDToString(id)] = id
		}
	}
	// Assignee first — the person whose work the verdict judges. An
	// agent-assigned issue routes to the agent's OWNER.
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
	// The acting human already knows — they just clicked the button.
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
		"recovery":   recovery,
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
			slog.Warn("qa notify: inbox write failed", "error", err, "type", itemType, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		s.publishQuickCreateInbox(item, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(actorID), issue.Status)
	}
	slog.Info("qa verdict inbox notified", "type", itemType, "issue_id", util.UUIDToString(issue.ID), "recipients", len(recipients))
}
