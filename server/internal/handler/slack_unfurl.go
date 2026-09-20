package handler

// Slack link unfurling (docs/slack-integration-plan.md §Phase 1 — "Unfurl").
//
// Someone pastes an Agora link into a Slack channel; Slack sends
// `link_shared`; we answer with `chat.unfurl` and the channel sees what the
// link is without anybody opening a tab. That is the whole feature, and its
// entire difficulty is in what it must REFUSE to show.
//
// Four fences, applied in this order, and every one of them is load-bearing:
//
//  1. HOST. The link must point at this deployment's own app origin. Slack
//     only delivers link_shared for domains the app registered, so this is
//     defence in depth against a look-alike URL.
//  2. TENANT. The workspace named by the slug must have an ACTIVE
//     slack_installation for the Slack team the event came from. This is the
//     fence that makes cross-tenant unfurling impossible: pasting another
//     company's Agora link into your Slack does not unfurl, because your team
//     does not appear in their installation rows.
//  3. IDENTITY. The Slack user who posted the link must be linked to an Agora
//     user (user_external_identity, provider 'slack') and be a member of that
//     workspace.
//  4. VISIBILITY. The non-owner issue-visibility gate applies unchanged. A
//     member who could not open the issue in Agora does not get to see its
//     title in Slack — an unfurl renders to the whole channel, so this is the
//     single most consequential of the four.
//
// Fences 3 and 4 answer with Slack's own `user_auth_required` prompt, which is
// private to the person who posted the link and leaks nothing into the
// channel. Fences 1 and 2 answer with silence: we do not tell a stranger's
// Slack workspace that a slug exists.
//
// Unfurls are read-only in Phase 1 (Linear's inline actions need the
// interactivity endpoint, which is Phase 3), and they need no dedup table:
// chat.unfurl REPLACES rather than appends, so a Slack retry of the same
// event is naturally idempotent.

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// slackUnfurlTimeout bounds one link_shared event's work: a handful of row
// reads plus one chat.unfurl call. Generous compared with the 3-second ACK
// budget because it runs AFTER the ack, detached from the request.
const slackUnfurlTimeout = 20 * time.Second

// slackMaxUnfurlLinks caps how many links from one message we resolve. Slack
// will happily report a dozen; a message with more than a few Agora links is
// a link dump, and each one costs database reads.
const slackMaxUnfurlLinks = 10

// slackSharedLink is one link Slack found in the message.
type slackSharedLink struct {
	URL    string `json:"url"`
	Domain string `json:"domain"`
}

// slackLinkSharedEvent is the subset of the link_shared payload we act on,
// copied out of the envelope so the detached goroutine owns its own data.
type slackLinkSharedEvent struct {
	TeamID    string
	Channel   string
	MessageTS string
	// SlackUserID is who posted the link. Identity and visibility are decided
	// for THIS person — an unfurl is rendered in the channel, but it is
	// authorised as the poster.
	SlackUserID string
	Links       []slackSharedLink
}

// handleSlackLinkShared is the events path's entry point. It copies what it
// needs and detaches: the Events API budget is "respond within three
// seconds", and an app that fails more than 95% of deliveries in an hour has
// its subscriptions disabled — so nothing that touches the database or the
// network may run before the ack.
func (h *Handler) handleSlackLinkShared(envelope slackEventEnvelope) {
	if h == nil || h.Queries == nil || !slackEnabled() {
		return
	}
	ev := slackLinkSharedEvent{
		TeamID:      strings.TrimSpace(envelope.TeamID),
		Channel:     strings.TrimSpace(envelope.Event.Channel),
		MessageTS:   strings.TrimSpace(envelope.Event.MessageTS),
		SlackUserID: strings.TrimSpace(envelope.Event.User),
		Links:       envelope.Event.Links,
	}
	if ev.TeamID == "" || ev.Channel == "" || ev.MessageTS == "" || len(ev.Links) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("slack unfurl: panic recovered", "recovered", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), slackUnfurlTimeout)
		defer cancel()
		h.unfurlSlackLinks(ctx, ev)
	}()
}

