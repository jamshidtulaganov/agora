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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// What a RUNNING agent may do with its own Telegram bot.
//
// Until now every outbound message was platform-initiated: the agent wrote a
// comment or finished a task, and an event subscriber later decided to post
// something. The agent could not choose a recipient, a chat, or a moment — so
// "tell the group the deploy is starting" or "the weekly report will be late"
// had no expression at all.
//
// These endpoints give it that, and nothing more. Three properties hold:
//
//   - The token never reaches the agent. It is sealed server-side; the agent
//     authenticates as itself and names a chat, and the server sends.
//   - The agent can only reach chats ALREADY bound to it. allowed_chat_ids is
//     the same list that governs who may instruct it, so granting an agent a
//     room and letting it speak there are one decision, not two.
//   - There is no edit and no delete. A message an agent has posted is part of
//     the room's record; letting it rewrite or erase that would make the
//     transcript unreliable in exactly the situation where someone is trying to
//     reconstruct what an agent did.

// resolveActingAgentInstallation returns the installation belonging to the
// agent making the request.
//
// Human callers are refused rather than served: a human has the Settings panel,
// and accepting them here would create a second, unaudited path for posting to
// a team group under an agent's name.
func (h *Handler) resolveActingAgentInstallation(w http.ResponseWriter, r *http.Request) (db.TelegramInstallation, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return db.TelegramInstallation{}, false
	}
	workspaceID := r.Header.Get("X-Workspace-ID")
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType != "agent" || strings.TrimSpace(actorID) == "" {
		writeError(w, http.StatusForbidden, "only a running agent can use its own bot")
		return db.TelegramInstallation{}, false
	}
	agentUUID, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusForbidden, "could not resolve the acting agent")
		return db.TelegramInstallation{}, false
	}
	row, err := h.Queries.GetTelegramInstallationByAgent(r.Context(), agentUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "this agent has no telegram bot installed")
		return db.TelegramInstallation{}, false
	}
	if row.Status != "active" {
		writeError(w, http.StatusForbidden, "this agent's telegram bot is revoked")
		return db.TelegramInstallation{}, false
	}
	return row, true
}

// telegramChatSummary is one chat the agent may post to.
type telegramChatSummary struct {
	ChatID string `json:"chat_id"`
	// Default marks the chat unsolicited output goes to, so an agent posting
	// without naming one can tell where it landed.
	Default bool `json:"default"`
}

// ListAgentTelegramChats handles GET /api/agents/me/telegram/chats.
//
// An agent cannot guess a chat id, and hardcoding one into a prompt breaks the
// moment a group is rebound. This is how it discovers where it may speak.
func (h *Handler) ListAgentTelegramChats(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveActingAgentInstallation(w, r)
	if !ok {
		return
	}
	defaultChat := ""
	if row.ChatID.Valid {
		defaultChat = strings.TrimSpace(row.ChatID.String)
	}
	out := make([]telegramChatSummary, 0, len(row.AllowedChatIds))
	for _, id := range row.AllowedChatIds {
		s := strconv.FormatInt(id, 10)
		out = append(out, telegramChatSummary{ChatID: s, Default: s == defaultChat})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bot_username": row.BotUsername,
		"chats":        out,
		"count":        len(out),
	})
}

