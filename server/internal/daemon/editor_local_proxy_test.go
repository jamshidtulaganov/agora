package daemon

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func registerTestPreview(t *testing.T, upstream *httptest.Server) int {
	t.Helper()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("upstream port: %v", err)
	}
	key := fmt.Sprintf("/tmp/test-preview-%d", port)
	previewsMu.Lock()
	previews[key] = &previewProc{port: port, done: make(chan struct{})}
	previewsMu.Unlock()
	t.Cleanup(func() {
		previewsMu.Lock()
		delete(previews, key)
		previewsMu.Unlock()
	})
	return port
}

func TestPreviewLocalProxyForwardsTrackedPreview(t *testing.T) {
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = fmt.Fprint(w, "preview ok")
	}))
	defer upstream.Close()
	port := registerTestPreview(t, upstream)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/editor/local/%d/assets/app.js?build=1", port), nil)
	rec := httptest.NewRecorder()
	previewLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "preview ok" {
		t.Errorf("body: got %q", string(body))
	}
	if gotPath != "/assets/app.js" || !strings.Contains(gotQuery, "build=1") {
		t.Errorf("upstream request = %q?%s", gotPath, gotQuery)
	}
}

func TestPreviewLocalProxyAllowsEmbeddingAndRewritesRootAssets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'self'")
		w.Header().Set("Content-Security-Policy-Report-Only", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		_, _ = fmt.Fprint(w, `<script src="/assets/app.js"></script>`)
	}))
	defer upstream.Close()
	port := registerTestPreview(t, upstream)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/editor/local/%d/", port), nil)
	rec := httptest.NewRecorder()
	previewLocalProxyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, header := range []string{"Content-Security-Policy-Report-Only", "X-Frame-Options"} {
		if got := rec.Header().Get(header); got != "" {
			t.Errorf("%s was not removed: %q", header, got)
		}
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("non-framing CSP changed: %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("sandbox asset CORS = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("credentialed CORS must stay disabled, got %q", got)
	}
	want := fmt.Sprintf(`src="/editor/local/%d/assets/app.js"`, port)
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("rewritten body missing %q: %s", want, rec.Body.String())
	}
}

func TestPreviewLocalProxyRejectsUntrackedAndInvalidPorts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/editor/local/59999/", nil)
	rec := httptest.NewRecorder()
	previewLocalProxyHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("untracked port: expected 404, got %d", rec.Code)
	}

	for _, port := range []string{"abc", "-1", "70000", ""} {
		req := httptest.NewRequest(http.MethodGet, "/editor/local/"+port+"/", nil)
		rec := httptest.NewRecorder()
		previewLocalProxyHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("port %q: expected 400, got %d", port, rec.Code)
		}
	}
}
