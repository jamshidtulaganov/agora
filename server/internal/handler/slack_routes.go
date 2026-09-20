package handler

// Slack channel routes — which Agora events reach which Slack channel.
//
//	GET    /api/workspaces/{id}/slack/routes             (owner/admin)
//	PUT    /api/workspaces/{id}/slack/routes             (owner/admin, human)
//	PUT    /api/workspaces/{id}/slack/routes/{routeId}   (owner/admin, human)
//	DELETE /api/workspaces/{id}/slack/routes/{routeId}   (owner/admin, human)
//	GET    /api/workspaces/{id}/slack/channels           (owner/admin) — picker
//
// Slack routing is CONFIGURED, not derived. A Telegram bot belongs to an agent,
// so Telegram can resolve a destination from the issue's assignee; a Slack app
// belongs to the workspace, so an admin picks the channels and the events each
// one hears.
//
// The vocabulary below is product-level, not protocol-level: a route stores
// "failed", never "task:failed". The bus event constants map onto these names
// in slack_notify.go, so renaming a wire event does not rewrite every stored
// route — the same separation release_integration.events[] already uses.
//
// The default set is the whole noise rule. Every competitor's Slack app dies
// the same way: volume. A fresh route hears only the three events that mean
// "something needs a human"; everything else is one checkbox away and stays
// the admin's decision.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Route event kinds. These strings are stored in slack_channel_route.events[]
// and are part of the API contract with the Settings UI (PR 4).
const (
	// slackEventAssigned — an issue was assigned to someone.
	slackEventAssigned = "assigned"
	// slackEventMentioned — someone was @-mentioned.
	slackEventMentioned = "mentioned"
	// slackEventCommented — a new comment. DM-ONLY by construction: a comment
	// is context for one person, and in a channel it is the thing that makes
	// people mute. It is deliberately absent from slackChannelRouteEvents.
	slackEventCommented = "commented"
	// slackEventFailed — an agent task failed.
	slackEventFailed = "failed"
	// slackEventQAVerdict — a QA gate landed a verdict (failures only, see
	// slack_notify.go).
	slackEventQAVerdict = "qa_verdict"
	// slackEventReviewVerdict — a code review landed a verdict (changes
	// requested only).
	slackEventReviewVerdict = "review_verdict"
	// slackEventAgentDone — an agent finished a task.
	slackEventAgentDone = "agent_done"
	// slackEventCreated — an issue was created.
	slackEventCreated = "created"
	// slackEventStatusChanged — an issue changed status.
	slackEventStatusChanged = "status_changed"
)

// slackChannelRouteEvents is every kind a CHANNEL route may subscribe to, in
// the order the UI should offer them (loudest-value first). `commented` is not
// here on purpose.
var slackChannelRouteEvents = []string{
	slackEventFailed,
	slackEventQAVerdict,
	slackEventReviewVerdict,
	slackEventAssigned,
	slackEventMentioned,
	slackEventAgentDone,
	slackEventStatusChanged,
	slackEventCreated,
}

// slackDefaultRouteEvents is what a route hears when the admin picks a channel
// and nothing else: only the events that mean a human is needed.
func slackDefaultRouteEvents() []string {
	return []string{slackEventFailed, slackEventQAVerdict, slackEventReviewVerdict}
}

// isSlackChannelRouteEvent reports whether kind may be stored on a route.
func isSlackChannelRouteEvent(kind string) bool {
	for _, known := range slackChannelRouteEvents {
		if known == kind {
			return true
		}
	}
	return false
}

// normalizeSlackRouteEvents keeps only known channel kinds, de-duplicated and
// in the canonical order. An unknown value — enum drift from a newer client,
// or `commented` which is DM-only — is dropped rather than stored, so a route
// can never carry a kind the fanout does not understand.
func normalizeSlackRouteEvents(in []string) []string {
	wanted := map[string]bool{}
	for _, raw := range in {
		kind := strings.ToLower(strings.TrimSpace(raw))
		if isSlackChannelRouteEvent(kind) {
			wanted[kind] = true
		}
	}
	out := []string{}
	for _, kind := range slackChannelRouteEvents {
		if wanted[kind] {
			out = append(out, kind)
		}
	}
	return out
}

