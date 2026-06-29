package handler

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const larkPushTimeout = 10 * time.Second

// SendIssueInboxLarkDM posts a best-effort Lark card to a member for a new
// inbox item, sent FROM the bot bound to the issue's assigned agent. It mirrors
// SendIssueInboxDM (Telegram) but the sender identity is per-issue: a proactive
// notification has no inbound chat session to borrow, so the card is delivered
// through the assignee agent's installation. That means it only fires when the
// issue is assigned to an agent that has an active Lark Bot bound AND the
// recipient has bound their Lark identity to that Bot — otherwise there is no
// bot to send from or no open_id to send to, and it skips silently.
//
// Everything runs on a detached goroutine so it never sits on the request path,
// and every failure is logged, never surfaced.
func (h *Handler) SendIssueInboxLarkDM(ctx context.Context, recipientType, recipientID, issueID, notifType, title string, body *string, actorType, actorID string, details []byte) {
	if h.LarkInstallations == nil || h.LarkAPIClient == nil {
		return
	}
	if recipientType != "member" || issueID == "" || recipientID == "" {
		return
	}
	// The stub APIClient cannot send to an open_id; only the real HTTP client
	// implements OpenIDCardSender. No real client wired → nothing to do.
	sender, ok := h.LarkAPIClient.(lark.OpenIDCardSender)
	if !ok {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("lark push: panic recovered", "recover", r)
			}
		}()

		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), larkPushTimeout)
		defer cancel()

		issueUUID, err := util.ParseUUID(issueID)
		if err != nil {
			return
		}
		recipientUUID, err := util.ParseUUID(recipientID)
		if err != nil {
			return
		}
		issue, err := h.Queries.GetIssue(bgctx, issueUUID)
		if err != nil {
			return
		}
		// Sender = the issue's assigned agent's bot. No agent assignee → no bot.
		if issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
			return
		}
		inst, err := h.Queries.GetLarkInstallationByAgent(bgctx, db.GetLarkInstallationByAgentParams{
			WorkspaceID: issue.WorkspaceID,
			AgentID:     issue.AssigneeID,
		})
		if err != nil || inst.Status != "active" {
			return
		}
		// Recipient must have bound their Lark identity to this installation.
		binding, err := h.Queries.GetLarkUserBindingByUser(bgctx, db.GetLarkUserBindingByUserParams{
			InstallationID: inst.ID,
			AgoraUserID:    recipientUUID,
		})
		if err != nil || strings.TrimSpace(binding.LarkOpenID) == "" {
			return
		}

		secret, err := h.LarkInstallations.DecryptAppSecret(inst)
		if err != nil {
			slog.Warn("lark push: decrypt app_secret failed", "installation_id", uuidToString(inst.ID), "error", err)
			return
		}
		creds := lark.InstallationCredentials{
			AppID:     inst.AppID,
			AppSecret: secret,
			TenantKey: inst.TenantKey.String,
			Region:    lark.RegionOrDefault(inst.Region),
		}

		wsSlug := ""
		if ws, werr := h.Queries.GetWorkspace(bgctx, issue.WorkspaceID); werr == nil {
			wsSlug = ws.Slug
		}

		headline, bodyText := composeLarkNotify(notifType, title, body, h.resolveActorName(bgctx, actorType, actorID))
		card, err := lark.IssueNotifyCard(headline, bodyText, h.larkIssueURL(wsSlug, issueID), "Open in Agora")
		if err != nil {
			slog.Warn("lark push: build card failed", "error", err)
			return
		}
		if _, err := sender.SendCardToOpenID(bgctx, lark.SendCardToOpenIDParams{
			InstallationID: creds,
			OpenID:         lark.OpenID(binding.LarkOpenID),
			CardJSON:       card,
		}); err != nil {
			slog.Warn("lark push: send failed", "open_id", binding.LarkOpenID, "error", err)
		}
	}()
}

// larkIssueURL builds the absolute web link to an issue for the card button.
// Base resolves from AGORA_PUBLIC_URL, falling back to FRONTEND_ORIGIN (local
// dev). The web route is workspace-scoped (/<wsSlug>/issues/<id>); a missing
// slug or base returns "" so the card renders without a button.
func (h *Handler) larkIssueURL(wsSlug, issueID string) string {
	if wsSlug == "" {
		return ""
	}
	base := strings.TrimRight(h.cfg.PublicURL, "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/")
	}
	if base == "" {
		return ""
	}
	return base + "/" + wsSlug + "/issues/" + issueID
}

// composeLarkNotify renders the card's headline and optional body line. The
// emoji map (dmEmoji) is shared with the Telegram path. body carries a comment
// snippet for mention/comment notifications; actorName names who acted.
func composeLarkNotify(notifType, title string, body *string, actorName string) (headline, bodyText string) {
	emoji := dmEmoji[notifType]
	if emoji == "" {
		emoji = "🔔"
	}
	headline = strings.TrimSpace(emoji + " " + strings.TrimSpace(title))

	var lines []string
	if actorName != "" {
		lines = append(lines, "**"+actorName+"**")
	}
	if body != nil {
		if snippet := strings.TrimSpace(*body); snippet != "" {
			lines = append(lines, snippet)
		}
	}
	bodyText = strings.Join(lines, "\n")
	return headline, bodyText
}
