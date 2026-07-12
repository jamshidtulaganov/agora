package daemon

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// registerTestTraceViewer points the package-level traces registry at a fake
// "playwright show-trace" (any 127.0.0.1 HTTP server) and returns its port + a
// cleanup. Mirrors registerTestEditor in editor_local_proxy_test.go.
func registerTestTraceViewer(t *testing.T, upstream *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("upstream port: %v", err)
	}
	key := fmt.Sprintf("/tmp/test-trace-%d.zip", port)
	tracesMu.Lock()
	traces[key] = &previewProc{port: port, cmd: exec.Command("true"), done: make(chan struct{}), startedAt: time.Now()}
	tracesMu.Unlock()
	t.Cleanup(func() {
		tracesMu.Lock()
		delete(traces, key)
		tracesMu.Unlock()
	})
	return port
}

func TestTraceLocalProxyForwardsPathAndQueryToTrackedViewer(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		fmt.Fprint(w, "show-trace ok")
	}))
	defer upstream.Close()
	port := registerTestTraceViewer(t, upstream)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/trace/local/%d/trace/index.html?trace=%s", port, url.QueryEscape("/data/t.zip")), nil)
	rec := httptest.NewRecorder()
	traceLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "show-trace ok" {
		t.Errorf("body: got %q", string(body))
	}
	if gotPath != "/trace/index.html" {
		t.Errorf("upstream path: got %q, want /trace/index.html", gotPath)
	}
	if gotQuery == "" {
		t.Errorf("upstream query lost: got %q", gotQuery)
	}
}

func TestTraceLocalProxyRootPathMapsToSlash(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer upstream.Close()
	port := registerTestTraceViewer(t, upstream)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/trace/local/%d/", port), nil)
	rec := httptest.NewRecorder()
	traceLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/" {
		t.Errorf("upstream path: got %q, want /", gotPath)
	}
}

// TestTraceLocalProxyRejectsUntrackedAndInvalidPorts is the port-allowlist
// regression test: /trace/local/{port} must never become an open proxy into
// the daemon's host — only ports of a live, daemon-tracked trace viewer may be
// dialed. Mirrors TestEditorLocalProxyRejectsUntrackedAndInvalidPorts.
func TestTraceLocalProxyRejectsUntrackedAndInvalidPorts(t *testing.T) {
	t.Parallel()

	// Untracked (but syntactically valid) port → 404, no dial.
	req := httptest.NewRequest(http.MethodGet, "/trace/local/59998/", nil)
	rec := httptest.NewRecorder()
	traceLocalProxyHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("untracked port: expected 404, got %d", rec.Code)
	}

	// Garbage port → 400.
	for _, p := range []string{"abc", "-1", "70000", ""} {
		req := httptest.NewRequest(http.MethodGet, "/trace/local/"+p+"/", nil)
		rec := httptest.NewRecorder()
		traceLocalProxyHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("port %q: expected 400, got %d", p, rec.Code)
		}
	}
}

func TestTraceLocalProxyTreatsExitedViewerAsDead(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	port := registerTestTraceViewer(t, upstream)

	// Flip the tracked instance to "exited" by closing its done channel — that
	// is exactly what running() gates on (see previewProc.running()).
	tracesMu.Lock()
	for _, tp := range traces {
		if tp.port == port {
			close(tp.done)
		}
	}
	tracesMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/trace/local/%d/", port), nil)
	rec := httptest.NewRecorder()
	traceLocalProxyHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("dead viewer: expected 404, got %d", rec.Code)
	}
}

// TestPruneOldTraceViewersKillsOldestPastCap covers the reaping added
// alongside the proxy fix: nothing ever explicitly "closes" a trace viewer
// from the frontend, so the daemon caps concurrent show-trace processes and
// kills the oldest to make room. This test drives real `sleep`-backed
// processes (via startPreview) so previewProc.cmd/done reflect a real killable
// child, and asserts exactly the oldest entries are reaped.
func TestPruneOldTraceViewersKillsOldestPastCap(t *testing.T) {
	dir := t.TempDir()
	var procs []*previewProc
	var paths []string
	for i := 0; i < 4; i++ {
		p, err := startPreview(dir, "sleep 30", 0)
		if err != nil {
			t.Fatalf("startPreview #%d: %v", i, err)
		}
		// Force a strictly increasing startedAt so "oldest" is deterministic —
		// real spawns can land in the same time.Now() tick on a fast machine.
		p.startedAt = time.Now().Add(time.Duration(i) * time.Second)
		path := fmt.Sprintf("/tmp/trace-%d.zip", i)
		procs = append(procs, p)
		paths = append(paths, path)
	}

	tracesMu.Lock()
	for i, p := range procs {
		traces[paths[i]] = p
	}
	// Cap at 2: the two oldest (index 0, 1) must be killed and removed;
	// the two newest (index 2, 3) must survive.
	pruneOldTraceViewers(2)
	remaining := len(traces)
	tracesMu.Unlock()

	t.Cleanup(func() {
		tracesMu.Lock()
		for _, p := range paths {
			if tp, ok := traces[p]; ok {
				killProcessGroup(tp.cmd)
			}
			delete(traces, p)
		}
		tracesMu.Unlock()
	})

	if remaining != 2 {
		t.Fatalf("expected 2 remaining trace viewers after pruning to cap 2, got %d", remaining)
	}
	tracesMu.Lock()
	_, oldest0 := traces[paths[0]]
	_, oldest1 := traces[paths[1]]
	_, newest2 := traces[paths[2]]
	_, newest3 := traces[paths[3]]
	tracesMu.Unlock()
	if oldest0 || oldest1 {
		t.Error("the two oldest viewers should have been reaped")
	}
	if !newest2 || !newest3 {
		t.Error("the two newest viewers should have survived")
	}

	// The reaped processes' cmd should actually have been killed (running()
	// flips false once their process group is terminated and Wait returns).
	deadline := time.Now().Add(5 * time.Second)
	for _, i := range []int{0, 1} {
		for procs[i].running() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if procs[i].running() {
			t.Errorf("reaped viewer #%d should have exited", i)
		}
	}
}
