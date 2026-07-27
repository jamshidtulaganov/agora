package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-agent Telegram bots.
//
// The platform bot (TELEGRAM_BOT_TOKEN) stays what it is: login OTP, inbox DMs,
// autopilot report delivery. It speaks for Agora. An INSTALLATION gives one
// agent its own bot so it can speak for itself in a group and be replied to —
// the same shape lark_installation already proves.
//
// A bot token is full control of that bot: it can post to every chat the bot is
// in and read everything addressed to it. So it is sealed with
// AGORA_TELEGRAM_SECRET_KEY at rest, never logged, and never returned by any
// endpoint — not even masked, since a partial token still narrows a search.

// ErrTelegramSealKeyMissing is returned when an install is attempted without
// AGORA_TELEGRAM_SECRET_KEY. Storing a bot token in plaintext is not an
// acceptable fallback, so the install fails loudly instead.
var ErrTelegramSealKeyMissing = errors.New("AGORA_TELEGRAM_SECRET_KEY is not set")

// telegramSealBox loads the secretbox used for bot tokens.
func telegramSealBox() (*secretbox.Box, error) {
	key, err := secretbox.LoadKey("AGORA_TELEGRAM_SECRET_KEY")
	if err != nil {
		return nil, ErrTelegramSealKeyMissing
	}
	return secretbox.New(key)
}

// TelegramInstallationResponse is the wire shape. Deliberately carries NO token
// field: a caller can see which bot an agent owns and whether it is wired to a
// chat, never the credential itself.
type TelegramInstallationResponse struct {
	AgentID     string `json:"agent_id"`
	BotUsername string `json:"bot_username"`
	BotUserID   string `json:"bot_user_id"`
	ChatID      string `json:"chat_id,omitempty"`
	Status      string `json:"status"`
	InstalledAt string `json:"installed_at"`
}

func telegramInstallationToResponse(row db.TelegramInstallation) TelegramInstallationResponse {
	resp := TelegramInstallationResponse{
		AgentID:     uuidToString(row.AgentID),
		BotUsername: row.BotUsername,
		BotUserID:   strconv.FormatInt(row.BotUserID, 10),
		Status:      row.Status,
	}
	if row.ChatID.Valid {
		resp.ChatID = row.ChatID.String
	}
	if row.InstalledAt.Valid {
		resp.InstalledAt = row.InstalledAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

type installTelegramBotRequest struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// InstallAgentTelegramBot handles PUT /api/agents/{id}/telegram.
//
// Verifies the token against getMe BEFORE storing it: an install that succeeds
// with a dead token would fail silently later, at the moment someone is waiting
// for a reply in a group. getMe also supplies the bot's username and id, which
// inbound routing needs and a human would otherwise have to copy by hand.
func (h *Handler) InstallAgentTelegramBot(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, uuidToString(agent.WorkspaceID),
		"agent not found", "owner", "admin")
	if !ok {
		return
	}

	var req installTelegramBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.BotToken)
	if token == "" {
		writeError(w, http.StatusBadRequest, "bot_token is required")
		return
	}

	box, err := telegramSealBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	me, err := telegram.NewBotClient(token).GetMe(r.Context())
	if err != nil {
		// The token never reaches the response or the log — only the fact that
		// Telegram rejected it.
		writeError(w, http.StatusBadRequest, "telegram rejected this bot token")
		return
	}

	sealed, err := box.Seal([]byte(token))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal bot token")
		return
	}

	params := db.UpsertTelegramInstallationParams{
		WorkspaceID:       agent.WorkspaceID,
		AgentID:           agent.ID,
		BotTokenEncrypted: sealed,
		BotUsername:       me.Username,
		BotUserID:         me.ID,
		InstallerUserID:   member.UserID,
	}
	if chat := strings.TrimSpace(req.ChatID); chat != "" {
		params.ChatID = pgtype.Text{String: chat, Valid: true}
	}

	row, err := h.Queries.UpsertTelegramInstallation(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to install telegram bot: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, telegramInstallationToResponse(row))
}

// GetAgentTelegramBot handles GET /api/agents/{id}/telegram.
func (h *Handler) GetAgentTelegramBot(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	row, err := h.Queries.GetTelegramInstallationByAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no telegram bot installed for this agent")
		return
	}
	writeJSON(w, http.StatusOK, telegramInstallationToResponse(row))
}

// DeleteAgentTelegramBot handles DELETE /api/agents/{id}/telegram.
func (h *Handler) DeleteAgentTelegramBot(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(agent.WorkspaceID),
		"agent not found", "owner", "admin"); !ok {
		return
	}
	if err := h.Queries.DeleteTelegramInstallation(r.Context(), db.DeleteTelegramInstallationParams{
		AgentID:     agent.ID,
		WorkspaceID: agent.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "failed to remove telegram bot")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// agentTelegramClient returns a bot client speaking as this agent, or nil when
// the agent has no installation. Callers treat nil as "this agent has no voice
// of its own" and fall back to the platform bot.
func (h *Handler) agentTelegramClient(ctx context.Context, agentID pgtype.UUID) (*telegram.BotClient, string) {
	row, err := h.Queries.GetTelegramInstallationByAgent(ctx, agentID)
	if err != nil || row.Status != "active" {
		return nil, ""
	}
	box, err := telegramSealBox()
	if err != nil {
		return nil, ""
	}
	token, err := box.Open(row.BotTokenEncrypted)
	if err != nil {
		return nil, ""
	}
	chat := ""
	if row.ChatID.Valid {
		chat = row.ChatID.String
	}
	return telegram.NewBotClient(string(token)), chat
}
