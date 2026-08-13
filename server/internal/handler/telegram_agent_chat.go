package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Two-way group chat for agents that own a bot.
//
// Inbound: one long-poll loop per installation. A group message addressed to
// that bot becomes a user message on a chat session for the agent, which
// dispatches a task exactly as the web chat panel does — the agent has one
// conversation surface, not two.
//
// Outbound: when a chat task completes, its assistant reply is posted back to
// the group by the same bot. The link is already implied by the data —
// session -> agent -> installation -> chat_id — so no message-mapping table is
// needed.
//
// LONG-POLL, not webhooks. Each agent bot would otherwise need its own publicly
// reachable webhook path, which a self-hosted or local deployment cannot
// provide. getUpdates needs no inbound connectivity at all. The cost is one
// goroutine per installed bot, which is fine at the scale of a team's agents.
//
// PRIVACY MODE is load-bearing and must stay ON (BotFather's default): a group
// bot then receives only messages that @mention it or reply to it. Turning it
// off would pipe every message in the group into an agent task.

const (
	// telegramPollTimeoutSec is the getUpdates long-poll window. Telegram holds
	// the request open until an update arrives or this elapses, so a longer
	// window means fewer requests, not slower delivery.
	telegramPollTimeoutSec = 50
	// telegramPollBackoff is the pause after a failed poll — a bot whose token
	// was revoked must not spin.
	telegramPollBackoff = 30 * time.Second
	// A user may answer a bot follow-up without tagging it again. This mirrors
	// the reference gateway while keeping ordinary room chatter out of Agora.
	telegramFollowupWindow = 3 * time.Minute
)

// agentTelegramPollers tracks the running long-poll loops so one can be
// replaced or stopped without a server restart.
//
// Replacing matters more than it looks: Telegram allows exactly ONE getUpdates
// consumer per bot, and a second one makes both sides fail with 409 Conflict.
// Re-installing a bot must therefore cancel the previous loop before starting
// the new one, or the agent goes deaf while appearing configured.
type agentTelegramPollers struct {
	mu     sync.Mutex
	base   context.Context
	cancel map[string]context.CancelFunc
}

func (p *agentTelegramPollers) ready() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.base != nil
}

// StartAgentTelegramPollers opens a long-poll loop for every active
// installation and remembers the context, so a bot installed later can be
// started immediately rather than waiting for the next restart. Best-effort: a
// bot that fails keeps retrying on a backoff without affecting the others.
func (h *Handler) StartAgentTelegramPollers(ctx context.Context) {
	h.tgPollers.mu.Lock()
	h.tgPollers.base = ctx
	if h.tgPollers.cancel == nil {
		h.tgPollers.cancel = map[string]context.CancelFunc{}
	}
	h.tgPollers.mu.Unlock()

	rows, err := h.Queries.ListActiveTelegramInstallations(ctx)
	if err != nil {
		slog.Warn("telegram agent pollers: list failed", "error", err)
		return
	}
	for _, row := range rows {
		h.startAgentTelegramPoller(row)
	}
	if len(rows) > 0 {
		slog.Info("telegram agent pollers started", "count", len(rows))
	}
}

// startAgentTelegramPoller (re)starts the loop for one installation, cancelling
// any loop already running for that agent. Safe to call on every install.
func (h *Handler) startAgentTelegramPoller(row db.TelegramInstallation) {
	h.tgPollers.mu.Lock()
	defer h.tgPollers.mu.Unlock()
	if h.tgPollers.base == nil {
		// Pollers were never started (tests, or a deployment that disables
		// them). Nothing to attach a lifetime to.
		return
	}
	key := uuidToString(row.AgentID)
	if stop, ok := h.tgPollers.cancel[key]; ok {
		stop() // never two consumers on one bot
	}
	ctx, cancel := context.WithCancel(h.tgPollers.base)
	h.tgPollers.cancel[key] = cancel
	go h.pollAgentTelegram(ctx, row)
}

