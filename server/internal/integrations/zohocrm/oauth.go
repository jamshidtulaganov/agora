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

// TokenGrant is the result of exchanging a self-client grant code: the
// long-lived refresh token to seal at rest plus the granted scope list.
type TokenGrant struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	APIDomain    string `json:"api_domain"`
}

// ExchangeGrantCode exchanges a Zoho self-client grant code for a refresh
// token under the given OAuth client (grant_type=authorization_code). A
// refresh token is bound to the (client, Zoho user) pair, which is why user
// bindings mint under the workspace connection's client. dc derives the
// accounts host; accountsBase overrides it for tests.
func ExchangeGrantCode(ctx context.Context, clientID, clientSecret, code, dc, accountsBase string) (TokenGrant, error) {
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

// CurrentUser is the identity projection of the token's Zoho user.
type CurrentUser struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// GetCurrentUser resolves the authenticated user behind the client's grant —
// the save-time probe for user bindings (also yields the identity hint shown
// in the UI).
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
