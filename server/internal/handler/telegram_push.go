package handler

import (
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// Bot push: when an inbox item is created for a member, DM that user on Telegram
// with an HTML-formatted card and a deep-link button into the Mini App. Wired
// from cmd/server as an EventInboxNew subscriber (see telegram_push_listeners.go),
// so it covers every inbox source (assign, mention, comment, reaction,
// task_failed) and inherits the mute-prefs filtering that runs upstream of inbox
// creation. Text is localized to the recipient's stored language (ru/uz/en),
// defaulting to Russian for this SalesDoctor fork.

const telegramPushTimeout = 15 * time.Second

// dmSnippetMaxLen caps the comment preview length (runes) in a DM.
const dmSnippetMaxLen = 140

// pushDefaultLang is the fallback push language for users with no stored
// language (the SalesDoctor audience is Russian-first).
const pushDefaultLang = "ru"

// TelegramPushEnabled reports whether bot push can run — only the bot client
// (token) is required.
func (h *Handler) TelegramPushEnabled() bool { return h.telegramBot != nil }

// SendIssueInboxDM best-effort DMs the recipient about one inbox item. It NEVER
// returns an error or blocks the caller: the lookup + send run on a detached
// goroutine with their own timeout. Only member recipients are DMed — agents
// have no Telegram identity. ctx is the base for the detached timeout; pass
// context.Background. body/actorType/actorID/details enrich the message and may
// all be zero.
func (h *Handler) SendIssueInboxDM(ctx context.Context, recipientType, recipientID, issueID, notifType, title string, body *string, actorType, actorID string, details []byte) {
	if h.telegramBot == nil {
		return
	}
	if recipientType != "member" {
		return
	}
	if issueID == "" || recipientID == "" {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("telegram push: panic recovered", "recover", r)
			}
		}()

		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPushTimeout)
		defer cancel()

		tgID, err := h.telegramIDByUserID(bgctx, recipientID)
		if err != nil {
			slog.Warn("telegram push: reverse lookup failed", "error", err, "recipient_id", recipientID)
			return
		}
		if tgID == "" {
			return
		}

		lang := h.recipientLang(bgctx, recipientID)

		identifier := ""
		wsSlug := ""
		if issueUUID, perr := util.ParseUUID(issueID); perr == nil {
			if issue, ierr := h.Queries.GetIssue(bgctx, issueUUID); ierr == nil {
				if prefix := h.getIssuePrefix(bgctx, issue.WorkspaceID); prefix != "" {
					identifier = prefix + "-" + strconv.Itoa(int(issue.Number))
				}
				// Carry the issue's workspace slug in the deep link so the Mini
				// App opens it in the right workspace (issue lookups are scoped).
				if ws, werr := h.Queries.GetWorkspace(bgctx, issue.WorkspaceID); werr == nil {
					wsSlug = ws.Slug
				}
			}
		}

		actorName := h.resolveActorName(bgctx, actorType, actorID)
		text := composeIssueDM(lang, notifType, identifier, title, body, actorName, details)
		// Default: a Mini App deep link (t.me/<bot>?startapp=...). Instances whose
		// bot has no Mini App (e.g. local dev on a separate bot) set
		// TELEGRAM_DM_LINK_MODE=web to instead open the web app directly —
		// otherwise the button would open the bot chat and do nothing.
		link := telegram.MiniAppLink(
			telegramBotUsername(),
			telegramMiniAppShortName(),
			telegram.MiniAppStartParam(wsSlug, issueID),
		)
		if web := h.webIssueLink(wsSlug, issueID); web != "" {
			link = web
		}

		if err := h.telegramBot.SendMessageWithButton(bgctx, tgID, text, dmOpenButton(lang), link); err != nil {
			slog.Warn("telegram push: DM failed", "error", err, "telegram_id", tgID)
		}
	}()
}

