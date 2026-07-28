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
	// AccessPolicy governs who may INSTRUCT this agent through the bot.
	// Reporting is unaffected — a 'closed' bot still speaks, it just takes no
	// orders.
	AccessPolicy   string   `json:"access_policy"`
	AllowedUserIDs []string `json:"allowed_user_ids"`
	// AllowedChatIDs are the groups that may instruct this agent. One agent can
	// serve several rooms; ChatID above is only where reports are posted.
	AllowedChatIDs []string `json:"allowed_chat_ids"`
	// AdminUserIDs may run /allow and /deny from inside Telegram.
	AdminUserIDs []string `json:"admin_user_ids"`
}

// idsToStrings renders 64-bit Telegram ids as strings. JSON numbers are IEEE
// doubles in a browser, which silently rounds ids past 2^53 — a chat id is
// already in that range.
func idsToStrings(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

// parseTelegramIDs converts request ids, rejecting anything non-numeric rather
// than skipping it: a typo that silently drops an entry looks like a grant that
// worked.
func parseTelegramIDs(raw []string) ([]int64, error) {
	out := make([]int64, 0, len(raw))
	for _, s := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
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
	resp.AccessPolicy = row.AccessPolicy
	resp.AllowedUserIDs = idsToStrings(row.AllowedTelegramUserIds)
	resp.AllowedChatIDs = idsToStrings(row.AllowedChatIds)
	resp.AdminUserIDs = idsToStrings(row.AdminTelegramUserIds)
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
	// Start listening now rather than at the next restart. Re-installing
	// cancels the previous loop first — Telegram allows one getUpdates
	// consumer per bot, and two make both fail with 409.
	h.startAgentTelegramPoller(row)
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
	// Stop consuming updates immediately; an uninstalled bot that keeps
	// polling would still dispatch work for an agent nobody can see.
	h.stopAgentTelegramPoller(agent.ID)
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

type setTelegramAccessRequest struct {
	// Policy: "closed" (default), "allowlist", or "open".
	Policy string `json:"policy"`
	// AllowedUserIDs are numeric Telegram user ids, as strings so a JS client
	// cannot lose precision on a 64-bit id.
	AllowedUserIDs []string `json:"allowed_user_ids"`
	// AllowedChatIDs are the groups allowed to instruct the agent. Omitted
	// (nil) leaves the current set alone, so a caller editing only the user
	// list cannot accidentally unbind every group.
	AllowedChatIDs []string `json:"allowed_chat_ids"`
	// AdminUserIDs may run /allow and /deny in Telegram. Same nil semantics.
	AdminUserIDs []string `json:"admin_user_ids"`
}

// SetAgentTelegramAccess handles PUT /api/agents/{id}/telegram/access.
//
// Owner/admin only, and human-only at the route: an agent must not be able to
// widen the door it is reached through.
func (h *Handler) SetAgentTelegramAccess(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(agent.WorkspaceID),
		"agent not found", "owner", "admin"); !ok {
		return
	}

	var req setTelegramAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	policy := strings.TrimSpace(req.Policy)
	switch policy {
	case "closed", "allowlist", "open":
	default:
		writeError(w, http.StatusBadRequest, "policy must be closed, allowlist or open")
		return
	}

	ids, err := parseTelegramIDs(req.AllowedUserIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "allowed_user_ids must be numeric Telegram user ids")
		return
	}
	// An allowlist with nobody on it is almost certainly a mistake — it reads
	// as "allow these people" while behaving exactly like closed.
	if policy == "allowlist" && len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "allowlist policy needs at least one allowed_user_id (use closed to block everyone)")
		return
	}

	row, err := h.Queries.SetTelegramInstallationAccess(r.Context(), db.SetTelegramInstallationAccessParams{
		AgentID:                agent.ID,
		AccessPolicy:           policy,
		AllowedTelegramUserIds: ids,
		WorkspaceID:            agent.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "no telegram bot installed for this agent")
		return
	}

	// Chats and admins are updated only when supplied. A PUT that carried the
	// whole shape would make every partial edit a full overwrite — the failure
	// mode being that saving a user list silently revokes every bound group.
	if req.AllowedChatIDs != nil {
		chats, chatErr := parseTelegramIDs(req.AllowedChatIDs)
		if chatErr != nil {
			writeError(w, http.StatusBadRequest, "allowed_chat_ids must be numeric Telegram chat ids")
			return
		}
		if updated, uErr := h.Queries.SetTelegramInstallationChats(r.Context(), db.SetTelegramInstallationChatsParams{
			AgentID: agent.ID, AllowedChatIds: chats,
		}); uErr == nil {
			row = updated
		}
	}
	if req.AdminUserIDs != nil {
		admins, adminErr := parseTelegramIDs(req.AdminUserIDs)
		if adminErr != nil {
			writeError(w, http.StatusBadRequest, "admin_user_ids must be numeric Telegram user ids")
			return
		}
		if updated, uErr := h.Queries.SetTelegramInstallationAdmins(r.Context(), db.SetTelegramInstallationAdminsParams{
			AgentID: agent.ID, AdminTelegramUserIds: admins, WorkspaceID: agent.WorkspaceID,
		}); uErr == nil {
			row = updated
		}
	}
	writeJSON(w, http.StatusOK, telegramInstallationToResponse(row))
}
