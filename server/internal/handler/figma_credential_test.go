package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// resetFigmaBox clears the sync.Once-cached secretbox so each test controls
// AGORA_FIGMA_SECRET_KEY. Same-package tests may touch the private vars.
func resetFigmaBox() {
	figmaBoxOnce = sync.Once{}
	figmaBoxVal = nil
	figmaBoxErr = nil
}

func figmaTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// putFigmaRequest builds an authenticated-shaped PUT request with the
// workspace id routed the way the real router does it.
func putFigmaRequest(t *testing.T, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/workspaces/33333333-3333-3333-3333-333333333333/figma-credential", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "33333333-3333-3333-3333-333333333333")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	return httptest.NewRecorder(), r
}

func TestClassifyFigmaProbe(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		reachable   bool
		wantStatus  string
		wantInvalid bool
	}{
		{"transport error", 0, false, "unreachable", false},
		{"200 ok", http.StatusOK, true, "ok", false},
		{"401 invalid", http.StatusUnauthorized, true, "invalid", true},
		{"403 invalid", http.StatusForbidden, true, "invalid", true},
		// Figma-side conditions must NOT block saving with a wrong 422.
		{"429 rate limited", http.StatusTooManyRequests, true, "unreachable", false},
		{"500 outage", http.StatusInternalServerError, true, "unreachable", false},
		{"503 outage", http.StatusServiceUnavailable, true, "unreachable", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotInvalid := classifyFigmaProbe(tt.status, tt.reachable)
			if gotStatus != tt.wantStatus || gotInvalid != tt.wantInvalid {
				t.Errorf("classifyFigmaProbe(%d, %v) = (%q, %v), want (%q, %v)",
					tt.status, tt.reachable, gotStatus, gotInvalid, tt.wantStatus, tt.wantInvalid)
			}
		})
	}
}

func TestPutFigmaCredential_RequestValidation(t *testing.T) {
	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)
	h := &Handler{}

	tests := []struct {
		name string
		body string
		want int
	}{
		{"invalid json", `{not json`, http.StatusBadRequest},
		{"missing token", `{"label":"x"}`, http.StatusBadRequest},
		{"blank token", `{"token":"   "}`, http.StatusBadRequest},
		{"probe file key traversal", `{"token":"figd_x","probe_file_key":"x/../../v1/teams/1"}`, http.StatusBadRequest},
		{"probe file key too short", `{"token":"figd_x","probe_file_key":"short"}`, http.StatusBadRequest},
		{"bad expires_at", `{"token":"figd_x","expires_at":"not-a-date"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r := putFigmaRequest(t, tt.body)
			h.PutFigmaCredential(w, r)
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestPutFigmaCredential_FailsClosedWithoutSecretKey(t *testing.T) {
	t.Setenv("AGORA_FIGMA_SECRET_KEY", "")
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)
	h := &Handler{}

	w, r := putFigmaRequest(t, `{"token":"figd_x"}`)
	h.PutFigmaCredential(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when AGORA_FIGMA_SECRET_KEY is unset", w.Code)
	}
}

func TestPutFigmaCredential_RejectsAuthFailedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	orig := figmaAPIBase
	figmaAPIBase = srv.URL
	defer func() { figmaAPIBase = orig }()

	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)
	h := &Handler{}

	w, r := putFigmaRequest(t, `{"token":"figd_wrong"}`)
	h.PutFigmaCredential(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 on a Figma auth rejection (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "figma_token_invalid") {
		t.Errorf("body should carry figma_token_invalid, got %s", w.Body.String())
	}
}

func TestPutFigmaCredential_FigmaOutageIsNot422(t *testing.T) {
	// A 503 from Figma must NOT be classified as an invalid token. The save
	// then proceeds to the DB layer — this handler test has no DB, so any
	// status other than 422 (and not a validation 400) proves the
	// classification; the concrete write is covered by the sqlc layer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	orig := figmaAPIBase
	figmaAPIBase = srv.URL
	defer func() { figmaAPIBase = orig }()

	t.Setenv("AGORA_FIGMA_SECRET_KEY", figmaTestKey(t))
	resetFigmaBox()
	t.Cleanup(resetFigmaBox)
	h := &Handler{}

	defer func() {
		// The nil-Queries DB write panics after classification — that is
		// EXPECTED here; reaching it means the 422 path was not taken.
		_ = recover()
	}()
	w, r := putFigmaRequest(t, `{"token":"figd_valid_but_figma_down"}`)
	h.PutFigmaCredential(w, r)
	if w.Code == http.StatusUnprocessableEntity {
		t.Errorf("a Figma outage must not reject the token as invalid (422)")
	}
}

func TestFigmaCredentialStatusFromRow_NeverLeaksToken(t *testing.T) {
	sealed := []byte("sealed-token-material-figd_secret123")
	row := db.FigmaCredential{
		ID:             pgtype.UUID{Valid: true},
		WorkspaceID:    pgtype.UUID{Valid: true},
		Label:          "SD design",
		TokenEncrypted: sealed,
		TokenLast4:     "d123",
		TokenKind:      "pat",
		ExpiresAt:      pgtype.Timestamptz{Time: time.Now().Add(10 * 24 * time.Hour), Valid: true},
		SeatProbe:      "ok",
		ProbeStatus:    "ok",
		ProbedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	resp := figmaCredentialStatusFromRow(row)
	buf, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(buf), "figd_secret123") || strings.Contains(string(buf), "sealed-token-material") {
		t.Fatalf("status response leaks token material: %s", buf)
	}
	if !resp.Configured {
		t.Error("configured should be true")
	}
	if resp.TokenLast4 != "d123" {
		t.Errorf("token_last4 = %q, want d123", resp.TokenLast4)
	}
	if !resp.ExpiringSoon {
		t.Error("10 days out must flag expiring_soon (<14d)")
	}

	row.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(60 * 24 * time.Hour), Valid: true}
	if figmaCredentialStatusFromRow(row).ExpiringSoon {
		t.Error("60 days out must not flag expiring_soon")
	}
}
