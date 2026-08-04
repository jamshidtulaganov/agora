package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/githubrelease"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// resetReleaseBox clears the sync.Once-cached secretbox so each test controls
// AGORA_RELEASE_SECRET_KEY. Same-package tests may touch the private vars.
func resetReleaseBox() {
	releaseBoxOnce = sync.Once{}
	releaseBoxVal = nil
	releaseBoxErr = nil
}

func releaseTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func createReleaseRequest(t *testing.T, wsID, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/release-integrations", strings.NewReader(body))
	r.Header.Set("X-User-ID", testUserID)
	r = withURLParam(r, "id", wsID)
	return httptest.NewRecorder(), r
}

func TestClassifyReleaseProbe(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		reachable   bool
		wantStatus  string
		wantInvalid bool
	}{
		{"transport error", 0, false, "unreachable", false},
		{"200 ok", http.StatusOK, true, "ok", false},
		{"204 ok", http.StatusNoContent, true, "ok", false},
		{"301 ok", http.StatusMovedPermanently, true, "ok", false},
		{"401 invalid", http.StatusUnauthorized, true, "invalid", true},
		{"403 invalid", http.StatusForbidden, true, "invalid", true},
		// 404/405/5xx are receiver-side conditions, NOT an auth rejection — save.
		{"404 unreachable", http.StatusNotFound, true, "unreachable", false},
		{"405 unreachable", http.StatusMethodNotAllowed, true, "unreachable", false},
		{"500 unreachable", http.StatusInternalServerError, true, "unreachable", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotInvalid := classifyReleaseProbe(tt.status, tt.reachable)
			if gotStatus != tt.wantStatus || gotInvalid != tt.wantInvalid {
				t.Errorf("classifyReleaseProbe(%d,%v) = (%q,%v), want (%q,%v)",
					tt.status, tt.reachable, gotStatus, gotInvalid, tt.wantStatus, tt.wantInvalid)
			}
		})
	}
}

func TestNormalizeReleaseEvents(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"both", []string{"deploy_recorded", "release_shipped"}, []string{"deploy_recorded", "release_shipped"}},
		{"dedup", []string{"deploy_recorded", "deploy_recorded"}, []string{"deploy_recorded"}},
		{"drop unknown (enum drift)", []string{"deploy_recorded", "future_event"}, []string{"deploy_recorded"}},
		{"all unknown", []string{"nope"}, []string{}},
		{"empty", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeReleaseEvents(tt.in)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("normalizeReleaseEvents(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidWebhookURL(t *testing.T) {
	ok := []string{"https://hooks.example.com/x", "http://localhost:9000/hook"}
	bad := []string{"", "   ", "ftp://example.com", "not a url", "example.com/no-scheme", "https://"}
	for _, u := range ok {
		if _, valid := validWebhookURL(u); !valid {
			t.Errorf("validWebhookURL(%q) = false, want true", u)
		}
	}
	for _, u := range bad {
		if _, valid := validWebhookURL(u); valid {
			t.Errorf("validWebhookURL(%q) = true, want false", u)
		}
	}
}

// TestCreateReleaseIntegration_FailsClosedWithoutKey: with the seal key unset,
// the write endpoint 503s rather than storing a URL in plaintext. DB-free — the
// box check runs before the role lookup.
func TestCreateReleaseIntegration_FailsClosedWithoutKey(t *testing.T) {
	t.Setenv("AGORA_RELEASE_SECRET_KEY", "")
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)
	h := &Handler{}

	w, r := createReleaseRequest(t, "33333333-3333-3333-3333-333333333333",
		`{"kind":"webhook","url":"https://x.example/h","events":["deploy_recorded"]}`)
	h.CreateReleaseIntegration(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when AGORA_RELEASE_SECRET_KEY unset (body: %s)", w.Code, w.Body.String())
	}
}

