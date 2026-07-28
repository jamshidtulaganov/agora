package handler

import (
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/config"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Autopilot report push — when an autopilot run completes, post the agent's
// write-up to a Telegram chat.
//
// Distinct from SendIssueInboxDM, which DMs one person about one notification.
// This posts a finished REPORT to a shared group, so the target is a configured
// chat id rather than a resolved user identity: nobody "receives" a weekly
// report, a room does.
//
// Best-effort throughout. A missing bot, missing chat id, or Bot API failure is
// logged and dropped — a report that failed to post must never mark the run
// failed or retry the agent's work.

// telegramMessageLimit is the Bot API's hard cap on a text message. Exceeding
// it fails the whole send, so a long report is trimmed rather than lost.
const telegramMessageLimit = 4096

// truncateForTelegram trims text to the Bot API limit, cutting at a line break
// when one is near the end so a report never ends mid-sentence. The suffix
// tells the reader the message was cut — silently dropping the tail would make
// a partial report look complete.
func truncateForTelegram(text string) string {
	const suffix = "\n\n… (trimmed — open the issue for the full report)"
	if len(text) <= telegramMessageLimit {
		return text
	}
	budget := telegramMessageLimit - len(suffix)
	cut := text[:budget]
	// Prefer a clean break, but only if one exists reasonably close to the end;
	// otherwise a report with no newlines near the cut would lose a lot.
	if nl := strings.LastIndex(cut, "\n"); nl > budget-400 {
		cut = cut[:nl]
	}
	return cut + suffix
}

// autopilotReportChatID resolves the destination chat for an autopilot's
// reports, honouring a project override before the instance value. Empty means
// reporting is off, which is the default.
func (h *Handler) autopilotReportChatID(ctx context.Context, ap db.Autopilot) string {
	var overrides map[string]string
	if ap.ProjectID.Valid {
		if project, err := h.Queries.GetProject(ctx, ap.ProjectID); err == nil {
			overrides = parseProjectConfigOverrides(project.Settings)
		}
	}
	return strings.TrimSpace(config.StringFrom(overrides, "AGORA_TELEGRAM_REPORT_CHAT_ID"))
}

// autopilotReportBody returns the text to post: the newest AGENT comment on the
// run's issue — the write-up the autopilot exists to produce.
//
// Returns "" when there is nothing worth posting. A run whose agent never wrote
// anything should stay silent rather than announce an empty report; the caller
// treats "" as "skip".
func (h *Handler) autopilotReportBody(ctx context.Context, issueID, workspaceID pgtype.UUID) string {
	comments, err := h.Queries.ListRecentCommentsForIssue(ctx, db.ListRecentCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: workspaceID,
		Limit:       20,
	})
	if err != nil {
		return ""
	}
	for _, c := range comments {
		if c.AuthorType != "agent" {
			continue
		}
		body := strings.TrimSpace(c.Content)
		// Skip the dispatch instruction the platform posts to summon the agent —
		// it is agent-authored too, but it is the prompt, not the report.
		if body == "" || strings.HasPrefix(body, "<!--agent-protocol:") {
			continue
		}
		return body
	}
	return ""
}