// stopAgentTelegramPoller ends the loop for one agent, so an uninstalled bot
// stops consuming updates immediately instead of at the next restart.
func (h *Handler) stopAgentTelegramPoller(agentID pgtype.UUID) {
	h.tgPollers.mu.Lock()
	defer h.tgPollers.mu.Unlock()
	key := uuidToString(agentID)
	if stop, ok := h.tgPollers.cancel[key]; ok {
		stop()
		delete(h.tgPollers.cancel, key)
	}
}

// pollAgentTelegram long-polls one bot until the context ends.
func (h *Handler) pollAgentTelegram(ctx context.Context, row db.TelegramInstallation) {
	box, err := telegramSealBox()
	if err != nil {
		slog.Warn("telegram agent poller: no seal key; not starting", "agent_id", uuidToString(row.AgentID))
		return
	}
	token, err := box.Open(row.BotTokenEncrypted)
	if err != nil {
		slog.Warn("telegram agent poller: cannot open token", "agent_id", uuidToString(row.AgentID))
		return
	}
	bot := telegram.NewBotClient(string(token))

	// A webhook and getUpdates are mutually exclusive; clear any leftover one
	// so a bot previously wired to a webhook can still be polled.
	if err := bot.DeleteWebhook(ctx); err != nil {
		slog.Debug("telegram agent poller: deleteWebhook", "error", err)
	}

	// Publish the command menu so /allow and /deny autocomplete in the group.
	// Discoverability is the whole reason these live in Telegram at all — an
	// admin who has to remember the exact syntax will go back to the web UI,
	// which is the friction the commands exist to remove.
	if err := bot.SetMyCommands(ctx, agentBotCommands()); err != nil {
		slog.Debug("telegram agent poller: setMyCommands", "error", err)
	}

	var offset int64
	slog.Info("telegram agent poller: listening", "bot", row.BotUsername, "agent_id", uuidToString(row.AgentID))

	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := bot.GetUpdates(ctx, offset, telegramPollTimeoutSec, "message", "callback_query")
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("telegram agent poller: getUpdates failed", "bot", row.BotUsername, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(telegramPollBackoff):
			}
			continue
		}
		for _, raw := range updates {
			update, ok := decodeTelegramUpdate(raw)
			if !ok {
				continue
			}
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.CallbackQuery != nil {
				h.handleAgentCallback(ctx, row, update)
				continue
			}
			h.handleAgentGroupMessage(ctx, row, update)
		}
	}
}

