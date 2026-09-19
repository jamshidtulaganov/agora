package slack

// Slack request signing (https://docs.slack.dev/authentication/verifying-requests-from-slack).
//
// Every request Slack sends to an app (Events API, interactivity, slash
// commands) carries:
//
//	X-Slack-Request-Timestamp: 1735689600
//	X-Slack-Signature:         v0=<hex hmac-sha256>
//
// where the MAC is computed over the base string `v0:<timestamp>:<raw body>`
// with the app's signing secret. Two rules from Slack's own docs are load
// bearing and implemented here: reject when the timestamp "differ[s] from
// local time by more than five minutes" (replay window), and compare the
// digests in constant time.
//
// Because the MAC covers the RAW body, an ingress route must read and retain
// the bytes BEFORE any JSON decode — re-serializing a decoded payload changes
// whitespace and key order and the signature will never match.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Signing header names and the version prefix Slack uses today. The version is
// part of the base string, so a future "v1" scheme is a different computation,
// not a different constant — we reject anything that is not v0 rather than
// guessing.
const (
	HeaderSignature = "X-Slack-Signature"
	HeaderTimestamp = "X-Slack-Request-Timestamp"

	signatureVersion = "v0"
)

// SignatureMaxAge is Slack's replay window. Applied in both directions: a
// timestamp far in the future is as suspicious as a stale one.
const SignatureMaxAge = 5 * time.Minute

// Signature verification failures. Distinct sentinels so an ingress handler
// can log which check failed without echoing the request, and so tests assert
// on the reason rather than on a message string.
var (
	ErrNoSigningSecret  = errors.New("slack: signing secret is not configured")
	ErrMissingSignature = errors.New("slack: request is missing signature headers")
	ErrBadTimestamp     = errors.New("slack: request timestamp is not an integer")
	ErrStaleTimestamp   = errors.New("slack: request timestamp outside the replay window")
	ErrBadSignature     = errors.New("slack: signature does not match")
)

// SignatureBaseString is the exact string Slack MACs: version, timestamp and
// the raw body joined by colons. Exported because the golden-vector tests and
// any future signing helper must agree on it byte for byte.
func SignatureBaseString(timestamp string, body []byte) string {
	var sb strings.Builder
	sb.Grow(len(signatureVersion) + len(timestamp) + len(body) + 2)
	sb.WriteString(signatureVersion)
	sb.WriteByte(':')
	sb.WriteString(timestamp)
	sb.WriteByte(':')
	sb.Write(body)
	return sb.String()
}

// ComputeSignature returns the `v0=<hex>` header value for a body and
// timestamp. Used to verify, and by tests to mint valid fixtures.
func ComputeSignature(signingSecret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(SignatureBaseString(timestamp, body)))
	return signatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks one request's signature headers against the raw body.
// `now` is injected so the replay window is testable without sleeping.
//
// Order of checks is deliberate: configuration, then presence, then freshness,
// then the MAC. A stale-but-valid replay and a forged signature are different
// incidents and the caller should be able to tell them apart.
func VerifySignature(signingSecret, signature, timestamp string, body []byte, now time.Time) error {
	if strings.TrimSpace(signingSecret) == "" {
		return ErrNoSigningSecret
	}
	if signature == "" || timestamp == "" {
		return ErrMissingSignature
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return ErrBadTimestamp
	}
	age := now.Sub(time.Unix(ts, 0))
	if age < 0 {
		age = -age
	}
	if age > SignatureMaxAge {
		return fmt.Errorf("%w: %s old", ErrStaleTimestamp, age.Round(time.Second))
	}
	if !strings.HasPrefix(signature, signatureVersion+"=") {
		return ErrBadSignature
	}
	want, err := hex.DecodeString(strings.TrimPrefix(signature, signatureVersion+"="))
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(SignatureBaseString(timestamp, body)))
	// hmac.Equal is constant time: a byte-wise early return would leak the
	// matching prefix length and let an attacker walk the digest.
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrBadSignature
	}
	return nil
}

// VerifyRequestSignature is the http.Header-shaped convenience wrapper the
// ingress handlers use. It takes the body explicitly rather than reading
// r.Body, because the caller must retain those bytes anyway (see the package
// note above) and a helper that consumed the body would make that easy to get
// wrong.
func VerifyRequestSignature(signingSecret string, headers http.Header, body []byte, now time.Time) error {
	return VerifySignature(
		signingSecret,
		headers.Get(HeaderSignature),
		headers.Get(HeaderTimestamp),
		body,
		now,
	)
}
