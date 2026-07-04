package handler

import "testing"

// TestEditorWorktreeGone covers the cloud-launch failure classifier: a daemon
// "workdir does not exist" (the worktree was GC'd ~24h after the issue closed)
// maps to a 410-Gone, every other launch error stays a 502. Pure (no DB, no
// HTTP). This replaced the old cloud→self-host degrade, which handed a remote
// browser an unreachable 127.0.0.1 URL (CORS failure + stuck spinner).
func TestEditorWorktreeGone(t *testing.T) {
	cases := []struct {
		name      string
		launchErr string
		want      bool
	}{
		{"missing_workdir_is_gone", "daemon 400: workdir does not exist", true},
		{"other_error_is_not_gone", "daemon 500: internal error", false},
		{"conn_refused_is_not_gone", "connection refused", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := editorWorktreeGone(c.launchErr); got != c.want {
				t.Errorf("editorWorktreeGone(%q) = %v, want %v", c.launchErr, got, c.want)
			}
		})
	}
}

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

// TestResolveDaemonInternalAddr covers the per-runtime cloud-address resolution
// (Remote Boxes P1). The contract that protects existing deployments: a runtime
// with NO editor_addr resolves EXACTLY to the global AGORA_DAEMON_INTERNAL env
// (unchanged Fly-6PN behavior); only a runtime that carries its own editor_addr
// (a managed remote box) overrides it. Pure (no DB).
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
			t.Errorf("empty runtime addr must fall back to the global env (unchanged behavior), got: %q", got)
		}
	})

	t.Run("whitespace_runtime_addr_falls_back", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_INTERNAL", "global.internal:19514")
		if got := resolveDaemonInternalAddr("   "); got != "global.internal:19514" {
			t.Errorf("blank runtime addr must fall back, got: %q", got)
		}
	})

	t.Run("both_empty_yields_self_host", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_INTERNAL", "")
		if got := resolveDaemonInternalAddr(""); got != "" {
			t.Errorf("no addr anywhere must yield \"\" (self-host), got: %q", got)
		}
	})
}
