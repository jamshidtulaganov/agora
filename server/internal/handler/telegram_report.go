package handler

import (
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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
	if err != nil {
		return
	}
	// No IssueID check: a run_only autopilot never has an issue, and bailing
	// here meant those runs — the ones that exist precisely to produce output
	// without opening a ticket — never reported at all. The body lookup below
	// handles a missing issue on its own and falls back to the run's result.
	ap, err := h.Queries.GetAutopilot(ctx, run.AutopilotID)
	if err != nil {
		return
	}
	bot, chatID := h.autopilotDestination(ctx, ap)
	if bot == nil || chatID == "" {
		return
	}
	body := h.autopilotRunReportBody(ctx, run, ap)
	body = stripReportPreamble(body)
	if body == "" {
		// Nothing anywhere. WARN, not INFO: a scheduled report that silently
		// produced nothing is a failure, and it is invisible by construction —
		// the only symptom is a group that stays quiet on Monday morning.
		slog.Warn("autopilot report: run completed with no recoverable report",
			"run_id", runID, "autopilot", ap.Title, "issue_id", uuidToString(run.IssueID))
		return
	}

	// A report with tables goes out as a spreadsheet: Telegram renders no
	// markdown table, and pasted inline it collapses into unreadable pipe soup
	// — while a spreadsheet keeps the per-assignee and per-month breakdowns
	// sortable, which is what anyone acting on the report does first.
	//
	// A SHORT report stays a message. A daily sprint pulse is six lines with no
	// table, and delivering that as a .xlsx attachment buries it: the reader
	// has to download and open a file to learn one sentence, so they stop
	// looking. Same rule the chat replies use, for the same reason.
	if !replyNeedsDocument(body) {
		// Markdown, not plain: agents write `**bold**` and readers were seeing
		// the asterisks around the number that mattered.
		if err := bot.SendMarkdown(ctx, chatID, truncateForTelegram(body)); err != nil {
			slog.Warn("autopilot report: telegram send failed",
				"run_id", runID, "chat_id", chatID, "error", err)
			return
		}
		slog.Info("autopilot report posted to telegram", "run_id", runID,
			"autopilot", ap.Title, "as", "message")
		return
	}

	title := ap.Title + " — " + time.Now().Format("02.01.2006")
	doc, err := renderReportPDF(title, body)
	if err != nil {
		slog.Warn("autopilot report: pdf render failed", "run_id", runID, "error", err)
		return
	}
	filename := "hisobot-" + time.Now().Format("2006-01-02") + ".pdf"

	if err := bot.SendDocument(ctx, chatID, filename, doc, reportCaption(title, body)); err != nil {
		slog.Warn("autopilot report: telegram send failed",
			"run_id", runID, "chat_id", chatID, "error", err)
		return
	}
	slog.Info("autopilot report posted to telegram", "run_id", runID, "autopilot", ap.Title)
}

