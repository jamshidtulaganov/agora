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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// daemonEditorBase is the local daemon health server the browser calls to launch
// code-server (self-host: daemon + browser + code-server share a host, so
// 127.0.0.1:<health-port> is reachable directly). Override with
// AGORA_DAEMON_EDITOR_URL.
func daemonEditorBase() string {
	if v := strings.TrimSpace(os.Getenv("AGORA_DAEMON_EDITOR_URL")); v != "" {
		return v
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

	var workdir string
	err = h.DB.QueryRow(r.Context(),
		`SELECT work_dir FROM agent_task_queue
		 WHERE issue_id = $1 AND work_dir IS NOT NULL AND work_dir <> ''
		 ORDER BY COALESCE(completed_at, started_at, created_at) DESC
		 LIMIT 1`, issue.ID).Scan(&workdir)
	if err != nil || strings.TrimSpace(workdir) == "" {
		writeError(w, http.StatusNotFound, "no worktree yet — assign an agent to this issue first")
		return
	}

	if internal := daemonInternalAddr(); internal != "" {
		port, lerr := launchEditorOnDaemon(r.Context(), internal, workdir, userID)
		if lerr != nil {
			writeError(w, http.StatusBadGateway, "failed to launch editor on the daemon: "+lerr.Error())
			return
		}
		host := internal
		if i := strings.LastIndex(internal, ":"); i > 0 {
			host = internal[:i] // strip the health port; keep just the daemon host
		}
		tok := registerEditorTarget(fmt.Sprintf("%s:%d", host, port))
		writeJSON(w, http.StatusOK, map[string]string{
			"mode":       "cloud",
			"editor_url": "/editor/proxy/" + tok + "/?folder=" + url.QueryEscape(workdir),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"mode":       "self-host",
		"workdir":    workdir,
		"daemon_url": daemonEditorBase(),
		"user_id":    userID,
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
	addr    string // host:port of code-server on the daemon (6PN-reachable)
	expires time.Time
}

var (
	editorTargetsMu sync.Mutex
	editorTargets   = map[string]editorTarget{}
)

func registerEditorTarget(addr string) string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	tok := hex.EncodeToString(buf[:])
	editorTargetsMu.Lock()
	editorTargets[tok] = editorTarget{addr: addr, expires: time.Now().Add(8 * time.Hour)}
	editorTargetsMu.Unlock()
	return tok
}

func lookupEditorTarget(tok string) (string, bool) {
	editorTargetsMu.Lock()
	defer editorTargetsMu.Unlock()
	t, ok := editorTargets[tok]
	if !ok || time.Now().After(t.expires) {
		return "", false
	}
	return t.addr, true
}

// ProxyEditor reverse-proxies /editor/proxy/{token}/* (HTTP + WebSocket) to the
// code-server bound on the daemon for that token. Lives behind the authed
// session (the iframe carries the user cookie); the token is the capability that
// maps to a specific code-server instance.
func (h *Handler) ProxyEditor(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	addr, ok := lookupEditorTarget(tok)
	if !ok {
		writeError(w, http.StatusNotFound, "editor session not found or expired")
		return
	}
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
