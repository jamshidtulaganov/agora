package handler

import (
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// The openai provider rides the Zhipu client (same wire format, different
// host); these tests pin the resolution table so a provider flip cannot
// silently fall back to zhipu.
func TestAssistantProviderOpenAI(t *testing.T) {
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ZHIPU_API_KEY", "")
	t.Setenv("AGORA_ASSISTANT_MODEL", "")
	t.Setenv("AGORA_ASSISTANT_ENABLED", "true")

	if got := assistantProvider(); got != "openai" {
		t.Fatalf("provider = %q", got)
	}
	if got := assistantModel("openai"); got != llm.DefaultOpenAIModel {
		t.Fatalf("model = %q, want %q", got, llm.DefaultOpenAIModel)
	}
	if !assistantEnabled() {
		t.Fatal("expected assistant enabled with OPENAI_API_KEY set")
	}

	client, model, err := testHandler.AssistantClient()
	if err != nil {
		t.Fatalf("AssistantClient: %v", err)
	}
	if model != llm.DefaultOpenAIModel {
		t.Fatalf("client model = %q", model)
	}
	zc, ok := client.(*llm.ZhipuClient)
	if !ok {
		t.Fatalf("client type = %T", client)
	}
	if zc.BaseURL != llm.DefaultOpenAIBaseURL {
		t.Fatalf("base url = %q", zc.BaseURL)
	}
}

// A pinned zhipu-default model must not leak to openai; an explicit custom
// model must win.
func TestAssistantModelOpenAIOverride(t *testing.T) {
	t.Setenv("AGORA_ASSISTANT_MODEL", llm.FreeModel)
	if got := assistantModel("openai"); got != llm.DefaultOpenAIModel {
		t.Fatalf("free-model passthrough: got %q", got)
	}
	t.Setenv("AGORA_ASSISTANT_MODEL", "gpt-4.1")
	if got := assistantModel("openai"); got != "gpt-4.1" {
		t.Fatalf("custom model: got %q", got)
	}
}

// Missing key ⇒ disabled, and the client factory refuses.
func TestAssistantOpenAIWithoutKeyDisabled(t *testing.T) {
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ZHIPU_API_KEY", "irrelevant")
	t.Setenv("AGORA_ASSISTANT_ENABLED", "true")

	if assistantEnabled() {
		t.Fatal("expected disabled without OPENAI_API_KEY")
	}
	if _, _, err := testHandler.AssistantClient(); err == nil {
		t.Fatal("expected client factory to refuse")
	}
}
