package handler

// The platform bot's private chat as an Agora Assistant conversation.
//
// A linked person can type "what is assigned to me?", "create a task to fix the
// invoice export" or "summarize MUL-12" and get the same Assistant the web app
// has — same tools, same permissions, same confirm-before-destroying rule. The
// bot adds no capability of its own: every turn is an ordinary assistant run in
// an ordinary assistant session, which is also why the conversation appears in
// the web app.
//
// Inbound: a plain message starts a run (startAssistantRun, the one path that
// may). Outbound: the run_finished subscriber delivers the run's answer. The
// two are joined by telegram_assistant_delivery, keyed on the RUN, so a turn
// typed in the web app into the same conversation is not echoed to Telegram.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// telegramAssistantChunk leaves headroom under Telegram's 4096-character limit
// for the tags MarkdownToHTML adds.
const telegramAssistantChunk = 3500

// Callback namespace for the confirm / reject buttons under a parked action.
const (
	telegramAssistantConfirmPrefix = "ao:c:"
	telegramAssistantRejectPrefix  = "ao:r:"
)

// TelegramAssistantEnabled reports whether a private message should reach the
// Assistant at all. When it is false the bot keeps its older behavior.
func (h *Handler) TelegramAssistantEnabled() bool {
	return h.telegramBot != nil && h.Assistant != nil && assistantEnabled()
}

// telegramAssistantRequest is one message for the Assistant from Telegram.
type telegramAssistantRequest struct {
	UserID string
	// ChatID is where the answer goes and which conversation the turn joins:
	// the person's own id in private, the group id in a group.
	ChatID string
	// ReplyTo is the group message being answered; 0 in private.
	ReplyTo int64
	Lang    string
	Text    string
	// Title names a conversation opened for this chat.
	Title string
	// Workspace pins the run to a workspace (a group's own). Zero leaves the
	// conversation's focus as it is.
	Workspace pgtype.UUID
	// Mode is set by the caller for a turn whose policy does not depend on
	// its text (a group turn is always RunModeRoom). Empty resolves it from
	// the text, as the web composer does.
	Mode assistant.RunMode
}

// telegramAssistantTurn sends one private message to the person's Assistant
// conversation. It reports false when the Assistant is not available to this
// person, so the caller can fall back to the create-task wizard.
func (h *Handler) telegramAssistantTurn(ctx context.Context, tgID, lang, text string) bool {
	if !h.TelegramAssistantEnabled() {
		return false
	}
	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if userID == "" {
		return false
	}
	h.telegramAssistantStart(ctx, telegramAssistantRequest{
		UserID: userID, ChatID: tgID, Lang: lang, Text: text, Title: "Telegram",
	})
	return true
}

// telegramAssistantStart starts the run and records where its answer goes.
// Every outcome the person should hear about is said in the chat.
func (h *Handler) telegramAssistantStart(ctx context.Context, req telegramAssistantRequest) {
	say := func(key string) {
		if req.ReplyTo > 0 {
			_ = h.telegramBot.SendMessageReply(ctx, req.ChatID, botT(req.Lang, key), req.ReplyTo)
			return
		}
		h.botSend(ctx, req.ChatID, botT(req.Lang, key))
	}
	if len(req.Text) > assistantMessageMaxLen {
		say("ai.toolong")
		return
	}
	session, err := h.telegramAssistantSession(ctx, req)
	if err != nil {
		slog.Warn("telegram assistant: session failed", "user_id", req.UserID, "error", err)
		say("ai.failed")
		return
	}
	mode, err := assistant.ResolveRunMode(req.Mode, req.Text)
	if err != nil {
		say("ai.failed")
		return
	}
	runContext := assistant.RunContext{WorkspaceID: uuidToPtr(session.FocusWorkspaceID), Mode: mode}
	if req.Workspace.Valid {
		runContext.WorkspaceID = uuidToPtr(req.Workspace)
	}
	accepted, err := h.startAssistantRun(ctx, session, req.UserID, req.Text, nil, "", runContext, []byte(`{}`))
	if errors.Is(err, assistant.ErrRunInProgress) {
		say("ai.busy")
		return
	}
	if err != nil {
		slog.Warn("telegram assistant: start run failed", "user_id", req.UserID, "error", err)
		say("ai.failed")
		return
	}
	runID, err := util.ParseUUID(accepted.Run.ID)
	if err != nil {
		return
	}
	if err := h.Queries.CreateTelegramAssistantDelivery(ctx, db.CreateTelegramAssistantDeliveryParams{
		RunID: runID, ChatID: req.ChatID, ReplyToMessageID: req.ReplyTo, Lang: req.Lang,
	}); err != nil {
		slog.Warn("telegram assistant: record delivery failed", "run_id", accepted.Run.ID, "error", err)
		return
	}
	_ = h.telegramBot.SendChatAction(ctx, req.ChatID, "typing")

	// A run can end before its delivery row exists (an unconfigured provider
	// fails at once). The claim makes this second attempt harmless.
	var finished bool
	if err := h.DB.QueryRow(ctx,
		`SELECT status IN ('completed','failed','cancelled','interrupted') FROM assistant_run WHERE id = $1`, runID,
	).Scan(&finished); err == nil && finished {
		h.DeliverTelegramAssistantRun(ctx, accepted.Run.ID, "")
	}
}

