package handler

// Slack outbound fanout — bus event in, channel message or DM out.
//
// The shape of this file is dictated by two facts:
//
//  1. The event bus is SYNCHRONOUS and on the HTTP request path
//     (internal/events/bus.go). So the exported entry point destructures,
//     spawns and returns; everything that touches the database or the network
//     happens on a detached, bounded goroutine — and the delivery itself is
//     handed to the worker in integrations/slack, which paces and coalesces.
//  2. Mute inheritance is free and must stay free. Personal DMs hang off
//     `inbox:new`, and a muted notification never creates an inbox item, so a
//     user's existing notification preferences already govern their Slack DMs
//     with zero new configuration surface. The one thing this file must never
//     do is invent a second way to be notified that bypasses that.
//
// Channel routes are the other half: workspace-level, configured by an admin
// (slack_routes.go), and quiet by default — a channel hears about the team's
// work only when something needs a human.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// slackInstallationStatusActive is the only status that may deliver.
const slackInstallationStatusActive = "active"

// slackFanoutTimeout bounds one event's resolution work (issue load, route
// list, IM open). The delivery itself outlives it: the worker owns its own
// context so a coalescing window is not cut short by the event that opened it.
const slackFanoutTimeout = 15 * time.Second

// slackNotifyFlagKey gates the whole fanout per project. Default true (the
// registry says so) — an admin who wired a channel expects it to speak — but
// project-scoped, so one noisy project can be silenced without uninstalling.
const slackNotifyFlagKey = "AGORA_SLACK_NOTIFY_ENABLED"

// ---------------------------------------------------------------------------
// Delivery worker lifetime
// ---------------------------------------------------------------------------

// The dispatcher is process-wide, like releaseHookClient and the Telegram
// progress relay: it holds per-channel rate-limit state that MUST be shared by
// every caller, because Slack's 1 msg/sec/channel budget is per Slack channel,
// not per Agora request.
var (
	slackDeliveryMu sync.Mutex
	slackDelivery   *slack.Dispatcher
)

// StartSlackDelivery starts the outbound worker. Called once from cmd/server
// beside the listener registration; ctx is the process lifetime, so cancelling
// it abandons in-flight waits instead of posting after shutdown.
func (h *Handler) StartSlackDelivery(ctx context.Context) {
	h.startSlackDelivery(ctx, slack.Options{})
}

// startSlackDelivery is StartSlackDelivery with injectable options (tests run
// the whole machine on a fake clock).
func (h *Handler) startSlackDelivery(ctx context.Context, opts slack.Options) *slack.Dispatcher {
	if opts.OnTokenInvalid == nil {
		opts.OnTokenInvalid = func(installationID string) {
			// A dead token is the same fact app_uninstalled carries. Mark it
			// once, here, so the next event does not queue against a token
			// Slack will never honour again.
			h.markSlackInstallationRevoked(context.WithoutCancel(ctx), installationID)
		}
	}
	d := slack.NewDispatcher(ctx, newSlackAPIClient(), opts)
	slackDeliveryMu.Lock()
	previous := slackDelivery
	slackDelivery = d
	slackDeliveryMu.Unlock()
	if previous != nil {
		previous.Stop()
	}
	return d
}

// slackDispatcher returns the running worker, or nil when delivery has not
// been started (unit tests, or a deployment with Slack switched off).
func slackDispatcher() *slack.Dispatcher {
	slackDeliveryMu.Lock()
	defer slackDeliveryMu.Unlock()
	return slackDelivery
}

// StopSlackDelivery drains the worker. Used by graceful shutdown and tests.
func (h *Handler) StopSlackDelivery() {
	slackDeliveryMu.Lock()
	d := slackDelivery
	slackDelivery = nil
	slackDeliveryMu.Unlock()
	d.Stop()
}

// ---------------------------------------------------------------------------
// The fanout
// ---------------------------------------------------------------------------

