package handler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// /allow, /deny and /access — access control from inside Telegram, the shape
// hamroh's access.json proved out.
//
// The point is that the person who needs to grant access is standing IN the
// group, on a phone, when the need arises. Making them open a laptop, find the
// agent in Settings and paste a numeric Telegram user id is enough friction
// that the real-world outcome is an installation left on `open` instead.
//
// Two lists, deliberately not one:
//   - allowed_telegram_user_ids — may INSTRUCT the agent.
//   - admin_telegram_user_ids   — may run these commands.
//
// Being able to ask an agent to do something must not imply being able to hand
// that power to anyone else, otherwise the first grant silently becomes a
// grant of the grant itself.

// telegramAccessCommandResult tells the caller whether the message was a
// command, so a handled command is not also dispatched to the agent.
type telegramAccessCommandResult bool

// handleTelegramAccessCommand processes /allow, /deny and /access. Returns true
// when the message was one of them, handled or refused.
//
// Runs AFTER the chat gate but BEFORE the sender gate: a chat must be bound for
// its commands to count, but an admin who is not on the allowlist must still be
// able to manage it — that is the normal state for whoever set the bot up.
func (h *Handler) handleTelegramAccessCommand(ctx context.Context, row db.TelegramInstallation, chatIDNum, fromID int64, text string) telegramAccessCommandResult {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	// Telegram appends @botname to commands in groups.
	cmd := strings.ToLower(fields[0])
	if at := strings.Index(cmd, "@"); at > 0 {
		cmd = cmd[:at]
	}
	switch cmd {
	case "/allow", "/deny", "/access":
	default:
		return false
	}

	chatID := strconv.FormatInt(chatIDNum, 10)
	reply := func(msg string) {
		if bot, _ := h.agentTelegramClient(ctx, row.AgentID); bot != nil {
			_ = bot.SendMessage(ctx, chatID, msg)
		}
	}

	if !telegramUserIsAdmin(row, fromID) {
		// Named refusal, unlike the silent drop for a plain message: whoever
		// typed this is a person expecting an answer, and silence reads as a
		// broken bot. It leaks nothing — they already know the bot is here.
		reply("Ruxsat yo'q. Bu buyruqni faqat shu botning administratori ishlata oladi.")
		return true
	}

	if cmd == "/access" {
		reply(describeTelegramAccess(row))
		return true
	}

	target, targetID, err := parseAccessTarget(fields, chatIDNum, fromID)
	if err != "" {
		reply(err)
		return true
	}

	grant := cmd == "/allow"
	switch target {
	case "chat":
		next := toggleID(row.AllowedChatIds, targetID, grant)
		updated, dbErr := h.Queries.SetTelegramInstallationChats(ctx, db.SetTelegramInstallationChatsParams{
			AgentID: row.AgentID, AllowedChatIds: next,
		})
		if dbErr != nil {
			reply("Saqlanmadi, qayta urinib ko'ring.")
			return true
		}
		slog.Info("telegram access: chat updated", "bot", row.BotUsername,
			"chat", targetID, "grant", grant, "by", fromID)
		_ = updated
		if grant {
			reply("Guruh " + strconv.FormatInt(targetID, 10) + " ruxsat oldi.")
		} else {
			reply("Guruh " + strconv.FormatInt(targetID, 10) + " ruxsatdan chiqarildi.")
		}
	case "user":
		next := toggleID(row.AllowedTelegramUserIds, targetID, grant)
		// Granting a user only means anything under `allowlist`; a `closed`
		// installation would keep refusing them and the admin would be left
		// debugging a grant that visibly did nothing. Flip the policy with the
		// first grant rather than making that a second, undiscoverable step.
		policy := row.AccessPolicy
		if grant && policy == "closed" {
			policy = "allowlist"
		}
		if _, dbErr := h.Queries.SetTelegramInstallationAccess(ctx, db.SetTelegramInstallationAccessParams{
			AgentID:                row.AgentID,
			AccessPolicy:           policy,
			AllowedTelegramUserIds: next,
			WorkspaceID:            row.WorkspaceID,
		}); dbErr != nil {
			reply("Saqlanmadi, qayta urinib ko'ring.")
			return true
		}
		slog.Info("telegram access: user updated", "bot", row.BotUsername,
			"user", targetID, "grant", grant, "by", fromID)
		if grant {
			reply("Foydalanuvchi " + strconv.FormatInt(targetID, 10) + " ruxsat oldi.")
		} else {
			reply("Foydalanuvchi " + strconv.FormatInt(targetID, 10) + " ruxsatdan chiqarildi.")
		}
	}
	return true
}

