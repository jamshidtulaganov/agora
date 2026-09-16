package llm

// DefaultOpenAIBaseURL is OpenAI's chat-completions endpoint. The wire format
// is the one ZhipuClient already speaks (Zhipu v4 is OpenAI-compatible), so
// the OpenAI provider is the same client pointed at a different host.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// DefaultOpenAIModel is used when the instance selects the openai provider
// without pinning AGORA_ASSISTANT_MODEL. gpt-4.1-mini: cheap, strong
// tool-calling, and — unlike the gpt-5/o reasoning models — accepts the
// explicit temperature this client sends.
const DefaultOpenAIModel = "gpt-4.1-mini"

// NewOpenAIClient builds a chat client for the OpenAI API. It reuses
// ZhipuClient wholesale rather than duplicating the request/response types:
// the two providers differ only in base URL and key. Provider-labelled error
// strings from the shared client will read "zhipu" — they are logged, never
// shown to end users (the run loop maps them to a generic user-safe message).
func NewOpenAIClient(apiKey string) *ZhipuClient {
	c := NewZhipuClient(apiKey)
	c.BaseURL = DefaultOpenAIBaseURL
	return c
}
