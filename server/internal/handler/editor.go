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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// editorAgent is one agent that has a worktree on an issue. The editor lists
// these so a human can switch between each agent's worktree to review the work.
type editorAgent struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	WorkDir   string `json:"work_dir"`
	Status    string `json:"status"`
	// VSCodeURL is a desktop-VS-Code deep link that opens THIS worktree in the
	// human's local VS Code: vscode://file/<abs-path> for a self-host/local
	// daemon (worktree on the user's machine), or a Remote-SSH link
	// (vscode://vscode-remote/ssh-remote+<host>/<path>) for a remote box. Sent to
	// the browser so the "Open in VS Code" affordance works for every agent's
	// worktree, in cloud AND self-host mode.
	VSCodeURL string `json:"vscode_url,omitempty"`
	// editorPort is the local health/editor port the agent's runtime daemon
	// reported at registration (from agent_runtime.metadata.editor_port).
	// Unexported: internal routing only, never sent to the browser. Empty when
	// the daemon predates editor-port reporting — the handler falls back to the
	// legacy 19514 default.
	editorPort string
	// editorAddr is the per-runtime address the BACKEND should dial to reach this
	// daemon's /editor/launch when the box is not on loopback (from
	// agent_runtime.metadata.editor_addr, e.g. a Remote Boxes SSH-tunnel
	// endpoint). Non-empty ⇒ cloud-mode editor for THIS runtime; empty ⇒ fall
	// back to the global AGORA_DAEMON_INTERNAL env (existing behavior). Internal
	// routing only, never sent to the browser.
	editorAddr string
}

// listIssueEditorAgents returns, for every agent that ran a NON-leader task with
// a worktree on the issue, that agent's LATEST worktree — most-recently-active
// agent first. Squad-leader tasks are excluded: the leader dispatches + stops
// and never executes, so its worktree carries no reviewable changes (it would
// just show up as an empty "no changes" chip alongside the members who actually
// wrote the code).
func (h *Handler) listIssueEditorAgents(ctx context.Context, issueID pgtype.UUID) []editorAgent {
	rows, err := h.DB.Query(ctx,
		`SELECT agent_id, name, work_dir, status, editor_port, editor_addr FROM (
			SELECT DISTINCT ON (atq.agent_id)
			       atq.agent_id::text AS agent_id, COALESCE(a.name, '') AS name,
			       atq.work_dir, atq.status,
			       COALESCE(ar.metadata->>'editor_port', '') AS editor_port,
			       COALESCE(ar.metadata->>'editor_addr', '') AS editor_addr,
			       COALESCE(atq.completed_at, atq.started_at, atq.created_at) AS ts
			FROM agent_task_queue atq
			JOIN agent a ON a.id = atq.agent_id
			LEFT JOIN agent_runtime ar ON ar.id = atq.runtime_id
			WHERE atq.issue_id = $1 AND atq.agent_id IS NOT NULL
			  AND atq.work_dir IS NOT NULL AND atq.work_dir <> ''
			  AND atq.is_leader_task = false
			  -- Exclude the squad LEADER entirely: it coordinates and never writes
			  -- reviewable code, yet child-done / comment re-triggers enqueue it
			  -- NON-leader tasks too (so is_leader_task alone can't hide it). The
			  -- COALESCE keeps every agent when the issue isn't squad-assigned (no
			  -- real agent has the zero UUID).
			  AND atq.agent_id <> COALESCE(
			      (SELECT s.leader_id FROM issue i
			         JOIN squad s ON s.id = i.assignee_id
			        WHERE i.id = $1 AND i.assignee_type = 'squad'),
			      '00000000-0000-0000-0000-000000000000'::uuid)
			  -- Exclude failed/cancelled runs: their worktree is stale/partial, so
			  -- a chip for it (e.g. an agent that died on a usage limit before
			  -- writing anything) just lets the reviewer open broken work. The
			  -- DISTINCT ON then surfaces the agent's latest GOOD worktree, or
			  -- drops the agent entirely if it only ever failed.
			  AND atq.status NOT IN ('failed', 'cancelled')
			ORDER BY atq.agent_id, (atq.status IN ('running','dispatched')) DESC, ts DESC
		) sub ORDER BY ts DESC`, issueID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []editorAgent
	for rows.Next() {
		var a editorAgent
		if err := rows.Scan(&a.AgentID, &a.AgentName, &a.WorkDir, &a.Status, &a.editorPort, &a.editorAddr); err == nil {
			a.VSCodeURL = vscodeURLForWorktree(a.WorkDir, a.editorAddr)
			out = append(out, a)
		}
	}
	return out
}

// vscodeURLForWorktree builds a desktop-VS-Code deep link for a worktree. A
// local/self-host daemon (editorAddr empty) puts the worktree on the human's own
// machine → vscode://file/<abs-path>. A remote box (editorAddr set) opens over
// Remote-SSH against the daemon host. Empty workdir → "".
func vscodeURLForWorktree(workdir, editorAddr string) string {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return ""
	}
	// vscode://file wants a leading slash before the absolute path.
	path := workdir
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.TrimSpace(editorAddr) == "" {
		return "vscode://file" + path
	}
	host := editorAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i] // strip the port; Remote-SSH takes a host/alias
	}
	return "vscode://vscode-remote/ssh-remote+" + host + path
}

