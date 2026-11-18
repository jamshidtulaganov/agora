package assistant

import (
	"encoding/json"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// Telegram group history tools. Like the knowledge tools they are not in the
// static catalog: a run carries them only when one of the person's workspaces
// has a group the platform bot is allowed to remember (Service.RunExtras).
// Both are reads.
const (
	ToolListTelegramChats = "list_telegram_chats"
	ToolReadTelegramChat  = "read_telegram_chat"
)

// TelegramToolSpecs are the Telegram history tools as model tools.
func TelegramToolSpecs() []llm.Tool {
	return []llm.Tool{
		{
			Name:        ToolListTelegramChats,
			Description: "List the Telegram group chats whose recent messages the user can read here: chat_id, title and workspace.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}, "additionalProperties": false}`),
		},
		{
			Name: ToolReadTelegramChat,
			Description: "Read the recent text messages of one Telegram group chat, oldest first, to summarize a discussion or " +
				"find decisions and action items. Returns a transcript with time, sender and text. Media is not included.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "chat_id": {"type": "string", "description": "chat_id from list_telegram_chats"},
    "hours": {"type": "integer", "minimum": 1, "maximum": 168, "description": "How far back to read; default 24"}
  },
  "required": ["chat_id"],
  "additionalProperties": false
}`),
		},
	}
}

// IsTelegramTool reports whether name is one of the Telegram history tools.
func IsTelegramTool(name string) bool {
	return name == ToolListTelegramChats || name == ToolReadTelegramChat
}
