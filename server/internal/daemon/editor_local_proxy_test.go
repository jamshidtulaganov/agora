package daemon

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// registerTestEditor points the package-level editors registry at a fake
// "code-server" (any 127.0.0.1 HTTP server) and returns its port + a cleanup.
// A non-started exec.Cmd has ProcessState == nil, which is exactly the "alive"
// signal the proxy gate keys on.
func registerTestEditor(t *testing.T, upstream *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("upstream port: %v", err)
	}
	key := fmt.Sprintf("test-user\x00/tmp/test-workdir-%d", port)
	editorsMu.Lock()
	editors[key] = &editorProc{port: port, cmd: exec.Command("true")}
	editorsMu.Unlock()
	t.Cleanup(func() {
		editorsMu.Lock()
		delete(editors, key)
		editorsMu.Unlock()
	})
	return port
}

func TestEditorLocalProxyForwardsPathAndQueryToTrackedEditor(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		fmt.Fprint(w, "code-server ok")
	}))
	defer upstream.Close()
	port := registerTestEditor(t, upstream)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/editor/local/%d/static/out/vs/loader.js?folder=%s", port, url.QueryEscape("/data/w")), nil)
	rec := httptest.NewRecorder()
	editorLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "code-server ok" {
		t.Errorf("body: got %q", string(body))
	}
	if gotPath != "/static/out/vs/loader.js" {
		t.Errorf("upstream path: got %q, want /static/out/vs/loader.js", gotPath)
	}
	if !strings.Contains(gotQuery, "folder=") {
		t.Errorf("upstream query lost: got %q", gotQuery)
	}
}

func TestEditorLocalProxyRootPathMapsToSlash(t *testing.T) {
	t.Parallel()

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer upstream.Close()
	port := registerTestEditor(t, upstream)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/editor/local/%d/", port), nil)
	rec := httptest.NewRecorder()
	editorLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/" {
		t.Errorf("upstream path: got %q, want /", gotPath)
	}
}

func TestEditorLocalProxyRejectsUntrackedAndInvalidPorts(t *testing.T) {
	t.Parallel()

	// Untracked (but syntactically valid) port → 404, no dial.
	req := httptest.NewRequest(http.MethodGet, "/editor/local/59999/", nil)
	rec := httptest.NewRecorder()
	editorLocalProxyHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("untracked port: expected 404, got %d", rec.Code)
	}

	// Garbage port → 400.
	for _, p := range []string{"abc", "-1", "70000", ""} {
		req := httptest.NewRequest(http.MethodGet, "/editor/local/"+p+"/", nil)
		rec := httptest.NewRecorder()
		editorLocalProxyHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("port %q: expected 400, got %d", p, rec.Code)
		}
	}
}

func TestEditorLocalProxyTreatsExitedEditorAsDead(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	port := registerTestEditor(t, upstream)

	// Flip the tracked instance to "exited": a completed cmd has ProcessState set.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	editorsMu.Lock()
	for _, e := range editors {
		if e.port == port {
			e.cmd = cmd
		}
	}
	editorsMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/editor/local/%d/", port), nil)
	rec := httptest.NewRecorder()
	editorLocalProxyHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("dead editor: expected 404, got %d", rec.Code)
	}
}
