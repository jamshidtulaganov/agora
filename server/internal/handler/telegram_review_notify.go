package handler

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Review-verdict group notice — posts the code-review outcome to the project's
// Telegram room (AGORA_TELEGRAM_REPORT_CHAT_ID), the same destination autopilot
// reports and issue-created notices use.
//
// This is the SHARED-ROOM half of the notification. The per-USER half needs
// nothing here: NotifyReviewVerdict writes typed inbox items (review_failed /
// review_passed / merge_ready) for the assignee's owner, the human creator and
// the subscribers, and the EventInboxNew subscriber (telegram_push_listeners.go)
// already DMs every member recipient. A group chat has no inbox, hence this.
//
// Best-effort and detached throughout: a missing bot, an unset chat id or a Bot
// API failure must never affect the verdict, the label or the routing.

// SendReviewVerdictGroupNotify posts the review outcome to the project's report
// chat. No-ops unless the platform bot exists, the project enabled
// AGORA_TELEGRAM_REVIEW_NOTIFY_ENABLED, and a report chat id resolves.
//
// nextStep is the human-readable consequence the routing already decided
// ("merge request opening", "returned to To Do") so the room reads one message
// instead of inferring the workflow state from a verdict word.
func (h *Handler) SendReviewVerdictGroupNotify(ctx context.Context, issue db.Issue, verdict, nextStep string) {
	if h.telegramBot == nil {
		return
	}
	if !h.reviewTelegramNotifyEnabled(ctx, issue) {
		return
	}
	chatID := strings.TrimSpace(config.StringFrom(h.projectConfigOverrides(ctx, issue), "AGORA_TELEGRAM_REPORT_CHAT_ID"))
	if chatID == "" {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("telegram review notify: panic recovered", "recover", r)
			}
		}()
		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPushTimeout)
		defer cancel()

		summary, blockers := h.reviewVerdictSummary(bgctx, issue)
		wsSlug := ""
		if ws, err := h.Queries.GetWorkspace(bgctx, issue.WorkspaceID); err == nil {
			wsSlug = ws.Slug
		}
		link := ""
		if l := h.webIssueLink(wsSlug, uuidToString(issue.ID)); l != "" && telegramGroupLinkOK(l) {
			link = l
		}
		text := composeReviewVerdictGroupNotifyText(
			h.issueKey(bgctx, issue), issue.Title, verdict, summary, nextStep, blockers,
			h.resolveIssueAssigneeDisplayName(bgctx, issueToResponse(issue, h.getIssuePrefix(bgctx, issue.WorkspaceID))),
		)
		if err := h.telegramBot.SendMessageWithButton(bgctx, chatID, text, "Open issue", link); err != nil {
			slog.Warn("telegram review notify: send failed",
				"chat_id", chatID, "issue_id", uuidToString(issue.ID), "error", err)
			return
		}
		slog.Info("telegram review notify posted",
			"chat_id", chatID, "issue_id", uuidToString(issue.ID), "verdict", verdict)
	}()
}

// composeReviewVerdictGroupNotifyText builds the Telegram-HTML body. Pure (no
// I/O) so the wording is unit-testable: every dynamic part is escaped, and the
// blocker count is only shown when the reviewer actually recorded blockers.
func composeReviewVerdictGroupNotifyText(identifier, title, verdict, summary, nextStep string, blockers int, assigneeLabel string) string {
	pass := strings.EqualFold(strings.TrimSpace(verdict), "pass")

	var b strings.Builder
	if pass {
		b.WriteString("✅ <b>Code review passed</b>\n")
	} else {
		b.WriteString("❌ <b>Code review failed</b>")
		if blockers > 0 {
			b.WriteString(fmt.Sprintf(" — %d blocker(s)", blockers))
		}
		b.WriteString("\n")
	}
	b.WriteString("📌 <b>")
	b.WriteString(html.EscapeString(strings.TrimSpace(identifier)))
	b.WriteString("</b>")
	if t := strings.TrimSpace(title); t != "" {
		b.WriteString(" — ")
		b.WriteString(html.EscapeString(t))
	}
	if s := strings.TrimSpace(summary); s != "" {
		b.WriteString("\n📝 ")
		b.WriteString(html.EscapeString(s))
	}
	if a := strings.TrimSpace(assigneeLabel); a != "" {
		b.WriteString("\n👤 <b>Owner:</b> ")
		b.WriteString(html.EscapeString(a))
	}
	if n := strings.TrimSpace(nextStep); n != "" {
		b.WriteString("\n➡️ <b>Next:</b> ")
		b.WriteString(html.EscapeString(n))
	}
	return b.String()
}

// reviewVerdictNextStep describes what the routing will do with this verdict, in
// the words a human needs. It reports the CONFIGURED consequence — the same
// flags the routing itself reads — so the room is never told about a step the
// project has turned off.
func (h *Handler) reviewVerdictNextStep(ctx context.Context, issue db.Issue, verdict string) string {
	if strings.EqualFold(strings.TrimSpace(verdict), service.ReviewLabelPass) ||
		strings.EqualFold(strings.TrimSpace(verdict), "pass") {
		if h.issueHasKnownPR(ctx, issue) {
			return "awaiting a human's approve & merge"
		}
		if h.reviewPassOpenPREnabled(ctx, issue) {
			return "opening the merge request"
		}
		return "ready for a merge request"
	}
	if h.reviewFailAutorouteEnabled(ctx, issue) {
		return "returned to To Do for the developer"
	}
	return "needs the developer's fix"
}
