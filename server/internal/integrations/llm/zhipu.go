// Package llm holds tiny, dependency-free clients for plain chat-completion
// calls used by lightweight product features (e.g. "summarize this thread").
// It is intentionally NOT the agent runtime — agents run via daemons. This is a
// single request/response round-trip to a hosted model.
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

// DefaultZhipuBaseURL is Zhipu's OpenAI-compatible v4 endpoint. glm-4-flash is
// the free tier — the branded "Agora" base model for free product features.
const DefaultZhipuBaseURL = "https://open.bigmodel.cn/api/paas/v4"

// FreeModel is the free Zhipu model used for summaries and other free features.
// glm-4.5-flash is the current free tier (the older glm-4-flash was retired).
const FreeModel = "glm-4.5-flash"

// ErrNotConfigured is returned when no API key is set, so callers can map it to
// a 503 (feature unavailable) instead of a hard error.
var ErrNotConfigured = errors.New("llm: api key not configured")

// Message is a single chat turn.
//
// ToolCalls (assistant turns) and ToolCallID (role="tool" turns) are the
// provider-neutral tool-calling fields. The json tags exist so a Message can be
// logged/persisted verbatim — they are NOT a wire format: every client below
// converts a Message into its own provider shape (OpenAI-style nested
// `function` objects for Zhipu, content blocks for Anthropic), because no two
// providers spell these the same way.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ZhipuClient is a minimal chat-completions client. Zero state beyond config so
// it is safe to construct per-call.
type ZhipuClient struct {
	APIKey  string
	BaseURL string // optional override (tests); falls back to DefaultZhipuBaseURL
	HTTP    *http.Client
}

// NewZhipuClient builds a client. An empty apiKey yields a client whose calls
// return ErrNotConfigured.
func NewZhipuClient(apiKey string) *ZhipuClient {
	return &ZhipuClient{
		APIKey: strings.TrimSpace(apiKey),
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs a single chat completion and returns the assistant text.
func (c *ZhipuClient) Complete(ctx context.Context, model string, messages []Message) (string, error) {
	if c.APIKey == "" {
		return "", ErrNotConfigured
	}
	if model == "" {
		model = FreeModel
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultZhipuBaseURL
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}

	body, err := json.Marshal(chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: zhipu status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("llm: zhipu error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: empty completion")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// ---------------------------------------------------------------------------
// Tool calling (OpenAI-compatible shape — Zhipu v4 supports `tools`)
// ---------------------------------------------------------------------------

type zhipuFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type zhipuTool struct {
	Type     string            `json:"type"`
	Function zhipuFunctionSpec `json:"function"`
}

type zhipuFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type zhipuToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function zhipuFunctionCall `json:"function"`
}

// zhipuMessage is the on-the-wire turn. Separate from Message because the
// provider nests name/arguments under `function` while our neutral ToolCall is
// flat.
type zhipuMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []zhipuToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type zhipuToolRequest struct {
	Model    string         `json:"model"`
	Messages []zhipuMessage `json:"messages"`
	Tools    []zhipuTool    `json:"tools,omitempty"`
	// Temperature is a pointer because OpenAI's reasoning models (gpt-5*/o*)
	// reject any explicit temperature; nil omits the field entirely.
	Temperature *float64 `json:"temperature,omitempty"`
	// Reasoning models likewise reject max_tokens and demand
	// max_completion_tokens — exactly one of these two is ever set.
	MaxTokens           int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	// OpenAI's gpt-5.x chat-completions endpoint refuses function tools unless
	// reasoning_effort is "none" (verified live: "Function tools with
	// reasoning_effort are not supported ... set reasoning_effort to 'none'").
	// The assistant is a tool router, not a deep reasoner — "none" is right.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// isReasoningModel reports whether the model rejects the classic sampling
// params (temperature, max_tokens). Covers OpenAI's gpt-5* and o* families;
// Zhipu glm-* and Anthropic ids never match.
func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
}

type zhipuToolResponse struct {
	Choices []struct {
		Message zhipuMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// CompleteWithTools runs one chat completion that may return tool calls
// instead of (or alongside) text. It is a single round-trip: driving the
// call → execute → call-again loop is the caller's job.
func (c *ZhipuClient) CompleteWithTools(ctx context.Context, model string, messages []Message, tools []Tool) (Message, error) {
	if c.APIKey == "" {
		return Message{}, ErrNotConfigured
	}
	if model == "" {
		model = FreeModel
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultZhipuBaseURL
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}

	wire := make([]zhipuMessage, 0, len(messages))
	for _, m := range messages {
		zm := zhipuMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			zm.ToolCalls = append(zm.ToolCalls, zhipuToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: zhipuFunctionCall{Name: tc.Name, Arguments: tc.Arguments},
			})
		}
		wire = append(wire, zm)
	}

	wireTools := make([]zhipuTool, 0, len(tools))
	for _, t := range tools {
		wireTools = append(wireTools, zhipuTool{
			Type: "function",
			Function: zhipuFunctionSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	toolReq := zhipuToolRequest{
		Model:    model,
		Messages: wire,
		Tools:    wireTools,
	}
	if isReasoningModel(model) {
		toolReq.MaxCompletionTokens = 2048
		if len(wireTools) > 0 {
			toolReq.ReasoningEffort = "none"
		}
	} else {
		temp := 0.3
		toolReq.Temperature = &temp
		toolReq.MaxTokens = 2048
	}
	body, err := json.Marshal(toolReq)
	if err != nil {
		return Message{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := httpc.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("llm: zhipu status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed zhipuToolResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Message{}, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, fmt.Errorf("llm: zhipu error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, errors.New("llm: empty completion")
	}

	choice := parsed.Choices[0].Message
	out := Message{Role: "assistant", Content: strings.TrimSpace(choice.Content)}
	for _, tc := range choice.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out, nil
}
