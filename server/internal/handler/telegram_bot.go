package handler

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Telegram bot beyond OTP login: command menu (/start /new /tasks /help) and a
// guided "create a task" wizard. Typing a task (or /new <text>) captures the
// title, then inline buttons walk the user through workspace → project → type
// (label) → create. Webhook-only (callback_query isn't delivered to the
// long-poll login path). All replies are localized to the user's Telegram
// language (ru default for this SalesDoctor fork).

// ── command menu ────────────────────────────────────────────────────────────

// telegramBotCommands is the "/" menu registered via setMyCommands.
var telegramBotCommands = []telegram.BotCommand{
	{Command: "start", Description: "Начать"},
	{Command: "new", Description: "Новая задача"},
	{Command: "tasks", Description: "Мои задачи"},
	{Command: "help", Description: "Помощь"},
}

// RegisterBotCommands sets the bot's command menu. No-op when the bot is
// unconfigured. Called once on server startup; best-effort (logs on failure).
func (h *Handler) RegisterBotCommands(ctx context.Context) {
	if h.telegramBot == nil {
		return
	}
	if err := h.telegramBot.SetMyCommands(ctx, telegramBotCommands); err != nil {
		slog.Warn("telegram: setMyCommands failed", "error", err)
	}
}

// ── dispatch ────────────────────────────────────────────────────────────────

// processTelegramUpdate routes one inbound webhook update to the login flow, a
// command, the create wizard, or a wizard callback.
func (h *Handler) processTelegramUpdate(ctx context.Context, update telegramUpdate) {
	if h.telegramBot == nil {
		return
	}
	if update.CallbackQuery != nil {
		h.processBotCallback(ctx, update)
		return
	}
	h.processBotMessage(ctx, update)
}

func (h *Handler) processBotMessage(ctx context.Context, update telegramUpdate) {
	msg := update.Message
	if msg == nil || msg.From == nil || msg.Chat == nil || msg.Chat.Type != "private" {
		return
	}
	text := strings.TrimSpace(msg.Text)
	tgID := strconv.FormatInt(msg.From.ID, 10)
	lang := botLang(msg.From.LanguageCode)

	// Login deep link keeps priority over everything (existing OTP flow):
	// "/start login_<nonce>" mints + DMs a code.
	if _, ok := telegram.ParseStartPayload(text); ok {
		h.processTelegramLoginUpdate(ctx, update)
		return
	}

	cmd, rest := splitCommand(text)
	switch cmd {
	case "/start":
		h.botSendStart(ctx, tgID, lang)
	case "/help":
		h.botSend(ctx, tgID, botT(lang, "help"))
	case "/tasks":
		h.botSendOpen(ctx, tgID, botT(lang, "tasks.text"), botT(lang, "tasks.btn"))
	case "/new":
		if rest == "" {
			h.botSend(ctx, tgID, botT(lang, "new.guide"))
			return
		}
		h.startCreateWizard(ctx, tgID, lang, rest)
	default:
		// Any other plain text in a DM is captured as a new task title.
		if text != "" && !strings.HasPrefix(text, "/") {
			h.startCreateWizard(ctx, tgID, lang, text)
			return
		}
		h.botSend(ctx, tgID, botT(lang, "help"))
	}
}

func (h *Handler) processBotCallback(ctx context.Context, update telegramUpdate) {
	cb := update.CallbackQuery
	if cb == nil || cb.From == nil {
		return
	}
	tgID := strconv.FormatInt(cb.From.ID, 10)
	lang := botLang(cb.From.LanguageCode)
	// Always ack so the client's inline spinner stops, even if the tap is stale.
	_ = h.telegramBot.AnswerCallback(ctx, cb.ID)

	st, ok := h.telegramWizards.Get(tgID)
	if !ok {
		return // expired / no active wizard — a stale button tap
	}
	data := cb.Data
	switch {
	case strings.HasPrefix(data, "nw:ws:") && st.Step == telegram.WizardStepWorkspace:
		h.telegramWizards.SetWorkspace(tgID, strings.TrimPrefix(data, "nw:ws:"))
		h.presentWizard(ctx, tgID, lang)
	case strings.HasPrefix(data, "nw:pj:") && st.Step == telegram.WizardStepProject:
		h.telegramWizards.SetProject(tgID, decodeNone(strings.TrimPrefix(data, "nw:pj:")))
		h.presentWizard(ctx, tgID, lang)
	case strings.HasPrefix(data, "nw:tp:") && st.Step == telegram.WizardStepType:
		h.telegramWizards.SetLabel(tgID, decodeNone(strings.TrimPrefix(data, "nw:tp:")))
		h.presentWizard(ctx, tgID, lang)
	}
}