// unfurlSlackLinks resolves every link in one message and answers Slack once.
//
// Synchronous and exported to the package so tests can drive it without
// racing a goroutine. Best-effort throughout: an unresolvable link costs one
// preview, never the event's acknowledgement (which has already been sent).
//
// One call answers the whole message, because chat.unfurl's auth prompt is a
// property of the CALL, not of an individual link. So when a message mixes a
// link we may show with one we may not, the showable previews win and the
// rest are silently omitted — a channel that sees one preview plus a "connect
// your account" banner would be confusing, and omitting is the quieter
// failure.
func (h *Handler) unfurlSlackLinks(ctx context.Context, ev slackLinkSharedEvent) {
	appHost := slackAppHost(h.cfg.PublicURL)

	unfurls := map[string]slack.Unfurl{}
	var install db.SlackInstallation
	haveInstall := false
	authNeeded := false

	links := ev.Links
	if len(links) > slackMaxUnfurlLinks {
		links = links[:slackMaxUnfurlLinks]
	}

	for _, link := range links {
		parsed, ok := slack.ParseAgoraLink(link.URL)
		if !ok {
			continue
		}
		// Fence 1 — host. Skipped only when the deployment has no configured
		// app origin at all, in which case there is nothing to compare against
		// and Slack's own domain registration is the only gate left.
		if appHost != "" && parsed.Host != appHost {
			continue
		}
		ws, err := h.Queries.GetWorkspaceBySlug(ctx, parsed.WorkspaceSlug)
		if err != nil {
			continue
		}
		// Fence 2 — tenant. The workspace must be installed on THIS Slack
		// team, and the install must be live. A link to a workspace that has
		// never heard of this team unfurls into silence.
		inst, err := h.Queries.GetSlackInstallationForTeam(ctx, db.GetSlackInstallationForTeamParams{
			WorkspaceID: ws.ID,
			TeamID:      ev.TeamID,
		})
		if err != nil {
			continue
		}
		if !haveInstall {
			install = inst
			haveInstall = true
		}

		// Fence 3 — identity. One lookup per message would be enough for a
		// single-workspace message; per link is correct because membership is
		// per workspace and a message may name two.
		viewer, role, ok := h.slackUnfurlViewer(ctx, ev.SlackUserID, ws.ID)
		if !ok {
			authNeeded = true
			continue
		}

		var blocks []slack.Block
		switch parsed.Kind {
		case slack.LinkKindIssue:
			blocks, ok = h.slackIssueUnfurlBlocks(ctx, ws, parsed.Identifier, viewer, role)
		case slack.LinkKindProject:
			blocks, ok = h.slackProjectUnfurlBlocks(ctx, ws, parsed.Identifier)
		default:
			continue
		}
		if !ok {
			// Fence 4 said no, or the object does not exist. Both answer with
			// the auth prompt rather than a "not found" card: telling a
			// channel that MUL-9999 is missing is itself information.
			authNeeded = true
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		unfurls[link.URL] = slack.Unfurl{Blocks: blocks}
	}

	if !haveInstall {
		// No Agora link we are allowed to speak about, and no installation to
		// speak through. Nothing is sent — not even an auth prompt, which
		// would confirm the workspace exists.
		return
	}
	if len(unfurls) == 0 && !authNeeded {
		return
	}

	token, err := h.openSlackBotToken(install)
	if err != nil {
		slog.Warn("slack unfurl: cannot open bot token", "installation_id", uuidToString(install.ID), "error", err)
		return
	}

	req := slack.UnfurlRequest{
		Channel: ev.Channel,
		TS:      ev.MessageTS,
		Unfurls: unfurls,
	}
	if len(unfurls) == 0 {
		req.UserAuthRequired = true
		req.UserAuthURL = h.slackUserLinkPromptURL(ctx, install.WorkspaceID)
		req.UserAuthMessage = "Connect your Agora account to preview this link."
	}
	if _, err := newSlackAPIClient().Unfurl(ctx, token, req); err != nil {
		if slack.IsTokenInvalid(err) {
			h.markSlackInstallationRevoked(ctx, uuidToString(install.ID))
			return
		}
		// A rate limit or a channel we cannot reach costs one preview. There
		// is nothing to retry into: Slack re-delivers link_shared on its own
		// schedule and chat.unfurl replaces, so a retry storm here would buy
		// duplicates rather than previews.
		slog.Warn("slack unfurl: chat.unfurl failed", "error", err)
	}
}

// slackUnfurlViewer resolves the posting Slack user to an Agora member of the
// workspace. Returns the member's user id and role.
//
// ok=false covers "not linked" and "linked but not a member" identically, and
// on purpose: both mean the poster gets the connect prompt, and
// distinguishing them in the response would turn the unfurl into a
// membership oracle.
func (h *Handler) slackUnfurlViewer(ctx context.Context, slackUserID string, wsID pgtype.UUID) (pgtype.UUID, string, bool) {
	if strings.TrimSpace(slackUserID) == "" {
		return pgtype.UUID{}, "", false
	}
	userID, err := h.userIDByExternalIdentity(ctx, providerSlack, slackUserID)
	if err != nil || strings.TrimSpace(userID) == "" {
		return pgtype.UUID{}, "", false
	}
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return pgtype.UUID{}, "", false
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      uid,
		WorkspaceID: wsID,
	})
	if err != nil {
		return pgtype.UUID{}, "", false
	}
	return uid, member.Role, true
}

