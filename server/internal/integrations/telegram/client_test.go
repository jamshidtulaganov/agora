package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
)

func TestGetUpdatesAsksForTheRequestedKinds(t *testing.T) {
	// The bug this guards: allowed_updates was hardcoded to ["message"], so
	// Telegram never delivered callback_query and the agent's own buttons
	// appeared to do nothing when tapped.
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	c := NewBotClient("token")
	c.BaseURL = srv.URL
	if _, err := c.GetUpdates(context.Background(), 0, 1, "message", "callback_query"); err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	decoded, err := neturl.QueryUnescape(gotQuery)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	if !strings.Contains(decoded, `allowed_updates=["message","callback_query"]`) {
		t.Fatalf("allowed_updates not carried: %s", decoded)
	}
}

func TestGetUpdatesDefaultsToMessagesOnly(t *testing.T) {
	// The login poller passes no kinds and must keep its narrow subscription —
	// widening it would hand that poller updates it has no handler for.
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	c := NewBotClient("token")
	c.BaseURL = srv.URL
	if _, err := c.GetUpdates(context.Background(), 0, 1); err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	decoded, _ := neturl.QueryUnescape(gotQuery)
	if !strings.Contains(decoded, `allowed_updates=["message"]`) {
		t.Fatalf("default subscription changed: %s", decoded)
	}
}
