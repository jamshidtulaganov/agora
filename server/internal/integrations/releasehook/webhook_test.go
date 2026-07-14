package releasehook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSign: the scheme is "sha256=<hex>", deterministic for a fixed key+body,
// and sensitive to both key and body.
func TestSign(t *testing.T) {
	got := Sign("secret", []byte("body"))
	if got[:7] != "sha256=" {
		t.Fatalf("signature must be sha256= prefixed, got %q", got)
	}
	if Sign("secret", []byte("body")) != got {
		t.Fatal("Sign must be deterministic for the same input")
	}
	if Sign("other", []byte("body")) == got {
		t.Fatal("a different key must produce a different signature")
	}
	if Sign("secret", []byte("other")) == got {
		t.Fatal("a different body must produce a different signature")
	}
}

// TestDeliverSignsBody: the receiver can recompute X-Agora-Signature over the
// exact bytes it received, and the event header + JSON body are correct.
func TestDeliverSignsBody(t *testing.T) {
	type received struct {
		sig   string
		event string
		body  []byte
	}
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{sig: r.Header.Get(SignatureHeader), event: r.Header.Get(EventHeader), body: b}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := map[string]any{"event": "release:shipped", "workspace_id": "ws-1", "environment": "production"}
	if err := NewClient().Deliver(context.Background(), srv.URL, "sign-key", "release:shipped", body); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	r := <-got
	if r.event != "release:shipped" {
		t.Errorf("event header = %q, want release:shipped", r.event)
	}
	if r.sig != Sign("sign-key", r.body) {
		t.Errorf("signature %q does not verify over the received body", r.sig)
	}
	// Body is the JSON of the payload.
	var decoded map[string]any
	if err := json.Unmarshal(r.body, &decoded); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if decoded["environment"] != "production" {
		t.Errorf("body missing payload field: %v", decoded)
	}
}

// TestDeliverNoSignatureWhenUnsigned: an unsigned integration sends no
// signature header (receiver can still accept it, just can't verify).
func TestDeliverNoSignatureWhenUnsigned(t *testing.T) {
	sigSeen := "unset"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigSeen = r.Header.Get(SignatureHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := NewClient().Deliver(context.Background(), srv.URL, "", "deploy:recorded", map[string]any{"x": 1}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sigSeen != "" {
		t.Errorf("no signature expected when unsigned, got %q", sigSeen)
	}
}

// TestDeliverNon2xxIsError: a non-2xx response surfaces an error (so the
// dispatcher logs a delivery failure) without panicking.
func TestDeliverNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := NewClient().Deliver(context.Background(), srv.URL, "k", "deploy:recorded", map[string]any{}); err == nil {
		t.Fatal("expected an error on a 500 response")
	}
}

// TestProbeReachability: OPTIONS probe returns the receiver's status and
// reachable=true; an unreachable URL returns reachable=false.
func TestProbeReachability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	status, reachable := NewClient().Probe(context.Background(), srv.URL)
	if !reachable || status != http.StatusNoContent {
		t.Errorf("probe = (%d, %v), want (204, true)", status, reachable)
	}

	// A closed server → transport error → reachable=false.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	if _, reachable := NewClient().Probe(context.Background(), deadURL); reachable {
		t.Error("a closed server must report reachable=false")
	}
}