// ── create wizard ───────────────────────────────────────────────────────────

func (h *Handler) startCreateWizard(ctx context.Context, tgID, lang, title string) {
	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if userID == "" {
		h.botSendOpen(ctx, tgID, botT(lang, "notlinked"), botT(lang, "open.btn"))
		return
	}
	workspaces := h.botUserWorkspaces(ctx, userID)
	if len(workspaces) == 0 {
		h.botSend(ctx, tgID, botT(lang, "new.noworkspace"))
		return
	}

	h.telegramWizards.Start(tgID, tgID, title)
	if len(workspaces) == 1 {
		// Only one workspace — skip the choice and move on.
		h.telegramWizards.SetWorkspace(tgID, workspaces[0].ID)
		h.presentWizard(ctx, tgID, lang)
		return
	}
	rows := make([][]telegram.Button, 0, len(workspaces))
	for _, w := range workspaces {
		rows = append(rows, []telegram.Button{{Text: w.Name, CallbackData: "nw:ws:" + w.ID}})
	}
	h.botSendButtons(ctx, tgID, botT(lang, "new.workspace"), rows)
}

// presentWizard renders the current step (project / type) or finalizes. Steps
// with no options auto-advance so the user is never shown an empty picker.
func (h *Handler) presentWizard(ctx context.Context, tgID, lang string) {
	for {
		st, ok := h.telegramWizards.Get(tgID)
		if !ok {
			return
		}
		switch st.Step {
		case telegram.WizardStepProject:
			projects := h.botListOptions(ctx, st.WorkspaceID, false)
			if len(projects) == 0 {
				h.telegramWizards.SetProject(tgID, "")
				continue
			}
			h.botSendButtons(ctx, tgID, botT(lang, "new.project"), optionRows("nw:pj:", projects, botT(lang, "new.none")))
			return
		case telegram.WizardStepType:
			labels := h.botListOptions(ctx, st.WorkspaceID, true)
			if len(labels) == 0 {
				h.telegramWizards.SetLabel(tgID, "")
				continue
			}
			h.botSendButtons(ctx, tgID, botT(lang, "new.type"), optionRows("nw:tp:", labels, botT(lang, "new.none")))
			return
		case telegram.WizardStepTitle:
			h.finalizeWizard(ctx, tgID, lang)
			return
		default:
			return
		}
	}
}

func (h *Handler) finalizeWizard(ctx context.Context, tgID, lang string) {
	st, ok := h.telegramWizards.Get(tgID)
	if !ok {
		return
	}
	h.telegramWizards.Clear(tgID)

	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if userID == "" {
		h.botSendOpen(ctx, tgID, botT(lang, "notlinked"), botT(lang, "open.btn"))
		return
	}

	identifier, issueID, err := h.botCreateIssue(ctx, userID, st.WorkspaceID, st.ProjectID, st.LabelID, st.Title)
	if err != nil {
		slog.Warn("telegram bot: create issue failed", "error", err, "telegram_id", tgID)
		h.botSend(ctx, tgID, botT(lang, "new.failed"))
		return
	}

	wsSlug := ""
	if wsUUID, perr := util.ParseUUID(st.WorkspaceID); perr == nil {
		if ws, werr := h.Queries.GetWorkspace(ctx, wsUUID); werr == nil {
			wsSlug = ws.Slug
		}
	}
	text := fmt.Sprintf("✅ <b>%s</b> %s",
		html.EscapeString(identifier), html.EscapeString(st.Title))
	link := telegram.MiniAppLink(telegramBotUsername(), telegramMiniAppShortName(), telegram.MiniAppStartParam(wsSlug, issueID))
	if link == "" {
		h.botSend(ctx, tgID, text)
		return
	}
	if err := h.telegramBot.SendMessageWithButton(ctx, tgID, text, botT(lang, "new.openBtn"), link); err != nil {
		slog.Warn("telegram bot: send confirmation failed", "error", err, "telegram_id", tgID)
	}
}

