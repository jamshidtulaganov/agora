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
var ErrNotConfigured = errors.New("llm: zhipu api key not configured")

// Message is a single chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