// TestCreateReleaseIntegration_AgentForbidden: an agent actor (task_token) is
// rejected before any DB or seal work. DB-free.
func TestCreateReleaseIntegration_AgentForbidden(t *testing.T) {
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)
	h := &Handler{}

	w, r := createReleaseRequest(t, "33333333-3333-3333-3333-333333333333",
		`{"kind":"webhook","url":"https://x.example/h","events":["deploy_recorded"]}`)
	r.Header.Set("X-Actor-Source", "task_token")
	r.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	h.CreateReleaseIntegration(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("agent POST: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
}

// TestReleaseIntegrationFromModel_NeverLeaksSecret: the response builder never
// echoes the sealed URL/signing material — only has_secret.
func TestReleaseIntegrationFromModel_NeverLeaksSecret(t *testing.T) {
	row := db.ReleaseIntegration{
		Kind:            "webhook",
		Config:          []byte(`{"name":"prod alerts"}`),
		SecretEncrypted: []byte("sealed-https://secret.example/hook?token=abcd1234"),
		Events:          []string{"release_shipped"},
		Enabled:         true,
		ProbeStatus:     "ok",
	}
	resp := releaseIntegrationFromModel(row)
	if !resp.HasSecret {
		t.Error("has_secret should be true when secret_encrypted is set")
	}
	// The response must not carry any part of the sealed material.
	buf := strings.ToLower(resp.Kind + string(resp.Config) + resp.ProbeStatus + strings.Join(resp.Events, ","))
	if strings.Contains(buf, "secret.example") || strings.Contains(buf, "abcd1234") || strings.Contains(buf, "sealed-") {
		t.Fatalf("response leaks secret material: %+v", resp)
	}
}

// TestReleaseIntegration_MemberForbidden: a plain member cannot create (the
// in-handler role check 403s, defense in depth behind the router group).
func TestReleaseIntegration_MemberForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-release-member", "member")

	w, r := createReleaseRequest(t, wsID,
		`{"kind":"webhook","url":"https://x.example/h","events":["deploy_recorded"]}`)
	testHandler.CreateReleaseIntegration(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member POST: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
}

// TestReleaseIntegration_CreateListDelete: the full admin round-trip. The
// created + listed rows never carry secret material; delete removes the row.
func TestReleaseIntegration_CreateListDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)

	// Probe target: OPTIONS → 204 (reachable, not an auth rejection → ok).
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer probe.Close()

	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-release-crud", "owner")
	secretURL := probe.URL + "/deliveries"

	// Create.
	body := `{"kind":"webhook","name":"prod alerts","url":"` + secretURL + `","secret":"sign-key","events":["deploy_recorded","release_shipped"]}`
	cw, cr := createReleaseRequest(t, wsID, body)
	testHandler.CreateReleaseIntegration(cw, cr)
	if cw.Code != http.StatusOK {
		t.Fatalf("create: status = %d (body: %s)", cw.Code, cw.Body.String())
	}
	if s := cw.Body.String(); strings.Contains(s, secretURL) || strings.Contains(s, "sign-key") {
		t.Fatalf("create response leaked secret material: %s", s)
	}
	if !strings.Contains(cw.Body.String(), `"has_secret":true`) || !strings.Contains(cw.Body.String(), `"probe_status":"ok"`) {
		t.Fatalf("create response missing has_secret/probe_status: %s", cw.Body.String())
	}

	// The stored secret is sealed — plaintext URL must not appear in the column.
	var sealed []byte
	if err := testPool.QueryRow(ctx, `SELECT secret_encrypted FROM release_integration WHERE workspace_id = $1`, wsID).Scan(&sealed); err != nil {
		t.Fatalf("load sealed secret: %v", err)
	}
	if strings.Contains(string(sealed), secretURL) || strings.Contains(string(sealed), "sign-key") {
		t.Fatal("webhook URL / signing secret stored in plaintext")
	}

	// List (member-visible surface): one row, no secret material.
	lw := httptest.NewRecorder()
	lr := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+wsID+"/release-integrations", nil), "id", wsID)
	testHandler.ListReleaseIntegrations(lw, lr)
	if lw.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", lw.Code, lw.Body.String())
	}
	if strings.Contains(lw.Body.String(), secretURL) || strings.Contains(lw.Body.String(), "sign-key") {
		t.Fatalf("list leaked secret material: %s", lw.Body.String())
	}
	if !strings.Contains(lw.Body.String(), `"has_secret":true`) {
		t.Fatalf("list missing has_secret: %s", lw.Body.String())
	}

	// Extract the created id to delete it.
	var intID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM release_integration WHERE workspace_id = $1`, wsID).Scan(&intID); err != nil {
		t.Fatalf("load id: %v", err)
	}
	dw := httptest.NewRecorder()
	dr := newRequest(http.MethodDelete, "/api/workspaces/"+wsID+"/release-integrations/"+intID, nil)
	dr = withURLParams(dr, "id", wsID, "integrationId", intID)
	testHandler.DeleteReleaseIntegration(dw, dr)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d (body: %s)", dw.Code, dw.Body.String())
	}
}

// TestCreateReleaseIntegration_GitHubSealsToken: a github_release create probes
// the PAT (authed GET), seals it (never plaintext), validates required config
// (owner/repo), and never echoes the token back.
func TestCreateReleaseIntegration_GitHubSealsToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)

	// GitHub probe target: authed GET /repos/octo/hello with the PAT → 200.
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/octo/hello" && r.Header.Get("Authorization") == "Bearer ghp_secret_tok" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer probe.Close()
	orig := githubrelease.APIBase
	githubrelease.APIBase = probe.URL
	defer func() { githubrelease.APIBase = orig }()

	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-release-github", "owner")

	// Missing repo → 400 (required config validated).
	bw, br := createReleaseRequest(t, wsID, `{"kind":"github_release","owner":"octo","token":"x","events":["release_shipped"]}`)
	testHandler.CreateReleaseIntegration(bw, br)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("missing repo: status = %d, want 400 (body: %s)", bw.Code, bw.Body.String())
	}

	// Complete → 200, probe ok, token sealed.
	body := `{"kind":"github_release","name":"prod release","owner":"octo","repo":"hello","token":"ghp_secret_tok","events":["release_shipped"]}`
	cw, cr := createReleaseRequest(t, wsID, body)
	testHandler.CreateReleaseIntegration(cw, cr)
	if cw.Code != http.StatusOK {
		t.Fatalf("create: status = %d (body: %s)", cw.Code, cw.Body.String())
	}
	if s := cw.Body.String(); strings.Contains(s, "ghp_secret_tok") {
		t.Fatalf("create response leaked token: %s", s)
	}
	if !strings.Contains(cw.Body.String(), `"has_secret":true`) || !strings.Contains(cw.Body.String(), `"probe_status":"ok"`) {
		t.Fatalf("create response missing has_secret/probe_status ok: %s", cw.Body.String())
	}
	// The config (non-secret) carries owner/repo and is returned.
	if !strings.Contains(cw.Body.String(), `"owner":"octo"`) || !strings.Contains(cw.Body.String(), `"repo":"hello"`) {
		t.Fatalf("create response missing owner/repo config: %s", cw.Body.String())
	}

	// The stored secret is sealed — plaintext token must not appear.
	var sealed []byte
	if err := testPool.QueryRow(ctx, `SELECT secret_encrypted FROM release_integration WHERE workspace_id = $1`, wsID).Scan(&sealed); err != nil {
		t.Fatalf("load sealed secret: %v", err)
	}
	if strings.Contains(string(sealed), "ghp_secret_tok") {
		t.Fatal("github PAT stored in plaintext")
	}
}

// TestCreateReleaseIntegration_RejectsAuthFailingURL: a probe that hard-rejects
// (403) is caught at save time with 422, not silently stored.
func TestCreateReleaseIntegration_RejectsAuthFailingURL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)

	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer probe.Close()

	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-release-badurl", "owner")
	body := `{"kind":"webhook","url":"` + probe.URL + `/h","events":["deploy_recorded"]}`
	w, r := createReleaseRequest(t, wsID, body)
	testHandler.CreateReleaseIntegration(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 on a 403-probing URL (body: %s)", w.Code, w.Body.String())
	}
}