// telegramAssistantSession returns the person's current conversation in this
// chat, opening one when there is none.
func (h *Handler) telegramAssistantSession(ctx context.Context, req telegramAssistantRequest) (db.AssistantSession, error) {
	user := parseUUID(req.UserID)
	session, err := h.Queries.GetTelegramAssistantSession(ctx, db.GetTelegramAssistantSessionParams{UserID: user, ChatID: req.ChatID})
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.AssistantSession{}, err
	}
	// A group's conversation belongs to the group's workspace. In private,
	// with a single workspace there is nothing to choose, so focus it; with
	// several, the Assistant asks or infers from the request.
	focus := req.Workspace
	if !focus.Valid {
		if workspaces, err := h.Queries.ListWorkspaces(ctx, user); err == nil && len(workspaces) == 1 {
			focus = workspaces[0].ID
		}
	}
	session, err = h.Queries.CreateAssistantSession(ctx, db.CreateAssistantSessionParams{
		UserID: user, Title: req.Title, FocusWorkspaceID: focus,
	})
	if err != nil {
		return db.AssistantSession{}, err
	}
	if err := h.Queries.SetTelegramAssistantSession(ctx, db.SetTelegramAssistantSessionParams{
		UserID: user, ChatID: req.ChatID, SessionID: session.ID,
	}); err != nil {
		return db.AssistantSession{}, err
	}
	return session, nil
}

// telegramAssistantReset forgets the person's conversation in a chat. The
// session stays in the web app; the next message simply opens a new one.
func (h *Handler) telegramAssistantReset(ctx context.Context, tgID, chatID, lang string) {
	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if userID == "" {
		h.botSendOpen(ctx, tgID, botT(lang, "notlinked"), botT(lang, "open.btn"))
		return
	}
	if err := h.Queries.ClearTelegramAssistantSession(ctx, db.ClearTelegramAssistantSessionParams{
		UserID: parseUUID(userID), ChatID: chatID,
	}); err != nil {
		h.botSend(ctx, chatID, botT(lang, "ai.failed"))
		return
	}
	h.botSend(ctx, chatID, botT(lang, "ai.reset"))
}

