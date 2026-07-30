package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleDesktopStartRedirectsWithState(t *testing.T) {
	t.Setenv("AGORA_TELEGRAM_ONLY", "false")
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URI", "https://api.example.com/auth/google/desktop/callback")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/google/desktop/start", nil)
	(&Handler{}).GoogleDesktopStart(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusFound, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Host != "accounts.google.com" {
		t.Fatalf("redirect host = %q", location.Host)
	}
	if location.Query().Get("state") == "" {
		t.Fatal("OAuth state is empty")
	}
	if location.Query().Get("redirect_uri") != "https://api.example.com/auth/google/desktop/callback" {
		t.Fatalf("redirect_uri = %q", location.Query().Get("redirect_uri"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != googleDesktopStateCookie || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("unexpected state cookie: %#v", cookies)
	}
}

func TestGoogleDesktopCallbackRejectsMissingState(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/google/desktop/callback?code=x", nil)
	(&Handler{}).GoogleDesktopCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "invalid Google login state") {
		t.Fatalf("body = %q", w.Body.String())
	}
}
