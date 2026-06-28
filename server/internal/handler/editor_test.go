package handler

import "testing"

// TestDaemonEditorBase covers the editor-base resolution order, the fix for the
// hardcoded-19514 bug: the local --profile daemon (and worktrees) serve the
// editor on a port offset off 19514, so the backend must use the port the
// daemon REPORTED at registration, not assume 19514. Pure (no DB).
//
// Order: (1) AGORA_DAEMON_EDITOR_URL env wins outright; (2) the reported port;
// (3) the legacy 19514 default.
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

	t.Run("whitespace_port_falls_back_to_default", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := daemonEditorBase("  "); got != "http://127.0.0.1:19514" {
			t.Errorf("blank port must fall back to 19514, got: %q", got)
		}
	})

	t.Run("empty_port_falls_back_to_default", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := daemonEditorBase(""); got != "http://127.0.0.1:19514" {
			t.Errorf("empty port must fall back to 19514, got: %q", got)
		}
	})
}
