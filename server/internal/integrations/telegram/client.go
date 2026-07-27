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
	"mime/multipart"
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
	ChatID      string       `json:"chat_id"`
	Text        string       `json:"text"`
	ParseMode   string       `json:"parse_mode,omitempty"`
	ReplyMarkup *replyMarkup `json:"reply_markup,omitempty"`
}

// replyMarkup models the inline keyboard used by push DMs (a single URL button
// that opens the Mini App). Only the subset we need is modeled.
type replyMarkup struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

// Button is the public shape for building inline keyboards (a URL button OR a
// callback button). Exactly one of URL / CallbackData is set per button.
type Button struct {
	Text         string
	URL          string
	CallbackData string
}

func toRows(rows [][]Button) [][]inlineButton {
	out := make([][]inlineButton, len(rows))
	for i, row := range rows {
		out[i] = make([]inlineButton, len(row))
		for j, b := range row {
			out[i][j] = inlineButton{Text: b.Text, URL: b.URL, CallbackData: b.CallbackData}
		}
	}
	return out
}

// BotCommand is one entry in the bot's command menu (setMyCommands).
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
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
	return c.sendMessage(ctx, sendMessageRequest{ChatID: chatID, Text: text})
}

// SendMessageWithButton delivers an HTML-formatted message plus a single inline
// URL button (used by push DMs to deep-link into the Mini App). The text is
// parsed as Telegram HTML (parse_mode=HTML), so callers MUST HTML-escape any
// dynamic content. A blank url degrades to a plain (button-free) HTML message.
// Separate from SendMessage so the OTP login path stays plain + button-free.
func (c *BotClient) SendMessageWithButton(ctx context.Context, chatID, text, buttonText, url string) error {
	req := sendMessageRequest{ChatID: chatID, Text: text, ParseMode: "HTML"}
	if strings.TrimSpace(url) != "" {
		req.ReplyMarkup = &replyMarkup{
			InlineKeyboard: [][]inlineButton{{{Text: buttonText, URL: url}}},
		}
	}
	return c.sendMessage(ctx, req)
}

func (c *BotClient) sendMessage(ctx context.Context, reqBody sendMessageRequest) error {
	if c.token == "" {
		return ErrNoToken
	}
	if strings.TrimSpace(reqBody.ChatID) == "" {
		return errors.New("telegram: chat id required")
	}

	body, err := json.Marshal(reqBody)
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

// call POSTs a JSON body to an arbitrary Bot API method and checks the ok
// envelope. Backs the non-sendMessage methods (callbacks, edits, commands).
func (c *BotClient) call(ctx context.Context, method string, body any) error {
	if c.token == "" {
		return ErrNoToken
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram: marshal %s: %w", method, err)
	}
	url := fmt.Sprintf("%s/bot%s/%s", c.baseURL(), c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("telegram: new %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s http: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read %s response: %w", method, err)
	}
	var parsed telegramResponse
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("telegram: %s http %d", method, resp.StatusCode)
		}
		return fmt.Errorf("telegram: decode %s response: %w", method, jsonErr)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram: %s failed: code=%d description=%q", method, parsed.ErrorCode, parsed.Description)
	}
	return nil
}

// SendButtons sends an HTML message with an inline keyboard (URL and/or callback
// buttons). Callers MUST HTML-escape dynamic text. A nil/empty rows sends plain.
func (c *BotClient) SendButtons(ctx context.Context, chatID, text string, rows [][]Button) error {
	req := sendMessageRequest{ChatID: chatID, Text: text, ParseMode: "HTML"}
	if len(rows) > 0 {
		req.ReplyMarkup = &replyMarkup{InlineKeyboard: toRows(rows)}
	}
	return c.sendMessage(ctx, req)
}

