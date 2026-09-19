package slack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	cfg := OAuthConfig{
		ClientID:     "123.456",
		ClientSecret: "shh",
		RedirectURI:  "https://api.agora.dev/slack/oauth/callback",
	}
	raw := cfg.AuthorizeURL("sealed-state")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != DefaultAuthorizeURL {
		t.Fatalf("authorize base = %q", u.Scheme+"://"+u.Host+u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != "123.456" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("state") != "sealed-state" {
		t.Fatalf("state = %q", q.Get("state"))
	}
	if q.Get("redirect_uri") != cfg.RedirectURI {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	// Scopes are comma separated (Slack's format), not space separated.
	if got := q.Get("scope"); got != strings.Join(DefaultBotScopes, ",") {
		t.Fatalf("scope = %q", got)
	}
	// user_scope exists only so the response carries authed_user.id.
	if got := q.Get("user_scope"); got != strings.Join(DefaultUserScopes, ",") {
		t.Fatalf("user_scope = %q", got)
	}
	// The consent screen must not advertise message-reading power we do not
	// need — and a history scope would also drag in the unlisted-app rate cap.
	for _, scope := range DefaultBotScopes {
		if strings.HasSuffix(scope, ":history") {
			t.Fatalf("Phase 1 must not request a history scope, found %q", scope)
		}
	}
}

func TestExchangeCode_Success(t *testing.T) {
	var gotForm url.Values
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth.v2.access" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("content-type = %q", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("the code exchange must not carry a bearer token, got %q", auth)
		}
		raw, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(raw))
		io.WriteString(w, `{
			"ok": true,
			"access_token": "xoxb-real-token",
			"token_type": "bot",
			"scope": "chat:write,links:read",
			"bot_user_id": "U0BOT",
			"app_id": "A123",
			"team": {"id": "T123", "name": "Agora HQ"},
			"enterprise": null,
			"authed_user": {"id": "U0HUMAN", "scope": "users:read", "access_token": "xoxp-user-token"}
		}`)
	})

	cfg := OAuthConfig{ClientID: "123.456", ClientSecret: "shh", RedirectURI: "https://api.agora.dev/slack/oauth/callback"}
	res, err := c.ExchangeCode(context.Background(), cfg, "the-code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if res.AccessToken != "xoxb-real-token" || res.BotUserID != "U0BOT" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.TeamID() != "T123" || res.TeamName() != "Agora HQ" {
		t.Fatalf("team = %q/%q", res.TeamID(), res.TeamName())
	}
	// enterprise: null must not panic and must read as "no Grid org".
	if res.EnterpriseID() != "" {
		t.Fatalf("enterprise = %q", res.EnterpriseID())
	}
	if res.AuthedUser.ID != "U0HUMAN" {
		t.Fatalf("authed_user.id = %q", res.AuthedUser.ID)
	}
	if gotForm.Get("client_secret") != "shh" || gotForm.Get("code") != "the-code" {
		t.Fatalf("form = %v", gotForm)
	}
	if gotForm.Get("redirect_uri") != cfg.RedirectURI {
		t.Fatalf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
}

func TestExchangeCode_Failures(t *testing.T) {
	cfg := OAuthConfig{ClientID: "123.456", ClientSecret: "shh"}

	tests := []struct {
		name     string
		body     string
		status   int
		wantCode string
	}{
		{name: "slack rejects the code", body: `{"ok":false,"error":"invalid_code"}`, wantCode: "invalid_code"},
		{name: "ok true but no token", body: `{"ok":true,"team":{"id":"T1"}}`, wantCode: "invalid_response"},
		{name: "ok true but no team", body: `{"ok":true,"access_token":"xoxb-x"}`, wantCode: "invalid_response"},
		{name: "team is null", body: `{"ok":true,"access_token":"xoxb-x","team":null}`, wantCode: "invalid_response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				io.WriteString(w, tc.body)
			})
			_, err := c.ExchangeCode(context.Background(), cfg, "the-code")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError, got %v", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.wantCode)
			}
		})
	}
}

func TestExchangeCode_MalformedBody(t *testing.T) {
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html>gateway error</html>`)
	})
	_, err := c.ExchangeCode(context.Background(), OAuthConfig{ClientID: "x", ClientSecret: "y"}, "code")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestExchangeCode_RefusesWithoutCredentials(t *testing.T) {
	called := false
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		io.WriteString(w, `{"ok":true}`)
	})
	if _, err := c.ExchangeCode(context.Background(), OAuthConfig{}, "code"); err == nil {
		t.Fatal("expected an error with no client credentials")
	}
	if _, err := c.ExchangeCode(context.Background(), OAuthConfig{ClientID: "x", ClientSecret: "y"}, ""); err == nil {
		t.Fatal("expected an error with an empty code")
	}
	if called {
		t.Fatal("a misconfigured exchange must not reach Slack")
	}
}