// botCreateIssue creates an issue assigned to the creator and (best-effort)
// attaches the chosen label, then publishes issue:created so connected clients
// update live. Returns the human identifier (MUL-123) + issue UUID string.
func (h *Handler) botCreateIssue(ctx context.Context, userID, wsIDStr, projectIDStr, labelIDStr, title string) (string, string, error) {
	wsUUID, err := util.ParseUUID(wsIDStr)
	if err != nil {
		return "", "", fmt.Errorf("bad workspace id: %w", err)
	}
	creatorUUID, err := util.ParseUUID(userID)
	if err != nil {
		return "", "", fmt.Errorf("bad creator id: %w", err)
	}

	number, err := h.Queries.IncrementIssueCounter(ctx, wsUUID)
	if err != nil {
		return "", "", fmt.Errorf("allocate number: %w", err)
	}

	params := db.CreateIssueParams{
		WorkspaceID:  wsUUID,
		Title:        title,
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "member", Valid: true},
		AssigneeID:   creatorUUID,
		CreatorType:  "member",
		CreatorID:    creatorUUID,
		Position:     0,
		Number:       number,
	}
	if projectUUID, perr := util.ParseUUID(projectIDStr); perr == nil && projectIDStr != "" {
		params.ProjectID = projectUUID
	}

	issue, err := h.Queries.CreateIssue(ctx, params)
	if err != nil {
		return "", "", fmt.Errorf("create issue: %w", err)
	}

	if labelIDStr != "" {
		if labelUUID, lerr := util.ParseUUID(labelIDStr); lerr == nil {
			if aerr := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
				IssueID: issue.ID,
				LabelID: labelUUID,
			}); aerr != nil {
				slog.Warn("telegram bot: attach label failed", "error", aerr, "issue_id", uuidToString(issue.ID))
			}
		}
	}

	prefix := h.getIssuePrefix(ctx, wsUUID)
	resp := issueToResponse(issue, prefix)
	h.publish(protocol.EventIssueCreated, uuidToString(wsUUID), "member", userID, map[string]any{"issue": resp})

	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	return identifier, uuidToString(issue.ID), nil
}

// ── data helpers ────────────────────────────────────────────────────────────

type botOption struct{ ID, Name string }

// botUserWorkspaces returns the default SD workspaces the user is a member of,
// in slug order, for the workspace picker.
func (h *Handler) botUserWorkspaces(ctx context.Context, userID string) []botOption {
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return nil
	}
	var out []botOption
	for _, slug := range defaultWorkspaceSlugs() {
		ws, err := h.Queries.GetWorkspaceBySlug(ctx, slug)
		if err != nil {
			continue
		}
		if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      userUUID,
			WorkspaceID: ws.ID,
		}); err != nil {
			continue // not a member
		}
		out = append(out, botOption{ID: uuidToString(ws.ID), Name: ws.Name})
	}
	return out
}

// botListOptions returns up to 8 projects (labels=false) or labels (labels=true)
// in a workspace for a picker.
func (h *Handler) botListOptions(ctx context.Context, wsIDStr string, labels bool) []botOption {
	wsUUID, err := util.ParseUUID(wsIDStr)
	if err != nil {
		return nil
	}
	var out []botOption
	if labels {
		ls, err := h.Queries.ListLabels(ctx, wsUUID)
		if err != nil {
			return nil
		}
		for _, l := range ls {
			out = append(out, botOption{ID: uuidToString(l.ID), Name: l.Name})
		}
	} else {
		ps, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: wsUUID})
		if err != nil {
			return nil
		}
		for _, p := range ps {
			out = append(out, botOption{ID: uuidToString(p.ID), Name: p.Title})
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// optionRows builds one button per option (callback prefix+id) plus a trailing
// "None" row (callback prefix+"-").
func optionRows(prefix string, opts []botOption, noneLabel string) [][]telegram.Button {
	rows := make([][]telegram.Button, 0, len(opts)+1)
	for _, o := range opts {
		rows = append(rows, []telegram.Button{{Text: o.Name, CallbackData: prefix + o.ID}})
	}
	rows = append(rows, []telegram.Button{{Text: noneLabel, CallbackData: prefix + "-"}})
	return rows
}

func decodeNone(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

// ── send helpers ────────────────────────────────────────────────────────────

func (h *Handler) botSend(ctx context.Context, chatID, text string) {
	if err := h.telegramBot.SendMessage(ctx, chatID, text); err != nil {
		slog.Warn("telegram bot: send failed", "error", err, "chat_id", chatID)
	}
}

func (h *Handler) botSendButtons(ctx context.Context, chatID, text string, rows [][]telegram.Button) {
	if err := h.telegramBot.SendButtons(ctx, chatID, text, rows); err != nil {
		slog.Warn("telegram bot: send buttons failed", "error", err, "chat_id", chatID)
	}
}

// botSendOpen sends a message with a single "open the app" deep-link button.
func (h *Handler) botSendOpen(ctx context.Context, chatID, text, buttonText string) {
	link := telegram.MiniAppLink(telegramBotUsername(), telegramMiniAppShortName(), "")
	if link == "" {
		h.botSend(ctx, chatID, text)
		return
	}
	if err := h.telegramBot.SendMessageWithButton(ctx, chatID, text, buttonText, link); err != nil {
		slog.Warn("telegram bot: send open failed", "error", err, "chat_id", chatID)
	}
}

func (h *Handler) botSendStart(ctx context.Context, chatID, lang string) {
	h.botSendOpen(ctx, chatID, botT(lang, "start.text"), botT(lang, "open.btn"))
}

// ── small parsing utils ─────────────────────────────────────────────────────

// splitCommand returns the lowercased command (first token, "@bot" stripped) and
// the remaining text. Non-command text yields cmd="" and rest=text.
func splitCommand(text string) (cmd, rest string) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	first := parts[0]
	if len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}
	if !strings.HasPrefix(first, "/") {
		return "", text
	}
	if at := strings.IndexByte(first, '@'); at >= 0 {
		first = first[:at]
	}
	return strings.ToLower(first), rest
}

