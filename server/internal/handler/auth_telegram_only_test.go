package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The AGORA_TELEGRAM_ONLY gate must refuse the email-code and Google login
// endpoints server-side. The frontend hides those methods, but a stale
// desktop build (or curl) calls the endpoints directly — hiding is only
// honest if the endpoints themselves 403.
func TestTelegramOnlyGateRefusesEmailAndGoogleLogin(t *testing.T) {
	t.Setenv("AGORA_TELEGRAM_ONLY", "true")
	h := newTestHandler(Config{AllowSignup: true})

	endpoints := []struct {
		name string
		fn   http.HandlerFunc
		body string
	}{
		{"SendCode", h.SendCode, `{"email":"a@x.com"}`},
		{"VerifyCode", h.VerifyCode, `{"email":"a@x.com","code":"123456"}`},
		{"GoogleLogin", h.GoogleLogin, `{"code":"oauth-code"}`},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(ep.body))
			ep.fn(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s with AGORA_TELEGRAM_ONLY: got %d, want 403 (body: %s)", ep.name, w.Code, w.Body.String())
			}
		})
	}
}

// Without the flag the gate must stay open. Empty-field bodies stop each
// handler at its own 400 validation (before any DB access), which proves the
// request got PAST the 403 gate without needing fixtures.
func TestTelegramOnlyGateOpenByDefault(t *testing.T) {
	t.Setenv("AGORA_TELEGRAM_ONLY", "")
	h := newTestHandler(Config{AllowSignup: true})

	endpoints := []struct {
		name string
		fn   http.HandlerFunc
	}{
		{"SendCode", h.SendCode},
		{"VerifyCode", h.VerifyCode},
		{"GoogleLogin", h.GoogleLogin},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			ep.fn(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s without AGORA_TELEGRAM_ONLY: got %d, want 400 field validation (body: %s)", ep.name, w.Code, w.Body.String())
			}
		})
	}
}