// DeliverTelegramAssistantRun posts a finished run's answer to the chat that
// asked. It is a no-op for runs that did not start in Telegram and for runs
// already delivered. runError is the failure the run reported, if any.
func (h *Handler) DeliverTelegramAssistantRun(ctx context.Context, runID, runError string) {
	if h.telegramBot == nil {
		return
	}
	runUUID, err := util.ParseUUID(runID)
	if err != nil {
		return
	}
	delivery, err := h.Queries.ClaimTelegramAssistantDelivery(ctx, runUUID)
	if err != nil {
		return
	}
	lang := delivery.Lang
	reply, _ := h.Queries.GetAssistantRunReply(ctx, runUUID)
	reply = strings.TrimSpace(reply)
	if reply == "" {
		if runError == "" {
			_ = h.DB.QueryRow(ctx, `SELECT COALESCE(error, '') FROM assistant_run WHERE id = $1`, runUUID).Scan(&runError)
		}
		reply = botT(lang, "ai.failed")
		if runError != "" {
			reply += "\n" + runError
		}
		_ = h.telegramBot.SendMessageReply(ctx, delivery.ChatID, reply, delivery.ReplyToMessageID)
		return
	}
	for _, part := range splitTelegramText(reply, telegramAssistantChunk) {
		if err := h.telegramBot.SendMarkdownReply(ctx, delivery.ChatID, part, delivery.ReplyToMessageID); err != nil {
			// Unbalanced markup is rejected by Telegram; the plain text still
			// carries the answer.
			slog.Warn("telegram assistant: formatted send failed", "run_id", runID, "error", err)
			_ = h.telegramBot.SendMessageReply(ctx, delivery.ChatID, part, delivery.ReplyToMessageID)
		}
	}

	// Anything the run parked for approval gets its own card. The Assistant's
	// text already said what it wants to do; the buttons are how to answer.
	operations, err := h.Queries.ListPendingAssistantOperationsByRun(ctx, runUUID)
	if err != nil {
		return
	}
	for _, op := range operations {
		id := uuidToString(op.ID)
		h.botSendButtons(ctx, delivery.ChatID, "⚠️ "+op.Summary, [][]telegram.Button{{
			{Text: botT(lang, "ai.confirm"), CallbackData: telegramAssistantConfirmPrefix + id},
			{Text: botT(lang, "ai.reject"), CallbackData: telegramAssistantRejectPrefix + id},
		}})
	}
}

// handleTelegramAssistantCallback answers a confirm / reject tap. It goes
// through the same HTTP handlers the web card uses, as the linked member, so
// ownership, expiry, review mode and the changed-target check all still apply.
func (h *Handler) handleTelegramAssistantCallback(ctx context.Context, update telegramUpdate) bool {
	cb := update.CallbackQuery
	if cb == nil || cb.From == nil {
		return false
	}
	confirm := strings.HasPrefix(cb.Data, telegramAssistantConfirmPrefix)
	if !confirm && !strings.HasPrefix(cb.Data, telegramAssistantRejectPrefix) {
		return false
	}
	operationID := cb.Data[len(telegramAssistantConfirmPrefix):]
	tgID, lang := strconv.FormatInt(cb.From.ID, 10), botLang(cb.From.LanguageCode)
	if _, err := util.ParseUUID(operationID); err != nil {
		return true
	}
	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, tgID)
	if userID == "" {
		h.botSendOpen(ctx, tgID, botT(lang, "notlinked"), botT(lang, "open.btn"))
		return true
	}

	action, target := h.RejectAssistantOperation, "reject"
	if confirm {
		action, target = h.ConfirmAssistantOperation, "confirm"
	}
	status, body := h.invokeAsMember(ctx, userID, "", action, http.MethodPost,
		"/api/assistant/operations/"+operationID+"/"+target, map[string]string{"id": operationID}, nil)

	// In a group anyone can tap a card. A tap by someone the action does not
	// belong to must leave the card standing for its owner.
	if status == http.StatusNotFound {
		return true
	}

	var text string
	switch {
	case status >= 200 && status < 300 && confirm:
		text = botT(lang, "ai.confirmed")
	case status >= 200 && status < 300:
		text = botT(lang, "ai.rejected")
	default:
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		text = botT(lang, "ai.failed")
		if failure.Error != "" {
			text += "\n" + failure.Error
		}
		slog.Info("telegram assistant: operation not applied", "operation_id", operationID, "action", target, "status", status)
	}
	// Replace the keyboard so a settled card cannot be answered twice.
	if cb.Message != nil && cb.Message.Chat != nil {
		h.replaceDecisionKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text)
		return true
	}
	h.botSend(ctx, tgID, text)
	return true
}

// splitTelegramText breaks a long answer into messages no longer than limit
// runes, preferring a paragraph break, then a line break, then a space.
func splitTelegramText(text string, limit int) []string {
	var parts []string
	for utf8.RuneCountInString(text) > limit {
		// Byte offset of the limit-th rune.
		cut, runes := 0, 0
		for i := range text {
			if runes == limit {
				cut = i
				break
			}
			runes++
		}
		head := text[:cut]
		at := strings.LastIndex(head, "\n\n")
		if at < cut/2 {
			at = strings.LastIndex(head, "\n")
		}
		if at < cut/2 {
			at = strings.LastIndex(head, " ")
		}
		if at < cut/2 {
			at = cut
		}
		if part := strings.TrimSpace(text[:at]); part != "" {
			parts = append(parts, part)
		}
		text = strings.TrimSpace(text[at:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}
