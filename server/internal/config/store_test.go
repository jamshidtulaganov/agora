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
