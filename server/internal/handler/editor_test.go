package handler

import "testing"

func TestDaemonEditorBase(t *testing.T) {
	t.Run("env_override_wins_over_port", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "http://example.test:1234")
		if got := daemonEditorBase("20038"); got != "http://example.test:1234" {
			t.Errorf("env override must win, got: %q", got)
		}
	})

	t.Run("reported_port_used_when_no_env", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := daemonEditorBase("20038"); got != "http://127.0.0.1:20038" {
			t.Errorf("reported port must be used, got: %q", got)
		}
	})

	t.Run("blank_port_falls_back_to_default", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := daemonEditorBase("  "); got != "http://127.0.0.1:19514" {
			t.Errorf("blank port must fall back to 19514, got: %q", got)
		}
	})
}

func TestResolveDaemonInternalAddr(t *testing.T) {
	t.Run("runtime_addr_wins_over_global_env", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_INTERNAL", "global.internal:19514")
		if got := resolveDaemonInternalAddr("box-tunnel:40000"); got != "box-tunnel:40000" {
			t.Errorf("per-runtime addr must win, got: %q", got)
		}
	})

	t.Run("empty_runtime_addr_falls_back_to_global_env", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_INTERNAL", "global.internal:19514")
		if got := resolveDaemonInternalAddr(""); got != "global.internal:19514" {
			t.Errorf("empty runtime addr must fall back, got: %q", got)
		}
	})

	t.Run("both_empty_yields_self_host", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_INTERNAL", "")
		if got := resolveDaemonInternalAddr(""); got != "" {
			t.Errorf("no addr anywhere must yield self-host, got: %q", got)
		}
	})
}
