package handler

import (
	"context"
	"html"
	"log/slog"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Issue-created group notify — posts a short card to AGORA_TELEGRAM_REPORT_CHAT_ID
// when an issue is created. Same destination as autopilot reports (a shared room,
// not a personal inbox DM). Best-effort and detached: a missing bot/chat or Bot
// API failure must never block issue create.

// issueCreatedUnassignedLabel is the assignee line when the issue has no
// assignee. Group notices are English (shared room, not a per-user DM).
const issueCreatedUnassignedLabel = "Unassigned"

// SendIssueCreatedGroupNotify posts a short "new issue" notice to the
// configured Telegram report chat. Safe to call for every issue:created event;
// no-ops when the platform bot or chat id is unset.
func (h *Handler) SendIssueCreatedGroupNotify(ctx context.Context, issue IssueResponse) {
	if h.telegramBot == nil {
		return
	}
	h.enqueueIssueCreatedGroupNotify(ctx, func(context.Context) (IssueResponse, error) {
		return issue, nil
	})
}

// SendIssueCreatedGroupNotifyByID loads the committed issue inside the bounded
// background operation. Event publishers are allowed to emit only issue_id;
// the synchronous create path therefore never waits on another database read.
func (h *Handler) SendIssueCreatedGroupNotifyByID(ctx context.Context, workspaceID, issueID string) {
	if h.telegramBot == nil {
		return
	}
	h.enqueueIssueCreatedGroupNotify(ctx, func(loadCtx context.Context) (IssueResponse, error) {
		workspaceUUID, err := util.ParseUUID(workspaceID)
		if err != nil {
			return IssueResponse{}, err
		}
		issueUUID, err := util.ParseUUID(issueID)
		if err != nil {
			return IssueResponse{}, err
		}
		issue, err := h.Queries.GetIssueInWorkspace(loadCtx, db.GetIssueInWorkspaceParams{
			ID: issueUUID, WorkspaceID: workspaceUUID,
		})
		if err != nil {
			return IssueResponse{}, err
		}
		workspace, err := h.Queries.GetWorkspace(loadCtx, workspaceUUID)
		if err != nil {
			return IssueResponse{}, err
		}
		return issueToResponse(issue, workspace.IssuePrefix), nil
	})
}

func (h *Handler) enqueueIssueCreatedGroupNotify(ctx context.Context, load func(context.Context) (IssueResponse, error)) {

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("telegram issue notify: panic recovered", "recover", r)
			}
		}()

		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPushTimeout)
		defer cancel()
		issue, err := load(bgctx)
		if err != nil {
			slog.Warn("telegram issue notify: load failed", "error", err)
			return
		}
		chatID := h.issueCreatedReportChatID(bgctx, issue)
		if chatID == "" {
			return
		}

		wsSlug := ""
		if wsUUID, err := util.ParseUUID(issue.WorkspaceID); err == nil {
			if ws, werr := h.Queries.GetWorkspace(bgctx, wsUUID); werr == nil {
				wsSlug = ws.Slug
			}
		}

		link := ""
		if l := h.webIssueLink(wsSlug, issue.ID); l != "" && telegramGroupLinkOK(l) {
			link = l
		}

		text := composeIssueCreatedGroupNotifyText(
			issue.Identifier,
			issue.Title,
			h.resolveIssueAssigneeDisplayName(bgctx, issue),
		)

		// SendMessageWithButton always enables Telegram HTML parsing, even when
		// the local/dev link was intentionally omitted. This keeps formatting
		// human-readable instead of exposing literal <b> tags in the room.
		if err := h.telegramBot.SendMessageWithButton(bgctx, chatID, text, "Open issue", link); err != nil {
			slog.Warn("telegram issue notify: send failed", "chat_id", chatID, "issue_id", issue.ID, "error", err)
			return
		}
		slog.Info("telegram issue notify posted",
			"chat_id", chatID, "issue_id", issue.ID, "identifier", strings.TrimSpace(issue.Identifier))
	}()
}

