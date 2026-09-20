package slack

// Slack OAuth v2 (https://docs.slack.dev/authentication/installing-with-oauth).
//
// The flow is the ordinary redirect dance, with two Agora-specific rules:
//
//   - `state` is a sealed blob minted by the handler, never a table row. Slack's
//     instruction is blunt — "If it doesn't match what you sent, consider the
//     authorization a forgery" — and the seal is what proves we minted it.
//     (Lark needs a token table because its binding token is handed to a third
//     party inside a chat message; an OAuth state never leaves the browser.)
//   - `user_scope` is requested for one reason only: the oauth.v2.access
//     response then carries `authed_user.id`, which is how the installer gets
//     bound to their Agora account in the same transaction as the install. The
//     user token itself is discarded and never stored.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultAuthorizeURL is where the browser is sent to consent.
const DefaultAuthorizeURL = "https://slack.com/oauth/v2/authorize"

// DefaultBotScopes is Phase 1's scope set — minimal by construction, and
// notable for what is absent: no `*:history` scope of any kind. An admin
// reading the consent screen can see the app cannot read their messages, and
// the 2025 unlisted-app rate limits (which bite only conversations.history /
// conversations.replies) cannot apply to us.
//
//	chat:write         post as the app
//	chat:write.public  post to public channels it has not been invited to
//	links:read         receive link_shared
//	links:write        answer with chat.unfurl
//	channels:read      list public channels for the route picker
//	groups:read        list private channels the app is in
//	im:write           open a DM for personal notifications
//	team:read          resolve the team name for display
var DefaultBotScopes = []string{
	"chat:write",
	"chat:write.public",
	"links:read",
	"links:write",
	"channels:read",
	"groups:read",
	"im:write",
	"team:read",
}

// DefaultUserScopes is requested solely so the access response carries
// authed_user.id. The user token is discarded; only the Slack user id is
// stored, as a user_external_identity external_id.
var DefaultUserScopes = []string{"users:read"}

// OAuthConfig is the deployment's Slack app credentials plus the redirect the
// app is registered with. Agora Cloud ships one Slack app registered to the
// cloud domain; self-hosters bring their own (Slack caps an app at five
// unfurl domains and changing them forces a re-install, so one shared app
// cannot serve every deployment's domain).
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// BotScopes / UserScopes default to the package defaults when empty.
	BotScopes  []string
	UserScopes []string
	// AuthorizeBaseURL overrides DefaultAuthorizeURL (tests only).
	AuthorizeBaseURL string
}

// botScopes / userScopes apply the defaults without mutating the caller's
// config, so a zero-value OAuthConfig is usable.
func (cfg OAuthConfig) botScopes() []string {
	if len(cfg.BotScopes) > 0 {
		return cfg.BotScopes
	}
	return DefaultBotScopes
}

func (cfg OAuthConfig) userScopes() []string {
	if len(cfg.UserScopes) > 0 {
		return cfg.UserScopes
	}
	return DefaultUserScopes
}

// AuthorizeURL builds the consent URL for one install attempt. `state` is
// opaque here — minting and verifying it is the handler's job.
func (cfg OAuthConfig) AuthorizeURL(state string) string {
	base := strings.TrimSpace(cfg.AuthorizeBaseURL)
	if base == "" {
		base = DefaultAuthorizeURL
	}
	q := url.Values{}
	q.Set("client_id", cfg.ClientID)
	q.Set("scope", strings.Join(cfg.botScopes(), ","))
	q.Set("user_scope", strings.Join(cfg.userScopes(), ","))
	q.Set("state", state)
	if cfg.RedirectURI != "" {
		// Sent on both legs or neither: Slack compares them, and a mismatch
		// fails the exchange with bad_redirect_uri after the user has already
		// consented.
		q.Set("redirect_uri", cfg.RedirectURI)
	}
	return base + "?" + q.Encode()
}

// UserAuthorizeURL builds the consent URL for the PERSONAL link flow — the
// growth loop an unfurl's `user_auth_required` prompt sends people into.
//
// It requests `user_scope` and NO bot scopes at all, which matters twice over:
// a non-admin must not be shown a consent screen that installs the app (Slack
// would mint a new bot token and re-run the install), and the screen they do
// see says "Agora wants to know who you are", which is the whole truth. The
// user token that comes back is discarded; only `authed_user.id` is kept.
func (cfg OAuthConfig) UserAuthorizeURL(state string) string {
	base := strings.TrimSpace(cfg.AuthorizeBaseURL)
	if base == "" {
		base = DefaultAuthorizeURL
	}
	q := url.Values{}
	q.Set("client_id", cfg.ClientID)
	q.Set("user_scope", strings.Join(cfg.userScopes(), ","))
	q.Set("state", state)
	if cfg.RedirectURI != "" {
		q.Set("redirect_uri", cfg.RedirectURI)
	}
	return base + "?" + q.Encode()
}

