package handler

// Slack OAuth v2 install flow.
//
//	POST /api/workspaces/{id}/slack/install/begin  (owner/admin, human actor)
//	GET  /slack/oauth/callback                     (unauthenticated, signed state)
//
// `state` is a secretbox-sealed {workspace, user, nonce, exp} blob with a
// 10-minute life, not a table row. Slack's own instruction — "If it doesn't
// match what you sent, consider the authorization a forgery" — is satisfied by
// the seal: only this deployment's key can mint one, and the code Slack
// returns is single-use on Slack's side. (Lark needs a token TABLE because its
// binding token is handed to a third party inside a chat message; an OAuth
// state never leaves the installer's browser.)

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"crypto/rand"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// slackStateTTL bounds how long a consent screen may sit open before the
// callback is refused.
const slackStateTTL = 10 * time.Minute

// slackStateKind values. PR 1 mints only "install"; the personal-link flow
// (Phase 1 unfurl, PR 3) adds "link" without changing the envelope, which is
// why the field exists now rather than after the wire shape is in production.
const slackStateKindInstall = "install"

// slackOAuthState is the sealed payload. Short JSON keys keep the state
// parameter well inside URL length limits.
type slackOAuthState struct {
	Kind        string `json:"k"`
	WorkspaceID string `json:"w"`
	UserID      string `json:"u"`
	Nonce       string `json:"n"`
	Exp         int64  `json:"e"`
}

var errSlackStateInvalid = errors.New("slack: oauth state is invalid or expired")

// sealSlackState mints a state parameter. The nonce makes two otherwise
// identical states differ, so a state cannot be recognised by its ciphertext.
func sealSlackState(box *secretbox.Box, state slackOAuthState) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	state.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	if state.Exp == 0 {
		state.Exp = time.Now().Add(slackStateTTL).Unix()
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sealed, err := box.Seal(raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openSlackState reverses sealSlackState. A tampered blob fails GCM
// authentication and an expired one fails the deadline check; both are the
// same answer to the caller — do not trust this callback.
func openSlackState(box *secretbox.Box, raw string) (slackOAuthState, error) {
	var state slackOAuthState
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return state, errSlackStateInvalid
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return state, errSlackStateInvalid
	}
	if err := json.Unmarshal(plain, &state); err != nil {
		return state, errSlackStateInvalid
	}
	if state.WorkspaceID == "" || state.UserID == "" || state.Exp == 0 {
		return state, errSlackStateInvalid
	}
	if time.Now().After(time.Unix(state.Exp, 0)) {
		return state, errSlackStateInvalid
	}
	return state, nil
}

// SlackInstallBeginResponse carries the URL the frontend opens.
type SlackInstallBeginResponse struct {
	AuthorizeURL string `json:"authorize_url"`
}

// BeginSlackInstall handles POST /api/workspaces/{id}/slack/install/begin.
//
// The route is owner/admin + RequireHumanActor: an agent must not be able to
// start an install, because the consent screen it would produce grants a bot
// token for the whole Slack workspace.
func (h *Handler) BeginSlackInstall(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	cfg, err := h.slackOAuthConfig()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable,
			"slack integration is not configured (AGORA_SLACK_CLIENT_ID / AGORA_SLACK_CLIENT_SECRET / AGORA_SLACK_SIGNING_SECRET / AGORA_SLACK_SECRET_KEY)")
		return
	}
	if cfg.RedirectURI == "" {
		// Without a public origin Slack has nowhere to send the user back to,
		// and a redirect derived from request headers would be forgeable.
		writeError(w, http.StatusServiceUnavailable, "AGORA_PUBLIC_URL is not set; slack cannot redirect back")
		return
	}
	box, err := slackSealBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	state, err := sealSlackState(box, slackOAuthState{
		Kind:        slackStateKindInstall,
		WorkspaceID: uuidToString(wsUUID),
		UserID:      userID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint slack install state")
		return
	}
	writeJSON(w, http.StatusOK, SlackInstallBeginResponse{AuthorizeURL: cfg.AuthorizeURL(state)})
}

// slackSettingsURL is where the callback bounces the browser: the workspace's
// Integrations tab, scrolled to Slack. Falls back to the workspace-less
// settings path when the slug is unknown (a state we could not open).
func slackSettingsURL(slug string, params url.Values) string {
	frontend := strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	if frontend == "" {
		frontend = "http://localhost:3000"
	}
	base := strings.TrimRight(frontend, "/")
	if slug != "" {
		base += "/" + url.PathEscape(slug)
	}
	base += "/settings"
	q := url.Values{}
	q.Set("tab", "integrations")
	q.Set("integration", "slack")
	for k, vs := range params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	return base + "?" + q.Encode()
}