// daemonEditorBase is the local daemon health server the browser calls to launch
// code-server (self-host: daemon + browser + code-server share a host, so
// 127.0.0.1:<health-port> is reachable directly).
//
// Resolution order: (1) AGORA_DAEMON_EDITOR_URL env wins outright — an explicit
// operator override / escape hatch; (2) the port the agent's runtime daemon
// reported at registration (named profiles + worktrees are offset off 19514, so
// this is the only value that's correct for them); (3) the legacy 19514 default
// for daemons that predate editor-port reporting.
func daemonEditorBase(port string) string {
	if v := strings.TrimSpace(os.Getenv("AGORA_DAEMON_EDITOR_URL")); v != "" {
		return v
	}
	if p := strings.TrimSpace(port); p != "" {
		return "http://127.0.0.1:" + p
	}
	return "http://127.0.0.1:19514"
}

// daemonInternalAddr is the daemon's private-network (fly 6PN) address, e.g.
// "sd-agora-daemon.internal:19514". When set, we run in CLOUD mode: the daemon
// is remote, the browser can't reach its 127.0.0.1, so the BACKEND launches
// code-server on the daemon and reverse-proxies it. Empty = self-host.
func daemonInternalAddr() string {
	return strings.TrimSpace(os.Getenv("AGORA_DAEMON_INTERNAL"))
}

// resolveDaemonInternalAddr picks the address the BACKEND dials to reach a
// daemon's /editor/launch, resolved PER-RUNTIME. A Remote Boxes runtime carries
// its own reachable endpoint (e.g. an SSH-tunnel address) in
// agent_runtime.metadata.editor_addr; when present it wins, so multiple boxes
// each resolve to their own daemon. Empty ⇒ fall back to the process-wide
// AGORA_DAEMON_INTERNAL env — the existing single-global-daemon (Fly 6PN)
// behavior, UNCHANGED. Both empty ⇒ self-host. This keeps the cloud/self-host
// decision identical for every existing deployment (no runtime sets editor_addr
// until the Remote Boxes control-plane does) while making it per-runtime.
func resolveDaemonInternalAddr(runtimeAddr string) string {
	if a := strings.TrimSpace(runtimeAddr); a != "" {
		return a
	}
	return daemonInternalAddr()
}

// editorWorktreeGone reports whether a failed cloud-daemon editor launch was
// caused by a missing worktree ("workdir does not exist"). The daemon GC removes
// a task's env ~24h after its issue is done/cancelled (see
// server/internal/daemon/config.go DefaultGCTTL), so the work_dir recorded in the
// DB stops existing on disk. This is a normal, expected end state — not a server
// error — so it maps to 410 Gone with an actionable message rather than a 502.
func editorWorktreeGone(launchErr string) bool {
	return strings.Contains(launchErr, "workdir does not exist")
}

