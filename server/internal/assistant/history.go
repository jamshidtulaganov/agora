package assistant

import "github.com/jamshidtulaganov/agora/server/internal/integrations/llm"

// repairToolHistory preserves complete tool exchanges and closes interrupted
// ones before a later user turn reaches the model. These synthetic results are
// context only: they never execute a tool or claim its effects were rolled back.
func repairToolHistory(messages []llm.Message) []llm.Message {
	messages = trimDanglingToolMessages(messages)
	out := make([]llm.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		if message.Role == "tool" {
			continue // Its requesting turn is absent or unreadable.
		}
		if message.Role != "assistant" || len(message.ToolCalls) == 0 {
			out = append(out, message)
			continue
		}

		results := map[string]llm.Message{}
		end := i + 1
		for end < len(messages) && messages[end].Role == "tool" {
			result := messages[end]
			if _, exists := results[result.ToolCallID]; !exists {
				results[result.ToolCallID] = result
			}
			end++
		}
		out = append(out, message)
		for _, call := range message.ToolCalls {
			if result, exists := results[call.ID]; exists {
				out = append(out, result)
				continue
			}
			out = append(out, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    string(toolError("This tool call was interrupted before a result was recorded. Its effects are unknown. Inspect the affected items before retrying; do not assume it failed or was rolled back.")),
			})
		}
		i = end - 1
	}
	return out
}