// webIssueLink returns an absolute web URL to the issue when the deploy opts
// into web deep links via TELEGRAM_DM_LINK_MODE=web, else "". Base resolves from
// AGORA_PUBLIC_URL, falling back to FRONTEND_ORIGIN (local dev). Needs wsSlug
// because the web route is workspace-scoped (/<wsSlug>/issues/<id>).
func (h *Handler) webIssueLink(wsSlug, issueID string) string {
	if strings.TrimSpace(os.Getenv("TELEGRAM_DM_LINK_MODE")) != "web" || wsSlug == "" {
		return ""
	}
	base := strings.TrimRight(h.cfg.PublicURL, "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/")
	}
	if base == "" {
		return ""
	}
	return base + "/" + wsSlug + "/issues/" + issueID
}

// recipientLang returns the recipient's push language ("ru" | "uz" | "en"),
// falling back to pushDefaultLang when unset/unknown or on lookup error.
func (h *Handler) recipientLang(ctx context.Context, recipientID string) string {
	id, err := util.ParseUUID(recipientID)
	if err != nil {
		return pushDefaultLang
	}
	user, err := h.Queries.GetUser(ctx, id)
	if err != nil {
		return pushDefaultLang
	}
	switch user.Language.String {
	case "ru", "uz", "en":
		return user.Language.String
	default:
		return pushDefaultLang
	}
}

// resolveActorName best-effort resolves a display name for the inbox actor.
func (h *Handler) resolveActorName(ctx context.Context, actorType, actorID string) string {
	if strings.TrimSpace(actorID) == "" {
		return ""
	}
	id, err := util.ParseUUID(actorID)
	if err != nil {
		return ""
	}
	switch actorType {
	case "member":
		if u, err := h.Queries.GetUser(ctx, id); err == nil {
			return strings.TrimSpace(u.Name)
		}
	case "agent":
		if a, err := h.Queries.GetAgent(ctx, id); err == nil {
			return strings.TrimSpace(a.Name)
		}
	}
	return ""
}

// dmEmoji is the lead emoji per notification type (language-independent).
var dmEmoji = map[string]string{
	"issue_assigned":   "🔔",
	"mentioned":        "💬",
	"new_comment":      "💬",
	"task_completed":   "✅",
	"task_failed":      "⚠️",
	"agent_blocked":    "⛔",
	"status_changed":   "🔄",
	"priority_changed": "🔼",
}

// dmLabels[lang][notifType] is the localized bold action label. The "_" key is
// the fallback for unmapped types.
var dmLabels = map[string]map[string]string{
	"en": {
		"issue_assigned": "Assigned to you", "mentioned": "You were mentioned",
		"new_comment": "New comment", "task_completed": "Task completed",
		"task_failed": "Task failed", "agent_blocked": "Agent blocked",
		"status_changed": "Status changed", "priority_changed": "Priority changed",
		"_": "Update",
	},
	"ru": {
		"issue_assigned": "Назначено вам", "mentioned": "Вас упомянули",
		"new_comment": "Новый комментарий", "task_completed": "Задача выполнена",
		"task_failed": "Задача не выполнена", "agent_blocked": "Агент заблокирован",
		"status_changed": "Статус изменён", "priority_changed": "Приоритет изменён",
		"_": "Обновление",
	},
	"uz": {
		"issue_assigned": "Sizga tayinlandi", "mentioned": "Siz eslatildingiz",
		"new_comment": "Yangi izoh", "task_completed": "Vazifa bajarildi",
		"task_failed": "Vazifa bajarilmadi", "agent_blocked": "Agent bloklandi",
		"status_changed": "Holat o‘zgardi", "priority_changed": "Muhimlik o‘zgardi",
		"_": "Yangilanish",
	},
}

var dmStatusLabels = map[string]map[string]string{
	"en": {"backlog": "Backlog", "todo": "Todo", "in_progress": "In Progress", "in_review": "In Review", "done": "Done", "blocked": "Blocked", "cancelled": "Cancelled"},
	"ru": {"backlog": "Бэклог", "todo": "К выполнению", "in_progress": "В работе", "in_review": "На проверке", "done": "Готово", "blocked": "Заблокировано", "cancelled": "Отменено"},
	"uz": {"backlog": "Backlog", "todo": "Bajariladi", "in_progress": "Jarayonda", "in_review": "Tekshiruvda", "done": "Bajarildi", "blocked": "Bloklangan", "cancelled": "Bekor qilingan"},
}