func (h *Handler) autopilotRunReportBody(ctx context.Context, run db.AutopilotRun, ap db.Autopilot) string {
	body := ""
	if run.IssueID.Valid {
		body = h.autopilotReportBody(ctx, run.IssueID, ap.WorkspaceID)
	}
	if body != "" {
		return body
	}
	// Stranded-output recovery, borrowed from hamroh's `dropped_text`
	// safety net: a completed run that produced no postable comment — or a
	// run_only execution with no issue by design — recovers its task result.
	return autopilotRunOutput(run.Result)
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

// autopilotDestination resolves the bot and chat a report goes to, as a PAIR.
//
// Picking them independently was a real delivery failure: an agent with an
// installed bot but no bound group got that agent's bot combined with the
// PLATFORM bot's configured chat — a group the agent's bot is almost never a
// member of, so every send failed. A bot and the chat it can reach are one
// decision, not two.
//
// Prefers the agent's own identity, because a report from "sd-bridge-lead"
// should arrive as that agent rather than as Agora. Falls back to the platform
// bot with its configured chat, which is what every workspace without
// installations uses.
func (h *Handler) autopilotDestination(ctx context.Context, ap db.Autopilot) (*telegram.BotClient, string) {
	destination := h.resolveAutopilotTelegramDestination(ctx, ap)
	return destination.bot, destination.ChatID
}

type resolvedAutopilotTelegramDestination struct {
	AutopilotTelegramDestinationResponse
	bot *telegram.BotClient
}

func (h *Handler) resolveAutopilotTelegramDestination(
	ctx context.Context,
	ap db.Autopilot,
) resolvedAutopilotTelegramDestination {
	agentID, ok := h.autopilotSpeakerAgent(ctx, ap)

	var agentBot *telegram.BotClient
	var agentChat string
	var agentBotUsername string
	agentReachesProjectChat := false
	projectChat := h.autopilotReportChatID(ctx, ap)
	if ok {
		agentBot, agentChat = h.agentTelegramClient(ctx, agentID)
		if row, err := h.Queries.GetTelegramInstallationByAgent(ctx, agentID); err == nil &&
			row.Status == "active" {
			agentBotUsername = row.BotUsername
		}
		if agentBot != nil && projectChat != "" {
			agentReachesProjectChat = h.agentReachesChat(ctx, agentID, projectChat)
		}
	}
	bot, chatID := chooseAutopilotDestination(
		agentBot, agentChat, agentReachesProjectChat,
		h.telegramBot, projectChat,
	)
	if bot == nil || chatID == "" {
		return resolvedAutopilotTelegramDestination{}
	}
	via := "platform"
	username := ""
	if bot == agentBot {
		via = "agent"
		username = agentBotUsername
	}
	return resolvedAutopilotTelegramDestination{
		AutopilotTelegramDestinationResponse: AutopilotTelegramDestinationResponse{
			Delivers:          true,
			Via:               via,
			BotUsername:       username,
			ChatID:            chatID,
			FromProjectConfig: projectChat != "" && projectChat == chatID,
		},
		bot: bot,
	}
}

// autopilotSpeakerAgent is the agent whose identity a report should carry.
//
// For a squad assignee that is the LEADER: the squad id is not an agent id, so
// looking up an installation by it finds nothing, and every squad autopilot
// fell through to the platform bot even when its leader had a bot of its own.
func (h *Handler) autopilotSpeakerAgent(ctx context.Context, ap db.Autopilot) (pgtype.UUID, bool) {
	if ap.AssigneeType != "squad" {
		return ap.AssigneeID, ap.AssigneeID.Valid
	}
	squad, err := h.Queries.GetSquad(ctx, ap.AssigneeID)
	if err != nil || squad.ArchivedAt.Valid || !squad.LeaderID.Valid {
		return pgtype.UUID{}, false
	}
	return squad.LeaderID, true
}

// agentReachesChat reports whether the agent's bot is bound to a chat.
// Membership is proven from allowed_chat_ids rather than assumed: posting
// through a bot that is not in the room fails at the Bot API, silently.
func (h *Handler) agentReachesChat(ctx context.Context, agentID pgtype.UUID, chatID string) bool {
	row, err := h.Queries.GetTelegramInstallationByAgent(ctx, agentID)
	if err != nil {
		return false
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if err != nil {
		return false
	}
	return telegramChatAllowed(row, parsed)
}

// chooseAutopilotDestination applies the precedence, and the order matters:
//
//  1. A project's configured chat beats the agent's default. The override is a
//     specific instruction ("this project reports HERE"); the agent's default
//     is a general one, and letting the general win sent two projects' reports
//     into whichever group their shared agent happened to be bound to.
//  2. Inside that chat the agent's own bot speaks if it can reach it, so a
//     report from "sd-bridge-lead" arrives as that agent rather than as Agora.
//  3. Otherwise the platform bot — the only one a workspace without
//     installations has.
func chooseAutopilotDestination(
	agentBot *telegram.BotClient,
	agentChat string,
	agentReachesProjectChat bool,
	platformBot *telegram.BotClient,
	projectChat string,
) (*telegram.BotClient, string) {
	if projectChat != "" {
		if agentBot != nil && agentReachesProjectChat {
			return agentBot, projectChat
		}
		if platformBot != nil {
			return platformBot, projectChat
		}
		return nil, ""
	}
	if agentBot != nil && agentChat != "" {
		return agentBot, agentChat
	}
	return nil, ""
}

// AutopilotTelegramDestinationResponse tells the UI where a report will land.
//
// Server-side on purpose. The precedence — project override, then the agent's
// own bot if it can reach that chat, then the platform bot — is subtle enough
// that a second implementation in TypeScript would drift, and a dialog that
// confidently names the wrong group is worse than one that says nothing.
type AutopilotTelegramDestinationResponse struct {
	// Delivers is false when nothing will be sent; the UI shows the reason
	// rather than a chat.
	Delivers bool `json:"delivers"`
	// Via is "agent" or "platform" — which identity the report arrives as.
	Via         string `json:"via,omitempty"`
	BotUsername string `json:"bot_username,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	// FromProjectConfig marks a chat that came from the project override, so
	// the UI can say where the setting lives.
	FromProjectConfig bool `json:"from_project_config"`
}

// GetAutopilotTelegramDestination handles
// GET /api/autopilots/{id}/telegram-destination.
func (h *Handler) GetAutopilotTelegramDestination(w http.ResponseWriter, r *http.Request) {
	ap, ok := h.loadAutopilotInWorkspace(w, r, chi.URLParam(r, "id"), h.resolveWorkspaceID(r))
	if !ok {
		return
	}
	destination := h.resolveAutopilotTelegramDestination(r.Context(), ap)
	writeJSON(w, http.StatusOK, destination.AutopilotTelegramDestinationResponse)
}

// stripReportPreamble drops a lead-in the agent wrote to itself.
//
// A run_only report is the agent's whole final message, and models habitually
// open one with a line addressed to the operator — "Data tayyor. Hisobotni
// tuzaman:", "Final natija yozaman:" — before the content. Instructing them not
// to works most of the time, which is the problem: the group periodically gets
// a status line that means nothing to it, at the top of the message.
//
// Narrow by construction: only a FIRST paragraph ending in a colon, only when
// something follows it. A report does not open that way, so nothing real
// matches — and if the shape ever changes, the failure is a preamble surviving
// rather than content being eaten.
func stripReportPreamble(body string) string {
	trimmed := strings.TrimSpace(body)
	blank := strings.Index(trimmed, "\n\n")
	if blank < 0 {
		return trimmed
	}
	first := strings.TrimSpace(trimmed[:blank])
	rest := strings.TrimSpace(trimmed[blank:])
	if rest == "" || !strings.HasSuffix(first, ":") {
		return trimmed
	}
	// A colon-terminated line that is itself structure — a heading, a list
	// item, a table row — is content, not narration.
	if strings.HasPrefix(first, "#") || strings.HasPrefix(first, "|") ||
		strings.HasPrefix(first, "-") || strings.HasPrefix(first, "*") ||
		strings.Contains(first, "\n") {
		return trimmed
	}
	return rest
}
