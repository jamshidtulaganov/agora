package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAnthropicBaseURL is the Anthropic Messages API root.
const DefaultAnthropicBaseURL = "https://api.anthropic.com"

// anthropicVersion is the dated API contract this client is written against.
// It is a pinned header, not a model version — bumping it is a deliberate
// migration, never an incidental upgrade.
const anthropicVersion = "2023-06-01"

// DefaultAnthropicModel is used when the instance selects the anthropic
// provider without naming a model (AGORA_ASSISTANT_MODEL defaults to the free
// Zhipu model, which means nothing here).
const DefaultAnthropicModel = "claude-sonnet-5"

// AnthropicClient is a minimal Messages-API client. Same zero-state,
// dependency-free shape as ZhipuClient so it is safe to construct per call.
type AnthropicClient struct {
	APIKey  string
	BaseURL string // optional override (tests); falls back to DefaultAnthropicBaseURL
	HTTP    *http.Client
}

// NewAnthropicClient builds a client. An empty apiKey yields a client whose
// calls return ErrNotConfigured.
func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		APIKey: strings.TrimSpace(apiKey),
		HTTP:   &http.Client{Timeout: 60 * time.Second},
	}
}

type anthropicToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicBlock is one content block, in both directions. Anthropic models a
// turn as a list of typed blocks rather than a string plus side-channels, so
// tool calls and their results are blocks, not message fields.
type anthropicBlock struct {
	Type string `json:"type"`
	// type=text
	Text string `json:"text,omitempty"`
	// type=tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// type=tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicToolSpec `json:"tools,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicBlock `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// emptyJSONObject is what a tool_use block carries when the model produced no
// arguments. Anthropic rejects a missing `input`, so a no-arg call must still
// send `{}`.
var emptyJSONObject = json.RawMessage(`{}`)

// buildAnthropicMessages converts the provider-neutral history into Anthropic's
// block form and lifts every system turn into the top-level `system` string
// (the Messages API has no system role). Consecutive role="tool" messages
// collapse into ONE user message of tool_result blocks — the API requires every
// tool_use in an assistant turn to be answered by tool_result blocks in the
// single user turn that follows it.
func buildAnthropicMessages(messages []Message) (string, []anthropicMessage) {
	var system []string
	out := make([]anthropicMessage, 0, len(messages))

	// pendingResults accumulates the current run of tool answers.
	var pendingResults []anthropicBlock
	flush := func() {
		if len(pendingResults) == 0 {
			return
		}
		out = append(out, anthropicMessage{Role: "user", Content: pendingResults})
		pendingResults = nil
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			flush()
			if s := strings.TrimSpace(m.Content); s != "" {
				system = append(system, s)
			}
		case "tool":
			pendingResults = append(pendingResults, anthropicBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		case "assistant":
			flush()
			blocks := make([]anthropicBlock, 0, len(m.ToolCalls)+1)
			if s := strings.TrimSpace(m.Content); s != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: s})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(strings.TrimSpace(tc.Arguments)) == 0 {
					input = emptyJSONObject
				}
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		default:
			flush()
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []anthropicBlock{{Type: "text", Text: m.Content}},
			})
		}
	}
	flush()

	return strings.Join(system, "\n\n"), out
}

// CompleteWithTools runs one Messages-API round-trip, mapping the common
// Message/Tool shapes onto Anthropic's content blocks in both directions.
func (c *AnthropicClient) CompleteWithTools(ctx context.Context, model string, messages []Message, tools []Tool) (Message, error) {
	if c.APIKey == "" {
		return Message{}, ErrNotConfigured
	}
	if model == "" {
		model = DefaultAnthropicModel
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultAnthropicBaseURL
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}

	system, wire := buildAnthropicMessages(messages)

	wireTools := make([]anthropicToolSpec, 0, len(tools))
	for _, t := range tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = emptyJSONObject
		}
		wireTools = append(wireTools, anthropicToolSpec{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	body, err := json.Marshal(anthropicRequest{
		Model:     model,
		MaxTokens: 2048,
		System:    system,
		Messages:  wire,
		Tools:     wireTools,
	})
	if err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := httpc.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("llm: anthropic status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Message{}, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, fmt.Errorf("llm: anthropic error: %s", parsed.Error.Message)
	}
	if len(parsed.Content) == 0 {
		return Message{}, errors.New("llm: empty completion")
	}

	out := Message{Role: "assistant"}
	var text []string
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if s := strings.TrimSpace(block.Text); s != "" {
				text = append(text, s)
			}
		case "tool_use":
			args := string(block.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
		// Unknown block types (thinking, server_tool_use, …) downgrade to
		// nothing rather than failing the run — enum drift must not crash.
	}
	out.Content = strings.Join(text, "\n\n")
	return out, nil
}
