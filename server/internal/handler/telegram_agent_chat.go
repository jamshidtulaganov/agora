package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
)

// StartAgentTelegramPollers opens a long-poll loop for every active
// installation. Best-effort: a bot that fails keeps retrying on a backoff
// without affecting the others or the server.
func (h *Handler) StartAgentTelegramPollers(ctx context.Context) {
	rows, err := h.Queries.ListActiveTelegramInstallations(ctx)
	if err != nil {
		slog.Warn("telegram agent pollers: list failed", "error", err)
		return
	}
	for _, row := range rows {
		go h.pollAgentTelegram(ctx, row)
	}
	if len(rows) > 0 {
		slog.Info("telegram agent pollers started", "count", len(rows))
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

	var offset int64
	slog.Info("telegram agent poller: listening", "bot", row.BotUsername, "agent_id", uuidToString(row.AgentID))

	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := bot.GetUpdates(ctx, offset, telegramPollTimeoutSec)
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
			h.handleAgentGroupMessage(ctx, row, update)
		}
	}
}

// handleAgentGroupMessage turns one addressed group message into agent work.
//
// Ignores anything that is not a group message with text. Privacy mode already
// filters to messages that mention or reply to this bot, so a message reaching
// here is addressed to this agent.
func (h *Handler) handleAgentGroupMessage(ctx context.Context, row db.TelegramInstallation, update telegramUpdate) {
	msg := update.Message
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return // DMs to an agent bot are out of scope; the platform bot owns DMs
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	// Strip the @mention so the agent reads the request, not the addressing.
	text = strings.TrimSpace(strings.ReplaceAll(text, "@"+row.BotUsername, ""))
	if text == "" {
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	// Remember which chat this agent lives in the first time it is addressed,
	// so replies and reports find their way back without manual configuration.
	if !row.ChatID.Valid || row.ChatID.String != chatID {
		if updated, err := h.Queries.SetTelegramInstallationChat(ctx, db.SetTelegramInstallationChatParams{
			AgentID: row.AgentID,
			ChatID:  pgtype.Text{String: chatID, Valid: true},
		}); err == nil {
			row = updated
		}
	}

	session, row, err := h.agentTelegramSession(ctx, row)
	if err != nil {
		slog.Warn("telegram agent chat: no session", "agent_id", uuidToString(row.AgentID), "error", err)
		return
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

	if _, err := h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       body,
	}); err != nil {
		slog.Warn("telegram agent chat: create message failed", "error", err)
		return
	}
	if _, err := h.TaskService.EnqueueChatTask(ctx, session, session.CreatorID); err != nil {
		slog.Warn("telegram agent chat: enqueue failed", "agent_id", uuidToString(row.AgentID), "error", err)
		return
	}
	slog.Info("telegram agent chat: dispatched", "bot", row.BotUsername, "chat_id", chatID)
}

// agentTelegramSession returns the session backing this bot's group
// conversation, creating it on first contact. The link is stored on the
// installation, so the thread survives a human renaming the session — matching
// on a title prefix would silently start a new conversation and lose context.
func (h *Handler) agentTelegramSession(ctx context.Context, row db.TelegramInstallation) (db.ChatSession, db.TelegramInstallation, error) {
	if row.ChatSessionID.Valid {
		if s, err := h.Queries.GetChatSession(ctx, row.ChatSessionID); err == nil && s.Status == "active" {
			return s, row, nil
		}
	}
	session, err := h.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: row.WorkspaceID,
		AgentID:     row.AgentID,
		CreatorID:   row.InstallerUserID,
		Title:       "Telegram: " + row.BotUsername,
	})
	if err != nil {
		return db.ChatSession{}, row, err
	}
	if updated, linkErr := h.Queries.SetTelegramInstallationSession(ctx, db.SetTelegramInstallationSessionParams{
		AgentID:       row.AgentID,
		ChatSessionID: session.ID,
	}); linkErr == nil {
		row = updated
	}
	return session, row, nil
}

// SendAgentChatReplyToTelegram posts a finished chat task's assistant reply
// back to the group. No-op unless the session's agent owns a bot bound to a
// chat, so web-only chats are unaffected.
func (h *Handler) SendAgentChatReplyToTelegram(ctx context.Context, chatSessionID string) {
	sessionUUID, err := util.ParseUUID(chatSessionID)
	if err != nil {
		return
	}
	// Resolving through the installation is what makes this a no-op for every
	// web-only chat: only a session a bot is bound to has a row here.
	row, err := h.Queries.GetTelegramInstallationBySession(ctx, sessionUUID)
	if err != nil {
		return
	}
	bot, chatID := h.agentTelegramClient(ctx, row.AgentID)
	if bot == nil || chatID == "" {
		return
	}
	messages, err := h.Queries.ListChatMessages(ctx, sessionUUID)
	if err != nil || len(messages) == 0 {
		return
	}
	// Newest assistant message wins; the list is chronological.
	reply := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			reply = strings.TrimSpace(messages[i].Content)
			break
		}
	}
	if reply == "" {
		return
	}
	if err := bot.SendMessage(ctx, chatID, truncateForTelegram(reply)); err != nil {
		slog.Warn("telegram agent chat: reply send failed", "chat_id", chatID, "error", err)
		return
	}
	slog.Info("telegram agent chat: replied", "chat_id", chatID)
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