// GetIssueEditor resolves the issue's latest task worktree and prepares a live
// browser-VS-Code (code-server) for it. Self-host returns the workdir + daemon
// URL for the browser to launch directly; cloud launches on the daemon over 6PN
// and returns a same-origin reverse-proxy URL the browser can iframe over https.
func (h *Handler) GetIssueEditor(w http.ResponseWriter, r *http.Request) {
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
	// Resolve + authorize from the issue's own workspace — the endpoint takes
	// only an issue id (no workspace_slug header/param needed); the user must be
	// a member of that workspace.
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: issue.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	agents := h.listIssueEditorAgents(r.Context(), issue.ID)
	if len(agents) == 0 {
		writeError(w, http.StatusNotFound, "no worktree yet — assign an agent to this issue first")
		return
	}
	// The most-recently-active agent's worktree is the default; the frontend's
	// agent chips let the human switch to any other agent's worktree to review.
	latest := agents[0].WorkDir

	// The caller's editor account tokens (Settings → editor integration) ride
	// into the code-server env so gh CLI / HTTPS git in the editor terminal are
	// authenticated. Best-effort: nil when none configured.
	editorEnv := h.editorEnvForUser(r.Context(), parseUUID(userID), issue.WorkspaceID)

	if internal := resolveDaemonInternalAddr(agents[0].editorAddr); internal != "" {
		// Cloud / Remote Box: the backend proxies a single code-server; launch the
		// latest worktree (per-agent switching in cloud is a follow-up).
		port, proxyPath, lerr := launchEditorOnDaemon(r.Context(), internal, latest, userID, editorEnv)
		if lerr == nil {
			// Prefer the daemon's health-listener route (proven 6PN-reachable);
			// fall back to dialing the code-server port directly for daemons
			// that predate proxy_path.
			addr, prefix := internal, proxyPath
			if prefix == "" {
				host := internal
				if i := strings.LastIndex(internal, ":"); i > 0 {
					host = internal[:i] // strip the health port; keep just the daemon host
				}
				addr = fmt.Sprintf("%s:%d", host, port)
			}
			tok := registerEditorTarget(addr, prefix, uuidToString(issue.WorkspaceID))
			// A same-origin proxied base for the daemon PANE surface (preview /
			// test / live browser — see browserProxyPathAllowed), so the Preview
			// pane works in cloud exactly like self-host: its fetches and the
			// dev-server iframe all ride this base.
			daemonBase := "/browser/proxy/" + registerBrowserTarget(internal, uuidToString(issue.WorkspaceID))
			writeJSON(w, http.StatusOK, map[string]any{
				"mode":       "cloud",
				"editor_url": "/editor/proxy/" + tok + "/?folder=" + url.QueryEscape(latest),
				"daemon_url": daemonBase,
				"user_id":    userID,
				// The full agent roster rides along so the frontend renders the
				// switch chips + per-worktree "Open in VS Code" links even in cloud
				// mode (the browser editor still shows the default worktree; per-agent
				// BROWSER switching in cloud is the remaining follow-up).
				"agents": agents,
			})
			return
		}
		// Launch failed. The common cause is a GC'd worktree: the daemon removes a
		// task's env ~24h after its issue is done/cancelled, so the recorded
		// work_dir no longer exists. We must NOT degrade to a 127.0.0.1 self-host
		// URL here — this backend runs in cloud mode (AGORA_DAEMON_INTERNAL set),
		// the browser is remote, and a loopback URL just yields a CORS failure and
		// a stuck spinner (the exact symptom this replaces). Return an honest,
		// specific error the UI can render.
		if editorWorktreeGone(lerr.Error()) {
			writeJSON(w, http.StatusGone, map[string]string{
				"reason": "worktree_gone",
				"error":  "This issue's live editor was cleaned up — worktrees are removed automatically about a day after the agent finishes. Re-run an agent on this issue to recreate an editable worktree.",
			})
			return
		}
		writeError(w, http.StatusBadGateway, "failed to launch editor on the daemon: "+lerr.Error())
		return
	}

	// The default worktree is agents[0]'s, so address its runtime's daemon. All
	// of an issue's agents are normally on the same local daemon (one port); if a
	// future multi-daemon split makes that false, per-agent launch URLs are the
	// follow-up — today the default agent's port is the right single answer.
	resp := map[string]any{
		"mode":       "self-host",
		"daemon_url": daemonEditorBase(agents[0].editorPort),
		"user_id":    userID,
		"agents":     agents,
	}
	// Self-host: the BROWSER posts the daemon /editor/launch directly, so hand
	// it the env to forward (user's own tokens, to their own local daemon —
	// same trust domain as the session that fetched them).
	if len(editorEnv) > 0 {
		resp["editor_env"] = editorEnv
	}
	writeJSON(w, http.StatusOK, resp)
}

