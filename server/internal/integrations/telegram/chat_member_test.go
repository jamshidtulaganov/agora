package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsChatMember(t *testing.T) {
	cases := []struct {
		body   string
		member bool
		fails  bool
	}{
		{`{"ok":true,"result":{"status":"member"}}`, true, false},
		{`{"ok":true,"result":{"status":"administrator"}}`, true, false},
		{`{"ok":true,"result":{"status":"creator"}}`, true, false},
		{`{"ok":true,"result":{"status":"restricted","is_member":true}}`, true, false},
		{`{"ok":true,"result":{"status":"restricted","is_member":false}}`, false, false},
		{`{"ok":true,"result":{"status":"left"}}`, false, false},
		{`{"ok":true,"result":{"status":"kicked"}}`, false, false},
		// Not being able to ask is an error, never a "no" or a "yes".
		{`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, false, true},
		{`not json`, false, true},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, tc.body)
		}))
		bot := NewBotClient("TOKEN")
		bot.BaseURL = srv.URL
		bot.HTTPClient = srv.Client()
		member, err := bot.IsChatMember(context.Background(), "-100", 42)
		srv.Close()
		if member != tc.member || (err != nil) != tc.fails {
			t.Errorf("%s → member %v err %v", tc.body, member, err)
		}
	}
}
