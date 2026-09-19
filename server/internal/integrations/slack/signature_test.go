package slack

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// Slack's own published example (docs.slack.dev, "Verifying requests from
// Slack"). Using their vector rather than a self-generated one is the point:
// it proves the base string is assembled exactly as Slack assembles it —
// `v0:<timestamp>:<raw body>` — and would catch a reordering or a stray
// separator that a round-trip test against our own ComputeSignature would
// happily agree with.
const (
	vectorSigningSecret = "8f742231b10e8888abcd99yyyzzz85a5"
	vectorTimestamp     = "1531420618"
	vectorBody          = "token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c"
	vectorSignature     = "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503"
)

// vectorNow is a clock inside the 5-minute window of vectorTimestamp.
func vectorNow() time.Time {
	ts, _ := strconv.ParseInt(vectorTimestamp, 10, 64)
	return time.Unix(ts+30, 0)
}

func TestComputeSignature_MatchesSlackPublishedVector(t *testing.T) {
	got := ComputeSignature(vectorSigningSecret, vectorTimestamp, []byte(vectorBody))
	if got != vectorSignature {
		t.Fatalf("signature mismatch\n got: %s\nwant: %s", got, vectorSignature)
	}
}

func TestSignatureBaseString(t *testing.T) {
	got := SignatureBaseString("123", []byte(`{"a":1}`))
	if want := `v0:123:{"a":1}`; got != want {
		t.Fatalf("base string = %q, want %q", got, want)
	}
}

func TestVerifySignature(t *testing.T) {
	ts, _ := strconv.ParseInt(vectorTimestamp, 10, 64)

	tests := []struct {
		name      string
		secret    string
		signature string
		timestamp string
		body      string
		now       time.Time
		wantErr   error
	}{
		{
			name:      "valid",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
		},
		{
			name:      "valid at the edge of the window",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       time.Unix(ts, 0).Add(SignatureMaxAge),
		},
		{
			name:      "expired timestamp",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       time.Unix(ts, 0).Add(SignatureMaxAge + time.Second),
			wantErr:   ErrStaleTimestamp,
		},
		{
			// A clock far in the future is as suspicious as a stale replay, and
			// accepting it would hand an attacker an unbounded replay window.
			name:      "timestamp too far in the future",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       time.Unix(ts, 0).Add(-(SignatureMaxAge + time.Second)),
			wantErr:   ErrStaleTimestamp,
		},
		{
			name:      "tampered body",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody + "&injected=1",
			now:       vectorNow(),
			wantErr:   ErrBadSignature,
		},
		{
			// The timestamp is inside the MAC, so re-dating a captured request
			// invalidates it — replay cannot be laundered by bumping the header.
			name:      "replayed with a fresh timestamp",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: strconv.FormatInt(ts+60, 10),
			body:      vectorBody,
			now:       time.Unix(ts+60, 0),
			wantErr:   ErrBadSignature,
		},
		{
			name:      "wrong signing secret",
			secret:    "not-the-signing-secret",
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrBadSignature,
		},
		{
			name:      "wrong version prefix",
			secret:    vectorSigningSecret,
			signature: "v1=" + vectorSignature[3:],
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrBadSignature,
		},
		{
			name:      "signature is not hex",
			secret:    vectorSigningSecret,
			signature: "v0=zzzz",
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrBadSignature,
		},
		{
			name:      "missing signature header",
			secret:    vectorSigningSecret,
			signature: "",
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrMissingSignature,
		},
		{
			name:      "missing timestamp header",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: "",
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrMissingSignature,
		},
		{
			name:      "non-numeric timestamp",
			secret:    vectorSigningSecret,
			signature: vectorSignature,
			timestamp: "yesterday",
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrBadTimestamp,
		},
		{
			// Degraded mode: a deployment without AGORA_SLACK_SIGNING_SECRET
			// must fail closed, never "verify" by accepting everything.
			name:      "no signing secret configured",
			secret:    "",
			signature: vectorSignature,
			timestamp: vectorTimestamp,
			body:      vectorBody,
			now:       vectorNow(),
			wantErr:   ErrNoSigningSecret,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySignature(tc.secret, tc.signature, tc.timestamp, []byte(tc.body), tc.now)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestVerifyRequestSignature_ReadsHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set(HeaderSignature, vectorSignature)
	headers.Set(HeaderTimestamp, vectorTimestamp)

	if err := VerifyRequestSignature(vectorSigningSecret, headers, []byte(vectorBody), vectorNow()); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	headers.Set(HeaderSignature, "v0=deadbeef")
	if err := VerifyRequestSignature(vectorSigningSecret, headers, []byte(vectorBody), vectorNow()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}
