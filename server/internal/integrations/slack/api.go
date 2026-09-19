package slack

// Slack Web API client (the *app* transport — distinct from the Incoming
// Webhook Client in client.go, which the release connector owns and keeps).
//
// PR 1 needs exactly two methods: auth.test (prove a freshly minted bot token
// works before we store it) and chat.postMessage (the one outbound call every
// later phase is built on). The shape, though, is the shape the Phase 1
// delivery queue needs, because retrofitting it later would mean touching
// every call site:
//
//   - the bot token is an argument, never client state — one process serves
//     every tenant, and a client that remembered a token would eventually post
//     one workspace's issues into another's channel;
//   - every response is decoded into an explicit struct and checked for
//     `ok: true` (CLAUDE.md's "parse, don't cast" applies with equal force to
//     a third-party API the backend consumes);
//   - failures come back as *APIError carrying Slack's own error code and the
//     Retry-After the 429 named, so the queue can honour the delay exactly
//     instead of guessing, and can recognise a dead token (IsTokenInvalid) and
//     mark the installation revoked rather than retrying forever.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBaseURL is Slack's Web API root. Overridable per client so tests
// can point at an httptest server; there is no env knob, because a deployment
// never talks to a different Slack.
const DefaultAPIBaseURL = "https://slack.com/api"

// APIClient calls Slack Web API methods on behalf of an installation.
type APIClient struct {
	http    *http.Client
	baseURL string
}

// APIOption configures an APIClient at construction.
type APIOption func(*APIClient)

// WithAPIBaseURL overrides the Web API root (tests).
func WithAPIBaseURL(base string) APIOption {
	return func(c *APIClient) {
		if trimmed := strings.TrimRight(strings.TrimSpace(base), "/"); trimmed != "" {
			c.baseURL = trimmed
		}
	}
}

// WithHTTPClient supplies the transport (tests, or a tuned pool).
func WithHTTPClient(hc *http.Client) APIOption {
	return func(c *APIClient) {
		if hc != nil {
			c.http = hc
		}
	}
}

// NewAPIClient builds a client with a bounded timeout. Slack's own ACK budget
// for inbound requests is 3s; an outbound call that outlives defaultTimeout is
// already a failure from the user's point of view.
func NewAPIClient(opts ...APIOption) *APIClient {
	c := &APIClient{
		http:    &http.Client{Timeout: defaultTimeout},
		baseURL: DefaultAPIBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is a failed Slack Web API call. Code is Slack's own `error` string
// ("channel_not_found", "ratelimited", "invalid_auth", …) — the delivery queue
// branches on it, so it is preserved verbatim rather than flattened into a
// message.
type APIError struct {
	Method     string
	Code       string
	StatusCode int
	// RetryAfter is the Retry-After header of a 429, in seconds, parsed. Zero
	// when Slack did not send one.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch {
	case e.Code != "":
		return fmt.Sprintf("slack: %s failed: %s", e.Method, e.Code)
	default:
		return fmt.Sprintf("slack: %s failed: http %d", e.Method, e.StatusCode)
	}
}

// rateLimitedCode is the `error` Slack returns alongside a 429.
const rateLimitedCode = "ratelimited"

// IsRateLimited reports whether err is a 429. The caller sleeps RetryAfter
// (from APIError) rather than applying its own backoff: Slack tells us the
// exact delay and a retry storm is worse than a dropped message.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.Code == rateLimitedCode
}

// IsTokenInvalid reports whether Slack says this installation's token is dead.
// Any of these means "stop and mark the installation revoked" — the same
// hygiene as an app_uninstalled event, and the only way to avoid a permanently
// failing queue after someone removes the app in Slack.
func IsTokenInvalid(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "invalid_auth", "token_revoked", "account_inactive", "token_expired", "not_authed":
		return true
	default:
		return false
	}
}

// slackResponse is the envelope every Web API method shares. Embedded in each
// concrete result so a caller can never skip the `ok` check.
type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// AuthTestResult is the subset of auth.test we use: it confirms the token is
// live and reports which bot identity and team it speaks for. Cross-checking
// the team id against the OAuth response is what makes a swapped or replayed
// token visible at install time instead of at first delivery.
type AuthTestResult struct {
	slackResponse
	URL    string `json:"url"`
	Team   string `json:"team"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
	BotID  string `json:"bot_id"`
}

// AuthTest calls auth.test with a bot token.
func (c *APIClient) AuthTest(ctx context.Context, token string) (AuthTestResult, error) {
	var out AuthTestResult
	err := c.call(ctx, "auth.test", token, map[string]any{}, &out, &out.slackResponse)
	return out, err
}

// PostMessageRequest is the chat.postMessage payload we support in Phase 1.
// Text is required even when Blocks is set: it is the notification/fallback
// string Slack shows in push notifications and on clients that cannot render
// the blocks.
type PostMessageRequest struct {
	Channel string  `json:"channel"`
	Text    string  `json:"text"`
	Blocks  []Block `json:"blocks,omitempty"`
	// ThreadTS posts as a reply to an existing message. Empty posts to the
	// channel. Phase 3's thread mirroring is the main consumer.
	ThreadTS string `json:"thread_ts,omitempty"`
	// UnfurlLinks defaults to Slack's own behaviour when nil. Notifications set
	// it false: a card that also unfurls its own link is noise twice over.
	UnfurlLinks *bool `json:"unfurl_links,omitempty"`
}

// PostMessageResult carries the message identity the delivery queue needs to
// thread follow-ups (`ts`) and to record where a notification landed.
type PostMessageResult struct {
	slackResponse
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

// PostMessage delivers a message to a channel as the installation's bot.
//
// chat.postMessage is rate-limited "Special": one message per second per
// channel. This client does not sleep on the caller's behalf — pacing belongs
// to the per-(installation, channel) bucket in the delivery queue, which can
// coalesce. It does surface the 429's Retry-After so that bucket does not have
// to invent one.
func (c *APIClient) PostMessage(ctx context.Context, token string, req PostMessageRequest) (PostMessageResult, error) {
	var out PostMessageResult
	err := c.call(ctx, "chat.postMessage", token, req, &out, &out.slackResponse)
	return out, err
}

// call performs one Web API request: JSON in, JSON out, bearer token,
// `ok`-checked. `env` points at the embedded envelope inside `out` so the
// helper can read the result of the decode without reflection.
func (c *APIClient) call(ctx context.Context, method, token string, payload any, out any, env *slackResponse) error {
	if strings.TrimSpace(token) == "" {
		return &APIError{Method: method, Code: "not_authed"}
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack: marshal %s payload: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("slack: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Agora-Slack/1")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("slack: read %s response: %w", method, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return &APIError{
			Method:     method,
			Code:       rateLimitedCode,
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Slack answers 2xx with `ok:false` for application errors, so a non-2xx
		// is a transport/platform failure and the body is not reliably JSON.
		return &APIError{Method: method, StatusCode: resp.StatusCode}
	}
	// A body that is not the shape we expect must not panic or half-fill the
	// result: decode failure is an error, not a zero value the caller might
	// mistake for success.
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("slack: decode %s response: %w", method, err)
	}
	if !env.OK {
		return &APIError{Method: method, Code: env.Error, StatusCode: resp.StatusCode}
	}
	return nil
}

// retryAfter parses the 429 header. Slack documents it in seconds; anything
// unparseable yields 0 so the caller falls back to its own floor rather than
// sleeping for a garbage duration.
func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
