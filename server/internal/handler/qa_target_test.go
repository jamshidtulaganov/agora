package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResponseBlocksFraming covers the Live-pane embeddability probe: the
// header check that decides whether a QA target can be embedded in a
// cross-origin iframe. Tokenizes the CSP frame-ancestors source list rather
// than substring-matching for "*" — a scoped subdomain wildcard inside one
// source value (e.g. `https://*.telegram.org`) is NOT the CSP special token
// `*` (any origin), and a naive strings.Contains would wrongly read it as
// wide open. Only an exact `*` token means "any origin may frame this."
func TestResponseBlocksFraming(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantBlocked bool
	}{
		{
			name:        "no headers at all -> not blocked",
			headers:     map[string]string{},
			wantBlocked: false,
		},
		{
			name:        "X-Frame-Options: DENY",
			headers:     map[string]string{"X-Frame-Options": "DENY"},
			wantBlocked: true,
		},
		{
			name:        "X-Frame-Options: SAMEORIGIN",
			headers:     map[string]string{"X-Frame-Options": "sameorigin"},
			wantBlocked: true,
		},
		{
			name:        "CSP frame-ancestors bare wildcard -> open, not blocked",
			headers:     map[string]string{"Content-Security-Policy": "frame-ancestors *"},
			wantBlocked: false,
		},
		{
			name: "scoped subdomain wildcard inside a source value -> BLOCKED",
			headers: map[string]string{
				"Content-Security-Policy": "frame-ancestors 'self' https://web.telegram.org https://*.telegram.org",
			},
			wantBlocked: true,
		},
		{
			name:        "frame-ancestors 'self' only -> blocked (no wildcard token)",
			headers:     map[string]string{"Content-Security-Policy": "frame-ancestors 'self'"},
			wantBlocked: true,
		},
		{
			name:        "unrelated CSP directives, no frame-ancestors -> not blocked",
			headers:     map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self' *.example.com"},
			wantBlocked: false,
		},
		{
			name: "frame-ancestors among multiple directives -> still detected",
			headers: map[string]string{
				"Content-Security-Policy": "default-src 'self'; frame-ancestors 'self'; script-src 'self'",
			},
			wantBlocked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			if got := responseBlocksFraming(h); got != tt.wantBlocked {
				t.Errorf("responseBlocksFraming(%v) = %v, want %v", tt.headers, got, tt.wantBlocked)
			}
		})
	}
}

// TestUrlAllowsFraming_WalksRedirectChain proves the function checks EVERY
// hop's headers, not just the final response Go's client happens to land
// on: a block on an intermediate redirect hop must not be missed just because
// the final destination looks clean.
func TestUrlAllowsFraming_WalksRedirectChain(t *testing.T) {
	t.Run("block on the redirect hop is caught even though the final page is clean", func(t *testing.T) {
		final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK) // no restrictive headers here
		}))
		defer final.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
			http.Redirect(w, r, final.URL, http.StatusFound)
		}))
		defer redirector.Close()

		if urlAllowsFraming(context.Background(), redirector.URL) {
			t.Error("expected false — the redirect hop itself carries a blocking CSP")
		}
	})

	t.Run("clean chain end to end -> true", func(t *testing.T) {
		final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer final.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, final.URL, http.StatusFound)
		}))
		defer redirector.Close()

		if !urlAllowsFraming(context.Background(), redirector.URL) {
			t.Error("expected true — neither hop sets a blocking header")
		}
	})

	t.Run("unreachable target -> false (fail closed)", func(t *testing.T) {
		if urlAllowsFraming(context.Background(), "http://127.0.0.1:1") {
			t.Error("expected false on a connection failure")
		}
	})
}
