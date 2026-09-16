package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testTools() []Tool {
	return []Tool{{
		Name:        "list_workspaces",
		Description: "List the workspaces the user belongs to.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}}
}

// ---------------------------------------------------------------------------
// Zhipu
// ---------------------------------------------------------------------------

func TestZhipuCompleteWithToolsParsesToolCall(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"list_workspaces","arguments":"{}"}}
		]}}]}`)
	}))
	defer srv.Close()

	c := &ZhipuClient{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	msg, err := c.CompleteWithTools(context.Background(), "glm-4.5-flash",
		[]Message{{Role: "user", Content: "what are my workspaces"}}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (%+v)", len(msg.ToolCalls), msg)
	}
	if msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Name != "list_workspaces" || msg.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("unexpected tool call: %+v", msg.ToolCalls[0])
	}

	// The tool catalog must reach the provider in its OpenAI-compatible shape.
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool in request, got %v", gotBody["tools"])
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "list_workspaces" {
		t.Fatalf("tool not sent as a function spec: %v", tools[0])
	}
}

// A tool answer must ride back as role="tool" + tool_call_id, and the assistant
// turn that requested it must re-serialize into the nested `function` shape —
// otherwise the provider rejects the follow-up round with "unknown tool call".
func TestZhipuCompleteWithToolsRoundTripsHistory(t *testing.T) {
	var gotBody struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"You are in 2 workspaces."}}]}`)
	}))
	defer srv.Close()

	c := &ZhipuClient{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	msg, err := c.CompleteWithTools(context.Background(), "", []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "list_workspaces", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"workspaces":[]}`},
	}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "You are in 2 workspaces." || len(msg.ToolCalls) != 0 {
		t.Fatalf("unexpected reply: %+v", msg)
	}
	if len(gotBody.Messages) != 3 {
		t.Fatalf("expected 3 messages on the wire, got %d", len(gotBody.Messages))
	}
	assistant := gotBody.Messages[1]
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Type != "function" ||
		assistant.ToolCalls[0].Function.Name != "list_workspaces" {
		t.Fatalf("assistant turn lost its function shape: %+v", assistant)
	}
	if gotBody.Messages[2].Role != "tool" || gotBody.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("tool turn lost its tool_call_id: %+v", gotBody.Messages[2])
	}
}

func TestZhipuCompleteWithToolsMalformedJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":`)
	}))
	defer srv.Close()

	c := &ZhipuClient{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.CompleteWithTools(context.Background(), "", []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestZhipuCompleteWithToolsNoKeyIsNotConfigured(t *testing.T) {
	c := NewZhipuClient("")
	if _, err := c.CompleteWithTools(context.Background(), "", nil, nil); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Anthropic
// ---------------------------------------------------------------------------

func TestAnthropicCompleteWithToolsParsesToolUse(t *testing.T) {
	var headers http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"content":[
			{"type":"text","text":"Let me look."},
			{"type":"tool_use","id":"toolu_1","name":"list_workspaces","input":{}}
		],"stop_reason":"tool_use"}`)
	}))
	defer srv.Close()

	c := &AnthropicClient{APIKey: "sk-test", BaseURL: srv.URL, HTTP: srv.Client()}
	msg, err := c.CompleteWithTools(context.Background(), "", []Message{
		{Role: "system", Content: "You are the Agora Assistant."},
		{Role: "user", Content: "what are my workspaces"},
	}, testTools())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "Let me look." {
		t.Fatalf("unexpected text: %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "toolu_1" || msg.ToolCalls[0].Name != "list_workspaces" {
		t.Fatalf("unexpected tool calls: %+v", msg.ToolCalls)
	}
	if headers.Get("x-api-key") != "sk-test" || headers.Get("anthropic-version") != anthropicVersion {
		t.Fatalf("missing auth/version headers: %v", headers)
	}
	// System turns are lifted out of messages into the top-level field.
	if gotBody["system"] != "You are the Agora Assistant." {
		t.Fatalf("system prompt not lifted: %v", gotBody["system"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected only the user turn in messages, got %v", msgs)
	}
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tool not sent with input_schema: %v", gotBody["tools"])
	}
}

// Anthropic requires every tool_use to be answered by tool_result blocks in the
// ONE user turn that follows. Two tool answers in a row must therefore collapse
// into a single message, not two.
func TestAnthropicCollapsesConsecutiveToolResults(t *testing.T) {
	system, msgs := buildAnthropicMessages([]Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "working", ToolCalls: []ToolCall{
			{ID: "a", Name: "one", Arguments: `{"x":1}`},
			{ID: "b", Name: "two"},
		}},
		{Role: "tool", ToolCallID: "a", Content: `{"ok":true}`},
		{Role: "tool", ToolCallID: "b", Content: `{"ok":false}`},
	})
	if system != "sys" {
		t.Fatalf("system = %q", system)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected user/assistant/tool-results, got %d: %+v", len(msgs), msgs)
	}
	assistant := msgs[1]
	if len(assistant.Content) != 3 || assistant.Content[0].Type != "text" ||
		assistant.Content[1].Type != "tool_use" || assistant.Content[2].Type != "tool_use" {
		t.Fatalf("assistant blocks wrong: %+v", assistant.Content)
	}
	// A tool call with no arguments still has to carry an object input.
	if string(assistant.Content[2].Input) != "{}" {
		t.Fatalf("empty arguments must become {}, got %q", assistant.Content[2].Input)
	}
	results := msgs[2]
	if results.Role != "user" || len(results.Content) != 2 {
		t.Fatalf("tool results did not collapse into one user turn: %+v", results)
	}
	if results.Content[0].ToolUseID != "a" || results.Content[1].ToolUseID != "b" {
		t.Fatalf("tool_use_id mapping wrong: %+v", results.Content)
	}
}

func TestAnthropicCompleteWithToolsMalformedJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":`)
	}))
	defer srv.Close()

	c := &AnthropicClient{APIKey: "sk", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.CompleteWithTools(context.Background(), "", []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// An unknown content-block type is enum drift, not a crash: the known blocks
// still parse and the unknown one is dropped.
func TestAnthropicUnknownBlockTypeDowngrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[
			{"type":"thinking","thinking":"hmm"},
			{"type":"text","text":"done"}
		]}`)
	}))
	defer srv.Close()

	c := &AnthropicClient{APIKey: "sk", BaseURL: srv.URL, HTTP: srv.Client()}
	msg, err := c.CompleteWithTools(context.Background(), "", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "done" || len(msg.ToolCalls) != 0 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestAnthropicNoKeyIsNotConfigured(t *testing.T) {
	c := NewAnthropicClient("  ")
	if _, err := c.CompleteWithTools(context.Background(), "", nil, nil); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

// Non-2xx bodies must surface as an error carrying the provider status, never
// be parsed as a completion.
func TestProviderErrorStatusSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	z := &ZhipuClient{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := z.CompleteWithTools(context.Background(), "", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "429") {
		t.Fatalf("zhipu: expected a 429 error, got %v", err)
	}

	a := &AnthropicClient{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := a.CompleteWithTools(context.Background(), "", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "429") {
		t.Fatalf("anthropic: expected a 429 error, got %v", err)
	}
}

// Both clients must satisfy ToolChat — the assistant service only ever holds
// the interface.
var (
	_ ToolChat = (*ZhipuClient)(nil)
	_ ToolChat = (*AnthropicClient)(nil)
)