// handleAgentGroupMessage turns one addressed group message into agent work.
//
// Ignores anything that is not a group message with text. Telegram privacy mode
// is only defense-in-depth: the code below independently requires a mention, a
// reply to this bot, or a short follow-up in the sender's own active thread.
func (h *Handler) handleAgentGroupMessage(ctx context.Context, row db.TelegramInstallation, update telegramUpdate) {
	msg := update.Message
	if msg == nil || msg.Chat == nil || msg.From == nil || msg.From.IsBot {
		return
	}
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return // DMs to an agent bot are out of scope; the platform bot owns DMs
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	// Re-read the installation per message. The poller captured its copy at
	// startup and never refreshes it, so a stale copy meant every message saw
	// chat_session_id as unset and started a NEW session — the conversation
	// lost its thread on the second message. Re-reading also picks up policy
	// and chat changes without a server restart, so revoking access takes
	// effect immediately rather than at the next deploy.
	if fresh, err := h.Queries.GetTelegramInstallationByAgent(ctx, row.AgentID); err == nil {
		row = fresh
	}
	if row.Status != "active" {
		return
	}

	// Binding runs BEFORE authorization: a scan is how a group becomes trusted
	// in the first place, so requiring it to already be trusted would make the
	// flow impossible. The one-time token is the authorization there.
	if h.tryBindTelegramGroup(ctx, row, msg.Chat.ID, msg.From.ID, text) {
		return
	}
	// Re-read once more: binding just mutated the row, and the copy above
	// predates it. Without this the very first message after a scan would be
	// judged against the pre-binding access state.
	if fresh, err := h.Queries.GetTelegramInstallationByAgent(ctx, row.AgentID); err == nil {
		row = fresh
	}

	// The room gate is separate from, and ahead of, the person gate: an
	// untrusted room gets nothing at all, not even a refusal, because a bot
	// that answers an unknown group confirms which agent it belongs to.
	if !telegramChatAllowed(row, msg.Chat.ID) {
		slog.Info("telegram agent chat: chat not allowed",
			"bot", row.BotUsername, "chat", msg.Chat.ID)
		return
	}

	// Access commands sit between the two gates deliberately. They must be
	// available to an admin who is NOT on the allowlist — the normal state for
	// whoever installed the bot — but only from a room already trusted.
	if h.handleTelegramAccessCommand(ctx, row, msg.Chat.ID, msg.From.ID, text) {
		return
	}

	// Authorization BEFORE any work. These agents hold repo, git, QA and deploy
	// tooling, so an inbound group message is an instruction to something that
	// can change code — not a chat line. Everyone in the group can send one.
	if !telegramSenderAllowed(row, msg.Chat.ID, msg.From.ID) {
		slog.Info("telegram agent chat: sender not allowed",
			"bot", row.BotUsername, "policy", row.AccessPolicy,
			"from", msg.From.ID, "chat", msg.Chat.ID)
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	explicitlyAddressed := telegramMessageAddressesAgent(
		text,
		msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil &&
			msg.ReplyToMessage.From.IsBot &&
			strings.EqualFold(strings.TrimPrefix(msg.ReplyToMessage.From.Username, "@"), strings.TrimPrefix(row.BotUsername, "@")),
		row.BotUsername,
	)
	if !explicitlyAddressed {
		// Privacy mode is defense-in-depth, not an authorization boundary. If an
		// operator disables it, ordinary group chatter still must not reach a
		// powerful runtime. Only a recent participant in this exact user thread
		// gets the short follow-up window used for "yes" / "continue" answers.
		link, err := h.Queries.GetTelegramChatSession(ctx, db.GetTelegramChatSessionParams{
			AgentID: row.AgentID, ChatID: chatID, TelegramUserID: msg.From.ID,
		})
		if err != nil || !link.LastEngagedAt.Valid || time.Since(link.LastEngagedAt.Time) > telegramFollowupWindow {
			return
		}
	}

	// Strip the @mention so the agent reads the request, not the addressing.
	text = stripTelegramBotMention(text, row.BotUsername)
	if text == "" {
		return
	}

	// The chat is NOT learned from an inbound message. It is bound at install
	// time, deliberately: learning it here would mean whichever group first
	// messaged the bot became its trusted chat, so an invite would grant
	// itself authorization. The gate above already required the bound chat.
	initiatorID := h.telegramInitiatorUserID(ctx, row, msg.From.ID)
	session, err := h.agentTelegramSession(ctx, row, chatID, msg.From.ID, initiatorID)
	if err != nil {
		slog.Warn("telegram agent chat: no session", "agent_id", uuidToString(row.AgentID), "error", err)
		return
	}
	if bot, _ := h.agentTelegramClient(ctx, row.AgentID); bot != nil {
		_ = bot.SendChatAction(ctx, chatID, "typing")
	}

	// Name the human so the agent knows who it is answering — a group has many
	// voices and "who asked" changes the answer.
	who := strings.TrimSpace(msg.From.FirstName)
	if msg.From.Username != "" {
		who = strings.TrimSpace(who + " (@" + msg.From.Username + ")")
	}
	body := text
	if who != "" {
		body = who + ":\n" + text
	}

	msgRow, err := h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       body,
	})
	if err != nil {
		slog.Warn("telegram agent chat: create message failed", "error", err)
		return
	}
	task, err := h.TaskService.EnqueueChatTaskWithLink(ctx, session, initiatorID,
		func(qtx *db.Queries, linkedTask db.AgentTaskQueue) error {
			if linkErr := qtx.LinkChatMessageToTask(ctx, db.LinkChatMessageToTaskParams{
				ID: msgRow.ID, TaskID: linkedTask.ID,
			}); linkErr != nil {
				return linkErr
			}
			_, linkErr := qtx.CreateTelegramTaskDelivery(ctx, db.CreateTelegramTaskDeliveryParams{
				TaskID: linkedTask.ID, AgentID: row.AgentID, ChatID: chatID,
				TelegramUserID: msg.From.ID, ReplyToMessageID: msg.MessageID,
			})
			return linkErr
		},
	)
	if err != nil {
		slog.Warn("telegram agent chat: enqueue failed", "agent_id", uuidToString(row.AgentID), "error", err)
		if bot, _ := h.agentTelegramClient(ctx, row.AgentID); bot != nil {
			message := "Agent hozir javob bera olmaydi. Birozdan keyin qayta urinib ko'ring."
			if errors.Is(err, service.ErrChatTaskAgentNoRuntime) {
				message = "Agent runtime'ga ulanmagan. Agora'da runtime sozlang va qayta urinib ko'ring."
			} else if errors.Is(err, service.ErrChatTaskAgentArchived) {
				message = "Bu agent arxivlangan va yangi so'rov qabul qilmaydi."
			}
			_ = bot.SendMessageReply(ctx, chatID, message, msg.MessageID)
		}
		return
	}
	_ = h.Queries.TouchTelegramChatSession(ctx, session.ID)
	slog.Info("telegram agent chat: dispatched", "bot", row.BotUsername, "chat_id", chatID, "task_id", uuidToString(task.ID))
}