type sendAgentTelegramRequest struct {
	// ChatID may be omitted, in which case the installation's default chat is
	// used. Naming a chat the agent is not bound to is an error, never a
	// silent redirect to the default.
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// telegramAgentMessageLimit is the Bot API's cap on a text message. Trimmed
// rather than rejected: an agent that gets a 400 for a long message usually
// retries with the same message.
const telegramAgentMessageLimit = 4096

// SendAgentTelegramMessage handles POST /api/agents/me/telegram/send.
func (h *Handler) SendAgentTelegramMessage(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveActingAgentInstallation(w, r)
	if !ok {
		return
	}
	var req sendAgentTelegramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	chatID, ok := resolveAgentTargetChat(w, row, req.ChatID)
	if !ok {
		return
	}

	bot, _ := h.agentTelegramClient(r.Context(), row.AgentID)
	if bot == nil {
		writeError(w, http.StatusServiceUnavailable, "this agent's bot is not available")
		return
	}
	// Markdown: an agent writing `**deploy failed**` should not have the
	// asterisks reach the room.
	if err := bot.SendMarkdown(r.Context(), chatID, truncateRunes(text, telegramAgentMessageLimit)); err != nil {
		slog.Warn("agent telegram send failed", "bot", row.BotUsername, "chat", chatID, "error", err)
		writeError(w, http.StatusBadGateway, "telegram rejected the message")
		return
	}
	// Logged at INFO: an agent speaking to a room of people is worth being able
	// to reconstruct afterwards, and the room itself is the only other record.
	slog.Info("agent posted to telegram", "bot", row.BotUsername, "chat", chatID,
		"agent_id", uuidToString(row.AgentID), "chars", len([]rune(text)))
	writeJSON(w, http.StatusOK, map[string]any{"chat_id": chatID, "sent": true})
}

// resolveAgentTargetChat picks the destination and proves the agent is allowed
// to reach it. Writes the error response itself; ok=false means "already
// answered, return".
func resolveAgentTargetChat(w http.ResponseWriter, row db.TelegramInstallation, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if !row.ChatID.Valid || strings.TrimSpace(row.ChatID.String) == "" {
			writeError(w, http.StatusBadRequest,
				"no chat_id given and this agent has no default chat")
			return "", false
		}
		requested = strings.TrimSpace(row.ChatID.String)
	}
	parsed, err := strconv.ParseInt(requested, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "chat_id must be a numeric telegram chat id")
		return "", false
	}
	// The same list that governs who may INSTRUCT the agent governs where it
	// may speak. An unbound chat is refused outright rather than falling back
	// to the default — a message delivered to the wrong room is worse than one
	// not delivered.
	if !telegramChatAllowed(row, parsed) {
		writeError(w, http.StatusForbidden, "this agent is not bound to that chat")
		return "", false
	}
	return requested, true
}

// ---- Asking the room a question -------------------------------------------
//
// The human gate currently lives in the web app: an agent that needs a decision
// stops and waits for someone to notice in Agora. The people who can decide are
// usually already in the group, so the question belongs there.
//
// Split into ask + poll rather than one blocking call. A decision can take
// minutes; holding an HTTP request open that long means a proxy timeout decides
// the outcome, and a server redeploy loses it entirely. The question is
// persisted, so the answer survives both.

// telegramQuestionMaxOptions caps the choices. More than a handful wraps into
// an unreadable keyboard on a phone, and a gate nobody can read is not a gate.
const telegramQuestionMaxOptions = 6

// telegramCallbackPrefix namespaces the callback payload so a stray button from
// another flow can never be read as an answer.
const telegramCallbackPrefix = "q:"

