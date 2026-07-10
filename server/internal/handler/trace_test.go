package handler

import (
	"testing"
	"time"
)

// TestTraceDaemonBase covers the bug this fix closes: the trace-viewer proxy
// target used to be <proxyHost>:<port> — a direct dial at the show-trace
// process's port, which is unreachable from a containerized self-host backend
// (the process binds the DAEMON HOST's loopback, not the container's). The fix
// routes both the launch call AND the later reverse-proxy through the SAME
// daemon health-listener base, exactly like the live code editor's
// /editor/local/{port}. This test locks in that base resolution (self-host vs
// cloud) — the routing-through-the-daemon behavior itself (not a raw
// host:port) is asserted by TestRegisterTraceTargetStoresDaemonRoutedTarget
// below. Pure (no DB).
func TestTraceDaemonBase(t *testing.T) {
	t.Run("cloud_uses_daemon_internal_address", func(t *testing.T) {
		if got := traceDaemonBase("sd-agora-daemon.internal:19514", "20038"); got != "http://sd-agora-daemon.internal:19514" {
			t.Errorf("cloud base: got %q", got)
		}
	})

	t.Run("self_host_uses_local_editor_base", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := traceDaemonBase("", "20038"); got != "http://127.0.0.1:20038" {
			t.Errorf("self-host base: got %q", got)
		}
	})

	t.Run("self_host_falls_back_to_default_port", func(t *testing.T) {
		t.Setenv("AGORA_DAEMON_EDITOR_URL", "")
		if got := traceDaemonBase("", ""); got != "http://127.0.0.1:19514" {
			t.Errorf("self-host default base: got %q", got)
		}
	})
}

// TestRegisterTraceTargetStoresDaemonRoutedTarget is the regression test for
// the actual bug: registerTraceTarget must store a target that ROUTES THROUGH
// the daemon (base + /trace/local/{port} path), never a bare "host:port" the
// backend would dial directly — a containerized backend cannot reach a
// show-trace process bound to the daemon host's loopback that way (proven live
// against the self-host Docker stack: connection refused from inside the
// container). ProxyTrace's target must be the base URL itself, with the
// per-viewer path carried separately and joined onto every proxied request.
func TestRegisterTraceTargetStoresDaemonRoutedTarget(t *testing.T) {
	tok := registerTraceTarget("http://127.0.0.1:20038", "/trace/local/54321", "ws-1")
	t.Cleanup(func() {
		traceTargetsMu.Lock()
		delete(traceTargets, tok)
		traceTargetsMu.Unlock()
	})

	got, ok := lookupTraceTarget(tok)
	if !ok {
		t.Fatal("expected the registered token to be found")
	}
	if got.base != "http://127.0.0.1:20038" {
		t.Errorf("base: got %q, want the daemon base (not a raw host:port)", got.base)
	}
	if got.path != "/trace/local/54321" {
		t.Errorf("path: got %q, want the daemon-side /trace/local/{port} route", got.path)
	}
	if got.workspaceID != "ws-1" {
		t.Errorf("workspaceID: got %q", got.workspaceID)
	}
}

// TestLookupTraceTargetRejectsUnknownOrExpired covers the two ways a trace
// token proxy request is refused before it ever reaches ProxyTrace's
// membership check: an unrecognized token, and one past its 8h TTL.
func TestLookupTraceTargetRejectsUnknownOrExpired(t *testing.T) {
	if _, ok := lookupTraceTarget("does-not-exist"); ok {
		t.Error("unknown token must not resolve")
	}

	tok := registerTraceTarget("http://127.0.0.1:20038", "/trace/local/1", "ws-1")
	traceTargetsMu.Lock()
	expired := traceTargets[tok]
	expired.expires = expired.expires.Add(-9 * time.Hour) // well past the 8h TTL
	traceTargets[tok] = expired
	traceTargetsMu.Unlock()
	t.Cleanup(func() {
		traceTargetsMu.Lock()
		delete(traceTargets, tok)
		traceTargetsMu.Unlock()
	})

	if _, ok := lookupTraceTarget(tok); ok {
		t.Error("expired token must not resolve")
	}
}
