package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httputil"
	"net/url"
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
// directly — return its base URL, exactly like GetIssueEditor's self-host arm.
// Cloud / Remote Box: return a same-origin proxied base the pane can use for
// both the start POST and the stream WebSocket.
//
// Unlike GetIssueEditor this never depends on a worktree existing: the bay's
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
// serves /editor/launch, /trace/launch, /update and friends. The capability
// token grants exactly the live-browser surface, nothing else.
func browserProxyPathAllowed(p string) bool {
	return strings.HasPrefix(p, "/editor/browser/")
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
	// ProxyEditor/ProxyTrace: a leaked token must not let a non-member watch —
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
	addr := t.addr
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: addr})
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.URL.Path = upstream
		req.Host = addr
		// The daemon's WebSocket upgrader and CORS helper only trust an empty or
		// localhost Origin (they predate any cross-host caller). The proxied
		// request carries the app's https origin, which would be rejected — drop
		// it; the session + membership checks above are the real access control.
		req.Header.Del("Origin")
	}
	// The global CSP middleware stamps the API policy on every response; these
	// responses are consumed by the pane's fetch/WS (not an iframe), but keep
	// parity with the editor/trace proxies so nothing downstream is surprised.
	w.Header().Del("Content-Security-Policy")
	proxy.ServeHTTP(w, r)
}