// OAuthTeam identifies the Slack workspace an install landed on.
type OAuthTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OAuthAuthedUser is the installing human. Only ID is used; AccessToken is
// present in the wire shape because Slack sends it when user_scope was
// requested, and naming it here documents that we deliberately drop it.
type OAuthAuthedUser struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	AccessToken string `json:"access_token"`
}

// OAuthAccess is the oauth.v2.access response. Team and Enterprise are
// pointers because Slack sends `null` for them on org-level and non-Grid
// installs respectively — dereferencing an assumed object is exactly the
// "parse, don't cast" failure CLAUDE.md warns about, so the accessors below
// are the only supported way to read them.
type OAuthAccess struct {
	slackResponse
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	Scope       string          `json:"scope"`
	BotUserID   string          `json:"bot_user_id"`
	AppID       string          `json:"app_id"`
	Team        *OAuthTeam      `json:"team"`
	Enterprise  *OAuthTeam      `json:"enterprise"`
	AuthedUser  OAuthAuthedUser `json:"authed_user"`
}

// TeamID returns the Slack team id, or "" when Slack omitted the object.
func (a OAuthAccess) TeamID() string {
	if a.Team == nil {
		return ""
	}
	return a.Team.ID
}

// TeamName returns the Slack team display name, or "".
func (a OAuthAccess) TeamName() string {
	if a.Team == nil {
		return ""
	}
	return a.Team.Name
}

// EnterpriseID returns the Enterprise Grid org id, or "" for an ordinary
// single-workspace install.
func (a OAuthAccess) EnterpriseID() string {
	if a.Enterprise == nil {
		return ""
	}
	return a.Enterprise.ID
}

// ExchangeCode trades the single-use `code` for a bot token (the install
// flow).
func (c *APIClient) ExchangeCode(ctx context.Context, cfg OAuthConfig, code string) (OAuthAccess, error) {
	out, err := c.exchange(ctx, cfg, code)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out.AccessToken) == "" || out.TeamID() == "" {
		// `ok:true` with no token or no team is a contract violation, not a
		// success: storing it would create an installation that can never post.
		return out, &APIError{Method: oauthAccessMethod, Code: "invalid_response"}
	}
	return out, nil
}

// ExchangeUserCode trades the code from the PERSONAL link flow for the Slack
// user's identity. No bot scopes were requested, so there is no bot token in
// the response and demanding one (as ExchangeCode does) would reject a
// perfectly good link. The user token Slack returns is deliberately dropped —
// `authed_user.id` is the only thing this flow keeps.
func (c *APIClient) ExchangeUserCode(ctx context.Context, cfg OAuthConfig, code string) (OAuthAccess, error) {
	out, err := c.exchange(ctx, cfg, code)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out.AuthedUser.ID) == "" {
		return out, &APIError{Method: oauthAccessMethod, Code: "invalid_response"}
	}
	return out, nil
}

// oauthAccessMethod is the one endpoint both exchanges call.
const oauthAccessMethod = "oauth.v2.access"

// exchange performs the oauth.v2.access call itself.
//
// oauth.v2.access is form-encoded (not JSON like the rest of the Web API) and
// is the one Web API call that carries no bearer token — the client secret in
// the body is the credential. What counts as a USABLE response differs between
// the install and link flows, so that assertion belongs to the callers above.
func (c *APIClient) exchange(ctx context.Context, cfg OAuthConfig, code string) (OAuthAccess, error) {
	const method = oauthAccessMethod
	var out OAuthAccess

	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return out, &APIError{Method: method, Code: "invalid_client_id"}
	}
	if strings.TrimSpace(code) == "" {
		return out, &APIError{Method: method, Code: "invalid_code"}
	}

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", code)
	if cfg.RedirectURI != "" {
		form.Set("redirect_uri", cfg.RedirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method,
		strings.NewReader(form.Encode()))
	if err != nil {
		return out, fmt.Errorf("slack: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Agora-Slack/1")

	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("slack: %s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return out, fmt.Errorf("slack: read %s response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &APIError{Method: method, StatusCode: resp.StatusCode}
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("slack: decode %s response: %w", method, err)
	}
	if !out.OK {
		return out, &APIError{Method: method, Code: out.Error, StatusCode: resp.StatusCode}
	}
	return out, nil
}
