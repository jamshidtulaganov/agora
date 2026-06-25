package handler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
)

// Bot push: when an inbox item is created for a member, DM that user on Telegram
// with a deep link into the Mini App. Wired from cmd/server as an EventInboxNew
// subscriber (see telegram_push_listeners.go), so it covers every inbox source
// (assign, mention, comment, reaction, task_failed) and inherits the mute-prefs
// filtering that already runs upstream of inbox creation.

// telegramPushTimeout bounds the whole DM fan-out (reverse lookup + issue load +
// Bot API call) on the background goroutine.
const telegramPushTimeout = 15 * time.Second

// TelegramPushEnabled reports whether bot push can run — only the bot client
// (token) is required. The Mini App short name is needed for the deep-link
// button but a plain-text DM is still sent without it.
func (h *Handler) TelegramPushEnabled() bool { return h.telegramBot != nil }

// SendIssueInboxDM best-effort DMs the recipient about one inbox item. It NEVER
// returns an error or blocks the caller: the lookup + send run on a detached
// goroutine with their own timeout (the caller is the synchronous bus dispatch
// on the request path, which must not wait on a Telegram round-trip). Only
// member recipients are DMed — agents have no Telegram identity and must never
// receive one. ctx is the base for the detached timeout; pass context.Background.
func (h *Handler) SendIssueInboxDM(ctx context.Context, recipientType, recipientID, issueID, notifType, title string) {
	if h.telegramBot == nil {
		return
	}
	// Hard gate: only members. Agents can be inbox recipients in some flows but
	// must never be DMed (defense-in-depth; agents have no linked telegram id).
	if recipientType != "member" {
		return
	}
	if issueID == "" || recipientID == "" {
		return
	}

	go func() {
		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPushTimeout)
		defer cancel()

		tgID, err := h.telegramIDByUserID(bgctx, recipientID)
		if err != nil {
			slog.Warn("telegram push: reverse lookup failed", "error", err, "recipient_id", recipientID)
			return
		}
		if tgID == "" {
			// User hasn't linked Telegram — nothing to do.
			return
		}

		// Resolve the human identifier (e.g. MUL-123). Best-effort: on any error
		// fall back to the title alone rather than dropping the DM.
		identifier := ""
		if issue, err := h.Queries.GetIssue(bgctx, parseUUID(issueID)); err == nil {
			if prefix := h.getIssuePrefix(bgctx, issue.WorkspaceID); prefix != "" {
				identifier = prefix + "-" + strconv.Itoa(int(issue.Number))
			}
		}

		text := composeIssueDM(notifType, identifier, title)
		link := telegram.MiniAppLink(
			telegramBotUsername(),
			telegramMiniAppShortName(),
			telegram.MiniAppStartParam(issueID),
		)

		var sendErr error
		if link != "" {
			sendErr = h.telegramBot.SendMessageWithButton(bgctx, tgID, text, "Open in Agora", link)
		} else {
			sendErr = h.telegramBot.SendMessage(bgctx, tgID, text)
		}
		if sendErr != nil {
			// Blocked bot, deactivated chat, etc. — log and move on.
			slog.Warn("telegram push: DM failed", "error", sendErr, "telegram_id", tgID)
		}
	}()
}

// composeIssueDM builds the concise English DM text for an inbox notification.
// i18n is out of scope for push v1. identifier may be empty (falls back to the
// title alone).
func composeIssueDM(notifType, identifier, title string) string {
	lead := issueDMLeads[notifType]
	if lead == "" {
		lead = "🔔"
	}
	if identifier != "" {
		return lead + " " + identifier + ": " + title
	}
	return lead + " " + title
}

// issueDMLeads maps an inbox notification type to its DM lead-in. Unmapped types
// fall back to a plain bell (composeIssueDM).
var issueDMLeads = map[string]string{
	"issue_assigned":   "🔔 You were assigned",
	"mentioned":        "💬 You were mentioned in",
	"new_comment":      "💬 New comment on",
	"task_completed":   "✅ Task completed on",
	"task_failed":      "⚠️ Task failed on",
	"agent_blocked":    "⛔ Agent blocked on",
	"status_changed":   "🔄 Status changed on",
	"priority_changed": "🔼 Priority changed on",
}
