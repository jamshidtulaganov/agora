package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Email authentication remains enabled even when an older deployment still
// carries AGORA_TELEGRAM_ONLY=true. Agora's current hosted auth is email-first,
// and stale Render environment values must not disable its endpoints.
func TestLegacyTelegramOnlyFlagDoesNotBlockEmailAndGoogleLogin(t *testing.T) {
	t.Setenv("AGORA_TELEGRAM_ONLY", "true")
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
				t.Fatalf("%s with legacy AGORA_TELEGRAM_ONLY: got %d, want 400 field validation (body: %s)", ep.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestEmailAndGoogleLoginOpenByDefault(t *testing.T) {
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