// agentTelegramSession returns the session backing this bot's group
// conversation, creating it on first contact. The link is stored on the
// installation, so the thread survives a human renaming the session — matching
// on a title prefix would silently start a new conversation and lose context.
func (h *Handler) agentTelegramSession(ctx context.Context, row db.TelegramInstallation, chatID string, telegramUserID int64, creatorID pgtype.UUID) (db.ChatSession, error) {
	// The query already restricts to an active session, so a hit is usable as
	// is; a miss means this chat has no live conversation and needs a new one.
	if link, err := h.Queries.GetTelegramChatSession(ctx, db.GetTelegramChatSessionParams{
		AgentID: row.AgentID, ChatID: chatID, TelegramUserID: telegramUserID,
	}); err == nil {
		if s, sErr := h.Queries.GetChatSession(ctx, link.ChatSessionID); sErr == nil {
			return s, nil
		}
	}
	session, err := h.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: row.WorkspaceID,
		AgentID:     row.AgentID,
		CreatorID:   creatorID,
		Title:       "Telegram " + chatID + "/" + strconv.FormatInt(telegramUserID, 10) + ": " + row.BotUsername,
	})
	if err != nil {
		return db.ChatSession{}, err
	}
	if _, linkErr := h.Queries.UpsertTelegramChatSession(ctx, db.UpsertTelegramChatSessionParams{
		AgentID: row.AgentID, ChatID: chatID, TelegramUserID: telegramUserID,
		ChatSessionID: session.ID,
	}); linkErr != nil {
		return db.ChatSession{}, linkErr
	}
	return session, nil
}

