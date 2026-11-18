package handler

// The platform bot in a group: a teammate you can talk to, with a memory of
// the conversation it is allowed to keep.
//
// Nothing here happens until a workspace owner or admin runs /track in the
// group. That one command does three things at once, deliberately: it binds
// the group to a workspace (so the bot knows whose tasks "our tasks" are), it
// lets the bot answer when addressed, and it starts recording the group's text
// so the Assistant can summarize it. /untrack undoes all three and deletes
// what was recorded.
//
// The bot never acts as itself. A message addressed to it runs the Assistant
// as the Agora member who wrote it, and the answer is posted as a reply. A
// group turn runs in room mode (assistant.RunModeRoom): read-only, this
// group's workspace only, nothing personal. The room is full of text the asker
// did not write and of readers who are not the asker, so a turn there must not
// be able to change anything or to carry in data from elsewhere. Creating and
// changing things is done in a private chat with the bot. Someone who has not linked Telegram to Agora,
// or is not in the group's workspace, gets a refusal instead of an answer.
//
// Telegram delivers ordinary group messages to a bot only when its privacy
// mode is off (BotFather → Bot Settings → Group Privacy) or it is a group
// admin. With privacy mode on the bot still answers mentions, replies and
// commands, but has nothing to summarize.

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const (
	// telegramHistoryRetention is how long a recorded message is kept.
	telegramHistoryRetention = 30 * 24 * time.Hour
	// telegramRecordedTextMax caps one stored message.
	telegramRecordedTextMax = 4000
	// telegramGroupRoleMax caps the /role text that is added to every turn.
	telegramGroupRoleMax = 600
)

