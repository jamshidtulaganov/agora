package handler

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDevServerPort(t *testing.T) {
	cases := map[string]string{
		"/editor/local/5173/":                  "5173",
		"/editor/local/42873/assets/index.js":  "42873",
		"/editor/local/5173":                   "5173", // no trailing slash: port is the whole rest
		"/editor/browser/start":                "",
		"/editor/preview":                      "",
		"/editor/local//assets":                "", // empty port
		"/editor/local/12ab/x":                 "", // non-numeric
		"/editor/local/../etc":                 "", // traversal-ish, non-numeric
	}
	for path, want := range cases {
		if got := devServerPort(path); got != want {
			t.Errorf("devServerPort(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRewriteDevServerBody(t *testing.T) {
	prefix := "/browser/proxy/tok123/editor/local/5173"
	in := strings.Join([]string{
		`<script type="module" src="/@vite/client"></script>`,
		`<script type="module" src="/src/main.tsx"></script>`,
		`import { createApp } from "/node_modules/.vite/deps/vue.js";`,
		`import App from "/src/App.vue";`,
		`background: url(/assets/logo.png);`,
		`fetch("/@fs/home/u/proj/x");`,
		`const rel = "./local.js";`, // relative — must NOT change
		`const api = "/api/data";`,  // app data path, not a dev root — must NOT change
	}, "\n")
	out := string(rewriteDevServerBody([]byte(in), prefix))

	mustHave := []string{
		`src="` + prefix + `/@vite/client"`,
		`src="` + prefix + `/src/main.tsx"`,
		`"` + prefix + `/node_modules/.vite/deps/vue.js"`,
		`"` + prefix + `/src/App.vue"`,
		`url(` + prefix + `/assets/logo.png)`,
		`"` + prefix + `/@fs/home/u/proj/x"`,
	}
	for _, s := range mustHave {
		if !strings.Contains(out, s) {
			t.Errorf("expected rewritten output to contain %q\n---\n%s", s, out)
		}
	}
	// Untouched: relative import + non-dev-root app path.
	if !strings.Contains(out, `"./local.js"`) {
		t.Error("relative import was wrongly rewritten")
	}
	if !strings.Contains(out, `"/api/data"`) {
		t.Error("app data path /api/data was wrongly rewritten")
	}
}

func TestIsRewritableContentType(t *testing.T) {
	yes := []string{"text/html", "text/html; charset=utf-8", "application/javascript", "text/javascript", "text/css; charset=utf-8"}
	no := []string{"application/json", "image/png", "font/woff2", "application/wasm", ""}
	for _, c := range yes {
		if !isRewritableContentType(c) {
			t.Errorf("%q should be rewritable", c)
		}
	}
	for _, c := range no {
		if isRewritableContentType(c) {
			t.Errorf("%q should NOT be rewritable", c)
		}
	}
}

// ModifyResponse leaves a non-text response byte-identical.
func TestRewriteDevServerResponsePassthroughForImages(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	resp := &http.Response{
		Header: http.Header{"Content-Type": {"image/png"}},
		Body:   io.NopCloser(strings.NewReader(string(raw))),
	}
	if err := rewriteDevServerResponse(resp, "/p/editor/local/5173"); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(raw) {
		t.Errorf("image body mutated: %v", got)
	}
}
