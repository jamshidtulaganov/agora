package config

import (
	"context"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	// A registry key with a default.
	const key = "ALLOW_SIGNUP" // default "true"

	// No store, no env → registry default.
	singleton = nil
	t.Setenv(key, "")
	if got := Resolve(key); got != "true" {
		t.Errorf("default: got %q, want registry default true", got)
	}

	// Env beats default.
	t.Setenv(key, "false")
	if got := Resolve(key); got != "false" {
		t.Errorf("env: got %q, want false", got)
	}

	// DB override beats env.
	Init(context.Background(), func(context.Context) (map[string]string, error) {
		return map[string]string{key: "true"}, nil
	})
	if got := Resolve(key); got != "true" {
		t.Errorf("override: got %q, want true (override beats env)", got)
	}
	if Source(key) != "override" {
		t.Errorf("source: got %q, want override", Source(key))
	}

	// NotifyDelete reverts to env.
	NotifyDelete(key)
	if got := Resolve(key); got != "false" {
		t.Errorf("after delete: got %q, want env false", got)
	}
	if Source(key) != "env" {
		t.Errorf("source after delete: got %q, want env", Source(key))
	}
}

func TestBoolIntString(t *testing.T) {
	singleton = nil
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")
	if !Bool("AGORA_AUTO_QA_ENABLED") {
		t.Error("Bool should read env true")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "1")
	if !Bool("AGORA_AUTO_QA_ENABLED") {
		t.Error("Bool should accept 1")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "false")
	if Bool("AGORA_AUTO_QA_ENABLED") {
		t.Error("Bool false")
	}

	t.Setenv("AGORA_QA_WATCHDOG_WINDOW_HOURS", "48")
	if Int("AGORA_QA_WATCHDOG_WINDOW_HOURS", 24) != 48 {
		t.Error("Int should read env")
	}
	t.Setenv("AGORA_QA_WATCHDOG_WINDOW_HOURS", "notanumber")
	if Int("AGORA_QA_WATCHDOG_WINDOW_HOURS", 24) != 24 {
		t.Error("Int should fall back to default on bad value")
	}

	t.Setenv("AGORA_QA_HOST_SSH_HOST", "  qa.example  ")
	if String("AGORA_QA_HOST_SSH_HOST") != "qa.example" {
		t.Error("String should trim")
	}
}

func TestNotifySetOverridesEnv(t *testing.T) {
	singleton = nil
	Init(context.Background(), func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	})
	t.Setenv("AGORA_AUTO_QA_ENABLED", "false")
	if Bool("AGORA_AUTO_QA_ENABLED") {
		t.Fatal("precondition: env false")
	}
	NotifySet("AGORA_AUTO_QA_ENABLED", "true")
	if !Bool("AGORA_AUTO_QA_ENABLED") {
		t.Error("NotifySet override should win over env false")
	}
}

func TestSecretNeverExposedViaResolveDefault(t *testing.T) {
	// Secrets have no Default and are read via SecretIsSet, not Resolve.
	singleton = nil
	d, ok := Lookup("JWT_SECRET")
	if !ok || d.Kind != KindSecret || d.Editable() {
		t.Fatalf("JWT_SECRET should be a non-editable secret: %+v", d)
	}
	t.Setenv("JWT_SECRET", "")
	if SecretIsSet("JWT_SECRET") {
		t.Error("unset secret should report not-set")
	}
	t.Setenv("JWT_SECRET", "supersecret")
	if !SecretIsSet("JWT_SECRET") {
		t.Error("set secret should report set")
	}
}

func TestScopedResolvePrecedence(t *testing.T) {
	const key = "AGORA_QA_FAIL_AUTOROUTE_ENABLED" // default "true", ProjectScoped
	singleton = nil
	t.Setenv(key, "")

	// nil overrides → identical to the unscoped read (registry default true).
	if !BoolFrom(nil, key) {
		t.Error("nil overrides should fall through to the instance value (default true)")
	}
	if SourceFrom(nil, key) != "default" {
		t.Errorf("nil-source: got %q, want default", SourceFrom(nil, key))
	}

	// A project override beats the instance value both ways.
	proj := map[string]string{key: "false"}
	if BoolFrom(proj, key) {
		t.Error("project override false must win over default true")
	}
	if SourceFrom(proj, key) != "project" {
		t.Errorf("source: got %q, want project", SourceFrom(proj, key))
	}

	// A project override wins even over an instance env value.
	t.Setenv(key, "false")
	on := map[string]string{key: "true"}
	if !BoolFrom(on, key) {
		t.Error("project override true must win over env false")
	}

	// A blank project value is treated as unset → falls through to instance.
	t.Setenv(key, "false")
	blank := map[string]string{key: "   "}
	if BoolFrom(blank, key) {
		t.Error("blank project value should fall through to instance (env false)")
	}
	if SourceFrom(blank, key) != "env" {
		t.Errorf("blank-source: got %q, want env", SourceFrom(blank, key))
	}

	// IntFrom honours the project override, else the default.
	t.Setenv("AGORA_QA_WATCHDOG_WINDOW_HOURS", "")
	if IntFrom(map[string]string{"AGORA_QA_WATCHDOG_WINDOW_HOURS": "6"}, "AGORA_QA_WATCHDOG_WINDOW_HOURS", 24) != 6 {
		t.Error("IntFrom should read the project override")
	}
	if IntFrom(nil, "AGORA_QA_WATCHDOG_WINDOW_HOURS", 24) != 24 {
		t.Error("IntFrom nil should fall to the registry default 24")
	}
}

func TestProjectScopedFlags(t *testing.T) {
	// The pipeline keys are project-scopable; platform/secret keys are not.
	scoped := map[string]bool{
		"AGORA_QA_FAIL_AUTOROUTE_ENABLED": true,
		"AGORA_AUTO_QA_ENABLED":           true,
		"AGORA_AUTO_REVIEW_ENABLED":       true,
		"AGORA_AUTO_DOCS_ENABLED":         true,
	}
	for k, want := range scoped {
		if IsProjectScoped(k) != want {
			t.Errorf("%s: IsProjectScoped=%v want %v", k, IsProjectScoped(k), want)
		}
	}
	// Sprint-cluster flags couple to the daemon and stay instance-global.
	for _, k := range []string{
		"ALLOW_SIGNUP", "AGORA_TELEGRAM_ONLY", "JWT_SECRET", "BITRIX_PUSH_STATUS",
		"AGORA_SPRINT_PR_MODE", "AGORA_SPRINT_WORKTREE_ENABLED",
	} {
		if IsProjectScoped(k) {
			t.Errorf("%s must NOT be project-scoped", k)
		}
	}
	// No secret is ever project-scoped.
	for _, d := range ProjectScopedRegistry() {
		if d.Kind == KindSecret {
			t.Errorf("secret %q must not be project-scoped", d.Key)
		}
	}
	if len(ProjectScopedRegistry()) == 0 {
		t.Error("expected some project-scoped keys")
	}
}

func TestRegistryKeysUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Registry {
		if seen[d.Key] {
			t.Errorf("duplicate registry key %q", d.Key)
		}
		seen[d.Key] = true
		if d.Category == "" || d.Label == "" {
			t.Errorf("registry entry %q missing category/label", d.Key)
		}
	}
}
