// Package releasehook is a DB-free client for the generic release webhook
// connector (release-hub Thread B / Phase 2). It POSTs a release-lifecycle
// event as JSON to an arbitrary receiver URL, optionally HMAC-signing the body
// so the receiver can verify the delivery really came from Agora.
//
// It deliberately depends on nothing from the handler/service layers so it can
// be unit-tested against httptest servers without a database — the same
// separation integrations/bitrix keeps.
package releasehook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SignatureHeader carries the hex HMAC-SHA256 of the request body so a receiver
// can authenticate the delivery. Prefixed "sha256=" like GitHub's webhook
// signatures so integrators recognize the scheme.
const SignatureHeader = "X-Agora-Signature"

// EventHeader echoes the lifecycle event name for cheap routing on the
// receiver side without parsing the body.
const EventHeader = "X-Agora-Event"

// maxResponseBytes caps how much of a receiver's response we read. A webhook
// body we never use, so a misbehaving receiver can't exhaust memory.
const maxResponseBytes = 1 << 20 // 1 MiB

// defaultTimeout bounds a single delivery at the transport level. Callers that
// need a tighter deadline pass their own context; this is the safety net so a
// hung receiver can't pin a goroutine forever.
const defaultTimeout = 10 * time.Second

// ChangelogEntry is one shipped issue in a release:shipped payload — the
// release-notes line a connector renders.
type ChangelogEntry struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Verdict    string `json:"verdict"`
}

// Client delivers signed release events over HTTP.
type Client struct {
	http *http.Client
}

// NewClient builds a Client with a bounded HTTP timeout.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: defaultTimeout}}
}

// Sign returns the "sha256=<hex>" HMAC of body keyed by secret. Exported so the
// dispatcher and tests share ONE definition of the signature scheme.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Deliver POSTs body as JSON to url. When signingSecret is non-empty the body
// is HMAC-signed into the X-Agora-Signature header. event, when non-empty, is
// echoed in X-Agora-Event. On a non-2xx response Deliver returns an error
// carrying the status; the caller logs it (best-effort delivery, never a
// panic). The response body is read (and discarded) up to maxResponseBytes so
// the connection can be reused.
func (c *Client) Deliver(ctx context.Context, url, signingSecret, event string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("releasehook: marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("releasehook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
	if event != "" {
		req.Header.Set(EventHeader, event)
	}
	if signingSecret != "" {
		req.Header.Set(SignatureHeader, Sign(signingSecret, buf))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("releasehook: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("releasehook: http %d", resp.StatusCode)
	}
	return nil
}

// Probe checks a webhook URL's reachability at save time WITHOUT delivering a
// real event: a cheap OPTIONS request that never carries release data. Returns
// the HTTP status (0 with reachable=false on a transport error/timeout).
// Classification into ok/invalid/unreachable lives in the handler
// (classifyReleaseProbe) so it stays unit-testable.
func (c *Client) Probe(ctx context.Context, url string) (status int, reachable bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, url, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("User-Agent", "Agora-Release-Hook/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, true
}
