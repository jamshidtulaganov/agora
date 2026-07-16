package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Live QA browser in CLOUD mode — the missing half of the "Live testing" bay.
//
// The bay's screencast (EditorBrowserPane) talks to the daemon's
// /editor/browser/{start,stop,stream} endpoints. Self-host that's a direct
// 127.0.0.1 call; in cloud the daemon is on a private network the user's
// browser can't reach, so until now cloud degraded to a bare "open in new tab"
// link — no live watching exactly where QA runs in production. This endpoint
// pair closes that: the backend resolves the daemon that runs (or will run)
// the issue's QA, mints a capability token, and reverse-proxies ONLY the
// /editor/browser/* subtree of that daemon's health mux — HTTP + the
// screencast WebSocket — same authed-session + token model as the editor and
// trace proxies.

// GetIssueBrowser resolves where the Live-testing bay reaches a CDP browser
// for this issue. GET /api/issues/{id}/browser.
//
// Self-host (no internal daemon addr): the browser dials the local daemon
// directly — return its browser-facing base URL.
// Cloud / Remote Box: return a same-origin proxied base the pane can use for
// both the start POST and the stream WebSocket.
//
// This never depends on a worktree existing: the bay's
// primary use is driving the DEPLOYED QA target (workdir key
// "qa-target:<url>"), which needs only a reachable daemon — so a GC'd
// worktree or an issue with no dev task yet must not take the live bay down.
func (h *Handler) GetIssueBrowser(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), issueUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: issue.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// The daemon that ran the issue's most recent task is where QA drives the
	// shared browser (agent and reviewer must attach to the SAME Chromium). No
	// task yet ⇒ empty ⇒ fall through to the global daemon / self-host default,
	// mirroring the editor/trace resolution.
	editorAddr, editorPort := h.resolveTraceDaemon(r.Context(), issue.ID, pgtype.UUID{})
	internal := resolveDaemonInternalAddr(editorAddr)
	if internal == "" {
		writeJSON(w, http.StatusOK, map[string]string{
			"mode":       "self-host",
			"daemon_url": daemonEditorBase(editorPort),
		})
		return
	}
	tok := registerBrowserTarget(internal, uuidToString(issue.WorkspaceID))
	writeJSON(w, http.StatusOK, map[string]string{
		"mode":        "cloud",
		"browser_url": "/browser/proxy/" + tok,
	})
}

// GetDaemonBrowseTarget resolves the base URL the web folder picker walks one
// daemon's filesystem through (GET <daemon_url>/editor/fs/list?path=…).
//
// The issue-scoped resolvers (GetIssueBrowser, the artifact live surface) can't
// serve this: attaching a local folder to a PROJECT happens with no issue in
// hand, so the target is addressed by daemon_id instead.
//
// Authorization mirrors attaching a local_directory exactly (requireDaemonAccess:
// the daemon must be registered in this workspace and the caller must own it or
// be a workspace owner/admin). For cloud the returned base is a /browser/proxy
// token, so ProxyBrowser re-checks workspace membership on every listing
// request; for self-host it is the loopback daemon the caller's own box serves.
func (h *Handler) GetDaemonBrowseTarget(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if strings.TrimSpace(workspaceID) == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	daemonID := strings.TrimSpace(chi.URLParam(r, "daemonId"))
	if daemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon id is required")
		return
	}
	if !h.requireDaemonAccess(w, r, parseUUID(workspaceID), daemonID, "") {
		return
	}
	runtime, err := h.Queries.GetOnlineRuntimeForDaemon(r.Context(), db.GetOnlineRuntimeForDaemonParams{
		WorkspaceID: parseUUID(workspaceID),
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
	})
	if err != nil {
		// The access gate matches rows of any status, so an authorized caller
		// can land here for a daemon that is simply switched off. Report that
		// as a state the picker renders ("machine offline"), not a 500.
		writeJSON(w, http.StatusOK, map[string]string{"mode": "offline", "daemon_url": ""})
		return
	}
	var meta struct {
		EditorAddr string `json:"editor_addr"`
		EditorPort string `json:"editor_port"`
	}
	_ = json.Unmarshal(runtime.Metadata, &meta)
	if internal := resolveDaemonInternalAddr(meta.EditorAddr); internal != "" {
		writeJSON(w, http.StatusOK, map[string]string{
			"mode":       "cloud",
			"daemon_url": "/browser/proxy/" + registerBrowserTarget(internal, workspaceID),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"mode":       "self-host",
		"daemon_url": daemonEditorBase(meta.EditorPort),
	})
}