// processTelegramGroupMessage handles one message the platform bot saw in a
// group: a command, something addressed to the bot, or conversation to record.
func (h *Handler) processTelegramGroupMessage(ctx context.Context, update telegramUpdate) {
	msg := update.Message
	if msg == nil || msg.From == nil || msg.Chat == nil || msg.From.IsBot {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	g := telegramGroupMessage{
		ChatID:    msg.Chat.ID,
		Chat:      strconv.FormatInt(msg.Chat.ID, 10),
		Title:     strings.TrimSpace(msg.Chat.Title),
		MessageID: msg.MessageID,
		FromID:    msg.From.ID,
		Lang:      botLang(msg.From.LanguageCode),
	}

	if cmd, rest := splitCommand(text); cmd != "" {
		switch cmd {
		case "/track":
			h.telegramTrackCommand(ctx, g, rest)
		case "/untrack":
			h.telegramUntrackCommand(ctx, g)
		case "/role":
			h.telegramRoleCommand(ctx, g, rest)
		case "/summary":
			h.telegramSummaryCommand(ctx, g, rest)
		case "/reset":
			h.telegramAssistantReset(ctx, strconv.FormatInt(g.FromID, 10), g.Chat, g.Lang)
		}
		return
	}

	repliesToBot := false
	if reply := msg.ReplyToMessage; reply != nil && reply.From != nil && reply.From.IsBot {
		repliesToBot = strings.EqualFold(strings.TrimPrefix(reply.From.Username, "@"), strings.TrimPrefix(telegramBotUsername(), "@"))
	}
	addressed := telegramMessageAddressesAgent(text, repliesToBot, telegramBotUsername())

	chat, err := h.Queries.GetTelegramTrackedChat(ctx, g.ChatID)
	if err != nil {
		// An untracked group is not recorded and not answered. Say why only
		// when someone actually spoke to the bot.
		if addressed {
			h.telegramGroupSay(ctx, g, "grp.nottracked")
		}
		return
	}

	sent := time.Now()
	if msg.Date > 0 {
		sent = time.Unix(msg.Date, 0)
	}
	h.telegramSeeParticipant(ctx, g, sent)
	name := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	if name == "" {
		name = msg.From.Username
	}
	if err := h.Queries.RecordTelegramChatMessage(ctx, db.RecordTelegramChatMessageParams{
		ChatID: g.ChatID, MessageID: g.MessageID, SenderID: g.FromID, SenderName: name,
		Text:   truncateRunes(text, telegramRecordedTextMax),
		SentAt: pgtype.Timestamptz{Time: sent, Valid: true},
	}); err != nil {
		slog.Warn("telegram group: record message failed", "chat_id", g.ChatID, "error", err)
	}
	// Retention without a sweeper: every hundredth message trims the chat.
	if g.MessageID%100 == 0 {
		_ = h.Queries.PruneTelegramChatMessages(ctx, db.PruneTelegramChatMessagesParams{
			ChatID: g.ChatID, SentAt: pgtype.Timestamptz{Time: time.Now().Add(-telegramHistoryRetention), Valid: true},
		})
	}

	if !addressed {
		return
	}
	question := strings.TrimSpace(stripTelegramBotMention(text, telegramBotUsername()))
	if question == "" {
		return
	}
	userID, ok := h.telegramGroupMember(ctx, g, chat.WorkspaceID)
	if !ok {
		return
	}
	if !h.TelegramAssistantEnabled() {
		h.telegramGroupSay(ctx, g, "grp.noassistant")
		return
	}
	h.telegramAssistantStart(ctx, telegramAssistantRequest{
		UserID: userID, ChatID: g.Chat, ReplyTo: g.MessageID, Lang: g.Lang,
		Text: telegramGroupPreamble(chat) + question, Title: telegramGroupTitle(chat.Title), Workspace: chat.WorkspaceID,
		Mode: assistant.RunModeRoom,
	})
}

// telegramSeeParticipant notes the first time the bot saw someone in a tracked
// group. It bounds how far back that person may read (assistant_telegram.go).
func (h *Handler) telegramSeeParticipant(ctx context.Context, g telegramGroupMessage, at time.Time) {
	if err := h.Queries.SeeTelegramChatParticipant(ctx, db.SeeTelegramChatParticipantParams{
		ChatID: g.ChatID, TelegramUserID: g.FromID, FirstSeenAt: pgtype.Timestamptz{Time: at, Valid: true},
	}); err != nil {
		slog.Warn("telegram group: note participant failed", "chat_id", g.ChatID, "error", err)
	}
}

type telegramGroupMessage struct {
	ChatID    int64
	Chat      string
	Title     string
	MessageID int64
	FromID    int64
	Lang      string
}

func telegramGroupTitle(title string) string {
	if title == "" {
		return "Telegram group"
	}
	return truncateRunes("Telegram · "+title, assistantSessionTitleMaxLen)
}

// telegramGroupPreamble tells the Assistant where it is speaking and, when the
// group has one, the role it was given. It travels with the message because a
// group turn must carry its own setting: the same person's private
// conversation has none of it.
func telegramGroupPreamble(chat db.TelegramTrackedChat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Telegram group %q. Your answer is posted to the whole group: keep it short and share only what suits the room.", chat.Title)
	if role := strings.TrimSpace(chat.Role); role != "" {
		b.WriteString(" Your role in this group: " + role)
	}
	b.WriteString("]\n\n")
	return b.String()
}

func (h *Handler) telegramGroupSay(ctx context.Context, g telegramGroupMessage, key string, args ...any) {
	text := botT(g.Lang, key)
	if len(args) > 0 {
		text = fmt.Sprintf(text, args...)
	}
	_ = h.telegramBot.SendMessageReply(ctx, g.Chat, text, g.MessageID)
}

// telegramGroupLinkedUser resolves the Agora account behind a group message,
// telling the sender when there is none.
func (h *Handler) telegramGroupLinkedUser(ctx context.Context, g telegramGroupMessage) (string, bool) {
	userID, _ := h.userIDByExternalIdentity(ctx, providerTelegram, strconv.FormatInt(g.FromID, 10))
	if userID == "" {
		h.telegramGroupSay(ctx, g, "grp.notlinked")
		return "", false
	}
	return userID, true
}

// telegramGroupMember resolves the sender as a member of the group's
// workspace. Being in the Telegram group is not enough to use the workspace.
func (h *Handler) telegramGroupMember(ctx context.Context, g telegramGroupMessage, workspaceID pgtype.UUID) (string, bool) {
	userID, ok := h.telegramGroupLinkedUser(ctx, g)
	if !ok {
		return "", false
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: parseUUID(userID), WorkspaceID: workspaceID,
	}); err != nil {
		h.telegramGroupSay(ctx, g, "grp.notmember")
		return "", false
	}
	return userID, true
}

// telegramGroupAdmin resolves the sender as an owner or admin of the group's
// workspace. Like /allow and /deny it is the Agora role that counts, not the
// Telegram one: there is one list of who administers a workspace.
func (h *Handler) telegramGroupAdmin(ctx context.Context, g telegramGroupMessage, workspaceID pgtype.UUID) (string, bool) {
	userID, ok := h.telegramGroupLinkedUser(ctx, g)
	if !ok {
		return "", false
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: parseUUID(userID), WorkspaceID: workspaceID,
	})
	if err != nil || (member.Role != "owner" && member.Role != "admin") {
		h.telegramGroupSay(ctx, g, "grp.notadmin")
		return "", false
	}
	return userID, true
}

