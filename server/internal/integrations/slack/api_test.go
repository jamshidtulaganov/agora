package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestAPIClient points an APIClient at an httptest server standing in for
// https://slack.com/api.
func newTestAPIClient(t *testing.T, handler http.HandlerFunc) *APIClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewAPIClient(WithAPIBaseURL(srv.URL), WithHTTPClient(srv.Client()))
}

func TestPostMessage_Success(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any

	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"channel":"C123","ts":"1735689600.000100"}`)
	})

	res, err := c.PostMessage(context.Background(), "xoxb-token", PostMessageRequest{
		Channel: "C123",
		Text:    "MUL-1 failed",
		Blocks:  []Block{HeaderBlock("MUL-1 failed")},
	})
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if res.TS != "1735689600.000100" || res.Channel != "C123" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if gotAuth != "Bearer xoxb-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/chat.postMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["channel"] != "C123" {
		t.Fatalf("channel not sent: %+v", gotBody)
	}
	if _, ok := gotBody["blocks"]; !ok {
		t.Fatalf("blocks not sent: %+v", gotBody)
	}
}

// Slack signals application errors with HTTP 200 + ok:false. Treating that as
// success is the classic integration bug: the queue would mark a message
// delivered that nobody ever saw.
func TestPostMessage_OKFalseIsAnError(t *testing.T) {
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"channel_not_found"}`)
	})

	_, err := c.PostMessage(context.Background(), "xoxb-token", PostMessageRequest{Channel: "C404", Text: "hi"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Code != "channel_not_found" {
		t.Fatalf("code = %q", apiErr.Code)
	}
	if IsTokenInvalid(err) || IsRateLimited(err) {
		t.Fatalf("channel_not_found must not be classified as auth/rate-limit: %v", err)
	}
}

func TestPostMessage_RateLimitedCarriesRetryAfter(t *testing.T) {
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"ok":false,"error":"ratelimited"}`)
	})

	_, err := c.PostMessage(context.Background(), "xoxb-token", PostMessageRequest{Channel: "C1", Text: "hi"})
	if !IsRateLimited(err) {
		t.Fatalf("expected a rate-limit error, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", apiErr.RetryAfter)
	}
}

// A dead token must be recognisable so the caller can mark the installation
// revoked instead of retrying until Slack disables the app.
func TestPostMessage_TokenRevokedIsClassified(t *testing.T) {
	for _, code := range []string{"invalid_auth", "token_revoked", "account_inactive"} {
		t.Run(code, func(t *testing.T) {
			c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"ok":false,"error":"`+code+`"}`)
			})
			_, err := c.PostMessage(context.Background(), "xoxb-dead", PostMessageRequest{Channel: "C1", Text: "hi"})
			if !IsTokenInvalid(err) {
				t.Fatalf("expected IsTokenInvalid for %s, got %v", code, err)
			}
		})
	}
}

// Malformed responses degrade rather than panic or silently "succeed".
func TestPostMessage_MalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `<html>502 Bad Gateway</html>`},
		{"empty body", ``},
		{"null", `null`},
		{"ok wrong type", `{"ok":"yes"}`},
		{"missing ok", `{"channel":"C1","ts":"1.0"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			})
			if _, err := c.PostMessage(context.Background(), "xoxb-token", PostMessageRequest{Channel: "C1", Text: "hi"}); err == nil {
				t.Fatalf("expected an error for %s", tc.name)
			}
		})
	}
}

func TestPostMessage_NonJSONErrorStatus(t *testing.T) {
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream down")
	})
	_, err := c.PostMessage(context.Background(), "xoxb-token", PostMessageRequest{Channel: "C1", Text: "hi"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d", apiErr.StatusCode)
	}
}

func TestPostMessage_EmptyTokenNeverLeavesTheProcess(t *testing.T) {
	called := false
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		io.WriteString(w, `{"ok":true}`)
	})
	if _, err := c.PostMessage(context.Background(), "  ", PostMessageRequest{Channel: "C1", Text: "hi"}); err == nil {
		t.Fatal("expected an error for an empty token")
	}
	if called {
		t.Fatal("an empty token must not produce a request")
	}
}

func TestAuthTest_Success(t *testing.T) {
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"ok":true,"team":"Agora","team_id":"T1","user_id":"U1","bot_id":"B1"}`)
	})
	res, err := c.AuthTest(context.Background(), "xoxb-token")
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if res.TeamID != "T1" || res.UserID != "U1" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