// botLang maps a Telegram language_code to a supported bot language, defaulting
// to Russian for this SD fork.
func botLang(code string) string {
	if l := normalizeUserLang(code); l != "" {
		return l
	}
	return "ru"
}

// ── i18n ────────────────────────────────────────────────────────────────────

var botStrings = map[string]map[string]string{
	"en": {
		"start.text":      "👋 Welcome to Agora. Send me a task and I’ll create it — or use /new. Tap below to open the app.",
		"open.btn":        "📋 Open Agora",
		"help":            "Send a task (or /new <text>) to create one — I’ll ask the workspace, project and type.\n\n/new — new task\n/tasks — open my tasks\n/help — this help",
		"tasks.text":      "Your tasks:",
		"tasks.btn":       "📋 My tasks",
		"notlinked":       "Open Agora once to sign in, then send me a task.",
		"new.guide":       "Send the task, e.g. “Fix the login bug”.",
		"new.workspace":   "Which workspace?",
		"new.project":     "Which project?",
		"new.type":        "Task type?",
		"new.none":        "— None —",
		"new.openBtn":     "Open",
		"new.failed":      "Couldn’t create the task. Try again.",
		"new.noworkspace": "You’re not a member of any workspace yet.",
	},
	"ru": {
		"start.text":      "👋 Добро пожаловать в Agora. Отправьте задачу — я её создам, или нажмите /new. Кнопка ниже открывает приложение.",
		"open.btn":        "📋 Открыть Agora",
		"help":            "Отправьте задачу (или /new <текст>), чтобы создать — я спрошу пространство, проект и тип.\n\n/new — новая задача\n/tasks — мои задачи\n/help — помощь",
		"tasks.text":      "Ваши задачи:",
		"tasks.btn":       "📋 Мои задачи",
		"notlinked":       "Откройте Agora один раз для входа, затем отправьте задачу.",
		"new.guide":       "Отправьте задачу, например «Починить вход».",
		"new.workspace":   "Какое пространство?",
		"new.project":     "Какой проект?",
		"new.type":        "Тип задачи?",
		"new.none":        "— Без —",
		"new.openBtn":     "Открыть",
		"new.failed":      "Не удалось создать задачу. Повторите попытку.",
		"new.noworkspace": "Вы пока не состоите ни в одном пространстве.",
	},
	"uz": {
		"start.text":      "👋 Agora’ga xush kelibsiz. Vazifa yuboring — men uni yarataman, yoki /new bosing. Quyidagi tugma ilovani ochadi.",
		"open.btn":        "📋 Agora’ni ochish",
		"help":            "Vazifa yuboring (yoki /new <matn>) — men ish maydoni, loyiha va turni so‘rayman.\n\n/new — yangi vazifa\n/tasks — vazifalarim\n/help — yordam",
		"tasks.text":      "Vazifalaringiz:",
		"tasks.btn":       "📋 Vazifalarim",
		"notlinked":       "Kirish uchun Agora’ni bir marta oching, so‘ng vazifa yuboring.",
		"new.guide":       "Vazifani yuboring, masalan «Kirishni tuzatish».",
		"new.workspace":   "Qaysi ish maydoni?",
		"new.project":     "Qaysi loyiha?",
		"new.type":        "Vazifa turi?",
		"new.none":        "— Yo‘q —",
		"new.openBtn":     "Ochish",
		"new.failed":      "Vazifa yaratilmadi. Qayta urinib ko‘ring.",
		"new.noworkspace": "Siz hali birorta ish maydoniga a’zo emassiz.",
	},
}

func botT(lang, key string) string {
	if m := botStrings[lang]; m != nil {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := botStrings["ru"][key]; ok {
		return v
	}
	return key
}
