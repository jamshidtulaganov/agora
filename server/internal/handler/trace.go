package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Playwright trace viewer — Slice 3 of QA observability. A run_test_cases run
// can capture a full Playwright trace (DOM snapshots + screenshots + sources per
// step) as a .zip on the agent's runtime box (the daemon). This endpoint spawns
// `playwright show-trace` on THAT daemon (the trace file is local to it) and
// reverse-proxies the resulting viewer, so a QA reviewer time-travels the run
// inside Agora — same security model as the live code editor: same-origin,
// behind the authed session, a per-launch capability token maps to one viewer.
//
// The architecture deliberately reuses the editor reverse-proxy (editor.go): we
// do NOT vendor the viewer and do NOT send the reviewer to trace.playwright.dev
// (that hits auth/CORS walls loading a same-origin trace). show-trace ships with
// the Playwright the box already installed for run_test_cases, so there is no
// extra dependency to provision.

// LaunchTrace resolves a test_run's captured trace + the daemon that holds it,
// asks that daemon to spawn `playwright show-trace`, and returns a same-origin
// reverse-proxy URL the frontend iframes. GET /api/qa/trace/{testRunId}.
//
// It takes only an id and authorizes off the resolved
// entity's own workspace (the issue's), so no X-Workspace-ID header is required.
func (h *Handler) LaunchTrace(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	runUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "testRunId"), "test run id")
	if !ok {
		return
	}
	run, err := h.Queries.GetTestRunByID(r.Context(), runUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "test run not found")
		return
	}
	tracePath := strings.TrimSpace(run.TracePath)
	if tracePath == "" {
		writeError(w, http.StatusNotFound, "no trace captured for this run")
		return
	}
	if !run.IssueID.Valid {
		// A trace is only ever captured against an issue's QA run; without the
		// issue there is no daemon to resolve. Defensive — should not happen.
		writeError(w, http.StatusNotFound, "run has no issue context")
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), run.IssueID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	// Authorize from the run's own workspace — the caller must be a member.
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: issue.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// Which daemon holds the trace file: the runtime that ran the QA task for
	// this issue+agent. Its per-runtime editor_addr (Remote Box endpoint) or the
	// global AGORA_DAEMON_INTERNAL (cloud) tells the backend where to dial;
	// empty ⇒ self-host (the daemon's own health listener on localhost).
	editorAddr, editorPort := h.resolveTraceDaemon(r.Context(), run.IssueID, run.RunByID)
	daemonBase := traceDaemonBase(resolveDaemonInternalAddr(editorAddr), editorPort)

	port, lerr := launchTraceOnDaemon(r.Context(), daemonBase, tracePath)
	if lerr != nil {
		// The common terminal case: the trace .zip is gone from the runtime box.
		// Older runs recorded traces under /tmp, which the OS purges within days;
		// a GC'd worktree or daemon reinstall loses them too. That is an expired
		// artifact, not a server fault — answer 410 with an actionable message
		// (mirrors the artifact-runtime-gone contract).
		if strings.Contains(lerr.Error(), "trace file does not exist") {
			writeJSON(w, http.StatusGone, map[string]string{
				"reason": "trace_gone",
				"error":  "This run's trace file no longer exists on the runtime box (temporary storage was cleaned). Re-run the tests to capture a fresh trace.",
			})
			return
		}
		writeError(w, http.StatusBadGateway, "failed to launch trace viewer on the daemon: "+lerr.Error())
		return
	}
	// The proxy target is the daemon's OWN /trace/local/{port} route, not
	// <host>:<port> directly: the show-trace process binds the DAEMON HOST's
	// loopback, which a containerized (self-host Docker) or remote-node (cloud)
	// backend cannot dial directly. Same routing model as the live code editor
	// (/trace/local/{port}), which reaches the trace viewer through this same
	// daemon base for the identical reason.
	tok := registerTraceTarget(daemonBase, fmt.Sprintf("/trace/local/%d", port), uuidToString(issue.WorkspaceID))
	writeJSON(w, http.StatusOK, map[string]string{
		"trace_url": "/trace/proxy/" + tok + "/",
	})
}

// traceDaemonBase resolves the ONE daemon health-listener base URL used both
// to launch the trace viewer (POST /trace/launch) and to reverse-proxy it
// afterward (GET /trace/local/{port}/*, via ProxyTrace). Cloud/Remote Box
// (internal set) → the daemon's private address; self-host (internal empty) →
// the local daemon health port. It uses the same runtime resolution as artifacts,
// unlike the old two-value split there is no separate "proxy host:port" —
// both operations always go through the SAME daemon base, because the trace
// viewer's port is only reachable THROUGH the daemon's own reverse proxy, not
// by dialing it directly (see ProxyTrace).
func traceDaemonBase(internal, editorPort string) string {
	if internal != "" {
		return "http://" + internal
	}
	return daemonEditorBase(editorPort)
}

