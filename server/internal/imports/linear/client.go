// Package linear is the Linear source adapter (docs/importers-plan.md §2.1,
// §6 Phase 1). It fetches and normalizes; it never touches the database.
//
// Two scope decisions are baked in here because §2.1 settled them:
//
//   - PERSONAL API KEY ONLY. Linear's OAuth access tokens are valid 24 hours,
//     and an import job must not be able to outlive its own credential.
//     The header is `Authorization: <key>` with NO `Bearer` prefix — a real
//     trap, since `Bearer lin_api_…` fails.
//   - GRAPHQL ONLY. Linear's CSV export carries no comments and no attachment
//     files, so it cannot produce the thing a migration is for.
//
// The rate limit that actually binds is not requests (2,500/hour on a personal
// key) but COMPLEXITY: a single query may not exceed 10,000 points, complexity
// is structural (each scalar 0.1, each object 1) and EACH CONNECTION MULTIPLIES
// ITS CHILDREN'S COST BY ITS PAGE SIZE, defaulting to 50 when `first` is
// omitted. A naive issues(first:50){comments(first:50) attachments(first:50)}
// multiplies out and trips the per-query ceiling on its own.
//
// So this client does two things no ordinary GraphQL client does: it sends a
// page size it chose rather than a constant, and it reads `X-Complexity` and
// `X-RateLimit-Complexity-Remaining` off every response to tune that size for
// the next one. Exact throughput is unverified against a live workspace and is
// deliberately not promised anywhere in the UI.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is Linear's single GraphQL endpoint.
const DefaultEndpoint = "https://api.linear.app/graphql"

// Complexity ceiling and the page-size band the client moves within.
//
// maxPageSize is capped at 100 rather than left open because NO universal
// maximum `first` is documented for the standard connections — one unrelated
// field (templateSearch) documents 250, which must not be generalized. Treat
// the real maximum as unknown and stay well inside it.
const (
	queryComplexityCeiling = 10000
	minPageSize            = 10
	maxPageSize            = 100
	defaultPageSize        = 50
)

// Config builds a client.
type Config struct {
	// APIKey is a Linear personal API key. Sent verbatim, no Bearer prefix.
	APIKey string
	// Endpoint overrides the API host. Tests point this at an httptest server;
	// production leaves it empty.
	Endpoint string
	// HTTPClient is injectable for tests and for a caller that wants its own
	// timeouts. A nil value gets a client with a sane request timeout.
	HTTPClient *http.Client
	// PageSize is the starting page size. The client adapts from here.
	PageSize int
	// MaxRetries bounds the 429/5xx retry loop per request.
	MaxRetries int
	// Sleep is injectable so a rate-limit test does not actually wait.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is a complexity-aware Linear GraphQL client. One per import run.
type Client struct {
	endpoint   string
	apiKey     string
	http       *http.Client
	maxRetries int
	sleep      func(ctx context.Context, d time.Duration) error

	// pageSize is mutable state tuned from response headers. A Client belongs
	// to one run on one goroutine (the adapter's Fetch), so it carries no lock.
	pageSize int
	// lastComplexity / complexityRemaining are the last values the API
	// reported, kept for the plan's honesty about throughput.
	lastComplexity      int
	complexityRemaining int
}

// New builds a client. It does not validate the key — Probe does that, and it
// is the only place that can tell "wrong key" from "Linear is down".
func New(cfg Config) *Client {
	c := &Client{
		endpoint:   strings.TrimSpace(cfg.Endpoint),
		apiKey:     strings.TrimSpace(cfg.APIKey),
		http:       cfg.HTTPClient,
		maxRetries: cfg.MaxRetries,
		sleep:      cfg.Sleep,
		pageSize:   cfg.PageSize,
	}
	if c.endpoint == "" {
		c.endpoint = DefaultEndpoint
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}
	if c.maxRetries <= 0 {
		c.maxRetries = 4
	}
	if c.pageSize <= 0 {
		c.pageSize = defaultPageSize
	}
	c.pageSize = clampPageSize(c.pageSize)
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	return c
}

// PageSize is the size the client will ask for next. Exposed so the adapter can
// report it and a test can assert the adaptation happened.
func (c *Client) PageSize() int { return c.pageSize }

// LastComplexity is the complexity Linear charged for the most recent query.
func (c *Client) LastComplexity() int { return c.lastComplexity }

// ComplexityRemaining is the hourly complexity budget Linear last reported.
// Zero means the header was absent, which is not the same as "exhausted" —
// callers must not treat a missing header as a stop signal.
func (c *Client) ComplexityRemaining() int { return c.complexityRemaining }

// Error is a GraphQL-level failure. HTTPStatus is 0 when the transport
// succeeded and the errors came back in the body, which is the normal GraphQL
// shape and the reason a status check alone is not enough.
type Error struct {
	HTTPStatus int
	Messages   []string
}

func (e *Error) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("linear: http %d", e.HTTPStatus)
	}
	return "linear: " + strings.Join(e.Messages, "; ")
}

