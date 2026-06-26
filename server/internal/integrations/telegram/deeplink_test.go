package telegram

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMiniAppStartParam_RoundTrips(t *testing.T) {
	const issueID = "550e8400-e29b-41d4-a716-446655440000"
	param := MiniAppStartParam("sd-main", issueID)

	// Telegram restricts startapp to [A-Za-z0-9_-] and ≤64 chars.
	if len(param) > 64 {
		t.Fatalf("start param length = %d, want ≤64", len(param))
	}
	for _, c := range param {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			t.Fatalf("start param has illegal char %q", c)
		}
	}

	decoded, err := base64.RawURLEncoding.DecodeString(param)
	if err != nil {
		t.Fatalf("decode start param: %v", err)
	}
	if want := "i:sd-main:" + issueID; string(decoded) != want {
		t.Fatalf("decoded = %q, want %q", decoded, want)
	}

	// No slug → legacy "i:<id>" form.
	noSlug, _ := base64.RawURLEncoding.DecodeString(MiniAppStartParam("", issueID))
	if want := "i:" + issueID; string(noSlug) != want {
		t.Fatalf("no-slug decoded = %q, want %q", noSlug, want)
	}
}

func TestMiniAppLink(t *testing.T) {
	cases := []struct {
		name     string
		bot      string
		short    string
		param    string
		want     string
	}{
		{"named app", "agora_bot", "pm", "p123", "https://t.me/agora_bot/pm?startapp=p123"},
		{"strips at-prefix", "@agora_bot", "pm", "p123", "https://t.me/agora_bot/pm?startapp=p123"},
		{"named app no param", "agora_bot", "pm", "", "https://t.me/agora_bot/pm"},
		{"main app (no short name)", "agora_bot", "", "p123", "https://t.me/agora_bot?startapp=p123"},
		{"main app strips at-prefix", "@agora_bot", "", "p123", "https://t.me/agora_bot?startapp=p123"},
		{"main app no param", "agora_bot", "", "", "https://t.me/agora_bot"},
		{"empty bot", "", "pm", "p123", ""},
		{"empty bot no short", "", "", "p123", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MiniAppLink(c.bot, c.short, c.param); got != c.want {
				t.Fatalf("MiniAppLink(%q,%q,%q) = %q, want %q", c.bot, c.short, c.param, got, c.want)
			}
		})
	}
}

func TestMiniAppLink_UsesStartParamFromIssue(t *testing.T) {
	link := MiniAppLink("agora_bot", "pm", MiniAppStartParam("sd-main", "issue-1"))
	if !strings.HasPrefix(link, "https://t.me/agora_bot/pm?startapp=") {
		t.Fatalf("link = %q, missing startapp", link)
	}
}