// SlackOAuthCallback handles GET /slack/oauth/callback.
//
// Unauthenticated by necessity — the browser arrives from slack.com with no
// Agora session guaranteed — so every authorization fact comes from the sealed
// state, never from the request. Failures redirect with a `slack_error` code
// rather than rendering an API error: the user is in a browser, mid-flow, and
// a JSON body is a dead end for them.
func (h *Handler) SlackOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	fail := func(slug, code string) {
		http.Redirect(w, r, slackSettingsURL(slug, url.Values{"slack_error": {code}}), http.StatusFound)
	}

	// The user pressed Cancel on the consent screen.
	if errCode := strings.TrimSpace(q.Get("error")); errCode != "" {
		fail("", errCode)
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	rawState := strings.TrimSpace(q.Get("state"))
	if code == "" || rawState == "" {
		fail("", "missing_params")
		return
	}
	box, err := slackSealBox()
	if err != nil {
		fail("", "not_configured")
		return
	}
	state, err := openSlackState(box, rawState)
	if err != nil {
		// Slack: "If it doesn't match what you sent, consider the authorization
		// a forgery."
		fail("", "invalid_state")
		return
	}
	cfg, err := h.slackOAuthConfig()
	if err != nil {
		fail("", "not_configured")
		return
	}

	wsUUID, err := parseStrictUUID(state.WorkspaceID)
	if err != nil {
		fail("", "invalid_state")
		return
	}
	installerUUID, err := parseStrictUUID(state.UserID)
	if err != nil {
		fail("", "invalid_state")
		return
	}
	// Slug for the redirect. A workspace deleted while the consent screen was
	// open is a dead end, not an install.
	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		fail("", "workspace_not_found")
		return
	}

	client := newSlackAPIClient()
	access, err := client.ExchangeCode(r.Context(), cfg, code)
	if err != nil {
		// The code, and any token Slack may have returned, stay out of the log:
		// only the fact that the exchange failed is recorded by the redirect.
		fail(ws.Slug, "exchange_failed")
		return
	}
	// Verify the freshly minted token BEFORE storing it, the same discipline
	// InstallAgentTelegramBot applies with getMe: an install that succeeds with
	// a token Slack will not honour fails later, silently, at the moment
	// somebody is waiting for a notification.
	authed, err := client.AuthTest(r.Context(), access.AccessToken)
	if err != nil || authed.TeamID != access.TeamID() {
		fail(ws.Slug, "token_verification_failed")
		return
	}

	sealed, err := box.Seal([]byte(access.AccessToken))
	if err != nil {
		fail(ws.Slug, "seal_failed")
		return
	}

	identityClaimed, err := h.storeSlackInstallation(r.Context(), slackInstallParams{
		WorkspaceID: wsUUID,
		InstallerID: installerUUID,
		SlackUserID: access.AuthedUser.ID,
		SealedToken: sealed,
		Access:      access,
	})
	if err != nil {
		fail(ws.Slug, "store_failed")
		return
	}

	params := url.Values{"slack_installed": {"1"}}
	if identityClaimed {
		// The install itself succeeded; only the personal link did not, because
		// this Slack user id already belongs to a different Agora account. The
		// steal guard in linkExternalIdentity is what refused it, and silently
		// dropping that fact would leave the installer wondering why their DMs
		// never arrive.
		params.Set("slack_warning", "identity_claimed")
	}
	http.Redirect(w, r, slackSettingsURL(ws.Slug, params), http.StatusFound)
}

// slackInstallParams is the post-exchange write set.
type slackInstallParams struct {
	WorkspaceID pgtype.UUID
	InstallerID pgtype.UUID
	SlackUserID string
	SealedToken []byte
	Access      slack.OAuthAccess
}

// storeSlackInstallation writes the installation row and binds the installer's
// Slack identity in ONE transaction: an install whose installer is not linked
// would silently drop that person's DMs, and a link without an install points
// at nothing.
//
// It returns whether the identity link was refused because the Slack user id
// already belongs to another Agora user. That is NOT an error: the
// (provider, external_id) steal guard did its job, no row changed, and the
// workspace's install is still valid — the caller surfaces it as a warning.
func (h *Handler) storeSlackInstallation(ctx context.Context, p slackInstallParams) (identityClaimed bool, err error) {
	if h.TxStarter == nil {
		return false, errors.New("slack: no transaction starter configured")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := h.Queries.WithTx(tx)
	if _, err = qtx.UpsertSlackInstallation(ctx, db.UpsertSlackInstallationParams{
		WorkspaceID:       p.WorkspaceID,
		TeamID:            p.Access.TeamID(),
		TeamName:          p.Access.TeamName(),
		EnterpriseID:      p.Access.EnterpriseID(),
		AppID:             p.Access.AppID,
		BotUserID:         p.Access.BotUserID,
		BotTokenEncrypted: p.SealedToken,
		Scopes:            p.Access.Scope,
		InstallerUserID:   p.InstallerID,
	}); err != nil {
		return false, fmt.Errorf("upsert slack installation: %w", err)
	}

	if strings.TrimSpace(p.SlackUserID) != "" {
		linkErr := linkExternalIdentityOn(ctx, tx, providerSlack, p.SlackUserID, uuidToString(p.InstallerID))
		switch {
		case linkErr == nil:
		case errors.Is(linkErr, errExternalIdentityClaimed):
			// No row changed, the transaction is still healthy — commit the
			// install and report the refusal.
			identityClaimed = true
		default:
			return false, fmt.Errorf("link slack identity: %w", linkErr)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return identityClaimed, nil
}