// SlackNotification is one bus event, destructured by the cmd/server
// subscriber into the facts the fanout needs. Keeping the bus payload shapes
// in cmd/server and the product logic here means neither file has to know the
// other's vocabulary.
type SlackNotification struct {
	// EventType is the protocol event constant that fired.
	EventType   string
	WorkspaceID string
	IssueID     string
	ActorType   string
	ActorID     string
	// Verdict carries "pass" / "fail" for QA and review events.
	Verdict string
	// Status / PrevStatus describe an issue:updated status transition.
	Status     string
	PrevStatus string
	// InboxType is the inbox item type for inbox:new ("issue_assigned",
	// "mention", "comment", …) and decides both the DM copy and whether a
	// channel kind exists at all.
	InboxType string
	// RecipientType / RecipientID name the inbox item's owner. A DM goes to
	// members only; an agent has no Slack account to talk to.
	RecipientType string
	RecipientID   string
	// Suppressed mirrors EventPayloadSuppressExternalNotifications: a dev or
	// E2E fixture event that must reach websocket subscribers and nothing
	// outside this deployment.
	Suppressed bool
}

// SlackNotify is the bus subscriber's entry point. It returns immediately.
func (h *Handler) SlackNotify(n SlackNotification) {
	if h == nil || h.Queries == nil || !slackEnabled() {
		return
	}
	if n.Suppressed || strings.TrimSpace(n.WorkspaceID) == "" || strings.TrimSpace(n.IssueID) == "" {
		return
	}
	if slackKindForNotification(n) == "" && !n.isDMCandidate() {
		// Nothing this event could ever deliver — skip before spending a
		// goroutine and an issue load on it.
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("slack notify: panic recovered", "recovered", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), slackFanoutTimeout)
		defer cancel()
		h.fanOutSlackNotification(ctx, n)
	}()
}

// isDMCandidate reports whether this event can produce a personal DM at all.
func (n SlackNotification) isDMCandidate() bool {
	return n.EventType == protocol.EventInboxNew && n.RecipientType == "member"
}

// fanOutSlackNotification resolves the issue, the routes and the recipients
// and hands each delivery to the worker. Synchronous — so a test can call it
// directly and assert what was queued — and best-effort throughout: a miss
// costs one notification, never the write that triggered it.
func (h *Handler) fanOutSlackNotification(ctx context.Context, n SlackNotification) {
	wsUUID, err := util.ParseUUID(n.WorkspaceID)
	if err != nil {
		return
	}
	issueUUID, err := util.ParseUUID(n.IssueID)
	if err != nil {
		return
	}
	issue, err := h.Queries.GetIssue(ctx, issueUUID)
	if err != nil {
		return
	}
	// Multi-tenancy: the event names a workspace, the issue names a workspace,
	// and a mismatch means the payload is not what it claims to be.
	if uuidToString(issue.WorkspaceID) != uuidToString(wsUUID) {
		return
	}
	if !config.BoolFrom(h.projectConfigOverrides(ctx, issue), slackNotifyFlagKey) {
		return
	}

	install, ok := h.activeSlackInstallation(ctx, wsUUID)
	if !ok {
		return
	}
	token, err := h.openSlackBotToken(install)
	if err != nil {
		slog.Warn("slack notify: cannot open bot token", "workspace_id", n.WorkspaceID, "error", err)
		return
	}

	item := h.slackIssueItem(ctx, issue)
	actor := h.slackActorLabel(ctx, issue.WorkspaceID, n.ActorType, n.ActorID)

	if n.isDMCandidate() {
		h.deliverSlackDM(ctx, install, token, n, item, actor)
	}
	h.deliverSlackChannels(ctx, install, token, issue, n, item, actor)
}

// deliverSlackChannels queues one message per matching channel route.
//
// Routes are workspace-level and project-scoped: a route with a NULL
// project_id hears every project, one with a project_id hears only that
// project's issues.
func (h *Handler) deliverSlackChannels(
	ctx context.Context, install db.SlackInstallation, token string,
	issue db.Issue, n SlackNotification, item slack.Item, actor string,
) {
	kind := slackKindForNotification(n)
	if kind == "" {
		return
	}
	routes, err := h.Queries.ListEnabledSlackChannelRoutesByWorkspace(ctx, issue.WorkspaceID)
	if err != nil || len(routes) == 0 {
		return
	}
	dispatcher := slackDispatcher()
	if dispatcher == nil {
		return
	}
	wording := slackCopyFor(kind, n)
	for _, route := range routes {
		if uuidToString(route.InstallationID) != uuidToString(install.ID) {
			continue
		}
		if !slackRouteMatchesEvent(route.Events, kind) {
			continue
		}
		if route.ProjectID.Valid && uuidToString(route.ProjectID) != uuidToString(issue.ProjectID) {
			continue
		}
		dispatcher.Enqueue(slack.Message{
			InstallationID: uuidToString(install.ID),
			Token:          token,
			ChannelID:      route.ChannelID,
			Kind:           kind,
			Emoji:          wording.emoji,
			Singular:       wording.singular,
			Plural:         wording.plural,
			Actor:          actor,
			Detail:         wording.detail,
			Item:           item,
		})
	}
}

