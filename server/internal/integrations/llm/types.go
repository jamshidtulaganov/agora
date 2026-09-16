package llm

import (
	"context"
	"encoding/json"
)

// Tool is one function the model may call. Parameters is a raw JSON Schema
// object — kept raw so a caller writes the schema once, literally, instead of
// modelling JSON Schema in Go types that every provider would then re-flatten.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one invocation the model asked for. Arguments is the raw JSON
// string the model produced: it is NOT validated here, because a malformed
// argument blob must reach the executor as a tool error the model can correct,
// not as a transport failure that aborts the run.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolChat is the tool-calling surface shared by every provider client. Kept
// separate from Complete so the existing summarize path (plain completion, no
// tools) is untouched by anything the assistant needs.
type ToolChat interface {
	CompleteWithTools(ctx context.Context, model string, messages []Message, tools []Tool) (Message, error)
}