// parseAccessTarget reads `/allow`, `/allow chat`, `/allow user <id>`,
// `/allow <id>`. A bare command targets the sender's own chat, which is the
// common case: someone standing in a new group wants THIS group allowed.
//
// Returns a user-facing error string rather than an error value — every failure
// here is something to say back in the group.
func parseAccessTarget(fields []string, chatIDNum, fromID int64) (target string, id int64, errMsg string) {
	const usage = "Foydalanish:\n/allow chat — shu guruhga ruxsat\n/allow user <id> — foydalanuvchiga ruxsat\n/access — hozirgi holat"

	if len(fields) == 1 {
		return "chat", chatIDNum, ""
	}
	switch strings.ToLower(fields[1]) {
	case "chat", "group", "guruh":
		if len(fields) >= 3 {
			parsed, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return "", 0, usage
			}
			return "chat", parsed, ""
		}
		return "chat", chatIDNum, ""
	case "user", "me", "men":
		if len(fields) >= 3 {
			// Accept @username only to say why it cannot work: the Bot API
			// gives no username→id lookup, and guessing would grant the wrong
			// person. Numeric ids are the only unambiguous handle.
			if strings.HasPrefix(fields[2], "@") {
				return "", 0, "@username orqali bo'lmaydi — Telegram raqamli id kerak.\nO'sha odam guruhga yozsin, log'da id ko'rinadi, yoki @userinfobot dan olsin."
			}
			parsed, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return "", 0, usage
			}
			return "user", parsed, ""
		}
		return "user", fromID, ""
	default:
		if strings.HasPrefix(fields[1], "@") {
			return "", 0, "@username orqali bo'lmaydi — Telegram raqamli id kerak."
		}
		parsed, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return "", 0, usage
		}
		// A negative id is a group in Telegram's numbering; a positive one is a
		// user. Inferring it saves the admin a keyword and cannot be ambiguous.
		if parsed < 0 {
			return "chat", parsed, ""
		}
		return "user", parsed, ""
	}
}

// toggleID adds or removes an id, keeping the list free of duplicates. Order is
// preserved so the list stays readable in /access.
func toggleID(list []int64, id int64, add bool) []int64 {
	out := make([]int64, 0, len(list)+1)
	found := false
	for _, existing := range list {
		if existing == id {
			found = true
			if !add {
				continue
			}
		}
		out = append(out, existing)
	}
	if add && !found {
		out = append(out, id)
	}
	return out
}

// describeTelegramAccess renders the current state for /access.
func describeTelegramAccess(row db.TelegramInstallation) string {
	var b strings.Builder
	b.WriteString("Bot: @" + row.BotUsername + "\n")
	b.WriteString("Siyosat: " + row.AccessPolicy)
	switch row.AccessPolicy {
	case "closed":
		b.WriteString(" (hech kim buyruq bera olmaydi)")
	case "open":
		b.WriteString(" (ruxsat berilgan guruhdagi hamma)")
	case "allowlist":
		b.WriteString(" (faqat ro'yxatdagilar)")
	}
	b.WriteString("\nGuruhlar: " + joinIDs(row.AllowedChatIds))
	b.WriteString("\nFoydalanuvchilar: " + joinIDs(row.AllowedTelegramUserIds))
	b.WriteString("\nAdminlar: " + joinIDs(row.AdminTelegramUserIds))
	return b.String()
}

func joinIDs(ids []int64) string {
	if len(ids) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ")
}