// SendAgentChatReplyToTelegram posts a finished chat task's assistant reply
// back to the group. No-op unless the session's agent owns a bot bound to a
// chat, so web-only chats are unaffected.
func (h *Handler) SendAgentChatReplyToTelegram(ctx context.Context, taskID, chatSessionID string) {
	sessionUUID, err := util.ParseUUID(chatSessionID)
	if err != nil {
		return
	}
	// Resolve the chat that ASKED, not the installation's report destination:
	// several groups can talk to one agent, and an answer belongs to the room
	// that raised the question. Only a session a group opened has a row here,
	// so web-only chats no-op.
	var agentID pgtype.UUID
	chatID := ""
	var replyTo int64
	var deliveryTaskID pgtype.UUID
	if taskUUID, parseErr := util.ParseUUID(taskID); parseErr == nil {
		if delivery, deliveryErr := h.Queries.GetTelegramTaskDeliveryByTask(ctx, taskUUID); deliveryErr == nil {
			deliveryTaskID = taskUUID
			agentID = delivery.AgentID
			chatID = delivery.ChatID
			replyTo = delivery.ReplyToMessageID
		}
	}
	if chatID == "" {
		link, linkErr := h.Queries.GetTelegramChatSessionBySession(ctx, sessionUUID)
		if linkErr != nil {
			return
		}
		agentID = link.AgentID
		chatID = link.ChatID
	}
	bot, _ := h.agentTelegramClient(ctx, agentID)
	if bot == nil {
		return
	}
	reply := ""
	if deliveryTaskID.Valid {
		if message, messageErr := h.Queries.GetAssistantChatMessageByTask(ctx, deliveryTaskID); messageErr == nil {
			reply = strings.TrimSpace(message.Content)
		}
	} else {
		// Compatibility for tasks queued before telegram_task_delivery existed.
		messages, messagesErr := h.Queries.ListChatMessages(ctx, sessionUUID)
		if messagesErr != nil {
			return
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				reply = strings.TrimSpace(messages[i].Content)
				break
			}
		}
	}
	if reply == "" {
		return
	}
	deliveryClaimed := false
	if deliveryTaskID.Valid {
		if _, claimErr := h.Queries.ClaimTelegramTaskDelivery(ctx, deliveryTaskID); claimErr != nil {
			// Another completion event already owns or finished this delivery.
			return
		}
		deliveryClaimed = true
	}
	markDelivered := func() {
		if deliveryClaimed {
			_ = h.Queries.MarkTelegramTaskDeliveryDelivered(ctx, deliveryTaskID)
		}
	}
	releaseDelivery := func() {
		if deliveryClaimed {
			_ = h.Queries.ReleaseTelegramTaskDelivery(ctx, deliveryTaskID)
		}
	}
	// A reply carrying a table, or one too long to read in a group, goes out as
	// a rendered HTML attachment: Telegram displays no markdown table, so the
	// most useful answers were arriving as raw pipes. Short prose stays a
	// message — a chat where every answer is a file stops being a chat.
	if replyNeedsDocument(reply) {
		now := time.Now()
		doc, err := renderReportPDF(replyDocumentTitle(now), reply)
		if err != nil {
			// Fall back to text rather than going silent. A reply the reader
			// has to squint at beats no reply at all, and a document renderer
			// failing is not the agent's answer being wrong.
			slog.Warn("telegram agent chat: pdf render failed, sending text", "error", err)
		} else if sendErr := bot.SendDocument(ctx, chatID, replyDocumentFilename(now), doc, replyCaption(reply)); sendErr != nil {
			slog.Warn("telegram agent chat: reply document send failed", "chat_id", chatID, "error", sendErr)
			releaseDelivery()
			return
		} else {
			markDelivered()
			slog.Info("telegram agent chat: replied with spreadsheet", "chat_id", chatID)
			return
		}
	}
	if err := bot.SendMarkdownReply(ctx, chatID, truncateForTelegram(reply), replyTo); err != nil {
		slog.Warn("telegram agent chat: reply send failed", "chat_id", chatID, "error", err)
		releaseDelivery()
		return
	}
	markDelivered()
	slog.Info("telegram agent chat: replied", "chat_id", chatID)
}

