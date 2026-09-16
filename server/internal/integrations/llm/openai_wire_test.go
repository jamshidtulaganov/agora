package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureWire returns the raw request body the client sent for the given model.
func captureWire(t *testing.T, model string) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient("sk-test")
	c.BaseURL = srv.URL
	if _, err := c.CompleteWithTools(context.Background(), model, []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	return got
}

// Reasoning models (gpt-5*/o*) reject temperature and max_tokens; the client
// must send max_completion_tokens and omit both classic params.
func TestReasoningModelWire(t *testing.T) {
	got := captureWire(t, "gpt-5.6-luna")
	if _, present := got["temperature"]; present {
		t.Fatalf("temperature must be omitted for reasoning models, body: %v", got)
	}
	if _, present := got["max_tokens"]; present {
		t.Fatalf("max_tokens must be omitted for reasoning models, body: %v", got)
	}
	if got["max_completion_tokens"] == nil {
		t.Fatalf("max_completion_tokens missing, body: %v", got)
	}
}

// gpt-5.x chat completions refuse function tools unless reasoning_effort is
// "none"; without tools the field must stay absent so the model keeps its
// default reasoning for plain answers.
func TestReasoningEffortOnlyWithTools(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient("sk-test")
	c.BaseURL = srv.URL
	tool := Tool{Name: "t", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}

	if _, err := c.CompleteWithTools(context.Background(), "gpt-5.6-luna", []Message{{Role: "user", Content: "hi"}}, []Tool{tool}); err != nil {
		t.Fatalf("with tools: %v", err)
	}
	if got["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort = %v, want none", got["reasoning_effort"])
	}

	// Fresh map — json.Decode into a non-nil map merges keys from the
	// previous request instead of replacing them.
	got = nil
	if _, err := c.CompleteWithTools(context.Background(), "gpt-5.6-luna", []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("without tools: %v", err)
	}
	if _, present := got["reasoning_effort"]; present {
		t.Fatalf("reasoning_effort must be absent without tools, body: %v", got)
	}
}

// Non-reasoning models keep the classic params.
func TestClassicModelWire(t *testing.T) {
	got := captureWire(t, "gpt-4.1-mini")
	if got["temperature"] == nil {
		t.Fatalf("temperature missing for classic model, body: %v", got)
	}
	if got["max_tokens"] == nil {
		t.Fatalf("max_tokens missing for classic model, body: %v", got)
	}
	if _, present := got["max_completion_tokens"]; present {
		t.Fatalf("max_completion_tokens must be omitted for classic model, body: %v", got)
	}
}
