package handler

import (
	"github.com/multica-ai/multica/server/internal/config"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/analytics"
)

type AppConfig struct {
	CdnDomain string `json:"cdn_domain"`
	// Public auth config consumed by the web app at runtime so self-hosted
	// deployments do not need to rebuild the frontend image when operators
	// toggle signup or wire Google OAuth.
	AllowSignup    bool   `json:"allow_signup"`
	GoogleClientID string `json:"google_client_id,omitempty"`
	// WorkspaceCreationDisabled mirrors the server-side
	// DISABLE_WORKSPACE_CREATION env var so the UI can hide every
	// "Create workspace" affordance on self-hosted instances. Omitted
	// from the JSON when false to keep responses identical to the
	// previous shape for the common managed-cloud case (#3433).
	WorkspaceCreationDisabled bool `json:"workspace_creation_disabled,omitempty"`
	// TelegramOnly mirrors the server-side AGORA_TELEGRAM_ONLY env var so the
	// login page can hide every non-Telegram auth path (the email send-code
	// form, the Google button, and the "or" divider) and present Telegram as
	// the sole way in. Omitted from the JSON when false to keep responses
	// identical to the previous shape for the common managed-cloud case,
	// mirroring WorkspaceCreationDisabled above.
	TelegramOnly bool `json:"telegram_only,omitempty"`
	// Public daemon setup config consumed by the web app at runtime so
	// self-hosted instances can show `agora setup self-host` commands
	// with the operator's own domains instead of Agora Cloud defaults.
	DaemonServerURL string `json:"daemon_server_url,omitempty"`
	DaemonAppURL    string `json:"daemon_app_url,omitempty"`

	// TelegramBotUsername is the bot's @username (without the @) used to
	// build the t.me login deep link. Exposed (omitempty) so the web app
	// renders the Telegram login button only when bot-OTP login is
	// configured; empty/omitted when TELEGRAM_BOT_USERNAME is unset.
	TelegramBotUsername string `json:"telegram_bot_username,omitempty"`

	// TelegramMiniAppShortName is the @BotFather Mini App short name used to
	// build https://t.me/<bot>/<shortName>?startapp=<payload> deep links (push
	// DMs and the SPA's own links). Exposed (omitempty) only alongside
	// TelegramBotUsername; empty/omitted when TELEGRAM_MINIAPP_SHORTNAME is unset.
	TelegramMiniAppShortName string `json:"telegram_miniapp_shortname,omitempty"`

	// PostHog public config for the frontend. The key is the same Project
	// API Key the backend uses; returning it here (instead of baking it
	// into the frontend bundle via NEXT_PUBLIC_*) means self-hosted
	// instances — whose server returns an empty key — automatically
	// disable frontend event shipping too.
	PosthogKey           string `json:"posthog_key"`
	PosthogHost          string `json:"posthog_host"`
	AnalyticsEnvironment string `json:"analytics_environment"`

	// RemoteBoxesEnabled mirrors AGORA_REMOTE_BOXES_ENABLED so the web app shows
	// the Remote Boxes onboarding UI only where the server has the feature on.
	// Omitted when false to keep the response shape identical for every existing
	// deployment.
	RemoteBoxesEnabled bool `json:"remote_boxes_enabled,omitempty"`
}

// GetConfig is mounted on the public (unauthenticated) route group because
// the web app calls it before login to decide whether to render the Google
// sign-in button and signup UI. Only add fields here that are safe to expose
// to anonymous callers — never user- or tenant-scoped data.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config := AppConfig{
		AllowSignup:               config.String("ALLOW_SIGNUP") != "false",
		GoogleClientID:            os.Getenv("GOOGLE_CLIENT_ID"),
		WorkspaceCreationDisabled: config.Bool("DISABLE_WORKSPACE_CREATION"),
		TelegramOnly:              config.Bool("AGORA_TELEGRAM_ONLY"),
		RemoteBoxesEnabled:        config.Bool("AGORA_REMOTE_BOXES_ENABLED"),
	}
	if h.Storage != nil {
		config.CdnDomain = h.Storage.CdnDomain()
	}
	config.DaemonServerURL, config.DaemonAppURL = daemonSetupURLsFromEnv()
	if h.telegramLoginEnabled() {
		config.TelegramBotUsername = telegramBotUsername()
		config.TelegramMiniAppShortName = telegramMiniAppShortName()
	}

	// Re-read from env on every request so operators can rotate keys via
	// secret refresh without a server restart.
	if v := os.Getenv("ANALYTICS_DISABLED"); v != "true" && v != "1" {
		config.PosthogKey = os.Getenv("POSTHOG_API_KEY")
		config.PosthogHost = os.Getenv("POSTHOG_HOST")
		config.AnalyticsEnvironment = analytics.EnvironmentFromEnv()
		if config.PosthogHost == "" && config.PosthogKey != "" {
			config.PosthogHost = "https://us.i.posthog.com"
		}
	}

	writeJSON(w, http.StatusOK, config)
}

func daemonSetupURLsFromEnv() (string, string) {
	serverURL := normalizePublicURL(os.Getenv("AGORA_PUBLIC_URL"))
	appURL := normalizePublicURL(os.Getenv("AGORA_APP_URL"))
	if appURL == "" {
		appURL = normalizePublicURL(os.Getenv("FRONTEND_ORIGIN"))
	}
	if appURL == "" {
		return "", ""
	}

	if serverURL == "" {
		serverURL = appURL
	}
	if isOfficialCloudDaemonConfig(appURL) {
		return "", ""
	}
	return serverURL, appURL
}

func normalizePublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// isOfficialCloudDaemonConfig reports whether this deployment is the official
// Agora Cloud, identified by its frontend host alone (agora.dev /
// app.agora.dev). The daemon setup for the managed cloud is always
// `agora setup` (which hardcodes api.agora.dev), so the per-deployment URLs
// must be omitted from /api/config even when AGORA_PUBLIC_URL is unset or
// misconfigured. Previously this also required serverURL==api.agora.dev, so a
// cloud deployment that forgot AGORA_PUBLIC_URL fell through and emitted a
// `setup self-host --server-url https://agora.dev` command — pointing the
// daemon's backend at the frontend (no /health, no WebSocket proxy).
func isOfficialCloudDaemonConfig(appURL string) bool {
	return urlHostEquals(appURL, "agora.dev") || urlHostEquals(appURL, "app.agora.dev")
}

func urlHostEquals(raw, want string) bool {
	host := canonicalURLHost(raw)
	if host == "" {
		return false
	}
	want = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(want)), ".")
	return host == want
}

func canonicalURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" && !strings.Contains(raw, "://") {
		u, err = url.Parse("https://" + raw)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