// deliverSlackDM queues a personal DM for one inbox item.
//
// This is where mute inheritance pays off: the item exists only because the
// recipient's notification preferences allowed it, so there is nothing to
// check here and no second preference surface to keep in sync.
func (h *Handler) deliverSlackDM(
	ctx context.Context, install db.SlackInstallation, token string,
	n SlackNotification, item slack.Item, actor string,
) {
	dispatcher := slackDispatcher()
	if dispatcher == nil {
		return
	}
	userID := strings.TrimSpace(n.RecipientID)
	if userID == "" {
		return
	}
	slackUserID, err := h.slackUserIDByUserID(ctx, userID)
	if err != nil || slackUserID == "" {
		return // not linked: nothing to DM, and nothing to complain about
	}
	imChannel, err := h.slackIMChannel(ctx, install, token, slackUserID)
	if err != nil || imChannel == "" {
		if slack.IsTokenInvalid(err) {
			h.markSlackInstallationRevoked(ctx, uuidToString(install.ID))
		}
		return
	}
	kind := slackDMKindFor(n.InboxType)
	wording := slackCopyFor(kind, n)
	dispatcher.Enqueue(slack.Message{
		InstallationID: uuidToString(install.ID),
		Token:          token,
		ChannelID:      imChannel,
		Kind:           kind,
		Emoji:          wording.emoji,
		Singular:       wording.singular,
		Plural:         wording.plural,
		Actor:          actor,
		Detail:         wording.detail,
		Item:           item,
	})
}

// ---------------------------------------------------------------------------
// Event → product vocabulary
// ---------------------------------------------------------------------------

// slackKindForNotification maps a bus event onto the CHANNEL route kind it
// can fire, or "" when this event routes nowhere.
//
// Two mappings are deliberately lossy, and both are the noise rule:
//
//   - `commented` is never a channel kind. A comment is context for one
//     person; in a channel it is the thing that makes people mute.
//   - A PASSING QA or review verdict is not delivered to a channel. The plan's
//     default is "fail only" for both, and a channel that also hears every
//     green gate is a channel nobody reads by Thursday. Teams that want the
//     good news route `agent_done`, which is exactly that signal.
func slackKindForNotification(n SlackNotification) string {
	switch n.EventType {
	case protocol.EventTaskFailed:
		return slackEventFailed
	case protocol.EventTaskCompleted:
		return slackEventAgentDone
	case protocol.EventIssueCreated:
		return slackEventCreated
	case protocol.EventIssueUpdated:
		if strings.TrimSpace(n.Status) == "" || n.Status == n.PrevStatus {
			return ""
		}
		return slackEventStatusChanged
	case protocol.EventQAEvidenceReady:
		if !slackVerdictIsFail(n.Verdict) {
			return ""
		}
		return slackEventQAVerdict
	case protocol.EventReviewVerdict:
		if !slackVerdictIsFail(n.Verdict) {
			return ""
		}
		return slackEventReviewVerdict
	case protocol.EventInboxNew:
		switch n.InboxType {
		case "issue_assigned":
			return slackEventAssigned
		case "mention":
			return slackEventMentioned
		default:
			return ""
		}
	default:
		return ""
	}
}

// slackVerdictIsFail reports whether a QA / review verdict is the one a
// channel is meant to hear.
func slackVerdictIsFail(verdict string) bool {
	return strings.EqualFold(strings.TrimSpace(verdict), "fail")
}

// slackDMKindFor maps an inbox item type onto the DM copy. Unlike channels,
// DMs cover EVERY inbox type — the item's existence already proves the
// recipient wants it — so unknown types get a generic headline rather than
// being dropped.
func slackDMKindFor(inboxType string) string {
	switch inboxType {
	case "issue_assigned":
		return slackEventAssigned
	case "mention":
		return slackEventMentioned
	case "comment":
		return slackEventCommented
	case "task_failed":
		return slackEventFailed
	default:
		return slackDMKindUpdate
	}
}

// slackDMKindUpdate is the catch-all DM kind. It is not a route kind: no
// channel can subscribe to it.
const slackDMKindUpdate = "update"