// telegramTrackCommand binds the group to a workspace the sender administers
// and starts recording. "/track <workspace-slug>" picks one of several.
func (h *Handler) telegramTrackCommand(ctx context.Context, g telegramGroupMessage, slug string) {
	userID, ok := h.telegramGroupLinkedUser(ctx, g)
	if !ok {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT w.id, w.slug, w.name FROM member m
		JOIN workspace w ON w.id = m.workspace_id
		WHERE m.user_id = $1 AND m.role IN ('owner', 'admin')
		ORDER BY w.slug`, parseUUID(userID))
	if err != nil {
		h.telegramGroupSay(ctx, g, "ai.failed")
		return
	}
	type workspace struct {
		ID         pgtype.UUID
		Slug, Name string
	}
	var administered []workspace
	for rows.Next() {
		var w workspace
		if rows.Scan(&w.ID, &w.Slug, &w.Name) == nil {
			administered = append(administered, w)
		}
	}
	rows.Close()
	if len(administered) == 0 {
		h.telegramGroupSay(ctx, g, "grp.notadmin")
		return
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	var chosen *workspace
	if slug == "" && len(administered) == 1 {
		chosen = &administered[0]
	}
	for i := range administered {
		if slug != "" && administered[i].Slug == slug {
			chosen = &administered[i]
		}
	}
	if chosen == nil {
		slugs := make([]string, 0, len(administered))
		for _, w := range administered {
			slugs = append(slugs, "/track "+w.Slug)
		}
		h.telegramGroupSay(ctx, g, "grp.pick", strings.Join(slugs, "\n"))
		return
	}

	if _, err := h.Queries.TrackTelegramChat(ctx, db.TrackTelegramChatParams{
		ChatID: g.ChatID, WorkspaceID: chosen.ID, Title: g.Title, EnabledBy: parseUUID(userID),
	}); err != nil {
		// No row came back: the group already belongs to another workspace.
		h.telegramGroupSay(ctx, g, "grp.other")
		return
	}
	h.telegramGroupSay(ctx, g, "grp.tracked", chosen.Name)
}

func (h *Handler) telegramUntrackCommand(ctx context.Context, g telegramGroupMessage) {
	chat, err := h.Queries.GetTelegramTrackedChat(ctx, g.ChatID)
	if err != nil {
		h.telegramGroupSay(ctx, g, "grp.nottracked")
		return
	}
	if _, ok := h.telegramGroupAdmin(ctx, g, chat.WorkspaceID); !ok {
		return
	}
	if _, err := h.Queries.UntrackTelegramChat(ctx, g.ChatID); err != nil {
		h.telegramGroupSay(ctx, g, "ai.failed")
		return
	}
	h.telegramGroupSay(ctx, g, "grp.untracked")
}

// telegramRoleCommand sets how the bot behaves in this group. "/role" alone
// shows it, "/role clear" removes it.
func (h *Handler) telegramRoleCommand(ctx context.Context, g telegramGroupMessage, role string) {
	chat, err := h.Queries.GetTelegramTrackedChat(ctx, g.ChatID)
	if err != nil {
		h.telegramGroupSay(ctx, g, "grp.nottracked")
		return
	}
	role = strings.TrimSpace(role)
	if role == "" {
		if chat.Role == "" {
			h.telegramGroupSay(ctx, g, "grp.role.none")
			return
		}
		h.telegramGroupSay(ctx, g, "grp.role.show", chat.Role)
		return
	}
	if _, ok := h.telegramGroupAdmin(ctx, g, chat.WorkspaceID); !ok {
		return
	}
	key := "grp.role.set"
	if strings.EqualFold(role, "clear") {
		role, key = "", "grp.role.cleared"
	}
	if _, err := h.Queries.SetTelegramTrackedChatRole(ctx, db.SetTelegramTrackedChatRoleParams{
		ChatID: g.ChatID, Role: truncateRunes(role, telegramGroupRoleMax),
	}); err != nil {
		h.telegramGroupSay(ctx, g, "ai.failed")
		return
	}
	h.telegramGroupSay(ctx, g, key)
}

// telegramSummaryCommand posts a summary of the group's recent conversation.
// "/summary 48" covers the last 48 hours; the default is 24.
func (h *Handler) telegramSummaryCommand(ctx context.Context, g telegramGroupMessage, arg string) {
	chat, err := h.Queries.GetTelegramTrackedChat(ctx, g.ChatID)
	if err != nil {
		h.telegramGroupSay(ctx, g, "grp.nottracked")
		return
	}
	h.telegramSeeParticipant(ctx, g, time.Now())
	userID, ok := h.telegramGroupMember(ctx, g, chat.WorkspaceID)
	if !ok {
		return
	}
	if !h.TelegramAssistantEnabled() {
		h.telegramGroupSay(ctx, g, "grp.noassistant")
		return
	}
	hours := telegramHistoryDefaultHours
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && n > 0 {
		hours = min(n, telegramHistoryMaxHours)
	}
	prompt := fmt.Sprintf("%sSummarize this group's conversation over the last %d hours. Read it with read_telegram_chat "+
		"(chat_id %s, hours %d). Give: what was decided, what is still open, and who agreed to do what. "+
		"Answer in the language the group writes in.",
		telegramGroupPreamble(chat), hours, g.Chat, hours)
	h.telegramAssistantStart(ctx, telegramAssistantRequest{
		UserID: userID, ChatID: g.Chat, ReplyTo: g.MessageID, Lang: g.Lang,
		Text: prompt, Title: telegramGroupTitle(chat.Title), Workspace: chat.WorkspaceID,
		Mode: assistant.RunModeRoom,
	})
}