// telegramInitiatorUserID attributes the task to the real Telegram sender
// when that identity belongs to this workspace. Raw allowlist users need not
// have an Agora account, so the installer remains the explicit fallback.
func (h *Handler) telegramInitiatorUserID(ctx context.Context, row db.TelegramInstallation, telegramUserID int64) pgtype.UUID {
	userID, err := h.userIDByExternalIdentity(ctx, providerTelegram, strconv.FormatInt(telegramUserID, 10))
	if err != nil || userID == "" {
		return row.InstallerUserID
	}
	parsed, err := util.ParseUUID(userID)
	if err != nil {
		return row.InstallerUserID
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: parsed, WorkspaceID: row.WorkspaceID,
	}); err != nil {
		return row.InstallerUserID
	}
	return parsed
}

func telegramMessageAddressesAgent(text string, repliesToBot bool, botUsername string) bool {
	if repliesToBot {
		return true
	}
	return telegramBotMentionIndex(text, botUsername) >= 0
}

func stripTelegramBotMention(text, botUsername string) string {
	for {
		idx := telegramBotMentionIndex(text, botUsername)
		if idx < 0 {
			return strings.TrimSpace(text)
		}
		needle := "@" + strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
		text = text[:idx] + text[idx+len(needle):]
	}
}

func telegramBotMentionIndex(text, botUsername string) int {
	username := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(botUsername), "@"))
	if username == "" {
		return -1
	}
	lower := strings.ToLower(text)
	needle := "@" + username
	for start := 0; start < len(lower); {
		rel := strings.Index(lower[start:], needle)
		if rel < 0 {
			return -1
		}
		idx := start + rel
		next := idx + len(needle)
		if next == len(lower) || !isTelegramUsernameByte(lower[next]) {
			return idx
		}
		start = next
	}
	return -1
}

func isTelegramUsernameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// decodeTelegramUpdate parses one raw update from getUpdates. A malformed
// update is skipped rather than killing the poll loop — one bad payload must
// not silence an agent.
func decodeTelegramUpdate(raw json.RawMessage) (telegramUpdate, bool) {
	var update telegramUpdate
	if err := json.Unmarshal(raw, &update); err != nil {
		return telegramUpdate{}, false
	}
	return update, true
}

// telegramChatAllowed reports whether this room may address the agent at all.
// Separate from the sender check because the two answer different questions:
// which ROOM is trusted, and which PERSON in it.
func telegramChatAllowed(row db.TelegramInstallation, chatID int64) bool {
	for _, allowed := range row.AllowedChatIds {
		if allowed == chatID {
			return true
		}
	}
	return false
}

// telegramSenderAllowed decides whether this message may instruct the agent.
//
// Two independent gates, both of which must pass:
//
//  1. The CHAT must be the one this installation is bound to. Without it,
//     adding the bot to any other group would silently grant that group the
//     same power — an invite is not an authorization.
//  2. The SENDER must satisfy the policy. 'closed' (the default) refuses
//     everyone, so an installation created only to deliver reports never
//     accepts instructions by accident.
//
// PURE, so the policy is unit-testable without a database or Telegram.
func telegramSenderAllowed(row db.TelegramInstallation, chatID, fromID int64) bool {
	// The chat must be one this installation was bound to. An installation
	// bound to no chat trusts none — an invite is not an authorization.
	if !telegramChatAllowed(row, chatID) {
		return false
	}
	switch row.AccessPolicy {
	case "open":
		return true
	case "allowlist":
		for _, allowed := range row.AllowedTelegramUserIds {
			if allowed == fromID {
				return true
			}
		}
		return false
	default: // "closed", and anything unrecognised — fail shut, never open
		return false
	}
}

// agentBotCommands is the menu Telegram shows for an agent bot. Kept short on
// purpose: a long list buries the two commands that matter.
func agentBotCommands() []telegram.BotCommand {
	return []telegram.BotCommand{
		{Command: "access", Description: "Kim ruxsatga ega — hozirgi holat"},
		{Command: "allow", Description: "Ruxsat berish: /allow user <id> yoki /allow chat"},
		{Command: "deny", Description: "Ruxsatni olib tashlash: /deny user <id>"},
		{Command: "reset", Description: "Suhbatni tozalash — agent eski javobini takrorlayotgan bo'lsa"},
	}
}