// SendAutopilotReport posts a completed autopilot run's write-up to the
// configured Telegram chat. Safe to call for every completed run: it no-ops
// when the bot is unconfigured, no chat is set, or the agent produced nothing.
//
// Detached — the caller is an event subscriber, not a request, but the Bot API
// call still should not hold the bus.
func (h *Handler) SendAutopilotReport(ctx context.Context, runID string) {
	// Deliberately NOT gated on TelegramPushEnabled(). That flag asks whether
	// the PLATFORM bot exists, which is the wrong question here: an autopilot
	// whose agent owns its own bot needs nothing from the platform bot, and
	// gating on it silently killed reports on any deployment that had per-agent
	// bots but no TELEGRAM_BOT_TOKEN. The real gate is further down — no bot
	// and no chat means no destination, and that path already returns.
	runUUID, err := util.ParseUUID(runID)
	if err != nil {
		return
	}
	run, err := h.Queries.GetAutopilotRun(ctx, runUUID)
	if err != nil || !run.IssueID.Valid {
		return
	}
	ap, err := h.Queries.GetAutopilot(ctx, run.AutopilotID)
	if err != nil {
		return
	}
	// Prefer the agent's OWN bot and chat when one is installed: a report from
	// "sd-bridge-lead" should arrive under that agent's identity, not the
	// platform bot's. Falls back to the platform bot + configured chat, which
	// is what every workspace without installations still uses.
	bot, chatID := h.agentTelegramClient(ctx, ap.AssigneeID)
	if bot == nil {
		bot = h.telegramBot
	}
	if chatID == "" {
		chatID = h.autopilotReportChatID(ctx, ap)
	}
	// Both fallbacks exhausted: no bot to speak with, or nowhere to speak.
	// This is the real gate — reached whether or not a platform bot exists.
	if bot == nil || chatID == "" {
		return
	}
	body := h.autopilotReportBody(ctx, run.IssueID, ap.WorkspaceID)
	if body == "" {
		// Stranded-output recovery, borrowed from hamroh's `dropped_text`
		// safety net: a completed run that produced no postable comment is not
		// deliberate silence, it is a report that failed to reach anyone. The
		// agent's task output usually still holds the text, so recover it
		// rather than letting the group see nothing and assume all is well.
		body = autopilotRunOutput(run.Result)
	}
	if body == "" {
		// Nothing anywhere. WARN, not INFO: a scheduled report that silently
		// produced nothing is a failure, and it is invisible by construction —
		// the only symptom is a group that stays quiet on Monday morning.
		slog.Warn("autopilot report: run completed with no recoverable report",
			"run_id", runID, "autopilot", ap.Title, "issue_id", uuidToString(run.IssueID))
		return
	}

	// Send the report as an attached spreadsheet, not as message text. Telegram
	// renders no markdown table, and the table IS the report — pasted inline it
	// collapses into unreadable pipe soup on a phone. A spreadsheet goes
	// further than a rendered page: the per-assignee and per-month breakdowns
	// stay sortable and summable, which is what anyone acting on the report
	// does with them first. The caption carries the headline so the chat still
	// shows something meaningful without opening the file.
	title := ap.Title + " — " + time.Now().Format("02.01.2006")
	doc, err := renderReportXLSX(title, body)
	if err != nil {
		slog.Warn("autopilot report: xlsx render failed", "run_id", runID, "error", err)
		return
	}
	filename := "bitrix-hisobot-" + time.Now().Format("2006-01-02") + ".xlsx"

	if err := bot.SendDocument(ctx, chatID, filename, doc, reportCaption(title, body)); err != nil {
		slog.Warn("autopilot report: telegram send failed",
			"run_id", runID, "chat_id", chatID, "error", err)
		return
	}
	slog.Info("autopilot report posted to telegram", "run_id", runID, "autopilot", ap.Title)
}

// telegramCaptionLimit is the Bot API cap on a document caption. Overflowing it
// rejects the whole upload, so the caption is a headline, never the report.
const telegramCaptionLimit = 1024

// reportCaption builds the short HTML blurb shown next to the attachment: the
// title plus the report's first substantive line, so the chat conveys the
// finding at a glance and the file carries the detail.
func reportCaption(title, body string) string {
	caption := "<b>" + html.EscapeString(title) + "</b>"

	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		// Skip headings, rules and empties — the first real sentence is wanted.
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "|") {
			continue
		}
		// Strip markdown emphasis; Telegram captions take HTML, not markdown.
		t = strings.ReplaceAll(t, "**", "")
		t = strings.ReplaceAll(t, "`", "")
		caption += "\n\n" + html.EscapeString(t)
		break
	}
	// Telegram counts CHARACTERS, not bytes. Byte-limiting would cut an Uzbek
	// or Russian caption at roughly half its allowed length, since Cyrillic is
	// two bytes per character. One rune is reserved for the ellipsis.
	if utf8.RuneCountInString(caption) > telegramCaptionLimit {
		caption = truncateRunes(caption, telegramCaptionLimit-1) + "…"
	}
	return caption
}

// autopilotRunOutput digs the agent's final output out of a stored run result.
// Best-effort by design: the payload shape varies by task kind, and a miss just
// means there was nothing to recover.
func autopilotRunOutput(result []byte) string {
	if len(result) == 0 {
		return ""
	}
	var payload struct {
		Output string `json:"output"`
	}
	if json.Unmarshal(result, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(util.UnescapeBackslashEscapes(payload.Output))
}