// --- cloud reverse-proxy: token -> the daemon health mux, /editor/browser/* only ---

type browserTarget struct {
	addr string // host:port of the daemon's health mux (serves /editor/browser/*)
	// workspaceID binds the token to its workspace so ProxyBrowser re-checks
	// the caller's membership on every request — the token rides page URLs and
	// leaks via referer/logs, same threat model as the editor/trace tokens.
	workspaceID string
	expires     time.Time
}

var (
	browserTargetsMu sync.Mutex
	browserTargets   = map[string]browserTarget{}
)

func registerBrowserTarget(addr, workspaceID string) string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	tok := hex.EncodeToString(buf[:])
	browserTargetsMu.Lock()
	browserTargets[tok] = browserTarget{addr: addr, workspaceID: workspaceID, expires: time.Now().Add(8 * time.Hour)}
	browserTargetsMu.Unlock()
	return tok
}

func lookupBrowserTarget(tok string) (browserTarget, bool) {
	browserTargetsMu.Lock()
	defer browserTargetsMu.Unlock()
	t, ok := browserTargets[tok]
	if !ok || time.Now().After(t.expires) {
		return browserTarget{}, false
	}
	return t, true
}

// browserProxyPathAllowed gates which upstream paths the browser proxy will
// forward. Unlike the trace proxy (whose upstream is a dedicated show-trace
// server), this proxy's upstream is the daemon's WHOLE health mux — which also
// serves preview, trace, update, and other internal routes. The capability
// token grants exactly the PANE surface the frontend drives — the live
// browser, the dev-server preview (start/stop/status + the proxied app via
// /editor/local/), and the one-shot test run — nothing else: no mutable
// repository actions, no checkout, no updater.
func browserProxyPathAllowed(p string) bool {
	return strings.HasPrefix(p, "/artifact/") ||
		strings.HasPrefix(p, "/editor/browser/") ||
		p == "/editor/preview" ||
		strings.HasPrefix(p, "/editor/preview/") ||
		p == "/editor/test" ||
		// Folder picker: read-only directory listing for the web "Add local
		// folder" flow. Matched exactly — a HasPrefix("/editor/fs/") would
		// also admit any future sibling route under that namespace, and this
		// proxy's upstream is the daemon's WHOLE health mux.
		p == "/editor/fs/list" ||
		strings.HasPrefix(p, "/editor/local/")
}

