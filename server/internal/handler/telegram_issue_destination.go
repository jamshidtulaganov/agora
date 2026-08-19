package handler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Where an issue-scoped Telegram notice goes.
//
// A workspace binds its groups through the UI (Settings → Integrations →
// Telegram → Connect a bot → Add group): each agent gets its OWN bot and the
// bound chat id is stored on telegram_installation.chat_id. Chat ids are
// therefore DYNAMIC per workspace/agent — requiring a static
// AGORA_TELEGRAM_REPORT_CHAT_ID would make every notice depend on an operator
// pasting an id that the product already knows.
//
// Resolution order, most specific first:
//
//  1. an explicit chat id on the caller (an automation step may name one) —
//     delivered through the agent's own bot when that bot is actually in the
//     room, else through the platform bot;
//  2. the issue's SPEAKER agent (its agent assignee, else its orchestrator /
//     squad leader) — its own bot, posting into its own bound group, so the
//     message arrives under the agent's identity;
//  3. the platform bot + the project-scoped AGORA_TELEGRAM_REPORT_CHAT_ID,
//     which stays supported as an override but is no longer required.
//
// Returns ok=false when nothing resolves, and the CALLER treats that as "no
// destination configured" rather than an error: a rule whose point is to move a
// task must not fail because no room is bound yet.

// issueTelegramDestination is a resolved delivery target.
type issueTelegramDestination struct {
	bot    *telegram.BotClient
	chatID string
	// via is "agent" or "platform" — logged so a missing message can be traced
	// to the bot that was actually used.
	via string
}

// workspaceTelegramClient finds ANY active installation in the workspace whose
// bot has a bound group — the workspace's team room. This is the fallback for the
// COMMON case the per-agent lookup cannot cover: a member-assigned issue (most
// tracker-synced tasks) has no agent to carry a bot, but the notice is a TEAM
// message and the team's room exists regardless of who owns the task. Newest
// installation first (ListTelegramInstallations orders by installed_at DESC), so
// the pick is deterministic.
func (h *Handler) workspaceTelegramClient(ctx context.Context, workspaceID pgtype.UUID) (*telegram.BotClient, string) {
	rows, err := h.Queries.ListTelegramInstallations(ctx, workspaceID)
	if err != nil {
		return nil, ""
	}
	box, err := telegramSealBox()
	if err != nil {
		return nil, ""
	}
	for _, row := range rows {
		if row.Status != "active" || !row.ChatID.Valid || strings.TrimSpace(row.ChatID.String) == "" {
			continue
		}
		token, err := box.Open(row.BotTokenEncrypted)
		if err != nil {
			continue
		}
		return telegram.NewBotClient(string(token)), strings.TrimSpace(row.ChatID.String)
	}
	return nil, ""
}

// workspaceTelegramClientForChat finds an active workspace bot whose access
// configuration explicitly includes chatID. This covers automations that pin a
// group while the issue's speaker agent either has no bot or uses a different
// one. Without this fallback, an authorized workspace bot is ignored and the
// resolver falls through to the platform bot, which may not belong to the room.
func (h *Handler) workspaceTelegramClientForChat(
	ctx context.Context, workspaceID pgtype.UUID, chatID string,
) *telegram.BotClient {
	parsedChatID, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if err != nil {
		return nil
	}
	rows, err := h.Queries.ListTelegramInstallations(ctx, workspaceID)
	if err != nil {
		return nil
	}
	box, err := telegramSealBox()
	if err != nil {
		return nil
	}
	for _, row := range rows {
		if row.Status != "active" || !telegramChatAllowed(row, parsedChatID) {
			continue
		}
		token, err := box.Open(row.BotTokenEncrypted)
		if err != nil {
			continue
		}
		return telegram.NewBotClient(string(token))
	}
	return nil
}

// issueTelegramSpeakerAgent is the agent whose identity an issue notice should
// carry: the agent assignee itself, else the issue's orchestrator (which for a
// squad assignee is its leader — a squad id is not an agent id, so looking up an
// installation by it would find nothing).
func (h *Handler) issueTelegramSpeakerAgent(ctx context.Context, issue db.Issue) (pgtype.UUID, bool) {
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		return issue.AssigneeID, true
	}
	if agent, ok := h.orchestratorForIssue(ctx, issue); ok {
		return agent.ID, true
	}
	return pgtype.UUID{}, false
}