// slackIssueUnfurlBlocks resolves one issue link and renders its card, or
// reports that this viewer may not see it.
//
// The visibility decision reuses assistantVisibilityRestriction — the PURE,
// role-only twin of issueVisibilityRestriction — precisely because it takes no
// *http.Request and therefore cannot inherit the X-Actor-Source exemption. A
// Slack event carries no Agora actor at all; treating it as one would hand a
// channel the agent-wide view of the workspace.
//
// Fails CLOSED: any lookup error answers "not visible".
func (h *Handler) slackIssueUnfurlBlocks(
	ctx context.Context, ws db.Workspace, identifier string, viewer pgtype.UUID, role string,
) ([]slack.Block, bool) {
	issue, ok := h.resolveSlackUnfurlIssue(ctx, ws.ID, identifier)
	if !ok {
		return nil, false
	}
	if restrict := assistantVisibilityRestriction(role, viewer); restrict.Valid {
		owned, err := h.Queries.IssueBelongsToUser(ctx, db.IssueBelongsToUserParams{
			IssueID:     issue.ID,
			WorkspaceID: ws.ID,
			UserID:      restrict,
		})
		if err != nil || !owned {
			return nil, false
		}
	}

	card := slack.IssueUnfurl{
		Identifier: h.issueKey(ctx, issue),
		Title:      issue.Title,
		Status:     issue.Status,
		Priority:   issue.Priority,
		Archived:   issue.ArchivedAt.Valid,
	}
	if card.Identifier == "" {
		card.Identifier = identifier
	}
	card.URL = slackIssueURL(h.cfg.PublicURL, ws.Slug, card.Identifier)
	card.Assignee, card.AssigneeKind = h.slackAssigneeLabel(ctx, issue)
	if issue.ProjectID.Valid {
		if project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          issue.ProjectID,
			WorkspaceID: ws.ID,
		}); err == nil {
			card.Project = project.Title
		}
	}
	return slack.IssueUnfurlBlocks(card), true
}

// resolveSlackUnfurlIssue accepts both link shapes an Agora issue URL can
// carry: the human identifier (MUL-123, what slackIssueURL writes) and a raw
// UUID (what a copied deep link may contain). Workspace-scoped in both paths.
func (h *Handler) resolveSlackUnfurlIssue(ctx context.Context, wsID pgtype.UUID, identifier string) (db.Issue, bool) {
	if issue, ok := h.resolveIssueByIdentifier(ctx, identifier, uuidToString(wsID)); ok {
		return issue, true
	}
	issueUUID, err := util.ParseUUID(identifier)
	if err != nil {
		return db.Issue{}, false
	}
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issueUUID,
		WorkspaceID: wsID,
	})
	if err != nil {
		return db.Issue{}, false
	}
	return issue, true
}

