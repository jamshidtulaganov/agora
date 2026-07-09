// Package config is the single catalog + resolver for instance-level
// configuration. Every AGORA_* feature flag / tunable that operators used to
// set only via environment variables is listed here once, so it can be read
// uniformly (config.Bool/String/Int) and surfaced + edited in Settings →
// Configs. A DB override (instance_config table) beats the environment, which
// beats the registry default. Secrets are catalogued too — but only as
// set/not-set status; their values never leave the process.
package config

// Kind classifies a config value for the UI (toggle vs input vs masked).
type Kind string

const (
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindString Kind = "string"
	KindSecret Kind = "secret" // value never exposed or DB-overridable; env-only
)

// Def is one configurable key.
type Def struct {
	Key         string
	Kind        Kind
	Category    string
	Label       string
	Description string
	Default     string // used when neither DB override nor env is set (bool/int/string only)
}

// Editable reports whether a human may set this key from the UI. Secrets are
// read-only status (managed via Fly secrets / env), never editable here.
func (d Def) Editable() bool { return d.Kind != KindSecret }

// Registry is the full catalog. Adding a new AGORA_* flag? Add it here and it
// becomes readable via config.Bool/etc AND appears in Settings → Configs.
var Registry = []Def{
	// ---- QA automation -------------------------------------------------
	{Key: "AGORA_AUTO_QA_ENABLED", Kind: KindBool, Category: "QA", Label: "Auto QA on in-review", Description: "Fire a run_qa gate automatically when an issue moves to in_review."},
	{Key: "AGORA_QA_GATE_ENFORCED", Kind: KindBool, Category: "QA", Label: "Enforce QA gate before done", Description: "Block in_review → done unless a qa:pass verdict exists."},
	{Key: "AGORA_QA_COMPILE_ENABLED", Kind: KindBool, Category: "QA", Label: "Compile tests", Description: "Enable the compile_tests slice action for Playwright scripts."},
	{Key: "AGORA_QA_FAIL_AUTOROUTE_ENABLED", Kind: KindBool, Category: "QA", Label: "Auto-route QA failures", Description: "On qa:fail, reset to todo and route back to the dev-squad lead."},
	{Key: "AGORA_QA_FAIL_AUTO_FILE_BUG_ENABLED", Kind: KindBool, Category: "QA", Label: "Auto-file bug on QA fail", Description: "Open a bug issue automatically when a QA gate fails."},
	{Key: "AGORA_QA_DISCRIMINATION_ENFORCED", Kind: KindBool, Category: "QA", Label: "Require discriminating test", Description: "Require a fail-before/pass-after test run before qa:pass counts."},
	{Key: "AGORA_RISK_TIER_GATE_ENFORCED", Kind: KindBool, Category: "QA", Label: "Enforce risk-tier gate", Description: "Gate QA depth on the issue's risk tier."},
	{Key: "AGORA_QA_WATCHDOG_WINDOW_HOURS", Kind: KindInt, Category: "QA", Label: "QA watchdog window (hours)", Description: "How long a silent QA gate waits before escalating to qa:stale.", Default: "24"},

	// ---- Sprint / dev flow ---------------------------------------------
	{Key: "AGORA_SPRINT_PR_MODE", Kind: KindBool, Category: "Sprint", Label: "Sprint PR-review mode", Description: "Dev tasks open PRs into the sprint branch; QA gates the PR branch."},
	{Key: "AGORA_SPRINT_WORKTREE_ENABLED", Kind: KindBool, Category: "Sprint", Label: "Shared sprint worktree", Description: "Put concurrent sprint tasks on one shared sprint branch."},
	{Key: "AGORA_SPRINT_AUTO_MERGE", Kind: KindBool, Category: "Sprint", Label: "Auto-merge on qa:pass", Description: "Squad lead auto-merges a PR into the sprint branch after qa:pass."},
	{Key: "AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", Kind: KindBool, Category: "Sprint", Label: "Squad failure recovery", Description: "Recover a squad run when a member task fails mid-orchestration."},

	// ---- Docs / knowledge ----------------------------------------------
	{Key: "AGORA_AUTO_DOCS_ENABLED", Kind: KindBool, Category: "Automation", Label: "Auto docs", Description: "Run the auto_docs slice action to keep a docs repo in sync."},

	// ---- Remote boxes / QA host ----------------------------------------
	{Key: "AGORA_REMOTE_BOXES_ENABLED", Kind: KindBool, Category: "Remote boxes", Label: "Remote boxes", Description: "Enable the connected-box / remote QA-box onboarding surface."},
	{Key: "AGORA_QA_HOST_BASE_DOMAIN", Kind: KindString, Category: "Remote boxes", Label: "QA host base domain", Description: "Base domain for provisioned per-dev QA boxes (e.g. sdteam.uz)."},
	{Key: "AGORA_QA_HOST_SSH_HOST", Kind: KindString, Category: "Remote boxes", Label: "QA host SSH host", Description: "SSH host the box control-plane connects to."},
	{Key: "AGORA_QA_HOST_SSH_USER", Kind: KindString, Category: "Remote boxes", Label: "QA host SSH user", Description: "SSH user for the QA host control-plane."},
	{Key: "AGORA_QA_HOST_WEB_ROOT", Kind: KindString, Category: "Remote boxes", Label: "QA host web root", Description: "Web root where box subdomains are served (e.g. /var/www)."},

	// ---- Bitrix integration --------------------------------------------
	{Key: "BITRIX_PUSH_STATUS", Kind: KindBool, Category: "Bitrix", Label: "Push status to Bitrix", Description: "Mirror Agora issue status transitions back to the linked Bitrix task."},
	{Key: "AGORA_BITRIX_ARCHIVE_DONE", Kind: KindBool, Category: "Bitrix", Label: "Archive done Bitrix tasks", Description: "Archive the Bitrix task when its Agora issue is completed."},
	{Key: "AGORA_BITRIX_USER_POLL_INTERVAL", Kind: KindInt, Category: "Bitrix", Label: "Bitrix user poll interval (s)", Description: "Seconds between per-user Bitrix task imports (0 disables polling).", Default: "0"},

	// ---- Platform ------------------------------------------------------
	{Key: "AGORA_TELEGRAM_ONLY", Kind: KindBool, Category: "Platform", Label: "Telegram-only mode", Description: "Restrict the web app to the Telegram mini-app login flow."},
	{Key: "ALLOW_SIGNUP", Kind: KindBool, Category: "Platform", Label: "Allow signup", Description: "Allow new-account signup (off = invite-only).", Default: "true"},
	{Key: "DISABLE_WORKSPACE_CREATION", Kind: KindBool, Category: "Platform", Label: "Disable workspace creation", Description: "Prevent members from creating new workspaces."},

	// ---- Secrets (status only — never editable, never exposed) ----------
	{Key: "JWT_SECRET", Kind: KindSecret, Category: "Secrets", Label: "JWT secret", Description: "Token signing key."},
	{Key: "AGORA_GIT_SECRET_KEY", Kind: KindSecret, Category: "Secrets", Label: "Git credential seal key", Description: "Secretbox key for per-workspace git PATs."},
	{Key: "AGORA_LARK_SECRET_KEY", Kind: KindSecret, Category: "Secrets", Label: "Lark seal key", Description: "Secretbox key for Lark integration."},
	{Key: "AGORA_ZOHO_SECRET_KEY", Kind: KindSecret, Category: "Secrets", Label: "Zoho seal key", Description: "Secretbox key for Zoho integration."},
	{Key: "AGORA_FIGMA_SECRET_KEY", Kind: KindSecret, Category: "Secrets", Label: "Figma seal key", Description: "Secretbox key for Figma integration."},
	{Key: "TELEGRAM_BOT_TOKEN", Kind: KindSecret, Category: "Secrets", Label: "Telegram bot token", Description: "Bot API token."},
	{Key: "ZHIPU_API_KEY", Kind: KindSecret, Category: "Secrets", Label: "Zhipu API key", Description: "GLM model API key."},
	{Key: "BITRIX_WEBHOOK_URL", Kind: KindSecret, Category: "Secrets", Label: "Bitrix webhook URL", Description: "Inbound Bitrix REST webhook (carries a token)."},
	{Key: "AGORA_REMOTE_BOXES_GIT_TOKEN", Kind: KindSecret, Category: "Secrets", Label: "Remote-boxes git token", Description: "Token the box bootstrapper uses to clone repos."},
	{Key: "AGORA_REMOTE_BOXES_SSH_KEY_B64", Kind: KindSecret, Category: "Secrets", Label: "Remote-boxes SSH key", Description: "Base64 SSH key for box onboarding."},
	{Key: "SMTP_PASSWORD", Kind: KindSecret, Category: "Secrets", Label: "SMTP password", Description: "Outbound email password."},
}

// byKey indexes the registry for O(1) lookup.
var byKey = func() map[string]Def {
	m := make(map[string]Def, len(Registry))
	for _, d := range Registry {
		m[d.Key] = d
	}
	return m
}()

// Lookup returns the def for a key and whether it exists.
func Lookup(key string) (Def, bool) {
	d, ok := byKey[key]
	return d, ok
}