var dmPriorityLabels = map[string]map[string]string{
	"en": {"urgent": "Urgent", "high": "High", "medium": "Medium", "low": "Low", "none": "No priority"},
	"ru": {"urgent": "Срочно", "high": "Высокий", "medium": "Средний", "low": "Низкий", "none": "Без приоритета"},
	"uz": {"urgent": "Shoshilinch", "high": "Yuqori", "medium": "O‘rta", "low": "Past", "none": "Muhimliksiz"},
}

var dmOpenButtonText = map[string]string{
	"en": "Open in Agora", "ru": "Открыть в Agora", "uz": "Agora’da ochish",
}

func dmOpenButton(lang string) string {
	if s, ok := dmOpenButtonText[lang]; ok {
		return s
	}
	return dmOpenButtonText[pushDefaultLang]
}

// composeIssueDM builds the localized HTML DM body for an inbox notification.
// All dynamic content is HTML-escaped (sent with parse_mode HTML).
func composeIssueDM(lang, notifType, identifier, title string, body *string, actorName string, details []byte) string {
	labels := dmLabels[lang]
	if labels == nil {
		labels = dmLabels[pushDefaultLang]
	}
	label := labels[notifType]
	if label == "" {
		label = labels["_"]
	}
	emoji := dmEmoji[notifType]
	if emoji == "" {
		emoji = "🔔"
	}

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(html.EscapeString(label))
	b.WriteString("</b>")
	if actorName != "" {
		b.WriteString(" · <i>")
		b.WriteString(html.EscapeString(actorName))
		b.WriteString("</i>")
	}

	b.WriteString("\n")
	if identifier != "" {
		b.WriteString("<b>")
		b.WriteString(html.EscapeString(identifier))
		b.WriteString("</b> ")
	}
	b.WriteString(html.EscapeString(title))

	switch notifType {
	case "status_changed", "priority_changed":
		from, to := transitionFromDetails(details)
		if to != "" {
			b.WriteString("\n")
			labelFn := func(tok string) string { return dmTransitionLabel(lang, notifType, tok) }
			if from != "" {
				b.WriteString(html.EscapeString(labelFn(from)))
				b.WriteString(" → ")
			}
			b.WriteString(html.EscapeString(labelFn(to)))
		}
	case "new_comment", "mentioned":
		if snippet := commentSnippet(body); snippet != "" {
			b.WriteString("\n<blockquote>")
			b.WriteString(html.EscapeString(snippet))
			b.WriteString("</blockquote>")
		}
	}

	return b.String()
}

// dmTransitionLabel localizes a status (status_changed) or priority
// (priority_changed) enum token, falling back to a humanized form for unknown
// tokens.
func dmTransitionLabel(lang, notifType, token string) string {
	var table map[string]map[string]string
	if notifType == "priority_changed" {
		table = dmPriorityLabels
	} else {
		table = dmStatusLabels
	}
	if m := table[lang]; m != nil {
		if v, ok := m[token]; ok {
			return v
		}
	}
	if m := table[pushDefaultLang]; m != nil {
		if v, ok := m[token]; ok {
			return v
		}
	}
	return humanizeToken(token)
}

// transitionFromDetails extracts {"from","to"} from a status/priority inbox
// item's details JSON. Returns empty strings on any decode failure.
func transitionFromDetails(details []byte) (from, to string) {
	if len(details) == 0 {
		return "", ""
	}
	var d struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(details, &d); err != nil {
		return "", ""
	}
	return d.From, d.To
}

// humanizeToken turns a snake_case enum value into a Title Cased label
// ("in_progress" -> "In Progress") — the last-resort fallback for tokens not in
// the localized tables.
func humanizeToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// commentSnippet produces a compact, single-line preview of a comment body,
// collapsing whitespace and truncating to dmSnippetMaxLen runes.
func commentSnippet(body *string) string {
	if body == nil {
		return ""
	}
	s := strings.Join(strings.Fields(*body), " ")
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > dmSnippetMaxLen {
		s = strings.TrimSpace(string(r[:dmSnippetMaxLen])) + "…"
	}
	return s
}