// Unauthorized reports whether this error is the credential's fault rather than
// the network's. The distinction is what lets a probe answer "invalid" instead
// of "unreachable" — different problems, different next actions for the
// operator.
func (e *Error) Unauthorized() bool {
	if e.HTTPStatus == http.StatusUnauthorized || e.HTTPStatus == http.StatusForbidden {
		return true
	}
	for _, m := range e.Messages {
		lower := strings.ToLower(m)
		if strings.Contains(lower, "authentication") || strings.Contains(lower, "unauthorized") ||
			strings.Contains(lower, "invalid api key") || strings.Contains(lower, "access denied") {
			return true
		}
	}
	return false
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors"`
}

// Query executes one GraphQL document and decodes `data` into out.
//
// Retries cover 429 (honouring Retry-After) and 5xx. A 4xx that is not 429 is
// not retried — re-sending a query the server rejected only burns budget.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := json.Marshal(graphQLRequest{Query: query, Variables: vars})
		if err != nil {
			return fmt.Errorf("linear: marshal request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("linear: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// NO "Bearer". A personal API key is sent verbatim; prefixing it fails.
		req.Header.Set("Authorization", c.apiKey)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("linear: %w", err)
			if waitErr := c.backoff(ctx, attempt, 0); waitErr != nil {
				return waitErr
			}
			continue
		}

		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		c.readBudgetHeaders(resp.Header)

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = &Error{HTTPStatus: resp.StatusCode, Messages: []string{"rate limited"}}
			if waitErr := c.backoff(ctx, attempt, retryAfter(resp.Header)); waitErr != nil {
				return waitErr
			}
			// A 429 is the clearest possible signal that the page size is too
			// ambitious; shrink before trying again.
			c.shrink()
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = &Error{HTTPStatus: resp.StatusCode, Messages: []string{"linear is unavailable"}}
			if waitErr := c.backoff(ctx, attempt, 0); waitErr != nil {
				return waitErr
			}
			continue
		}
		if readErr != nil {
			return fmt.Errorf("linear: read response: %w", readErr)
		}

		var envelope graphQLResponse
		if err := json.Unmarshal(payload, &envelope); err != nil {
			// A body that is not the GraphQL envelope is a proxy or an error
			// page. Surface the status rather than a JSON parse error, which
			// tells the operator nothing.
			return &Error{HTTPStatus: resp.StatusCode, Messages: []string{"unexpected response from " + c.endpoint}}
		}
		if len(envelope.Errors) > 0 {
			messages := make([]string, 0, len(envelope.Errors))
			for _, e := range envelope.Errors {
				messages = append(messages, e.Message)
			}
			// GraphQL reports errors with HTTP 200, so the status is carried
			// only when it was itself a failure.
			status := 0
			if resp.StatusCode >= 400 {
				status = resp.StatusCode
			}
			return &Error{HTTPStatus: status, Messages: messages}
		}
		if resp.StatusCode >= 400 {
			return &Error{HTTPStatus: resp.StatusCode}
		}
		if out != nil && len(envelope.Data) > 0 {
			if err := json.Unmarshal(envelope.Data, out); err != nil {
				return fmt.Errorf("linear: decode data: %w", err)
			}
		}
		c.adaptPageSize()
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("linear: request failed after %d attempts", c.maxRetries+1)
	}
	return lastErr
}

// readBudgetHeaders records what the response said about this query's cost and
// what is left. Missing headers leave the previous values alone — absence is
// not zero.
func (c *Client) readBudgetHeaders(h http.Header) {
	if v := headerInt(h, "X-Complexity"); v > 0 {
		c.lastComplexity = v
	}
	if v := headerInt(h, "X-RateLimit-Complexity-Remaining"); v > 0 {
		c.complexityRemaining = v
	}
}

// adaptPageSize moves the page size toward the largest value that keeps a query
// comfortably under the 10,000-point ceiling. It is deliberately asymmetric:
// shrink fast (halve), grow slow (+25%), because overshooting the ceiling costs
// a failed query and undershooting only costs a round trip.
func (c *Client) adaptPageSize() {
	if c.lastComplexity <= 0 {
		return
	}
	switch {
	case c.lastComplexity > queryComplexityCeiling*7/10:
		c.shrink()
	case c.lastComplexity < queryComplexityCeiling/4:
		c.pageSize = clampPageSize(c.pageSize + c.pageSize/4 + 1)
	}
}

func (c *Client) shrink() {
	c.pageSize = clampPageSize(c.pageSize / 2)
}

func clampPageSize(n int) int {
	if n < minPageSize {
		return minPageSize
	}
	if n > maxPageSize {
		return maxPageSize
	}
	return n
}

// backoff waits before a retry: the server's Retry-After when it gave one,
// otherwise exponential with a floor, and always cancellable.
func (c *Client) backoff(ctx context.Context, attempt int, retry time.Duration) error {
	if attempt >= c.maxRetries {
		return nil
	}
	wait := retry
	if wait <= 0 {
		wait = time.Duration(1<<attempt) * 500 * time.Millisecond
	}
	if wait > 60*time.Second {
		wait = 60 * time.Second
	}
	return c.sleep(ctx, wait)
}

func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func headerInt(h http.Header, name string) int {
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