// composeIssueCreatedGroupNotifyText builds the HTML body for an issue-created
// group notice. assigneeLabel is already resolved (member/agent/squad name, or
// "Unassigned"); this only escapes and assembles Telegram-safe HTML.
func composeIssueCreatedGroupNotifyText(identifier, title, assigneeLabel string) string {
	identifier = strings.TrimSpace(identifier)
	title = strings.TrimSpace(title)
	assigneeLabel = strings.TrimSpace(assigneeLabel)
	if assigneeLabel == "" {
		assigneeLabel = issueCreatedUnassignedLabel
	}

	var b strings.Builder
	b.WriteString("🆕 <b>New issue created</b>\n")
	b.WriteString("📌 <b>")
	b.WriteString(html.EscapeString(identifier))
	b.WriteString("</b>")
	if title != "" {
		b.WriteString(" — ")
		b.WriteString(html.EscapeString(title))
	}
	b.WriteString("\n👤 <b>Assigned to:</b> ")
	b.WriteString(html.EscapeString(assigneeLabel))
	return b.String()
}

// resolveIssueAssigneeDisplayName returns the human-facing assignee label for
// a group notice: member name, agent name, or squad name. Falls back to
// "Unassigned" when the issue has no assignee or the row cannot be loaded.
func (h *Handler) resolveIssueAssigneeDisplayName(ctx context.Context, issue IssueResponse) string {
	if issue.AssigneeType == nil || issue.AssigneeID == nil {
		return issueCreatedUnassignedLabel
	}
	assigneeType := strings.TrimSpace(*issue.AssigneeType)
	assigneeID := strings.TrimSpace(*issue.AssigneeID)
	if assigneeType == "" || assigneeID == "" {
		return issueCreatedUnassignedLabel
	}
	id, err := util.ParseUUID(assigneeID)
	if err != nil {
		return issueCreatedUnassignedLabel
	}

	wsUUID, wsErr := util.ParseUUID(issue.WorkspaceID)

	switch assigneeType {
	case "member":
		member, err := h.Queries.GetMember(ctx, id)
		if err != nil {
			return issueCreatedUnassignedLabel
		}
		if u, err := h.Queries.GetUser(ctx, member.UserID); err == nil {
			if name := strings.TrimSpace(u.Name); name != "" {
				return name
			}
		}
	case "agent":
		if wsErr == nil {
			if a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: id, WorkspaceID: wsUUID,
			}); err == nil {
				if name := strings.TrimSpace(a.Name); name != "" {
					return name
				}
			}
		}
		if a, err := h.Queries.GetAgent(ctx, id); err == nil {
			if name := strings.TrimSpace(a.Name); name != "" {
				return name
			}
		}
	case "squad":
		if wsErr == nil {
			if s, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
				ID: id, WorkspaceID: wsUUID,
			}); err == nil {
				if name := strings.TrimSpace(s.Name); name != "" {
					return name
				}
			}
		}
		if s, err := h.Queries.GetSquad(ctx, id); err == nil {
			if name := strings.TrimSpace(s.Name); name != "" {
				return name
			}
		}
	}
	return issueCreatedUnassignedLabel
}

// issueCreatedReportChatID resolves AGORA_TELEGRAM_REPORT_CHAT_ID for an issue,
// honouring a project override when the issue belongs to a project.
func (h *Handler) issueCreatedReportChatID(ctx context.Context, issue IssueResponse) string {
	var overrides map[string]string
	if issue.ProjectID != nil && *issue.ProjectID != "" {
		if projectUUID, err := util.ParseUUID(*issue.ProjectID); err == nil {
			if project, perr := h.Queries.GetProject(ctx, projectUUID); perr == nil {
				overrides = parseProjectConfigOverrides(project.Settings)
			}
		}
	}
	return strings.TrimSpace(config.StringFrom(overrides, "AGORA_TELEGRAM_REPORT_CHAT_ID"))
}

// telegramGroupLinkOK reports whether a URL is safe to embed in a group notice.
// Local/dev loopback hosts are skipped — they help nobody in Telegram.
func telegramGroupLinkOK(link string) bool {
	l := strings.ToLower(strings.TrimSpace(link))
	if l == "" {
		return false
	}
	if strings.HasPrefix(l, "http://localhost") || strings.HasPrefix(l, "https://localhost") ||
		strings.HasPrefix(l, "http://127.0.0.1") || strings.HasPrefix(l, "https://127.0.0.1") {
		return false
	}
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}