// EditButtons edits an existing message's text + inline keyboard, used by the
// create wizard to advance the same message through its steps.
func (c *BotClient) EditButtons(ctx context.Context, chatID string, messageID int64, text string, rows [][]Button) error {
	body := struct {
		ChatID      string       `json:"chat_id"`
		MessageID   int64        `json:"message_id"`
		Text        string       `json:"text"`
		ParseMode   string       `json:"parse_mode"`
		ReplyMarkup *replyMarkup `json:"reply_markup,omitempty"`
	}{ChatID: chatID, MessageID: messageID, Text: text, ParseMode: "HTML"}
	if len(rows) > 0 {
		body.ReplyMarkup = &replyMarkup{InlineKeyboard: toRows(rows)}
	}
	return c.call(ctx, "editMessageText", body)
}

// AnswerCallback acknowledges a callback query so the client stops its inline
// loading spinner. Best-effort; the UI change is done via EditButtons/SendButtons.
func (c *BotClient) AnswerCallback(ctx context.Context, callbackID string) error {
	return c.call(ctx, "answerCallbackQuery", struct {
		CallbackQueryID string `json:"callback_query_id"`
	}{CallbackQueryID: callbackID})
}

// SetMyCommands registers the bot's command menu (the "/" list in the chat UI).
func (c *BotClient) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.call(ctx, "setMyCommands", struct {
		Commands []BotCommand `json:"commands"`
	}{Commands: cmds})
}

// GetUpdates long-polls the Bot API for inbound updates, returning each update
// as raw JSON so this package need not model the full update schema (the
// handler layer owns it). offset is the next update_id to fetch (highest seen +
// 1); timeoutSec is the server-side long-poll window. Restricted to message
// updates — login only cares about "/start" DMs. This is the self-host path:
// when the backend has no public URL, Telegram cannot reach the webhook, so the
// server polls instead.
func (c *BotClient) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]json.RawMessage, error) {
	if c.token == "" {
		return nil, ErrNoToken
	}
	// allowed_updates=["message"], URL-encoded.
	url := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=%d&allowed_updates=%%5B%%22message%%22%%5D",
		c.baseURL(), c.token, offset, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: new getUpdates request: %w", err)
	}
	// The HTTP read must outlast the server-side long-poll window, so the
	// default 10s per-call timeout would cut a 25s poll short. Use a
	// poll-aware client when the caller hasn't injected one (tests do).
	client := c.httpClient()
	if c.HTTPClient == nil {
		client = &http.Client{Timeout: time.Duration(timeoutSec+10) * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: getUpdates http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("telegram: read getUpdates response: %w", err)
	}
	var parsed struct {
		telegramResponse
		Result []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("telegram: decode getUpdates response: %w", err)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram: getUpdates failed: code=%d description=%q", parsed.ErrorCode, parsed.Description)
	}
	return parsed.Result, nil
}