// resolveIssueTelegramDestination implements the order documented above.
func (h *Handler) resolveIssueTelegramDestination(
	ctx context.Context, issue db.Issue, explicitChatID string,
) (issueTelegramDestination, bool) {
	explicitChatID = strings.TrimSpace(explicitChatID)

	var agentBot *telegram.BotClient
	agentChat := ""
	agentID, hasAgent := h.issueTelegramSpeakerAgent(ctx, issue)
	if hasAgent {
		agentBot, agentChat = h.agentTelegramClient(ctx, agentID)
	}

	// Membership is PROVEN, not assumed: posting through a bot that is not in the
	// room fails at the Bot API, silently. Only asked when a room is named, since
	// the agent's own bound group needs no check.
	reachesExplicit := false
	if explicitChatID != "" && agentBot != nil && hasAgent {
		reachesExplicit = h.agentReachesChat(ctx, agentID, explicitChatID)
	}
	projectChat := strings.TrimSpace(config.StringFrom(h.projectConfigOverrides(ctx, issue), "AGORA_TELEGRAM_REPORT_CHAT_ID"))
	reachesProject := false
	if explicitChatID == "" && agentChat == "" && projectChat != "" && agentBot != nil && hasAgent {
		reachesProject = h.agentReachesChat(ctx, agentID, projectChat)
	}
	reaches := reachesExplicit
	if explicitChatID == "" {
		reaches = reachesProject
	}
	wsBot, wsChat := (*telegram.BotClient)(nil), ""
	if explicitChatID != "" && !reachesExplicit {
		if bot := h.workspaceTelegramClientForChat(ctx, issue.WorkspaceID, explicitChatID); bot != nil {
			wsBot, wsChat = bot, explicitChatID
		}
	}
	if wsBot == nil && (agentBot == nil || agentChat == "") {
		wsBot, wsChat = h.workspaceTelegramClient(ctx, issue.WorkspaceID)
	}
	return chooseIssueTelegramDestination(explicitChatID, agentBot, agentChat, reaches, wsBot, wsChat, h.telegramBot, projectChat)
}

// chooseIssueTelegramDestination is the pure decision behind the resolver, split
// out so the ORDER — the whole point of resolving instead of configuring — is
// testable without a bot token or a database.
func chooseIssueTelegramDestination(
	explicitChatID string,
	agentBot *telegram.BotClient,
	agentChat string,
	agentReachesNamedChat bool,
	extraWorkspaceBot *telegram.BotClient,
	extraWorkspaceChat string,
	platformBot *telegram.BotClient,
	projectChat string,
) (issueTelegramDestination, bool) {
	// 1. An explicitly named room wins.
	if explicitChatID != "" {
		if agentBot != nil && agentReachesNamedChat {
			return issueTelegramDestination{bot: agentBot, chatID: explicitChatID, via: "agent"}, true
		}
		if extraWorkspaceBot != nil {
			return issueTelegramDestination{bot: extraWorkspaceBot, chatID: explicitChatID, via: "workspace"}, true
		}
		if platformBot != nil {
			return issueTelegramDestination{bot: platformBot, chatID: explicitChatID, via: "platform"}, true
		}
		return issueTelegramDestination{}, false
	}

	// 2. The agent's own bot in its own bound group.
	if agentBot != nil && agentChat != "" {
		return issueTelegramDestination{bot: agentBot, chatID: agentChat, via: "agent"}, true
	}

	// 3. Any workspace bot with a bound group — the team room. Covers the
	//    member-assigned issue, which has no agent to speak through.
	if wsBot, wsChat := extraWorkspaceBot, extraWorkspaceChat; wsBot != nil && wsChat != "" {
		return issueTelegramDestination{bot: wsBot, chatID: wsChat, via: "workspace"}, true
	}

	// 4. The configured project/instance room.
	if projectChat != "" {
		if agentBot != nil && agentReachesNamedChat {
			return issueTelegramDestination{bot: agentBot, chatID: projectChat, via: "agent"}, true
		}
		if platformBot != nil {
			return issueTelegramDestination{bot: platformBot, chatID: projectChat, via: "platform"}, true
		}
	}
	return issueTelegramDestination{}, false
}

// sendIssueTelegramGroupNotice delivers HTML text (plus an optional deep-link
// button) to the resolved room. Reports whether it was delivered, and to where,
// so an automation's audit row can say "notified <chat> via <bot>".
func (h *Handler) sendIssueTelegramGroupNotice(
	ctx context.Context, issue db.Issue, explicitChatID, text, buttonText, buttonURL string,
) (issueTelegramDestination, bool) {
	dest, ok := h.resolveIssueTelegramDestination(ctx, issue, explicitChatID)
	if !ok {
		return issueTelegramDestination{}, false
	}
	if err := dest.bot.SendMessageWithButton(ctx, dest.chatID, text, buttonText, buttonURL); err != nil {
		slog.Warn("telegram issue notice: send failed",
			"error", err, "issue_id", uuidToString(issue.ID), "chat_id", dest.chatID, "via", dest.via)
		return dest, false
	}
	return dest, true
}
