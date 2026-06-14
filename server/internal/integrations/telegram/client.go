// Package telegram is a thin, DB-free client for the Telegram Bot API used by
// the bot-OTP login flow (NOT the Telegram Login Widget). The project-manager
// bot DMs a 6-digit code to the user who started the bot with a login nonce;
// the handler package layers session issuance + external-identity linking on
// top of the primitives here.
//
// Everything in this package is deliberately free of any database or handler
// dependency so it can be unit-tested with httptest mock servers and pure
// functions (no DATABASE_URL required).
package telegram

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

// defaultBaseURL is the Telegram Bot API root. Bot methods live under
// /bot<token>/<method>; tests substitute an httptest.Server URL via
// BotClient.BaseURL so no real Telegram traffic is generated.
const defaultBaseURL = "https://api.telegram.org"

// defaultRequestTimeout bounds each outbound Bot API call. Telegram is
// normally well under a second; this leaves headroom for cross-region
// latency from a self-hosted deployment.
const defaultRequestTimeout = 10 * time.Second

// startLoginPrefix is the deep-link payload prefix the bot expects. A login
// deep link is "https://t.me/<bot>?start=login_<nonce>", which Telegram
// delivers to the bot as the message text "/start login_<nonce>".
const startLoginPrefix = "login_"

// BotClient talks to a single bot's Telegram Bot API surface. It holds no
// database state. The zero value is not usable — construct one with
// NewBotClient.
type BotClient struct {
	token string

	// BaseURL overrides the Telegram API root. Empty means defaultBaseURL.
	// Tests point this at an httptest.Server. Trailing "/" is stripped on
	// use, so either form is accepted.
	BaseURL string

	// HTTPClient is the transport used for every call. Nil defaults to an
	// *http.Client with defaultRequestTimeout.
	HTTPClient *http.Client
}

// NewBotClient builds a BotClient bound to a bot token. A blank token yields a
// client whose calls fail fast with ErrNoToken so callers can treat "bot not
// configured" uniformly. The router/handler layer constructs nil instead when
// TELEGRAM_BOT_TOKEN is unset; this guard covers a token that is set but empty.
func NewBotClient(token string) *BotClient {
	return &BotClient{token: strings.TrimSpace(token)}
}

// ErrNoToken is returned by calls on a BotClient with an empty token.
var ErrNoToken = errors.New("telegram: bot token not configured")

func (c *BotClient) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBaseURL
}

func (c *BotClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: defaultRequestTimeout}
}

type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// telegramResponse is the common Bot API envelope. On failure Telegram returns
// ok=false plus a human-readable description and an error_code.
type telegramResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// SendMessage delivers text to a chat via the bot's sendMessage method. chatID
// is the Telegram numeric user/chat id rendered as a string (the same value
// the bot receives in an inbound update). Errors are returned so the caller can
// decide whether a failed DM is fatal (verify still works without it) — the
// login store already holds the code regardless of delivery.
func (c *BotClient) SendMessage(ctx context.Context, chatID, text string) error {
	if c.token == "" {
		return ErrNoToken
	}
	if strings.TrimSpace(chatID) == "" {
		return errors.New("telegram: chat id required")
	}

	body, err := json.Marshal(sendMessageRequest{ChatID: chatID, Text: text})
	if err != nil {
		return fmt.Errorf("telegram: marshal sendMessage: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", c.baseURL(), c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendMessage http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read sendMessage response: %w", err)
	}

	var parsed telegramResponse
	// A non-2xx with a parseable envelope yields the richer description; an
	// unparseable body falls back to the status code.
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("telegram: sendMessage http %d", resp.StatusCode)
		}
		return fmt.Errorf("telegram: decode sendMessage response: %w", jsonErr)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram: sendMessage failed: code=%d description=%q", parsed.ErrorCode, parsed.Description)
	}
	return nil
}

// ParseStartPayload extracts the login nonce from a "/start login_<nonce>"
// message. It tolerates surrounding whitespace and an optional "@botname"
// suffix on the command (Telegram appends it in group chats, e.g.
// "/start@my_bot login_abc"). Returns ok=false for any other text — including
// a bare "/start", a "/start" with a non-login payload, or an empty nonce.
func ParseStartPayload(text string) (nonce string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 2 {
		return "", false
	}
	cmd := fields[0]
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		cmd = cmd[:at]
	}
	if cmd != "/start" {
		return "", false
	}
	payload := fields[1]
	if !strings.HasPrefix(payload, startLoginPrefix) {
		return "", false
	}
	nonce = strings.TrimPrefix(payload, startLoginPrefix)
	if nonce == "" {
		return "", false
	}
	return nonce, true
}