// slackRouteMatchesEvent is the delivery path's predicate: does this route's
// filter include the fired kind? Pure and DB-free, exactly like
// releaseIntegrationMatchesEvent, so route matching is unit-testable.
func slackRouteMatchesEvent(filter []string, kind string) bool {
	if kind == "" {
		return false
	}
	for _, e := range filter {
		if e == kind {
			return true
		}
	}
	return false
}

// SlackChannelRouteResponse is the wire shape of one route.
type SlackChannelRouteResponse struct {
	ID             string   `json:"id"`
	WorkspaceID    string   `json:"workspace_id"`
	InstallationID string   `json:"installation_id"`
	ChannelID      string   `json:"channel_id"`
	ChannelName    string   `json:"channel_name"`
	ProjectID      string   `json:"project_id,omitempty"`
	Events         []string `json:"events"`
	Enabled        bool     `json:"enabled"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func slackChannelRouteToResponse(row db.SlackChannelRoute) SlackChannelRouteResponse {
	events := row.Events
	if events == nil {
		events = []string{}
	}
	return SlackChannelRouteResponse{
		ID:             uuidToString(row.ID),
		WorkspaceID:    uuidToString(row.WorkspaceID),
		InstallationID: uuidToString(row.InstallationID),
		ChannelID:      row.ChannelID,
		ChannelName:    row.ChannelName,
		ProjectID:      uuidToString(row.ProjectID),
		Events:         events,
		Enabled:        row.Enabled,
		CreatedAt:      timestampToString(row.CreatedAt),
		UpdatedAt:      timestampToString(row.UpdatedAt),
	}
}

// slackRouteRequest is the PUT body. Enabled is a pointer so "not mentioned"
// and "explicitly false" stay distinguishable on an update.
type slackRouteRequest struct {
	InstallationID string   `json:"installation_id"`
	ChannelID      string   `json:"channel_id"`
	ChannelName    string   `json:"channel_name"`
	ProjectID      string   `json:"project_id"`
	Events         []string `json:"events"`
	Enabled        *bool    `json:"enabled"`
}

// ListSlackChannelRoutes handles GET /api/workspaces/{id}/slack/routes.
//
// The response ships the vocabulary alongside the rows so the Settings UI does
// not hardcode a list that can drift from the server's: a kind the server has
// not learned yet would otherwise render as a checkbox that silently does
// nothing.
func (h *Handler) ListSlackChannelRoutes(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListSlackChannelRoutesByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list slack routes")
		return
	}
	out := make([]SlackChannelRouteResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, slackChannelRouteToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"routes":           out,
		"available_events": slackChannelRouteEvents,
		"default_events":   slackDefaultRouteEvents(),
	})
}

// PutSlackChannelRoute handles PUT /api/workspaces/{id}/slack/routes and
// PUT /api/workspaces/{id}/slack/routes/{routeId}.
//
// Without a route id it upserts by (installation, channel, project) — the same
// tuple the NULLS NOT DISTINCT unique index enforces, so picking the same
// channel twice edits the existing route instead of failing on a constraint.
// With a route id it edits that row: the row is resolved workspace-scoped
// FIRST and every subsequent write uses the resolved row's own identity, never
// the raw URL string.
func (h *Handler) PutSlackChannelRoute(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, wsIDStr, "workspace not found", "owner", "admin"); !ok {
		return
	}

	var req slackRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// An existing route supplies the identity fields the body may omit, so a
	// toggle-one-checkbox edit does not have to restate the channel.
	var existing db.SlackChannelRoute
	haveExisting := false
	if routeID := strings.TrimSpace(chi.URLParam(r, "routeId")); routeID != "" {
		routeUUID, ok := parseUUIDOrBadRequest(w, routeID, "route id")
		if !ok {
			return
		}
		row, err := h.Queries.GetSlackChannelRouteInWorkspace(r.Context(), db.GetSlackChannelRouteInWorkspaceParams{
			ID:          routeUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			// A forged route id from another workspace is a 404, not an edit.
			writeError(w, http.StatusNotFound, "slack route not found")
			return
		}
		existing = row
		haveExisting = true
	}

	installationID := strings.TrimSpace(req.InstallationID)
	if installationID == "" && haveExisting {
		installationID = uuidToString(existing.InstallationID)
	}
	instUUID, ok := parseUUIDOrBadRequest(w, installationID, "installation id")
	if !ok {
		return
	}
	// Workspace-scoped: a route may only hang off an installation this
	// workspace owns, and only off a live one — a revoked install has no token
	// to deliver with.
	install, err := h.Queries.GetSlackInstallationInWorkspace(r.Context(), db.GetSlackInstallationInWorkspaceParams{
		ID:          instUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "slack installation not found")
		return
	}
	if install.Status != slackInstallationStatusActive {
		writeError(w, http.StatusConflict, "slack installation is revoked; re-install before routing channels")
		return
	}

	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" && haveExisting {
		channelID = existing.ChannelID
	}
	if !validSlackChannelID(channelID) {
		writeError(w, http.StatusBadRequest, "channel_id is required")
		return
	}
	channelName := strings.TrimSpace(strings.TrimPrefix(req.ChannelName, "#"))
	if channelName == "" && haveExisting {
		channelName = existing.ChannelName
	}

	projectUUID, ok := h.resolveSlackRouteProject(w, r, wsUUID, req, existing, haveExisting)
	if !ok {
		return
	}

	// A create with no events at all gets the quiet default; an explicitly
	// empty (or entirely unknown) list is a mistake worth a 400 rather than a
	// route that silently hears nothing.
	var events []string
	switch {
	case req.Events == nil && haveExisting:
		events = existing.Events
	case req.Events == nil:
		events = slackDefaultRouteEvents()
	default:
		events = normalizeSlackRouteEvents(req.Events)
		if len(events) == 0 {
			writeError(w, http.StatusBadRequest,
				"at least one known event is required ("+strings.Join(slackChannelRouteEvents, ", ")+")")
			return
		}
	}

	enabled := true
	if haveExisting {
		enabled = existing.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	createdBy := pgtype.UUID{}
	if haveExisting && existing.CreatedBy.Valid {
		createdBy = existing.CreatedBy
	} else if u, err := parseStrictUUID(userID); err == nil {
		createdBy = u
	}

	row, err := h.Queries.UpsertSlackChannelRoute(r.Context(), db.UpsertSlackChannelRouteParams{
		WorkspaceID:    wsUUID,
		InstallationID: install.ID,
		ChannelID:      channelID,
		ChannelName:    channelName,
		ProjectID:      projectUUID,
		Events:         events,
		Enabled:        enabled,
		CreatedBy:      createdBy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save slack route")
		return
	}
	writeJSON(w, http.StatusOK, slackChannelRouteToResponse(row))
}

// resolveSlackRouteProject validates the optional project scope. An explicit
// empty string clears it (route every project); a project from another
// workspace is a 404, never a cross-tenant route.
func (h *Handler) resolveSlackRouteProject(
	w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID,
	req slackRouteRequest, existing db.SlackChannelRoute, haveExisting bool,
) (pgtype.UUID, bool) {
	raw := strings.TrimSpace(req.ProjectID)
	if raw == "" {
		if haveExisting && existing.ProjectID.Valid {
			return existing.ProjectID, true
		}
		return pgtype.UUID{}, true
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, raw, "project id")
	if !ok {
		return pgtype.UUID{}, false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found in this workspace")
		return pgtype.UUID{}, false
	}
	return projectUUID, true
}

// validSlackChannelID is a shape check, not an existence check: Slack ids are
// short opaque uppercase tokens (C…, G…, D…). Whether the channel exists and
// the bot can post to it is answered by chat.postMessage, and a route to a
// channel the app has not been invited to is a legitimate state the delivery
// path logs.
func validSlackChannelID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// DeleteSlackChannelRoute handles
// DELETE /api/workspaces/{id}/slack/routes/{routeId}.
func (h *Handler) DeleteSlackChannelRoute(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	routeUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "routeId"), "route id")
	if !ok {
		return
	}
	// Resolve first, then delete by the resolved row's id: the convention that
	// keeps a DELETE from answering 204 while matching zero rows (#1661).
	row, err := h.Queries.GetSlackChannelRouteInWorkspace(r.Context(), db.GetSlackChannelRouteInWorkspaceParams{
		ID:          routeUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "slack route not found")
		return
	}
	if err := h.Queries.DeleteSlackChannelRoute(r.Context(), db.DeleteSlackChannelRouteParams{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete slack route")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SlackChannelResponse is one row in the channel picker.
type SlackChannelResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
	IsMember  bool   `json:"is_member"`
}

// ListSlackChannels handles GET /api/workspaces/{id}/slack/channels.
//
// A thin proxy for conversations.list so the picker never needs the bot token
// in the browser. Paging is passed through (`cursor`, `limit`) rather than
// walked server-side: conversations.list is Tier 2 (20+/min) and a workspace
// with hundreds of channels would otherwise spend that budget on one page
// load. It is NOT one of the two methods throttled to 1 req/min on unlisted
// apps — those are conversations.history and conversations.replies — so this
// is safe on an install link that has not been through the Marketplace.
func (h *Handler) ListSlackChannels(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if !slackEnabled() {
		writeError(w, http.StatusServiceUnavailable, "slack integration is not configured")
		return
	}
	install, token, ok := h.resolveSlackInstallationForRequest(w, r, wsUUID)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	res, err := newSlackAPIClient().ConversationsList(r.Context(), token, slack.ConversationsListParams{
		Limit:  limit,
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
	})
	if err != nil {
		// A dead token here is the same fact an app_uninstalled event carries:
		// mark the installation and tell the UI to re-install rather than
		// leaving a picker that fails forever.
		if slack.IsTokenInvalid(err) {
			h.markSlackInstallationRevoked(r.Context(), uuidToString(install.ID))
			writeError(w, http.StatusConflict, "the slack installation is no longer valid; re-install the app")
			return
		}
		if slack.IsRateLimited(err) {
			writeError(w, http.StatusTooManyRequests, "slack is rate limiting channel listing; try again shortly")
			return
		}
		writeError(w, http.StatusBadGateway, "failed to list slack channels")
		return
	}

	channels := make([]SlackChannelResponse, 0, len(res.Channels))
	for _, ch := range res.Channels {
		if strings.TrimSpace(ch.ID) == "" || ch.IsArchived {
			continue
		}
		channels = append(channels, SlackChannelResponse{
			ID:        ch.ID,
			Name:      ch.Name,
			IsPrivate: ch.IsPrivate,
			IsMember:  ch.IsMember,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channels":        channels,
		"next_cursor":     res.NextCursor(),
		"installation_id": uuidToString(install.ID),
	})
}

// resolveSlackInstallationForRequest picks the installation a workspace-scoped
// Slack call speaks through: the one named by ?installation_id=, else the
// workspace's active install. It returns the row and its UNSEALED bot token.
func (h *Handler) resolveSlackInstallationForRequest(
	w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID,
) (db.SlackInstallation, string, bool) {
	var install db.SlackInstallation
	if raw := strings.TrimSpace(r.URL.Query().Get("installation_id")); raw != "" {
		instUUID, ok := parseUUIDOrBadRequest(w, raw, "installation id")
		if !ok {
			return db.SlackInstallation{}, "", false
		}
		row, err := h.Queries.GetSlackInstallationInWorkspace(r.Context(), db.GetSlackInstallationInWorkspaceParams{
			ID:          instUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "slack installation not found")
			return db.SlackInstallation{}, "", false
		}
		install = row
	} else {
		row, ok := h.activeSlackInstallation(r.Context(), wsUUID)
		if !ok {
			writeError(w, http.StatusNotFound, "this workspace has no active slack installation")
			return db.SlackInstallation{}, "", false
		}
		install = row
	}
	if install.Status != slackInstallationStatusActive {
		writeError(w, http.StatusConflict, "the slack installation is revoked; re-install the app")
		return db.SlackInstallation{}, "", false
	}
	token, err := h.openSlackBotToken(install)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "slack credentials cannot be opened on this server")
		return db.SlackInstallation{}, "", false
	}
	return install, token, true
}