// launchEditorOnDaemon asks the remote daemon (over 6PN) to spawn code-server
// for a workdir. Returns the port it bound and, from daemons that support it,
// a proxy_path on the daemon's HEALTH listener that reverse-proxies to that
// editor ("/editor/local/<port>/"). Prefer the proxy path: the health listener
// is the one address proven reachable over the private network, while dialing
// the code-server port directly was not on Fly cloud nodes. An empty proxyPath
// (older daemon / Remote Box build) falls back to the direct-port dial.
func launchEditorOnDaemon(ctx context.Context, internal, workdir, userID string, env map[string]string) (port int, proxyPath string, err error) {
	payload := map[string]any{"workdir": workdir, "user_id": userID}
	if len(env) > 0 {
		payload["env"] = env
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+internal+"/editor/launch", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, "", fmt.Errorf("daemon %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Port      int    `json:"port"`
		ProxyPath string `json:"proxy_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, "", err
	}
	if out.Port == 0 {
		return 0, "", fmt.Errorf("daemon returned no port")
	}
	return out.Port, strings.TrimSuffix(out.ProxyPath, "/"), nil
}

// --- cloud reverse-proxy: token -> code-server address on the daemon ---

type editorTarget struct {
	addr string // host:port on the daemon (6PN-reachable)
	// prefix, when set, is prepended to the upstream path — the daemon health
	// listener's per-editor route ("/editor/local/<port>"), so the proxy rides
	// the known-reachable health port instead of dialing code-server directly.
	// Empty for older daemons: addr is then the raw code-server host:port.
	prefix string
	// workspaceID binds the token to its workspace so ProxyEditor re-checks the
	// caller's membership on every request (F8). The token is minted only after
	// a membership check, but it lives 8h in the iframe URL and leaks via
	// referer/history/logs — without this bind a non-member who captures it
	// could proxy into another tenant's code-server.
	workspaceID string
	expires     time.Time
}

var (
	editorTargetsMu sync.Mutex
	editorTargets   = map[string]editorTarget{}
)

func registerEditorTarget(addr, prefix, workspaceID string) string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	tok := hex.EncodeToString(buf[:])
	editorTargetsMu.Lock()
	editorTargets[tok] = editorTarget{addr: addr, prefix: prefix, workspaceID: workspaceID, expires: time.Now().Add(8 * time.Hour)}
	editorTargetsMu.Unlock()
	return tok
}

func lookupEditorTarget(tok string) (editorTarget, bool) {
	editorTargetsMu.Lock()
	defer editorTargetsMu.Unlock()
	t, ok := editorTargets[tok]
	if !ok || time.Now().After(t.expires) {
		return editorTarget{}, false
	}
	return t, true
}

// ProxyEditor reverse-proxies /editor/proxy/{token}/* (HTTP + WebSocket) to the
// code-server bound on the daemon for that token. Lives behind the authed
// session (the iframe carries the user cookie); the token is the capability that
// maps to a specific code-server instance.
func (h *Handler) ProxyEditor(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	t, ok := lookupEditorTarget(tok)
	if !ok {
		writeError(w, http.StatusNotFound, "editor session not found or expired")
		return
	}
	// Re-verify workspace membership on every proxied request (F8): the token is
	// minted behind a membership check but rides the iframe URL, so a
	// leaked/referer-logged token must not let a non-member reach the
	// code-server of a workspace they're not in.
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, t.workspaceID); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	addr := t.addr
	prefix := "/editor/proxy/" + tok
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: addr})
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		// New daemons: ride the health listener's per-editor route instead of
		// the raw code-server port (t.prefix empty on older daemons).
		if t.prefix != "" {
			req.URL.Path = t.prefix + req.URL.Path
		}
		req.Host = addr
	}
	// The global CSP middleware stamps our API policy (frame-ancestors 'none',
	// script-src 'self', …) on every response — but these responses ARE the
	// code-server app, which must be iframed by the issue editor pane and needs
	// its own (Monaco: workers, eval) policy. Drop ours so the upstream's
	// headers stand; the capability token + per-request membership check above
	// remain the actual access control.
	w.Header().Del("Content-Security-Policy")
	proxy.ServeHTTP(w, r)
}