type askAgentTelegramRequest struct {
	ChatID  string   `json:"chat_id"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
	// TimeoutSeconds bounds how long the question stands. Past it the row stops
	// accepting answers, so a stale button tapped tomorrow cannot resurrect a
	// decision the agent already gave up on.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// AskAgentTelegramQuestion handles POST /api/agents/me/telegram/ask.
func (h *Handler) AskAgentTelegramQuestion(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveActingAgentInstallation(w, r)
	if !ok {
		return
	}
	var req askAgentTelegramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	options := make([]string, 0, len(req.Options))
	for _, o := range req.Options {
		if t := strings.TrimSpace(o); t != "" {
			options = append(options, t)
		}
	}
	if len(options) < 2 {
		writeError(w, http.StatusBadRequest, "at least two options are required")
		return
	}
	if len(options) > telegramQuestionMaxOptions {
		writeError(w, http.StatusBadRequest, "too many options")
		return
	}
	chatID, ok := resolveAgentTargetChat(w, row, req.ChatID)
	if !ok {
		return
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = telegramQuestionDefaultTimeout
	}
	if timeout > telegramQuestionMaxTimeout {
		timeout = telegramQuestionMaxTimeout
	}

	question, err := h.Queries.CreateTelegramQuestion(r.Context(), db.CreateTelegramQuestionParams{
		WorkspaceID: row.WorkspaceID,
		AgentID:     row.AgentID,
		ChatID:      chatID,
		Prompt:      prompt,
		Options:     options,
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(timeout), Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the question")
		return
	}

	bot, _ := h.agentTelegramClient(r.Context(), row.AgentID)
	if bot == nil {
		writeError(w, http.StatusServiceUnavailable, "this agent's bot is not available")
		return
	}
	// One button per row: option labels are sentences here ("Deploy to
	// staging"), and side-by-side they truncate on a phone.
	rows := make([][]telegram.Button, 0, len(options))
	for i, opt := range options {
		rows = append(rows, []telegram.Button{{
			Text: opt,
			// The INDEX travels, not the label. A callback payload is
			// client-supplied, so the answer is resolved from the stored
			// options rather than trusted off the wire.
			CallbackData: telegramCallbackPrefix + uuidToString(question.ID) + ":" + strconv.Itoa(i),
		}})
	}
	if err := bot.SendButtons(r.Context(), chatID, html.EscapeString(prompt), rows); err != nil {
		slog.Warn("agent telegram ask failed", "bot", row.BotUsername, "chat", chatID, "error", err)
		writeError(w, http.StatusBadGateway, "telegram rejected the question")
		return
	}
	slog.Info("agent asked telegram", "bot", row.BotUsername, "chat", chatID,
		"question_id", uuidToString(question.ID), "options", len(options))

	writeJSON(w, http.StatusOK, map[string]any{
		"question_id": uuidToString(question.ID),
		"chat_id":     chatID,
		"expires_at":  question.ExpiresAt.Time.UTC().Format(time.RFC3339),
	})
}

// Question lifetimes. The default is short enough that a forgotten question
// does not pin an agent for an hour, and the cap stops one from outliving the
// task that asked it.
const (
	telegramQuestionDefaultTimeout = 10 * time.Minute
	telegramQuestionMaxTimeout     = 60 * time.Minute
)

// GetAgentTelegramQuestion handles GET /api/agents/me/telegram/questions/{id}.
//
// Polled by the CLI. Returns the answer once someone taps, or the pending /
// expired state — expired is reported distinctly from unanswered so an agent
// can tell "nobody has decided yet" from "nobody is going to".
func (h *Handler) GetAgentTelegramQuestion(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveActingAgentInstallation(w, r)
	if !ok {
		return
	}
	questionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "question id")
	if !ok {
		return
	}
	question, err := h.Queries.GetTelegramQuestion(r.Context(), questionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "question not found")
		return
	}
	// Scoped to the asking agent: a task token must not be able to read a
	// decision made for a different agent's gate.
	if uuidToString(question.AgentID) != uuidToString(row.AgentID) {
		writeError(w, http.StatusNotFound, "question not found")
		return
	}

	status := "pending"
	answer := ""
	if question.Answer.Valid {
		status = "answered"
		answer = question.Answer.String
	} else if question.ExpiresAt.Valid && question.ExpiresAt.Time.Before(time.Now()) {
		status = "expired"
	}
	resp := map[string]any{
		"question_id": uuidToString(question.ID),
		"status":      status,
		"answer":      answer,
	}
	if question.AnsweredBy.Valid {
		resp["answered_by"] = strconv.FormatInt(question.AnsweredBy.Int64, 10)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAgentCallback resolves an inline-button tap into an answer.
//
// Runs on the agent's own poller, so it only ever sees taps on that agent's
// keyboards. Everything here fails closed and silently: a callback is
// client-supplied data, and a bot that explains why a button was rejected tells
// a stranger how the gate works.
func (h *Handler) handleAgentCallback(ctx context.Context, row db.TelegramInstallation, update telegramUpdate) {
	cb := update.CallbackQuery
	if cb == nil || cb.From == nil || cb.Message == nil || cb.Message.Chat == nil {
		return
	}
	bot, _ := h.agentTelegramClient(ctx, row.AgentID)
	// Acknowledge first so the client's spinner stops even if the answer is
	// rejected below; leaving it spinning reads as a hung bot.
	if bot != nil {
		_ = bot.AnswerCallback(ctx, cb.ID)
	}

	questionID, index, ok := parseQuestionCallback(cb.Data)
	if !ok {
		return
	}
	// Re-read the installation: access may have been revoked since the
	// question was asked, and a keyboard sitting in a group outlives that.
	if fresh, err := h.Queries.GetTelegramInstallationByAgent(ctx, row.AgentID); err == nil {
		row = fresh
	}
	if !telegramSenderAllowed(row, cb.Message.Chat.ID, cb.From.ID) {
		slog.Info("telegram question: tap from a sender who may not decide",
			"bot", row.BotUsername, "from", cb.From.ID, "chat", cb.Message.Chat.ID)
		return
	}

	question, err := h.Queries.GetTelegramQuestion(ctx, questionID)
	if err != nil || uuidToString(question.AgentID) != uuidToString(row.AgentID) {
		return
	}
	// The answer must come from the room the question was asked in. Being an
	// allowed chat is not enough: an agent bound to several groups would
	// otherwise let one of them settle another's decision, and the recorded
	// answer would name a chat that never saw the question.
	if !telegramQuestionMatchesChat(question, cb.Message.Chat.ID) {
		slog.Info("telegram question: tap from a different chat than it was asked in",
			"question_id", uuidToString(questionID), "asked_in", question.ChatID,
			"tapped_in", cb.Message.Chat.ID)
		return
	}
	// The label is read from what was STORED, never from the callback payload:
	// the payload is attacker-controllable and would otherwise let someone
	// answer with text that was never offered.
	if index < 0 || index >= len(question.Options) {
		return
	}
	choice := question.Options[index]

	answered, err := h.Queries.AnswerTelegramQuestion(ctx, db.AnswerTelegramQuestionParams{
		ID:         questionID,
		Answer:     pgtype.Text{String: choice, Valid: true},
		AnsweredBy: pgtype.Int8{Int64: cb.From.ID, Valid: true},
	})
	if err != nil {
		// Already answered, or expired. Both are ordinary races — someone else
		// tapped first, or the agent gave up — and neither is worth announcing.
		slog.Info("telegram question: answer not accepted",
			"question_id", uuidToString(questionID), "from", cb.From.ID)
		return
	}
	slog.Info("telegram question answered", "bot", row.BotUsername,
		"question_id", uuidToString(questionID), "answer", choice, "by", cb.From.ID)

	// Replace the keyboard with the outcome so the room can see what was
	// decided and nobody taps a question that is already settled.
	if bot != nil {
		_ = bot.EditButtons(ctx, strconv.FormatInt(cb.Message.Chat.ID, 10), cb.Message.MessageID,
			html.EscapeString(answered.Prompt)+"\n\n<b>"+html.EscapeString(choice)+"</b>", nil)
	}
}

func telegramQuestionMatchesChat(question db.TelegramQuestion, chatID int64) bool {
	return question.ChatID == strconv.FormatInt(chatID, 10)
}

// parseQuestionCallback reads "q:<uuid>:<index>". Anything else is not ours.
func parseQuestionCallback(data string) (pgtype.UUID, int, bool) {
	if !strings.HasPrefix(data, telegramCallbackPrefix) {
		return pgtype.UUID{}, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(data, telegramCallbackPrefix), ":")
	if len(parts) != 2 {
		return pgtype.UUID{}, 0, false
	}
	id, err := util.ParseUUID(parts[0])
	if err != nil {
		return pgtype.UUID{}, 0, false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return pgtype.UUID{}, 0, false
	}
	return id, index, true
}