// slackMessageCopy is one kind's wording.
type slackMessageCopy struct {
	emoji    string
	singular string
	plural   string
	detail   string
}

// slackCopyFor returns the headline for a kind. Compact on purpose: the
// channel line says what happened and to which issue, and the issue page says
// everything else.
func slackCopyFor(kind string, n SlackNotification) slackMessageCopy {
	switch kind {
	case slackEventFailed:
		return slackMessageCopy{emoji: ":x:", singular: "Task failed", plural: "tasks failed"}
	case slackEventQAVerdict:
		return slackMessageCopy{emoji: ":rotating_light:", singular: "QA failed", plural: "QA failures"}
	case slackEventReviewVerdict:
		return slackMessageCopy{emoji: ":mag:", singular: "Review requested changes", plural: "reviews requested changes"}
	case slackEventAgentDone:
		return slackMessageCopy{emoji: ":white_check_mark:", singular: "Task completed", plural: "tasks completed"}
	case slackEventAssigned:
		return slackMessageCopy{emoji: ":inbox_tray:", singular: "Issue assigned", plural: "issues assigned"}
	case slackEventMentioned:
		return slackMessageCopy{emoji: ":speech_balloon:", singular: "Mentioned", plural: "mentions"}
	case slackEventCommented:
		return slackMessageCopy{emoji: ":memo:", singular: "New comment", plural: "new comments"}
	case slackEventCreated:
		return slackMessageCopy{emoji: ":sparkles:", singular: "Issue created", plural: "issues created"}
	case slackEventStatusChanged:
		detail := ""
		if from, to := strings.TrimSpace(n.PrevStatus), strings.TrimSpace(n.Status); to != "" {
			if from != "" {
				detail = from + " → " + to
			} else {
				detail = to
			}
		}
		return slackMessageCopy{emoji: ":arrows_counterclockwise:", singular: "Status changed", plural: "status changes", detail: detail}
	default:
		return slackMessageCopy{emoji: ":bell:", singular: "Update", plural: "updates"}
	}
}

// ---------------------------------------------------------------------------
// Resolution helpers
// ---------------------------------------------------------------------------

// slackIssueItem renders the one issue a notification names.
func (h *Handler) slackIssueItem(ctx context.Context, issue db.Issue) slack.Item {
	identifier := h.issueKey(ctx, issue)
	if identifier == "" {
		identifier = fmt.Sprintf("#%d", issue.Number)
	}
	slug := ""
	if ws, err := h.Queries.GetWorkspace(ctx, issue.WorkspaceID); err == nil {
		slug = ws.Slug
	}
	return slack.Item{
		ID:         uuidToString(issue.ID),
		Identifier: identifier,
		Title:      issue.Title,
		URL:        slackIssueURL(h.cfg.PublicURL, slug, identifier),
	}
}

// slackIssueURL builds the canonical Agora issue link:
// https://<app host>/{workspaceSlug}/issues/{IDENT}.
//
// Canonical on purpose — it is the exact shape PR 3's unfurl parser
// recognises, so a link copied out of a Slack notification unfurls when a
// teammate pastes it back. The host is the FRONTEND origin, never the API's:
// the API's would render a JSON 404 for a human who clicks it.
func slackIssueURL(publicURL, slug, identifier string) string {
	base := normalizePublicURL(os.Getenv("AGORA_APP_URL"))
	if base == "" {
		base = normalizePublicURL(os.Getenv("FRONTEND_ORIGIN"))
	}
	if base == "" {
		base = normalizePublicURL(publicURL)
	}
	if base == "" || strings.TrimSpace(slug) == "" || strings.TrimSpace(identifier) == "" {
		return ""
	}
	return base + "/" + strings.TrimSpace(slug) + "/issues/" + strings.TrimSpace(identifier)
}