// DeleteWebhook clears any registered webhook. getUpdates and a webhook are
// mutually exclusive (Telegram rejects getUpdates with 409 while a webhook is
// set), so the long-poll login path calls this once before it starts polling.
func (c *BotClient) DeleteWebhook(ctx context.Context) error {
	if c.token == "" {
		return ErrNoToken
	}
	url := fmt.Sprintf("%s/bot%s/deleteWebhook", c.baseURL(), c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("telegram: new deleteWebhook request: %w", err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: deleteWebhook http: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// WebhookInfo is the subset of the Bot API's getWebhookInfo result the login
// flow needs: whether a webhook is registered and where it points.
type WebhookInfo struct {
	URL string `json:"url"`
}

// GetWebhookInfo reports the bot's currently registered webhook (URL is empty
// when none is set). The long-poll login path checks this before DeleteWebhook
// so a self-host/dev backend that shares a public deployment's bot token cannot
// clobber that deployment's webhook and steal its login updates.
func (c *BotClient) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	if c.token == "" {
		return WebhookInfo{}, ErrNoToken
	}
	url := fmt.Sprintf("%s/bot%s/getWebhookInfo", c.baseURL(), c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return WebhookInfo{}, fmt.Errorf("telegram: new getWebhookInfo request: %w", err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return WebhookInfo{}, fmt.Errorf("telegram: getWebhookInfo http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return WebhookInfo{}, fmt.Errorf("telegram: read getWebhookInfo response: %w", err)
	}
	var parsed struct {
		telegramResponse
		Result WebhookInfo `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return WebhookInfo{}, fmt.Errorf("telegram: decode getWebhookInfo response: %w", err)
	}
	if !parsed.OK {
		return WebhookInfo{}, fmt.Errorf("telegram: getWebhookInfo failed: code=%d description=%q", parsed.ErrorCode, parsed.Description)
	}
	return parsed.Result, nil
}

// setWebhookRequest is the JSON body for setWebhook. secret_token is echoed
// back by Telegram in the X-Telegram-Bot-Api-Secret-Token header on every
// delivered update; the webhook handler compares it against
// TELEGRAM_WEBHOOK_SECRET to reject forged calls. Empty fields are omitted so
// Telegram keeps its defaults.
type setWebhookRequest struct {
	URL            string   `json:"url"`
	SecretToken    string   `json:"secret_token,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

// SetWebhook registers webhookURL as the bot's update-delivery endpoint — the
// inverse of DeleteWebhook. A public deployment calls this on startup so
// Telegram POSTs "/start login_<nonce>" updates to the backend instead of the
// long-poll fallback. secretToken is validated by the webhook handler;
// allowedUpdates narrows delivered update types (["message"] is all the login
// flow needs). Idempotent — re-registering the same URL is a no-op on Telegram.
func (c *BotClient) SetWebhook(ctx context.Context, webhookURL, secretToken string, allowedUpdates []string) error {
	if strings.TrimSpace(webhookURL) == "" {
		return errors.New("telegram: webhook url required")
	}
	return c.call(ctx, "setWebhook", setWebhookRequest{
		URL:            webhookURL,
		SecretToken:    secretToken,
		AllowedUpdates: allowedUpdates,
	})
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

// SendDocument uploads a file to a chat with an optional caption.
//
// Unlike every other method here it is multipart, not JSON — the Bot API takes
// file bytes as a form part. Used to deliver a rendered report as an openable
// document rather than pasting a wall of text into the chat: Telegram cannot
// render a markdown table, so a long report posted as a message is unreadable
// on a phone.
//
// caption is HTML (same parse mode as SendMessage) and Telegram caps it at
// 1024 characters — callers keep it to a headline, not the report.
func (c *BotClient) SendDocument(ctx context.Context, chatID, filename string, data []byte, caption string) error {
	if c.token == "" {
		return ErrNoToken
	}

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	if err := form.WriteField("chat_id", chatID); err != nil {
		return fmt.Errorf("telegram: sendDocument chat_id: %w", err)
	}
	if caption != "" {
		if err := form.WriteField("caption", caption); err != nil {
			return fmt.Errorf("telegram: sendDocument caption: %w", err)
		}
		if err := form.WriteField("parse_mode", "HTML"); err != nil {
			return fmt.Errorf("telegram: sendDocument parse_mode: %w", err)
		}
	}
	part, err := form.CreateFormFile("document", filename)
	if err != nil {
		return fmt.Errorf("telegram: sendDocument part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("telegram: sendDocument write: %w", err)
	}
	if err := form.Close(); err != nil {
		return fmt.Errorf("telegram: sendDocument close: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendDocument", c.baseURL(), c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("telegram: new sendDocument request: %w", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendDocument http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read sendDocument response: %w", err)
	}
	var parsed telegramResponse
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("telegram: sendDocument http %d", resp.StatusCode)
		}
		return fmt.Errorf("telegram: decode sendDocument response: %w", jsonErr)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram: sendDocument failed: code=%d description=%q", parsed.ErrorCode, parsed.Description)
	}
	return nil
}
