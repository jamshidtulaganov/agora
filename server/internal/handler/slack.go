package handler

// Slack app integration — configuration gate, installation listing, revoke.
//
// This is the Slack *app* (OAuth v2 bot token, Events API, Block Kit) and is
// deliberately disjoint from the Slack *Incoming Webhook* release connector in
// release_connectors.go, which keeps `release:shipped` / `deploy:recorded` for
// itself. Nothing here touches release_integration rows.
//
// The credential model follows lark_installation / telegram_installation: the
// bot token is sealed with AGORA_SLACK_SECRET_KEY before it reaches the DB,
// never logged, and never returned by any endpoint — not even masked, since a
// partial token still narrows a search.

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// providerSlack is the user_external_identity provider key for a Slack user
// id. Declared here rather than in external_identity.go's const block so the
// Slack integration stays one self-contained set of files.
const providerSlack = "slack"

// ErrSlackSealKeyMissing is returned when an install is attempted without
// AGORA_SLACK_SECRET_KEY. Storing a bot token in plaintext is not an
// acceptable fallback, so the install fails loudly instead — the same posture
// as ErrTelegramSealKeyMissing and lark.NewInstallationService's nil-box
// refusal.
var ErrSlackSealKeyMissing = errors.New("AGORA_SLACK_SECRET_KEY is not set")

// ErrSlackNotConfigured is the degraded-mode error for a deployment missing
// any of the four Slack keys.
var ErrSlackNotConfigured = errors.New("slack integration is not configured")

// slackSealBox loads the secretbox used for bot tokens.
func slackSealBox() (*secretbox.Box, error) {
	key, err := secretbox.LoadKey("AGORA_SLACK_SECRET_KEY")
	if err != nil {
		return nil, ErrSlackSealKeyMissing
	}
	return secretbox.New(key)
}

// Slack app credentials. Read from the environment on every call so an
// operator can rotate a secret without a restart, matching how GetConfig
// re-reads analytics keys.
func slackClientID() string     { return strings.TrimSpace(os.Getenv("AGORA_SLACK_CLIENT_ID")) }
func slackClientSecret() string { return strings.TrimSpace(os.Getenv("AGORA_SLACK_CLIENT_SECRET")) }
func slackSigningSecret() string {
	return strings.TrimSpace(os.Getenv("AGORA_SLACK_SIGNING_SECRET"))
}

// slackEnabled is the four-way gate behind AppConfig.SlackEnabled: seal key,
// client id, client secret, signing secret. All four or nothing — a
// half-configured deployment that showed an install button would die at the
// exchange, after an admin had already granted scopes in Slack.
func slackEnabled() bool {
	if _, err := slackSealBox(); err != nil {
		return false
	}
	return slackClientID() != "" && slackClientSecret() != "" && slackSigningSecret() != ""
}

// SlackEnabled is the exported gate the push-listener registration in
// cmd/server consults before subscribing to the bus (Phase 1, PR 2). Exported
// because cmd/server is a different package; the logic is deliberately the
// same one /api/config reports, so "the UI offers it" and "the server does it"
// can never disagree.
func (h *Handler) SlackEnabled() bool { return slackEnabled() }

// slackAPIBaseURL lets tests (and a self-host proxy) redirect Web API calls,
// the same seam AGORA_LARK_HTTP_BASE_URL provides for Lark. Unset in every
// real deployment.
func slackAPIBaseURL() string { return strings.TrimSpace(os.Getenv("AGORA_SLACK_API_BASE_URL")) }

// newSlackAPIClient builds the Web API client for one request.
func newSlackAPIClient() *slack.APIClient {
	if base := slackAPIBaseURL(); base != "" {
		return slack.NewAPIClient(slack.WithAPIBaseURL(base))
	}
	return slack.NewAPIClient()
}

// slackRedirectURI is the OAuth callback Slack redirects back to. It is built
// from the server's configured public origin, never from request headers: a
// misconfigured reverse proxy must not be able to talk the server into minting
// a redirect at an attacker-controlled host (the same rule Config.PublicURL
// documents for autopilot webhook URLs).
func (h *Handler) slackRedirectURI() string {
	base := strings.TrimRight(strings.TrimSpace(h.cfg.PublicURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("AGORA_PUBLIC_URL")), "/")
	}
	if base == "" {
		return ""
	}
	return base + "/slack/oauth/callback"
}

