package handler

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const googleDesktopStateCookie = "agora_google_desktop_state"

// GoogleDesktopStart starts the browser half of Google OAuth for the desktop
// app. The backend owns this flow so a desktop-only deployment does not need a
// separately hosted Next.js service.
func (h *Handler) GoogleDesktopStart(w http.ResponseWriter, r *http.Request) {
	if telegramOnlyLoginGate(w) {
		return
	}

	clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	redirectURI := strings.TrimSpace(os.Getenv("GOOGLE_REDIRECT_URI"))
	if clientID == "" || strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET")) == "" || redirectURI == "" {
		writeError(w, http.StatusServiceUnavailable, "Google login is not configured")
		return
	}

	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start Google login")
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	http.SetCookie(w, &http.Cookie{
		Name:     googleDesktopStateCookie,
		Value:    state,
		Path:     "/auth/google/desktop",
		MaxAge:   int((10 * time.Minute).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	query := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"prompt":        {"select_account"},
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+query.Encode(), http.StatusFound)
}

// GoogleDesktopCallback completes OAuth through the existing JSON GoogleLogin
// implementation and hands its JWT back to Electron via the registered custom
// URL protocol.
func (h *Handler) GoogleDesktopCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(googleDesktopStateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		writeError(w, http.StatusBadRequest, "invalid Google login state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     googleDesktopStateCookie,
		Value:    "",
		Path:     "/auth/google/desktop",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "Google login did not return a code")
		return
	}
	body, _ := json.Marshal(GoogleLoginRequest{
		Code:        code,
		RedirectURI: strings.TrimSpace(os.Getenv("GOOGLE_REDIRECT_URI")),
	})
	internalRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/auth/google", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to complete Google login")
		return
	}
	internalRequest.Header = r.Header.Clone()
	internalRequest.Header.Set("Content-Type", "application/json")
	internalRequest.RemoteAddr = r.RemoteAddr

	capture := newGoogleDesktopCapture()
	h.GoogleLogin(capture, internalRequest)
	if capture.status < http.StatusOK || capture.status >= http.StatusMultipleChoices {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return
	}

	var login LoginResponse
	if err := json.Unmarshal(capture.body.Bytes(), &login); err != nil || login.Token == "" {
		writeError(w, http.StatusBadGateway, "failed to complete Google login")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "agora://auth/callback?token="+url.QueryEscape(login.Token), http.StatusFound)
}

type googleDesktopCapture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newGoogleDesktopCapture() *googleDesktopCapture {
	return &googleDesktopCapture{header: make(http.Header), status: http.StatusOK}
}

func (w *googleDesktopCapture) Header() http.Header {
	return w.header
}

func (w *googleDesktopCapture) WriteHeader(status int) {
	w.status = status
}

func (w *googleDesktopCapture) Write(body []byte) (int, error) {
	return w.body.Write(body)
}
