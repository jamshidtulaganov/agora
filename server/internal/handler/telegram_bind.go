package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// QR binding: connect an agent's bot to a group by scanning.
//
// Adding a bot to a group cannot authorize that group by itself — anyone in
// Telegram can invite a bot anywhere. So the operator mints a short-lived
// token, Agora encodes it in a t.me deep link, and the group is bound only when
// the bot is added carrying that token. Presenting it proves the binding was
// intended by someone who already had admin rights in Agora.
//
// Only the HASH is stored, mirroring lark_binding_token: a leaked database
// cannot be replayed into a binding. The raw value exists once, in the QR.

const (
	// telegramBindTokenTTL is deliberately short. The token is scanned within
	// seconds of being displayed; a long window is just a wider replay gap.
	telegramBindTokenTTL = 10 * time.Minute
	// telegramBindPayloadPrefix namespaces the deep-link payload so a /start
	// carrying anything else (the login flow, a stray click) is not mistaken
	// for a binding attempt.
	telegramBindPayloadPrefix = "bind_"
)

// hashBindToken is the at-rest form. SHA-256 is right here: the token is
// high-entropy random, so there is nothing to brute-force and no need for a
// slow KDF.
func hashBindToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// newBindToken mints an unguessable, URL-safe token. Telegram deep-link
// payloads allow only [A-Za-z0-9_-], which is exactly base64url's alphabet.
func newBindToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// TelegramBindLinkResponse carries what the UI needs to render the QR.
type TelegramBindLinkResponse struct {
	// GroupURL opens Telegram's "add to group" picker with the token attached.
	GroupURL string `json:"group_url"`
	// BotUsername lets the UI name the bot being connected.
	BotUsername string `json:"bot_username"`
	ExpiresAt   string `json:"expires_at"`
}

// CreateAgentTelegramBindLink handles POST /api/agents/{id}/telegram/bind-link.
//
// Owner/admin and human-only: minting a binding token is the act that lets a
// group start instructing an agent, so an agent must not be able to mint one
// for itself.
func (h *Handler) CreateAgentTelegramBindLink(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, uuidToString(agent.WorkspaceID),
		"agent not found", "owner", "admin")
	if !ok {
		return
	}
	install, err := h.Queries.GetTelegramInstallationByAgent(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "install a bot for this agent first")
		return
	}

	raw, err := newBindToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint a binding token")
		return
	}
	expires := time.Now().Add(telegramBindTokenTTL)
	if _, err := h.Queries.CreateTelegramBindingToken(r.Context(), db.CreateTelegramBindingTokenParams{
		TokenHash:   hashBindToken(raw),
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
		CreatedBy:   member.UserID,
		ExpiresAt:   pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store the binding token")
		return
	}

	// `startgroup` opens Telegram's group picker and adds the bot, delivering
	// the payload as `/start <payload>` in that chat — the moment we can bind
	// the chat id to this agent with proof the operator intended it.
	writeJSON(w, http.StatusOK, TelegramBindLinkResponse{
		GroupURL:    "https://t.me/" + install.BotUsername + "?startgroup=" + telegramBindPayloadPrefix + raw,
		BotUsername: install.BotUsername,
		ExpiresAt:   expires.UTC().Format(time.RFC3339),
	})
}

// tryBindTelegramGroup redeems a `/start bind_<token>` seen in a group and
// binds that chat to the agent. Reports whether the message was a binding
// attempt, so the caller can stop treating it as a question for the agent.
//
// Runs BEFORE the access check on purpose: binding is how a group becomes
// trusted in the first place, so requiring it to already be trusted would make
// the flow impossible. The token is the authorization here — it was minted by
// an owner/admin minutes earlier and is single-use.
func (h *Handler) tryBindTelegramGroup(ctx context.Context, row db.TelegramInstallation, chatIDNum int64, rawText string) bool {
	text := strings.TrimSpace(rawText)
	if !strings.HasPrefix(text, "/start") {
		return false
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return false
	}
	payload := strings.TrimPrefix(fields[1], telegramBindPayloadPrefix)
	if payload == fields[1] {
		return false // not ours — the login flow uses its own prefix
	}

	token, err := h.Queries.ConsumeTelegramBindingToken(ctx, hashBindToken(payload))
	if err != nil {
		// Expired, already used, or forged. Say nothing in the group: a bot
		// that announces "invalid token" tells a stranger they found the door.
		slog.Info("telegram bind: token rejected", "bot", row.BotUsername, "chat", chatIDNum)
		return true
	}
	if token.AgentID != row.AgentID {
		// A token minted for a different agent must not bind this one.
		slog.Warn("telegram bind: token belongs to another agent",
			"bot", row.BotUsername, "token_agent", uuidToString(token.AgentID))
		return true
	}

	chatID := strconv.FormatInt(chatIDNum, 10)
	if _, err := h.Queries.SetTelegramInstallationChat(ctx, db.SetTelegramInstallationChatParams{
		AgentID: row.AgentID,
		ChatID:  pgtype.Text{String: chatID, Valid: true},
	}); err != nil {
		slog.Warn("telegram bind: failed to store chat", "error", err)
		return true
	}
	slog.Info("telegram bind: group bound", "bot", row.BotUsername, "chat", chatID)

	// Confirm in the group so the operator sees the scan worked. Access is
	// still whatever the policy says — binding a chat is not granting it.
	if bot, _ := h.agentTelegramClient(ctx, row.AgentID); bot != nil {
		_ = bot.SendMessage(ctx, chatID,
			"Ulandi. Bu guruh endi shu agentga bog'landi.\n"+
				"Kim yozishi mumkinligi Agora sozlamalarida belgilanadi.")
	}
	return true
}