// slackProjectUnfurlBlocks renders a project card. Membership (fence 3) is the
// whole gate here: projects carry no per-member visibility rule, and the card
// deliberately shows only the title, the status and a link — a project
// description is long-form internal context and does not belong in a channel.
func (h *Handler) slackProjectUnfurlBlocks(ctx context.Context, ws db.Workspace, identifier string) ([]slack.Block, bool) {
	projectUUID, err := util.ParseUUID(identifier)
	if err != nil {
		return nil, false
	}
	project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          projectUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		return nil, false
	}
	return slack.ProjectUnfurlBlocks(slack.ProjectUnfurl{
		Name:   project.Title,
		Status: project.Status,
		URL:    slackProjectURL(h.cfg.PublicURL, ws.Slug, uuidToString(project.ID)),
	}), true
}

// slackAssigneeLabel names an issue's assignee for the card, with the kind so
// an agent stays visually distinct from a person. Best-effort: an
// unresolvable assignee yields no line rather than a wrong name.
func (h *Handler) slackAssigneeLabel(ctx context.Context, issue db.Issue) (string, slack.AssigneeKind) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return "", ""
	}
	id := issue.AssigneeID
	switch strings.TrimSpace(issue.AssigneeType.String) {
	case "member":
		// issue.assignee_id holds a USER id on the write paths the ownership
		// clause is built around (issueOwnershipClause compares it to a user
		// id directly) and a member id on others, so both are tried — the same
		// double lookup slackActorLabel does, for the same reason.
		if member, err := h.Queries.GetMember(ctx, id); err == nil {
			if user, err := h.Queries.GetUser(ctx, member.UserID); err == nil {
				if name := strings.TrimSpace(user.Name); name != "" {
					return name, slack.AssigneeMember
				}
			}
		}
		if user, err := h.Queries.GetUser(ctx, id); err == nil {
			if name := strings.TrimSpace(user.Name); name != "" {
				return name, slack.AssigneeMember
			}
		}
		return "", ""
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: id, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return "", ""
		}
		return strings.TrimSpace(agent.Name), slack.AssigneeAgent
	case "squad":
		squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID: id, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return "", ""
		}
		return strings.TrimSpace(squad.Name), slack.AssigneeSquad
	default:
		return "", ""
	}
}

// ---------------------------------------------------------------------------
// URLs
// ---------------------------------------------------------------------------

// slackAppBaseURL is the FRONTEND origin Agora links live on — never the
// API's, which would render a JSON 404 for a human who clicks it. Same
// precedence the notification links use, so a link we unfurl and a link we
// post are the same string.
func slackAppBaseURL(publicURL string) string {
	if base := normalizePublicURL(os.Getenv("AGORA_APP_URL")); base != "" {
		return base
	}
	if base := normalizePublicURL(os.Getenv("FRONTEND_ORIGIN")); base != "" {
		return base
	}
	return normalizePublicURL(publicURL)
}

// slackAppHost is the host component of the app origin, lower-cased, for the
// unfurl host fence. "" when the deployment has no configured origin — the
// caller then skips the fence rather than refusing every link.
func slackAppHost(publicURL string) string {
	base := slackAppBaseURL(publicURL)
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// slackProjectURL builds https://<app host>/{workspaceSlug}/projects/{id} —
// the shape ParseAgoraLink recognises.
func slackProjectURL(publicURL, slug, projectID string) string {
	base := slackAppBaseURL(publicURL)
	if base == "" || strings.TrimSpace(slug) == "" || strings.TrimSpace(projectID) == "" {
		return ""
	}
	return base + "/" + strings.TrimSpace(slug) + "/projects/" + strings.TrimSpace(projectID)
}

// slackUserLinkPromptURL is the `user_auth_url` Slack opens when it shows the
// private "connect your account" prompt.
//
// It points at the Notifications settings page, NOT at
// POST /api/me/links/slack/begin: Slack opens this URL in the viewer's
// browser, which carries no Agora session (the viewer may not even have an
// account yet). A URL that started an OAuth redirect without a session could
// not know which Agora user to bind — so the destination is the page that
// signs them in and then calls the begin endpoint from a session that exists.
// That page is also where they can see what connecting does before doing it.
func (h *Handler) slackUserLinkPromptURL(ctx context.Context, wsID pgtype.UUID) string {
	slug := ""
	if ws, err := h.Queries.GetWorkspace(ctx, wsID); err == nil {
		slug = ws.Slug
	}
	return slackSettingsURLForTab(slug, "notifications", url.Values{"connect": {"slack"}})
}
