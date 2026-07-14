// Package slack is a DB-free client for the Slack Incoming Webhook connector
// (release-hub Thread B / Phase 3). It POSTs a plain-text message to a Slack
// Incoming Webhook URL — the URL itself is the credential (possession = auth),
// so it is sealed at rest and only ever handed to this client at delivery time.
//
// Like integrations/releasehook it depends on nothing from the handler/service
// layers so it can be unit-tested against httptest servers without a database.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponseBytes caps how much of Slack's response we read (it replies with a
// tiny "ok"/"invalid_payload" body we never use) so a misbehaving receiver
// can't exhaust memory.
const maxResponseBytes = 1 << 20 // 1 MiB

// defaultTimeout bounds a single delivery at the transport level so a hung
// receiver can't pin a goroutine forever.
const defaultTimeout = 10 * time.Second

// Client posts messages to Slack Incoming Webhook URLs.
type Client struct {
	http *http.Client
}

// NewClient builds a Client with a bounded HTTP timeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: defaultTimeout}}
}

// message is the minimal Incoming Webhook payload. Slack renders `text` with
// mrkdwn, so the caller may embed newlines and "•" bullets directly.
type message struct {
	Text string `json:"text"`
}

// PostMessage delivers text to a Slack Incoming Webhook URL as {"text": ...}.
// On a non-2xx response it returns an error carrying the status (Slack returns
// 200 "ok" on success, 4xx with a reason on a bad payload/expired hook); the
// caller logs it. The response body is drained (up to maxResponseBytes) so the
// connection can be reused. Never panics.
func (c *Client) PostMessage(ctx context.Context, webhookURL, text string) error {
	buf, err := json.Marshal(message{Text: text})
	if err != nil {
		return fmt.Errorf("slack: marshal message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: http %d", resp.StatusCode)
	}
	return nil
}
