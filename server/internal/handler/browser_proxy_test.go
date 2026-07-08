package handler

import (
	"testing"
	"time"
)

// The browser proxy's upstream is the daemon's WHOLE health mux — the path
// gate is what keeps a capability token scoped to the live-browser surface
// instead of granting /editor/launch, /trace/launch, /update etc.
func TestBrowserProxyPathAllowed(t *testing.T) {
	allowed := []string{
		"/editor/browser/start",
		"/editor/browser/stop",
		"/editor/browser/stream",
		"/editor/browser/stream?workdir=qa-target:https://x", // query rides RawQuery, but a raw path form must still pass
		// Preview / test / proxied dev-server port + review surface — the panes
		// the cloud co-code editor drives through this proxy.
		"/editor/preview",
		"/editor/preview/status",
		"/editor/preview/stop",
		"/editor/test",
		"/editor/local/42873/",
		"/editor/local/42873/assets/index.js",
		"/editor/changes",
		"/editor/open-pr",
		"/editor/discard",
	}
	for _, p := range allowed {
		if !browserProxyPathAllowed(p) {
			t.Errorf("expected %q allowed", p)
		}
	}
	denied := []string{
		"/editor/launch",     // spawns code-server with caller-controlled env
		"/repo/checkout",     // clones an arbitrary repo
		"/trace/launch",      // spawns show-trace for an arbitrary path
		"/update",            // daemon self-update
		"/",                  // mux root
		"",                   // empty after prefix strip
		"/editor/browser",    // no trailing slash — not the subtree
		"/editor/browserish", // prefix-confusion sibling
		"/editor/localhost",  // not the /editor/local/ route
		"/editor/previewx",   // exact/slashed only — not a prefix sibling
	}
	for _, p := range denied {
		if browserProxyPathAllowed(p) {
			t.Errorf("expected %q denied", p)
		}
	}
}

func TestBrowserTargetRegistryExpiry(t *testing.T) {
	tok := registerBrowserTarget("10.0.0.1:19514", "ws-1")
	got, ok := lookupBrowserTarget(tok)
	if !ok || got.addr != "10.0.0.1:19514" || got.workspaceID != "ws-1" {
		t.Fatalf("lookup after register: ok=%v target=%+v", ok, got)
	}
	// Force-expire and confirm the token dies.
	browserTargetsMu.Lock()
	e := browserTargets[tok]
	e.expires = time.Now().Add(-time.Second)
	browserTargets[tok] = e
	browserTargetsMu.Unlock()
	if _, ok := lookupBrowserTarget(tok); ok {
		t.Fatal("expired token still resolves")
	}
	if _, ok := lookupBrowserTarget("no-such-token"); ok {
		t.Fatal("unknown token resolves")
	}
}
