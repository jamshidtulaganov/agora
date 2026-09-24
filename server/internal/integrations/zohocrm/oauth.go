package zohocrm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenGrant is the result of exchanging an authorization code: the
// long-lived refresh token to seal at rest plus the granted scope list.
type TokenGrant struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	APIDomain    string `json:"api_domain"`
}

// DCFromLocation maps the `location` parameter Zoho appends to an OAuth
// redirect (the data center that holds the person's account) to a dc id.
func DCFromLocation(location string) (string, bool) {
	dc := strings.ToLower(strings.TrimSpace(location))
	if dc == "" {
		dc = "us"
	}
	return dc, KnownDC(dc)
}

// AuthorizeURL builds the consent-screen URL a person is sent to when they
// connect their own Zoho account (authorization-code flow, offline access so
// a refresh token comes back). accountsBase overrides the DC host for tests
// and the local fake.
func AuthorizeURL(dc, accountsBase, clientID, redirectURI, scopes, state string) (string, error) {
	hosts, ok := DCHosts[dc]
	if !ok {
		return "", fmt.Errorf("zohocrm: unknown dc %q", dc)
	}
	if accountsBase == "" {
		accountsBase = hosts.Accounts
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"scope":         {scopes},
		"redirect_uri":  {redirectURI},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return strings.TrimRight(accountsBase, "/") + "/oauth/v2/auth?" + q.Encode(), nil
}

// ExchangeCode exchanges an authorization code for a refresh token under the
// given OAuth client (grant_type=authorization_code). redirectURI must match
// the one the consent screen was opened with. dc derives the accounts host;
// accountsBase overrides it for tests and the local fake.
func ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI, dc, accountsBase string) (TokenGrant, error) {
	hosts, ok := DCHosts[dc]
	if !ok {
		return TokenGrant{}, fmt.Errorf("zohocrm: unknown dc %q", dc)
	}
	if accountsBase == "" {
		accountsBase = hosts.Accounts
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
	}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(accountsBase, "/")+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return TokenGrant{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return TokenGrant{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var grant struct {
		TokenGrant
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &grant); err != nil {
		return TokenGrant{}, fmt.Errorf("zohocrm: grant response: %w", err)
	}
	// Zoho reports grant errors as 200 + {"error": "invalid_code"}; an empty
	// refresh token is a definite rejection either way.
	if grant.RefreshToken == "" {
		msg := grant.Error
		if msg == "" {
			msg = fmt.Sprintf("http %d", resp.StatusCode)
		}
		return TokenGrant{}, &AuthError{Msg: msg}
	}
	return grant.TokenGrant, nil
}

// CurrentUser is the identity projection of the token's Zoho user, including
// the CRM role and profile that decide what they can see.
type CurrentUser struct {
	ID       string  `json:"id"`
	FullName string  `json:"full_name"`
	Email    string  `json:"email"`
	Role     NameRef `json:"role"`
	Profile  NameRef `json:"profile"`
}

// NameRef is Zoho's {"name","id"} lookup shape.
type NameRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetCurrentUser resolves the authenticated user behind the client's grant —
// the connect-time probe that also yields the role/profile shown in the UI.
func (c *Client) GetCurrentUser(ctx context.Context) (CurrentUser, error) {
	var out struct {
		Users []CurrentUser `json:"users"`
	}
	if err := c.getJSON(ctx, "/crm/v8/users?type=CurrentUser", &out); err != nil {
		return CurrentUser{}, err
	}
	if len(out.Users) == 0 {
		return CurrentUser{}, fmt.Errorf("zohocrm: current user response empty")
	}
	return out.Users[0], nil
}

// RevokeToken revokes a refresh token at Zoho, so a disconnected account
// can't be used again even if a copy survived somewhere.
func RevokeToken(ctx context.Context, dc, accountsBase, token string) error {
	hosts, ok := DCHosts[dc]
	if !ok {
		return fmt.Errorf("zohocrm: unknown dc %q", dc)
	}
	if accountsBase == "" {
		accountsBase = hosts.Accounts
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(accountsBase, "/")+"/oauth/v2/token/revoke?"+url.Values{"token": {token}}.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("zohocrm: revoke: http %d", resp.StatusCode)
	}
	return nil
}