// resolveTraceDaemon finds the runtime endpoint of the daemon that ran the QA
// task producing this run — the box the trace .zip is local to. Picks the most
// recent task on the run's issue by the running agent (falling back to the most
// recent task on the issue when the run carries no agent), and reads the
// runtime's reported editor_addr / editor_port (same metadata the editor uses).
// Empty strings ⇒ no runtime found; the caller then falls back to the global
// daemon / self-host, exactly like the editor path.
func (h *Handler) resolveTraceDaemon(ctx context.Context, issueID, agentID pgtype.UUID) (editorAddr, editorPort string) {
	row := h.DB.QueryRow(ctx, `
		SELECT COALESCE(ar.metadata->>'editor_addr', ''), COALESCE(ar.metadata->>'editor_port', '')
		FROM agent_task_queue atq
		LEFT JOIN agent_runtime ar ON ar.id = atq.runtime_id
		WHERE atq.issue_id = $1 AND ($2::uuid IS NULL OR atq.agent_id = $2)
		ORDER BY COALESCE(atq.completed_at, atq.started_at, atq.created_at) DESC
		LIMIT 1`, issueID, agentID)
	_ = row.Scan(&editorAddr, &editorPort)
	return editorAddr, editorPort
}

// launchTraceOnDaemon asks a daemon (self-host loopback or cloud 6PN) to spawn
// `playwright show-trace` for a trace file and returns the port it bound. base
// is a full URL (scheme+host[:port]) so the same call serves both modes.
func launchTraceOnDaemon(ctx context.Context, base, tracePath string) (int, error) {
	body, _ := json.Marshal(map[string]string{"trace_path": tracePath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/trace/launch", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// show-trace has to unzip the trace and bring up its server; a large trace
	// needs a generous startup window.
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("daemon %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.Port == 0 {
		return 0, fmt.Errorf("daemon returned no port")
	}
	return out.Port, nil
}

// --- cloud reverse-proxy: token -> show-trace address on the daemon ---
// A registry separate from editorTargets: same shape, but its own lifecycle so
// a trace session and an editor session never collide on a token.

type traceTarget struct {
	// base is the daemon health-listener base URL this viewer's daemon is
	// reachable at (e.g. "http://127.0.0.1:20038" self-host, or
	// "http://<daemon>.internal:19514" cloud) — the SAME base LaunchTrace
	// POSTed /trace/launch to.
	base string
	// path is the daemon-side proxy path for this specific viewer instance,
	// e.g. "/trace/local/54321" — the daemon's own reverse proxy (mirroring
	// /editor/local/{port}) forwards it to the show-trace process on ITS OWN
	// loopback. ProxyTrace never dials the viewer's port directly.
	path string
	// workspaceID binds the token to its workspace so ProxyTrace re-checks the
	// caller's membership on every request (F8) — the token is minted behind a
	// membership check but rides the 8h iframe URL and leaks via referer/logs.
	workspaceID string
	expires     time.Time
}

var (
	traceTargetsMu sync.Mutex
	traceTargets   = map[string]traceTarget{}
)

func registerTraceTarget(base, path, workspaceID string) string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	tok := hex.EncodeToString(buf[:])
	traceTargetsMu.Lock()
	traceTargets[tok] = traceTarget{base: base, path: path, workspaceID: workspaceID, expires: time.Now().Add(8 * time.Hour)}
	traceTargetsMu.Unlock()
	return tok
}

func lookupTraceTarget(tok string) (traceTarget, bool) {
	traceTargetsMu.Lock()
	defer traceTargetsMu.Unlock()
	t, ok := traceTargets[tok]
	if !ok || time.Now().After(t.expires) {
		return traceTarget{}, false
	}
	return t, true
}

// ProxyTrace reverse-proxies /trace/proxy/{token}/* (HTTP + WebSocket) to the
// `playwright show-trace` server bound on the daemon for that token. Lives
// behind the authed session (the iframe carries the user cookie); the token is
// the capability that maps to a specific viewer.
func (h *Handler) ProxyTrace(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	t, ok := lookupTraceTarget(tok)
	if !ok {
		writeError(w, http.StatusNotFound, "trace session not found or expired")
		return
	}
	// Re-verify workspace membership on every proxied request (F8): a
	// leaked/referer-logged trace token must not let a non-member reach another
	// tenant's trace viewer (which renders that run's pages + network).
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, t.workspaceID); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	target, err := url.Parse(t.base)
	if err != nil {
		writeError(w, http.StatusBadGateway, "invalid trace viewer target")
		return
	}
	prefix := "/trace/proxy/" + tok
	tracePathPrefix := strings.TrimRight(t.path, "/")
	proxy := httputil.NewSingleHostReverseProxy(target)
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		rest := strings.TrimPrefix(req.URL.Path, prefix)
		if rest == "" {
			rest = "/"
		}
		// Route THROUGH the daemon's own /trace/local/{port} reverse proxy
		// (registered at launch time as t.path) instead of dialing the viewer's
		// port directly — the viewer binds the daemon HOST's loopback, which is
		// unreachable from a containerized or remote-node backend.
		req.URL.Path = tracePathPrefix + rest
		req.Host = target.Host
	}
	// The global CSP middleware stamps our API policy (frame-ancestors 'none')
	// on every response — but these responses ARE the Playwright trace viewer,
	// which must be iframed by the QA panel's TraceOverlay. Drop ours so the
	// upstream's headers stand; the capability token + the per-request
	// membership check above remain the actual access control (same model as
	// other capability-gated proxies).
	w.Header().Del("Content-Security-Policy")
	proxy.ServeHTTP(w, r)
}
