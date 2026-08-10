package handler

import (
	"encoding/json"
	"testing"
)

func decodeTelegramCleanupUpdate(t *testing.T, raw string) telegramUpdate {
	t.Helper()
	var update telegramUpdate
	if err := json.Unmarshal([]byte(raw), &update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	return update
}

func TestTelegramFixtureDeleteTarget(t *testing.T) {
	valid := decodeTelegramCleanupUpdate(t, `{
		"message":{"message_id":91,"text":"/delete_test@AgoraBot",
		"from":{"id":12,"is_bot":false},
		"chat":{"id":-10055,"type":"supergroup"},
		"reply_to_message":{"message_id":77,"text":"🆕 <b>EEW-38902</b> E2E Comment Test 1786",
		"from":{"id":42,"is_bot":true,"username":"AgoraBot"}}}}
	`)
	chatID, targetID, commandID, ok := telegramFixtureDeleteTarget(valid, "@AgoraBot")
	if !ok || chatID != "-10055" || targetID != 77 || commandID != 91 {
		t.Fatalf("target = (%q, %d, %d, %v)", chatID, targetID, commandID, ok)
	}
}

func TestTelegramFixtureDeleteTargetRejectsRealAndForeignMessages(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "real issue",
			raw:  `{"message":{"message_id":91,"text":"/delete_test@AgoraBot","from":{"id":12},"chat":{"id":-10055,"type":"group"},"reply_to_message":{"message_id":77,"text":"New issue created: Fix production login","from":{"is_bot":true,"username":"AgoraBot"}}}}`,
		},
		{
			name: "foreign bot",
			raw:  `{"message":{"message_id":91,"text":"/delete_test@AgoraBot","from":{"id":12},"chat":{"id":-10055,"type":"group"},"reply_to_message":{"message_id":77,"text":"E2E fixture","from":{"is_bot":true,"username":"OtherBot"}}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update := decodeTelegramCleanupUpdate(t, test.raw)
			if _, _, _, ok := telegramFixtureDeleteTarget(update, "AgoraBot"); ok {
				t.Fatal("unsafe cleanup target accepted")
			}
		})
	}
}
