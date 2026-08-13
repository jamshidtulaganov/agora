package handler

import (
	"encoding/json"
	"testing"
)

func TestDecodeTelegramUpdate(t *testing.T) {
	t.Run("group message decodes with the sender's username", func(t *testing.T) {
		raw := json.RawMessage(`{"update_id":42,"message":{"text":"@bot holat qanday?","from":{"id":7,"first_name":"Jamshid","username":"j_tulaganov_3101"},"chat":{"id":-1003107704922,"type":"supergroup"}}}`)
		up, ok := decodeTelegramUpdate(raw)
		if !ok {
			t.Fatal("valid update was rejected")
		}
		if up.UpdateID != 42 || up.Message == nil {
			t.Fatalf("decoded wrong: %+v", up)
		}
		if up.Message.Chat.Type != "supergroup" || up.Message.Chat.ID != -1003107704922 {
			t.Errorf("chat wrong: %+v", up.Message.Chat)
		}
		if up.Message.From.Username != "j_tulaganov_3101" {
			t.Errorf("username missing — the agent would not know who asked: %+v", up.Message.From)
		}
	})

	// One malformed payload must not kill the poll loop and silence an agent.
	t.Run("malformed update is skipped, not fatal", func(t *testing.T) {
		if _, ok := decodeTelegramUpdate(json.RawMessage(`{"update_id":"not-a-number"}`)); ok {
			t.Error("expected a decode failure to be reported as skip")
		}
	})

	t.Run("update with no message decodes cleanly", func(t *testing.T) {
		up, ok := decodeTelegramUpdate(json.RawMessage(`{"update_id":9}`))
		if !ok || up.Message != nil {
			t.Errorf("got ok=%v message=%v", ok, up.Message)
		}
	})
}

func TestTelegramMessageAddressesAgent(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		repliesToBot bool
		want         bool
	}{
		{name: "tag", text: "@AgoraAgent status?", want: true},
		{name: "tag case insensitive", text: "hello @agoraagent", want: true},
		{name: "reply", text: "continue", repliesToBot: true, want: true},
		{name: "ordinary group chatter", text: "deploy is done", want: false},
		{name: "similar username", text: "@AgoraAgentOther hello", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := telegramMessageAddressesAgent(test.text, test.repliesToBot, "AgoraAgent"); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestStripTelegramBotMentionIsCaseInsensitive(t *testing.T) {
	if got := stripTelegramBotMention("Hi @AGORAagent, please check", "AgoraAgent"); got != "Hi , please check" {
		t.Fatalf("got %q", got)
	}
}
