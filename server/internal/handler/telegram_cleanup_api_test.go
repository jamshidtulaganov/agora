package handler

import "testing"

func TestParseTelegramPrivateMessageLink(t *testing.T) {
	target, err := parseTelegramPrivateMessageLink("https://t.me/c/3501835836/3614")
	if err != nil {
		t.Fatal(err)
	}
	if target.ChatID != "-1003501835836" || target.MessageID != 3614 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestParseTelegramPrivateMessageLinkRejectsUnsafeForms(t *testing.T) {
	for _, raw := range []string{
		"http://t.me/c/3501835836/3614",
		"https://example.com/c/3501835836/3614",
		"https://t.me/agora/3614",
		"https://t.me/c/3501835836/0",
		"https://t.me/c/not-a-chat/3614",
	} {
		if _, err := parseTelegramPrivateMessageLink(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}
