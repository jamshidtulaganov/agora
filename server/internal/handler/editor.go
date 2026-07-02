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
			out = append(out, a)
		}
	}
	return out
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

	if internal := resolveDaemonInternalAddr(agents[0].editorAddr); internal != "" {
		// Cloud / Remote Box: the backend proxies a single code-server; launch the
		// latest worktree (per-agent switching in cloud is a follow-up).
		port, lerr := launchEditorOnDaemon(r.Context(), internal, latest, userID)
		if lerr == nil {
			host := internal
			if i := strings.LastIndex(internal, ":"); i > 0 {
				host = internal[:i] // strip the health port; keep just the daemon host
			}
			tok := registerEditorTarget(fmt.Sprintf("%s:%d", host, port), uuidToString(issue.WorkspaceID))
			writeJSON(w, http.StatusOK, map[string]string{
				"mode":       "cloud",
				"editor_url": "/editor/proxy/" + tok + "/?folder=" + url.QueryEscape(latest),
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
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       "self-host",
		"daemon_url": daemonEditorBase(agents[0].editorPort),
		"user_id":    userID,
		"agents":     agents,
	})
}

// launchEditorOnDaemon asks the remote daemon (over 6PN) to spawn code-server
// for a workdir and returns the port it bound.
func launchEditorOnDaemon(ctx context.Context, internal, workdir, userID string) (int, error) {
	body, _ := json.Marshal(map[string]string{"workdir": workdir, "user_id": userID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+internal+"/editor/launch", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
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

// --- cloud reverse-proxy: token -> code-server address on the daemon ---

type editorTarget struct {
	addr string // host:port of code-server on the daemon (6PN-reachable)
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

func registerEditorTarget(addr, workspaceID string) string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	tok := hex.EncodeToString(buf[:])
	editorTargetsMu.Lock()
	editorTargets[tok] = editorTarget{addr: addr, workspaceID: workspaceID, expires: time.Now().Add(8 * time.Hour)}
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
		req.Host = addr
	}
	proxy.ServeHTTP(w, r)
}