// slackActorLabel names who acted, for the small grey line under a card.
// Best-effort: an unresolvable actor means no line, never a wrong name.
func (h *Handler) slackActorLabel(ctx context.Context, workspaceID pgtype.UUID, actorType, actorID string) string {
	actorType = strings.TrimSpace(actorType)
	actorID = strings.TrimSpace(actorID)
	if actorType == "" || actorID == "" {
		return ""
	}
	id, err := util.ParseUUID(actorID)
	if err != nil {
		return ""
	}
	name := ""
	switch actorType {
	case "member", "user":
		// Event payloads carry a USER id in some paths and a member id in
		// others; try the member row first, then the user directly.
		if member, err := h.Queries.GetMember(ctx, id); err == nil {
			if u, err := h.Queries.GetUser(ctx, member.UserID); err == nil {
				name = strings.TrimSpace(u.Name)
			}
		}
		if name == "" {
			if u, err := h.Queries.GetUser(ctx, id); err == nil {
				name = strings.TrimSpace(u.Name)
			}
		}
	case "agent":
		if a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: id, WorkspaceID: workspaceID,
		}); err == nil {
			name = strings.TrimSpace(a.Name)
		}
	}
	if name == "" {
		return ""
	}
	return "by " + name
}

// activeSlackInstallation returns the workspace's live installation.
//
// A workspace has at most one row per Slack team; when an agency installed the
// app from two teams the first active row wins, which is the same answer the
// Settings UI shows as "connected".
func (h *Handler) activeSlackInstallation(ctx context.Context, wsUUID pgtype.UUID) (db.SlackInstallation, bool) {
	rows, err := h.Queries.ListSlackInstallationsByWorkspace(ctx, wsUUID)
	if err != nil {
		return db.SlackInstallation{}, false
	}
	for _, row := range rows {
		if row.Status == slackInstallationStatusActive {
			return row, true
		}
	}
	return db.SlackInstallation{}, false
}

// openSlackBotToken unseals an installation's bot token. The plaintext lives
// only for the length of one fanout and is never logged.
func (h *Handler) openSlackBotToken(install db.SlackInstallation) (string, error) {
	box, err := slackSealBox()
	if err != nil {
		return "", err
	}
	plain, err := box.Open(install.BotTokenEncrypted)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// markSlackInstallationRevoked stops an installation whose token Slack has
// rejected. Idempotent, and deliberately quiet: the row is marked, never
// deleted, so a re-install flips it back and the audit trail survives.
func (h *Handler) markSlackInstallationRevoked(ctx context.Context, installationID string) {
	id, err := util.ParseUUID(installationID)
	if err != nil {
		return
	}
	if err := h.Queries.SetSlackInstallationStatus(ctx, db.SetSlackInstallationStatusParams{
		ID:     id,
		Status: "revoked",
	}); err != nil {
		slog.Warn("slack notify: failed to mark installation revoked", "installation_id", installationID, "error", err)
		return
	}
	slog.Info("slack notify: installation marked revoked after a rejected token", "installation_id", installationID)
}

// slackUserIDByUserID returns the Slack user id linked to an Agora user, or ""
// when they have not connected Slack. The reverse of the lookup the OAuth
// callback writes; kept here rather than in external_identity.go so the Slack
// integration stays one self-contained set of files.
func (h *Handler) slackUserIDByUserID(ctx context.Context, userID string) (string, error) {
	var externalID string
	err := h.DB.QueryRow(ctx,
		`SELECT external_id FROM user_external_identity WHERE provider = $1 AND user_id = $2::uuid LIMIT 1`,
		providerSlack, userID).Scan(&externalID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(externalID), nil
}

// slackIMCache remembers the IM channel for a Slack user so a burst of inbox
// items costs one conversations.open instead of one per item. Bounded: past
// the cap the map is cleared wholesale rather than growing for the life of the
// process — an IM channel id is cheap to re-resolve and nothing breaks if we
// forget one.
var (
	slackIMCacheMu sync.Mutex
	slackIMCache   = map[string]string{}
)

const slackIMCacheMax = 1024

// slackIMChannel resolves (and caches) the DM channel with one Slack user.
func (h *Handler) slackIMChannel(ctx context.Context, install db.SlackInstallation, token, slackUserID string) (string, error) {
	key := uuidToString(install.ID) + "|" + slackUserID
	slackIMCacheMu.Lock()
	cached, ok := slackIMCache[key]
	slackIMCacheMu.Unlock()
	if ok {
		return cached, nil
	}
	res, err := newSlackAPIClient().ConversationsOpen(ctx, token, slackUserID)
	if err != nil {
		return "", err
	}
	channel := strings.TrimSpace(res.Channel.ID)
	if channel == "" {
		return "", nil
	}
	slackIMCacheMu.Lock()
	if len(slackIMCache) >= slackIMCacheMax {
		slackIMCache = map[string]string{}
	}
	slackIMCache[key] = channel
	slackIMCacheMu.Unlock()
	return channel, nil
}