// ProxyBrowser reverse-proxies /browser/proxy/{token}/editor/browser/* (HTTP +
// the screencast WebSocket) to the daemon behind that token. Lives behind the
// authed session; the token is the capability that maps to one daemon.
func (h *Handler) ProxyBrowser(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	t, ok := lookupBrowserTarget(tok)
	if !ok {
		writeError(w, http.StatusNotFound, "browser session not found or expired")
		return
	}
	// Re-verify workspace membership on every proxied request (F8), matching
	// A leaked token must not let a non-member watch —
	// or drive — another tenant's QA browser.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, t.workspaceID); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	prefix := "/browser/proxy/" + tok
	upstream := strings.TrimPrefix(r.URL.Path, prefix)
	if !browserProxyPathAllowed(upstream) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// When proxying a dev-server (/editor/local/<port>/…), its HTML/JS/CSS
	// reference assets at absolute-root paths (/@vite/client, /src/…,
	// /node_modules/…, /assets/…). Those resolve against OUR origin, not the
	// proxied app, so they 404. Rewrite them to carry the full proxy prefix so
	// the iframe's asset graph loads. devPrefix is that full browser prefix
	// ("/browser/proxy/<tok>/editor/local/<port>"), empty for non-preview paths.
	devPrefix := ""
	if port := devServerPort(upstream); port != "" {
		devPrefix = prefix + "/editor/local/" + port
	}
	addr := t.addr
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: addr})
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		// Ask the dev server for uncompressed bytes so ModifyResponse can
		// rewrite them without gunzip/regzip. Only matters for the app itself.
		if devPrefix != "" {
			req.Header.Set("Accept-Encoding", "identity")
		}
		req.URL.Path = upstream
		req.Host = addr
		// The daemon's WebSocket upgrader and CORS helper only trust an empty or
		// localhost Origin (they predate any cross-host caller). The proxied
		// request carries the app's https origin, which would be rejected — drop
		// it; the session + membership checks above are the real access control.
		req.Header.Del("Origin")
	}
	// Rewrite the dev-server app's absolute-root asset refs to carry the proxy
	// prefix (only for /editor/local/ HTML/JS/CSS). Everything else passes
	// through untouched.
	if devPrefix != "" {
		proxy.ModifyResponse = func(resp *http.Response) error {
			return rewriteDevServerResponse(resp, devPrefix)
		}
	}
	// The global CSP middleware stamps the API policy on every response; these
	// responses are consumed by the pane's fetch/WS (not an iframe), but keep
	// parity with the editor/trace proxies so nothing downstream is surprised.
	w.Header().Del("Content-Security-Policy")
	proxy.ServeHTTP(w, r)
}

// devServerPort returns the <port> in an upstream path of the form
// /editor/local/<port>/… (the proxied dev-server route), or "" for anything
// else. Digits only — the port was allocated by the daemon, but validate the
// shape so a crafted path can't smuggle a rewrite prefix.
func devServerPort(upstreamPath string) string {
	const p = "/editor/local/"
	if !strings.HasPrefix(upstreamPath, p) {
		return ""
	}
	rest := upstreamPath[len(p):]
	port, _, _ := strings.Cut(rest, "/")
	if port == "" {
		return ""
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return port
}

// devRootRefs matches an absolute-root reference to a Vite/bundler dev root,
// captured right after a delimiter (quote, paren, =, or whitespace) so we only
// touch real references, not arbitrary "/foo" substrings. The alternation is
// the fixed set of roots a dev server serves its module graph + assets from.
var devRootRefs = regexp.MustCompile(
	`([\"'` + "`" + `(=\s])/((?:@vite/|@react-refresh|@id/|@fs/|src/|node_modules/|assets/|@vite-plugin|__vite))`,
)

// rewriteDevServerResponse rewrites absolute-root asset refs in an HTML/JS/CSS
// dev-server response so they resolve through the proxy prefix. Non-text
// responses and other content types pass through untouched. Pure except for
// mutating resp.Body + headers; the matching logic is in rewriteDevServerBody
// for unit testing.
func rewriteDevServerResponse(resp *http.Response, devPrefix string) error {
	ct := resp.Header.Get("Content-Type")
	if !isRewritableContentType(ct) {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	out := rewriteDevServerBody(body, devPrefix)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	// A rewritten body no longer matches the upstream validator.
	resp.Header.Del("ETag")
	return nil
}

// isRewritableContentType gates rewriting to the text formats that carry
// absolute-root refs (HTML entry, JS modules, CSS url()). JSON, images, fonts,
// wasm and the like pass through byte-for-byte.
func isRewritableContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "text/html") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "text/css")
}

// rewriteDevServerBody is the pure core: prefix every absolute-root dev-server
// ref with devPrefix. Exported-ish (package) for unit tests.
func rewriteDevServerBody(body []byte, devPrefix string) []byte {
	return devRootRefs.ReplaceAll(body, []byte("${1}"+devPrefix+"/${2}"))
}