// slackOAuthConfig assembles the deployment's OAuth config, or an error when
// the deployment is not fully configured.
func (h *Handler) slackOAuthConfig() (slack.OAuthConfig, error) {
	if !slackEnabled() {
		return slack.OAuthConfig{}, ErrSlackNotConfigured
	}
	return slack.OAuthConfig{
		ClientID:     slackClientID(),
		ClientSecret: slackClientSecret(),
		RedirectURI:  h.slackRedirectURI(),
	}, nil
}

// SlackInstallationResponse is the wire shape. It deliberately carries NO
// token field: a caller can see which Slack team a workspace is wired to and
// who wired it, never the credential.
type SlackInstallationResponse struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspace_id"`
	TeamID          string   `json:"team_id"`
	TeamName        string   `json:"team_name"`
	EnterpriseID    string   `json:"enterprise_id,omitempty"`
	AppID           string   `json:"app_id"`
	BotUserID       string   `json:"bot_user_id"`
	Scopes          []string `json:"scopes"`
	InstallerUserID string   `json:"installer_user_id"`
	Status          string   `json:"status"`
	InstalledAt     string   `json:"installed_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func slackInstallationToResponse(row db.SlackInstallation) SlackInstallationResponse {
	resp := SlackInstallationResponse{
		ID:              uuidToString(row.ID),
		WorkspaceID:     uuidToString(row.WorkspaceID),
		TeamID:          row.TeamID,
		TeamName:        row.TeamName,
		EnterpriseID:    row.EnterpriseID,
		AppID:           row.AppID,
		BotUserID:       row.BotUserID,
		Scopes:          splitSlackScopes(row.Scopes),
		InstallerUserID: uuidToString(row.InstallerUserID),
		Status:          row.Status,
	}
	if row.InstalledAt.Valid {
		resp.InstalledAt = row.InstalledAt.Time.UTC().Format(time.RFC3339)
	}
	if row.UpdatedAt.Valid {
		resp.UpdatedAt = row.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// splitSlackScopes turns Slack's comma-separated scope string into a list the
// UI can diff against what a newer Agora build needs.
func splitSlackScopes(raw string) []string {
	out := []string{}
	for _, scope := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(scope); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ListSlackInstallations handles GET /api/workspaces/{id}/slack/installations.
//
// Member-visible, like the Lark and Telegram lists: the Integrations tab must
// not render blank for a non-admin. Nothing here is a management handle (every
// write route re-checks owner/admin) and the response carries no token.
//
// `configured` reports whether an install could succeed at all. Without it the
// UI would offer a button that dies at the last step, after the admin has
// already granted scopes in Slack.
func (h *Handler) ListSlackInstallations(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListSlackInstallationsByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list slack installations")
		return
	}
	out := make([]SlackInstallationResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, slackInstallationToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installations": out,
		"configured":    slackEnabled(),
	})
}

// RevokeSlackInstallation handles
// DELETE /api/workspaces/{id}/slack/installations/{installationId}.
//
// Flips status to 'revoked' and drops the installation's channel routes; the
// row itself is preserved for audit and a re-install flips it back.
//
// It deliberately does NOT call apps.uninstall: one Slack team may back several
// Agora workspaces, which legitimately share one bot token, so uninstalling on
// behalf of one workspace would silently break its siblings. Only the last
// active row for a team may uninstall, and the query that answers "is this the
// last one" already exists (CountOtherActiveSlackInstallationsForTeam) for the
// delivery path that will make that call.
func (h *Handler) RevokeSlackInstallation(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	instUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "installationId"), "installation id")
	if !ok {
		return
	}
	// Workspace-scoped lookup: a forged installation id from another workspace
	// is a 404, not a revoke.
	row, err := h.Queries.GetSlackInstallationInWorkspace(r.Context(), db.GetSlackInstallationInWorkspaceParams{
		ID:          instUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "slack installation not found")
		return
	}
	// Both writes use row.ID — the resolved entity — never the raw URL string.
	if err := h.Queries.DeleteSlackChannelRoutesForInstallation(r.Context(), db.DeleteSlackChannelRoutesForInstallationParams{
		InstallationID: row.ID,
		WorkspaceID:    row.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove slack routes")
		return
	}
	if err := h.Queries.SetSlackInstallationStatus(r.Context(), db.SetSlackInstallationStatusParams{
		ID:     row.ID,
		Status: "revoked",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke slack installation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
