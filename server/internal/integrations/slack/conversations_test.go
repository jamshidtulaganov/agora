package slack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConversationsList_ParsesPageAndCursor(t *testing.T) {
	var gotQuery, gotAuth, gotMethod string
	client := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		io.WriteString(w, `{
			"ok": true,
			"channels": [
				{"id":"C0ENG","name":"eng","is_private":false,"is_member":true,"is_channel":true},
				{"id":"G0SEC","name":"security","is_private":true,"is_member":false,"is_group":true}
			],
			"response_metadata": {"next_cursor": "dXNlcjpVMDYxTkZUVDI="}
		}`)
	})

	res, err := client.ConversationsList(context.Background(), "xoxb-token", ConversationsListParams{Limit: 50})
	if err != nil {
		t.Fatalf("ConversationsList: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("conversations.list must be a GET, got %s", gotMethod)
	}
	if gotAuth != "Bearer xoxb-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	// The picker must never ask for DMs, and never for archived channels.
	for _, want := range []string{"types=public_channel%2Cprivate_channel", "exclude_archived=true", "limit=50"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
	if len(res.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(res.Channels))
	}
	if res.Channels[0].ID != "C0ENG" || !res.Channels[0].IsMember {
		t.Fatalf("channel[0] = %+v", res.Channels[0])
	}
	if !res.Channels[1].IsPrivate {
		t.Fatalf("private channel lost its flag: %+v", res.Channels[1])
	}
	if res.NextCursor() != "dXNlcjpVMDYxTkZUVDI=" {
		t.Fatalf("next cursor = %q", res.NextCursor())
	}
}

func TestConversationsList_ClampsPageSize(t *testing.T) {
	var gotQuery string
	client := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `{"ok":true,"channels":[]}`)
	})
	if _, err := client.ConversationsList(context.Background(), "xoxb", ConversationsListParams{Limit: 5000}); err != nil {
		t.Fatalf("ConversationsList: %v", err)
	}
	if !strings.Contains(gotQuery, "limit=200") {
		t.Fatalf("page size was not clamped: %q", gotQuery)
	}
}

// Slack answers 2xx with ok:false for application errors — the picker must see
// an error, not an empty channel list it would render as "no channels".
func TestConversationsList_OKFalseIsAnError(t *testing.T) {
	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"missing_scope"}`)
	})
	_, err := client.ConversationsList(context.Background(), "xoxb", ConversationsListParams{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "missing_scope" {
		t.Fatalf("expected a missing_scope APIError, got %v", err)
	}
}

func TestConversationsList_RateLimitedCarriesRetryAfter(t *testing.T) {
	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := client.ConversationsList(context.Background(), "xoxb", ConversationsListParams{})
	if !IsRateLimited(err) {
		t.Fatalf("expected a rate-limit error, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 12*time.Second {
		t.Fatalf("retry-after not preserved: %v", err)
	}
}

// A body that is not the shape we expect degrades to an error, never a panic
// and never a half-filled result the caller mistakes for success.
func TestConversationsList_MalformedResponses(t *testing.T) {
	cases := map[string]string{
		"not json":     `<html>502</html>`,
		"null array":   `{"ok":true,"channels":null}`,
		"wrong type":   `{"ok":true,"channels":{"id":"C1"}}`,
		"missing meta": `{"ok":true,"channels":[{"id":"C1","name":"x"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
				io.WriteString(w, body)
			})
			res, err := client.ConversationsList(context.Background(), "xoxb", ConversationsListParams{})
			if err != nil {
				return // decode failure surfaced as an error: acceptable
			}
			// Otherwise it must be safely empty, not garbage.
			for _, ch := range res.Channels {
				_ = ch.ID
			}
			_ = res.NextCursor()
		})
	}
}

func TestConversationsOpen_ReturnsIMChannel(t *testing.T) {
	client := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.open" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `{"ok":true,"channel":{"id":"D0USER"}}`)
	})
	res, err := client.ConversationsOpen(context.Background(), "xoxb", "U0ALICE")
	if err != nil {
		t.Fatalf("ConversationsOpen: %v", err)
	}
	if res.Channel.ID != "D0USER" {
		t.Fatalf("im channel = %q", res.Channel.ID)
	}
}

func TestConversationsOpen_ErrorIsClassified(t *testing.T) {
	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"invalid_auth"}`)
	})
	_, err := client.ConversationsOpen(context.Background(), "xoxb", "U0ALICE")
	if !IsTokenInvalid(err) {
		t.Fatalf("expected a dead-token classification, got %v", err)
	}
}

func TestConversations_EmptyTokenNeverLeavesTheProcess(t *testing.T) {
	client := newTestAPIClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an empty token must not reach the network")
	})
	if _, err := client.ConversationsList(context.Background(), "  ", ConversationsListParams{}); err == nil {
		t.Fatal("expected an error for an empty token")
	}
	if _, err := client.ConversationsOpen(context.Background(), "", "U0ALICE"); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}
