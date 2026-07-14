package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetConfigIncludesRuntimeAuthConfig(t *testing.T) {
	origStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	defer func() { testHandler.Storage = origStorage }()

	t.Setenv("ALLOW_SIGNUP", "false")
	t.Setenv("GOOGLE_CLIENT_ID", "google-client-id")
	t.Setenv("POSTHOG_API_KEY", "phc_test")
	t.Setenv("POSTHOG_HOST", "https://eu.i.posthog.com")
	t.Setenv("AGORA_PUBLIC_URL", "https://api.example.com/")
	t.Setenv("AGORA_APP_URL", "https://app.example.com/")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if cfg.CdnDomain != "cdn.example.com" {
		t.Fatalf("cdn_domain: want cdn.example.com, got %q", cfg.CdnDomain)
	}
	if cfg.AllowSignup {
		t.Fatalf("allow_signup: want false, got true")
	}
	if cfg.GoogleClientID != "google-client-id" {
		t.Fatalf("google_client_id: want google-client-id, got %q", cfg.GoogleClientID)
	}
	if cfg.PosthogKey != "phc_test" {
		t.Fatalf("posthog_key: want phc_test, got %q", cfg.PosthogKey)
	}
	if cfg.PosthogHost != "https://eu.i.posthog.com" {
		t.Fatalf("posthog_host: want https://eu.i.posthog.com, got %q", cfg.PosthogHost)
	}
	if cfg.AnalyticsEnvironment != "dev" {
		t.Fatalf("analytics_environment: want dev, got %q", cfg.AnalyticsEnvironment)
	}
	if cfg.WorkspaceCreationDisabled {
		t.Fatalf("workspace_creation_disabled: want false by default, got true")
	}
	if cfg.DaemonServerURL != "https://api.example.com" {
		t.Fatalf("daemon_server_url: want https://api.example.com, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "https://app.example.com" {
		t.Fatalf("daemon_app_url: want https://app.example.com, got %q", cfg.DaemonAppURL)
	}
}

func TestGetConfigUsesAppURLForSameOriginDaemonSetup(t *testing.T) {
	t.Setenv("AGORA_APP_URL", "https://agora.internal.example/")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DaemonServerURL != "https://agora.internal.example" {
		t.Fatalf("daemon_server_url: want same-origin URL, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "https://agora.internal.example" {
		t.Fatalf("daemon_app_url: want app URL, got %q", cfg.DaemonAppURL)
	}
}

func TestGetConfigUsesFrontendOriginForSameOriginDaemonSetup(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://agora.internal.example/")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DaemonServerURL != "https://agora.internal.example" {
		t.Fatalf("daemon_server_url: want same-origin URL, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "https://agora.internal.example" {
		t.Fatalf("daemon_app_url: want frontend origin, got %q", cfg.DaemonAppURL)
	}
}

func TestGetConfigOmitsOfficialCloudDaemonSetup(t *testing.T) {
	t.Setenv("AGORA_PUBLIC_URL", "https://api.agora.dev")
	t.Setenv("FRONTEND_ORIGIN", "https://agora.dev")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DaemonServerURL != "" {
		t.Fatalf("daemon_server_url: want omitted for cloud, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "" {
		t.Fatalf("daemon_app_url: want omitted for cloud, got %q", cfg.DaemonAppURL)
	}
}

// TestGetConfigOmitsCloudDaemonSetupWithoutPublicURL reproduces the production
// regression behind the broken "Add a computer" command: the official cloud
// frontend is agora.dev, but the deployment does not set AGORA_PUBLIC_URL to
// the api host. Previously this fell through to the same-origin branch and
// emitted daemon_server_url=https://agora.dev, which the dialog turned into
// `agora setup self-host --server-url https://agora.dev` — pointing the
// daemon's backend at the frontend (no /health, no WebSocket proxy). The
// official cloud must be recognised by its frontend host alone so the daemon
// setup URLs are omitted and the dialog falls back to `agora setup`.
func TestGetConfigOmitsCloudDaemonSetupWithoutPublicURL(t *testing.T) {
	t.Setenv("AGORA_PUBLIC_URL", "")
	t.Setenv("AGORA_APP_URL", "")
	t.Setenv("FRONTEND_ORIGIN", "https://agora.dev")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DaemonServerURL != "" {
		t.Fatalf("daemon_server_url: want omitted for official cloud, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "" {
		t.Fatalf("daemon_app_url: want omitted for official cloud, got %q", cfg.DaemonAppURL)
	}
}

// TestGetConfigOmitsCloudDaemonSetupForAppSubdomain covers the app.agora.dev
// frontend variant of the official cloud.
func TestGetConfigOmitsCloudDaemonSetupForAppSubdomain(t *testing.T) {
	t.Setenv("AGORA_PUBLIC_URL", "")
	t.Setenv("AGORA_APP_URL", "https://app.agora.dev")
	t.Setenv("FRONTEND_ORIGIN", "")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DaemonServerURL != "" {
		t.Fatalf("daemon_server_url: want omitted for official cloud, got %q", cfg.DaemonServerURL)
	}
	if cfg.DaemonAppURL != "" {
		t.Fatalf("daemon_app_url: want omitted for official cloud, got %q", cfg.DaemonAppURL)
	}
}

func TestURLHostEqualsCanonicalizesCommonHostForms(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "full URL", raw: "https://api.agora.dev", want: true},
		{name: "bare host", raw: "api.agora.dev", want: true},
		{name: "host port", raw: "api.agora.dev:8080", want: true},
		{name: "trailing dot", raw: "https://api.agora.dev.", want: true},
		{name: "different host", raw: "https://evil.example", want: false},
		{name: "empty", raw: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := urlHostEquals(tt.raw, "api.agora.dev"); got != tt.want {
				t.Fatalf("urlHostEquals(%q): want %v, got %v", tt.raw, tt.want, got)
			}
		})
	}
}

// TestGetConfigExposesWorkspaceCreationDisabled verifies that the self-host
// gate added by #3433 surfaces to the frontend through /api/config so the UI
// can hide every "Create workspace" affordance.
func TestGetConfigExposesWorkspaceCreationDisabled(t *testing.T) {
	origStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	defer func() { testHandler.Storage = origStorage }()

	t.Setenv("DISABLE_WORKSPACE_CREATION", "true")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()

	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var cfg AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.WorkspaceCreationDisabled {
		t.Fatalf("workspace_creation_disabled: want true with env on, got false (body=%s)", w.Body.String())
	}
}

// TestGetConfigExposesIntegrationCapabilityFlags verifies that bitrix/zoho/lark
// availability surfaces to the frontend through /api/config, derived from the
// same env gates the integration endpoints already check. A general dev-team
// deployment (none configured) reports all three false so the Settings →
// Integrations UI hides those connector sections; SD (env set) flips them true.
func TestGetConfigExposesIntegrationCapabilityFlags(t *testing.T) {
	origStorage := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	defer func() { testHandler.Storage = origStorage }()

	// Default: no integration env configured → all three omitted (false).
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var off AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &off); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if off.BitrixEnabled || off.ZohoEnabled || off.LarkEnabled {
		t.Fatalf("integration flags: want all false by default, got bitrix=%v zoho=%v lark=%v",
			off.BitrixEnabled, off.ZohoEnabled, off.LarkEnabled)
	}

	// With each integration's env gate set → the matching flag flips true.
	t.Setenv("BITRIX_WEBHOOK_URL", "https://example.bitrix24.com/rest/1/token/")
	t.Setenv("ZOHO_PROJECTS_CLIENT_ID", "zoho-client")
	t.Setenv("ZOHO_PROJECTS_CLIENT_SECRET", "zoho-secret")
	t.Setenv("ZOHO_PROJECTS_REFRESH_TOKEN", "zoho-refresh")
	t.Setenv("AGORA_LARK_SECRET_KEY", "lark-master-key")

	req = httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w = httptest.NewRecorder()
	testHandler.GetConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var on AppConfig
	if err := json.Unmarshal(w.Body.Bytes(), &on); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !on.BitrixEnabled {
		t.Fatalf("bitrix_enabled: want true with BITRIX_WEBHOOK_URL set (body=%s)", w.Body.String())
	}
	if !on.ZohoEnabled {
		t.Fatalf("zoho_enabled: want true with Zoho OAuth env set (body=%s)", w.Body.String())
	}
	if !on.LarkEnabled {
		t.Fatalf("lark_enabled: want true with AGORA_LARK_SECRET_KEY set (body=%s)", w.Body.String())
	}
}
