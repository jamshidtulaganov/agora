package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/repocache"
)

// HealthResponse is returned by the daemon's local health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
	// OS is the daemon's runtime.GOOS. The desktop app compares it against its
	// own host OS to detect a daemon it cannot manage — e.g. a Windows desktop
	// reaching a Linux daemon inside WSL2 over localhost forwarding. The
	// lifecycle CLI (`daemon start/stop`) acts on the host process namespace,
	// so a foreign-OS daemon can't be started/stopped by the app even though
	// /health is reachable. See #3916.
	OS              string            `json:"os"`
	Uptime          string            `json:"uptime"`
	DaemonID        string            `json:"daemon_id"`
	DeviceName      string            `json:"device_name"`
	ServerURL       string            `json:"server_url"`
	CLIVersion      string            `json:"cli_version"`
	ActiveTaskCount int64             `json:"active_task_count"`
	Agents          []string          `json:"agents"`
	Workspaces      []healthWorkspace `json:"workspaces"`
}

type healthWorkspace struct {
	ID       string   `json:"id"`
	Runtimes []string `json:"runtimes"`
}

// listenHealth binds the health port. Returns the listener or an error if
// another daemon is already running (port taken).
func (d *Daemon) listenHealth() (net.Listener, error) {
	host := "127.0.0.1"
	if v := strings.TrimSpace(os.Getenv("AGORA_HEALTH_BIND")); v != "" {
		host = v // cloud sets 0.0.0.0 so the backend can reach /editor/launch over 6PN
	}
	addr := fmt.Sprintf("%s:%d", host, d.cfg.HealthPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("another daemon is already running on %s: %w", addr, err)
	}
	return ln, nil
}

// repoCheckoutRequest is the body of a POST /repo/checkout request.
type repoCheckoutRequest struct {
	URL          string `json:"url"`
	WorkspaceID  string `json:"workspace_id"`
	WorkDir      string `json:"workdir"`
	Ref          string `json:"ref,omitempty"`
	AgentName    string `json:"agent_name"`
	TaskID       string `json:"task_id"`
	SprintBranch string `json:"sprint_branch,omitempty"`
}

// healthHandler returns the /health HTTP handler. Extracted from serveHealth
// so tests can exercise it without spinning up a listener.
func (d *Daemon) healthHandler(startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		var wsList []healthWorkspace
		for id, ws := range d.workspaces {
			wsList = append(wsList, healthWorkspace{
				ID:       id,
				Runtimes: ws.runtimeIDs,
			})
		}
		d.mu.Unlock()

		agents := make([]string, 0, len(d.cfg.Agents))
		for name := range d.cfg.Agents {
			agents = append(agents, name)
		}

		// "starting" until preflight (PAT renew + initial workspace sync +
		// runtime registration) completes; "running" once the daemon can
		// actually claim tasks. The health port is bound before preflight for
		// liveness/diagnostics, so callers must not treat a reachable endpoint
		// as ready — they gate on this status. Consumers that only know
		// "running" (older CLI/desktop) safely treat "starting" as not-ready.
		status := "starting"
		if d.ready.Load() {
			status = "running"
		}

		resp := HealthResponse{
			Status:          status,
			PID:             os.Getpid(),
			OS:              runtime.GOOS,
			Uptime:          time.Since(startedAt).Truncate(time.Second).String(),
			DaemonID:        d.cfg.DaemonID,
			DeviceName:      d.cfg.DeviceName,
			ServerURL:       d.cfg.ServerBaseURL,
			CLIVersion:      d.cfg.CLIVersion,
			ActiveTaskCount: d.activeTasks.Load(),
			Agents:          agents,
			Workspaces:      wsList,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// shutdownHandler triggers a graceful daemon shutdown by cancelling the
// top-level context. Used by `agora daemon stop` so we don't depend on
// OS-signal delivery, which is unreliable on Windows once the daemon is
// spawned with DETACHED_PROCESS (no shared console with the stop caller).
// The listener is bound to 127.0.0.1 only, so only local processes can hit
// this endpoint.
func (d *Daemon) shutdownHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		if d.cancelFunc != nil {
			// Cancel asynchronously so the response flushes first; otherwise
			// srv.Close() races with the writer.
			go d.cancelFunc()
		}
	}
}

// serveHealth runs the health HTTP server on the given listener.
// Blocks until ctx is cancelled.
func (d *Daemon) serveHealth(ctx context.Context, ln net.Listener, startedAt time.Time) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.healthHandler(startedAt))
	mux.HandleFunc("/shutdown", d.shutdownHandler())

	mux.HandleFunc("/repo/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req repoCheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		if req.WorkspaceID == "" {
			http.Error(w, "workspace_id is required", http.StatusBadRequest)
			return
		}
		if req.WorkDir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}

		if d.repoCache == nil {
			http.Error(w, "repo cache not initialized", http.StatusInternalServerError)
			return
		}

		if err := d.ensureRepoReady(r.Context(), req.WorkspaceID, req.URL); err != nil {
			statusCode := http.StatusInternalServerError
			if errors.Is(err, ErrRepoNotConfigured) {
				statusCode = http.StatusBadRequest
			}
			d.logger.Error("repo checkout readiness failed", "workspace_id", req.WorkspaceID, "url", req.URL, "error", err)
			http.Error(w, err.Error(), statusCode)
			return
		}

		result, err := d.repoCache.CreateWorktree(repocache.WorktreeParams{
			WorkspaceID:         req.WorkspaceID,
			RepoURL:             req.URL,
			WorkDir:             req.WorkDir,
			Ref:                 req.Ref,
			AgentName:           req.AgentName,
			TaskID:              req.TaskID,
			CoAuthoredByEnabled: d.workspaceCoAuthoredByEnabled(req.WorkspaceID),
		})
		if err != nil {
			d.logger.Error("repo checkout failed", "url", req.URL, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Sprint-worktree: the worktree CreateWorktree just made is on a
		// per-agent fork branch (agent/<name>/<taskid>). For a sprint task,
		// re-point it onto the SHARED sprint branch so every dev's commits and
		// pushes target the team's one branch. This is the first-touch mirror of
		// the reused-workdir path (runTask → ensureSprintBranch): on a fresh
		// workdir that call no-ops because no repo exists yet at task start, so
		// the actual first checkout — here — must apply it. ensureSprintBranch is
		// idempotent and only re-points when origin/<branch> exists (prod-safe).
		if req.SprintBranch != "" {
			d.ensureSprintBranch(req.WorkDir, req.SprintBranch, shortID(req.TaskID), d.logger)
			// Report the shared-branch alias the worktree now sits on, not the
			// abandoned fork branch, so the agent's checkout output is accurate.
			for _, repo := range gitReposUnder(req.WorkDir) {
				if b := strings.TrimSpace(runGit(repo, "rev-parse", "--abbrev-ref", "HEAD")); b != "" {
					result.BranchName = b
					break
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- live code editor (code-server) per task worktree ---
	// Spawns a browser VS Code (code-server) bound to localhost, pointed at a
	// task's workdir, so a human can watch + edit the agent's code live. The
	// daemon, the browser and code-server are all on the same host for
	// self-host, so the URL we return (127.0.0.1:<port>) is directly reachable.
	// Reused per workdir. CORS is scoped to localhost origins (the Agora app).
	type editorProc struct {
		port int
		cmd  *exec.Cmd
	}
	editors := make(map[string]*editorProc)
	var editorsMu sync.Mutex

	mux.HandleFunc("/editor/launch", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			WorkDir string `json:"workdir"`
			UserID  string `json:"user_id"`
			// Env carries the user's editor account tokens (Settings →
			// editor integration) into the code-server process, so gh CLI /
			// HTTPS git in its terminals are authenticated. ALLOWLISTED
			// below — in self-host mode this request comes from the browser,
			// and arbitrary env injection into a process that runs shells
			// must be impossible.
			Env map[string]string `json:"env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		workdir := strings.TrimSpace(req.WorkDir)
		if workdir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}
		// One code-server per (user, worktree): different humans on the same
		// worktree each get an isolated VS Code (own --user-data-dir → own
		// session/layout, no single-window blocking). They still share the
		// files on disk (last-save-wins; VS Code reloads external changes).
		key := strings.TrimSpace(req.UserID) + "\x00" + workdir
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			http.Error(w, "workdir does not exist", http.StatusBadRequest)
			return
		}

		bin, err := exec.LookPath("code-server")
		if err != nil {
			http.Error(w, "code-server is not installed on the daemon host", http.StatusNotImplemented)
			return
		}

		editorsMu.Lock()
		defer editorsMu.Unlock()

		// Reuse a still-running editor for this (user, workdir).
		if e, ok := editors[key]; ok {
			if e.cmd.ProcessState == nil {
				writeEditorURL(w, e.port, workdir)
				return
			}
			delete(editors, key)
		}

		// Allocate a free localhost port.
		pl, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			http.Error(w, "failed to allocate a port", http.StatusInternalServerError)
			return
		}
		port := pl.Addr().(*net.TCPAddr).Port
		pl.Close()

		userDataDir, err := editorUserDataDir(key)
		if err != nil {
			http.Error(w, "failed to prepare editor data dir: "+err.Error(), http.StatusInternalServerError)
			return
		}
		cmd := exec.Command(bin,
			"--bind-addr", fmt.Sprintf("%s:%d", editorBindHost(), port),
			"--auth", "none",
			// The agent authored its own isolated worktree branch, so open it
			// trusted — no workspace-trust prompt. Belt-and-suspenders with the
			// seeded settings.json (security.workspace.trust.enabled=false).
			"--disable-workspace-trust",
			"--user-data-dir", userDataDir,
			workdir,
		)
		// Scrub PORT (and CODE_SERVER_*) from the child env: code-server honors
		// a PORT env var and it overrides --bind-addr's port. The daemon
		// commonly inherits PORT=8080 — the backend's port, via make/.env — so
		// every spawned editor tried to bind the backend's own port and died
		// instantly with EADDRINUSE (the silent-death 502 at the proxy).
		env := os.Environ()
		filtered := env[:0]
		for _, kv := range env {
			if strings.HasPrefix(kv, "PORT=") || strings.HasPrefix(kv, "CODE_SERVER_") {
				continue
			}
			filtered = append(filtered, kv)
		}
		// User editor tokens from the launch request — STRICT allowlist (the
		// self-host launch comes from the browser; nothing outside these
		// token vars may reach a shell-running process's environment).
		for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITLAB_TOKEN"} {
			if v := strings.TrimSpace(req.Env[k]); v != "" && !strings.ContainsAny(v, "\n\r\x00") {
				filtered = append(filtered, k+"="+v)
			}
		}
		cmd.Env = filtered
		// code-server's own output is the ONLY evidence when it dies right after
		// launch — a silent-death instance previously left nothing but a 502 at
		// the proxy. Tee it to a log in the instance's user-data dir.
		logPath := filepath.Join(userDataDir, "code-server.log")
		if lf, lerr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); lerr == nil {
			cmd.Stdout = lf
			cmd.Stderr = lf
		}
		if err := cmd.Start(); err != nil {
			http.Error(w, "failed to launch code-server: "+err.Error(), http.StatusInternalServerError)
			return
		}
		editors[key] = &editorProc{port: port, cmd: cmd}
		// Reap the child so (a) it never zombies and (b) ProcessState flips
		// non-nil on exit, which is exactly what the reuse check above keys on —
		// without Wait, a dead editor read as "still running" forever and every
		// later open handed out the dead instance's port (502 at the proxy).
		go func(c *exec.Cmd, k, lp string) {
			err := c.Wait()
			d.logger.Warn("code-server editor exited", "workdir", k, "error", err, "log", lp)
		}(cmd, key, logPath)
		d.logger.Info("launched code-server editor", "workdir", workdir, "port", port, "user", req.UserID, "log", logPath)
		writeEditorURL(w, port, workdir)
	})

	// --- agent changes (git diff) for a task worktree ---
	// For each git repo under the worktree, returns the agent's changes vs the
	// merge-base with the default branch (everything it did since branching) —
	// file list + unified diff. Powers the Agora-native "Changes" review panel.
	mux.HandleFunc("/editor/changes", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		workdir := strings.TrimSpace(req.WorkDir)
		if workdir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			http.Error(w, "workdir does not exist", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"repos": gitWorktreeChanges(workdir)})
	})

	// --- accept: open a pull request for the worktree changes ---
	// The human's "Accept" in co-code mode: commit any pending edits, push the
	// branch, and open (or reuse the existing open) GitHub PR for each repo under
	// the worktree that is ahead of its base. Opening the PR also triggers CI, so
	// the dev reviews/merges in their normal GitHub flow. Uses `gh`, authenticated
	// on the daemon host. CORS scoped to localhost (the Agora app).
	mux.HandleFunc("/editor/open-pr", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
			Title   string `json:"title"`
			Body    string `json:"body"`
			Base    string `json:"base"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		workdir := strings.TrimSpace(req.WorkDir)
		if workdir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			http.Error(w, "workdir does not exist", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": openWorktreePR(workdir, strings.TrimSpace(req.Title), req.Body, strings.TrimSpace(req.Base)),
		})
	})

	// --- reject: discard the worktree changes ---
	// The human's "Discard" in co-code mode: reset each repo under the worktree to
	// its base (merge-base with the default branch) and remove untracked files.
	// Local only — any already-pushed commits stay on the remote and remain
	// recoverable; this just clears the live worktree so the next run starts clean.
	mux.HandleFunc("/editor/discard", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		workdir := strings.TrimSpace(req.WorkDir)
		if workdir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}
		if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
			http.Error(w, "workdir does not exist", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"repos": discardWorktreeChanges(workdir)})
	})

	// --- live preview: run the project's dev server in the worktree ---
	// The vibecoder's "Verify": see the app RUN, not the diff. Starts the repo's
	// dev server (detected from package.json, or a caller-supplied command),
	// scans its output for the port it bound, and returns a localhost URL the app
	// iframes. One per repo dir; reused while alive. CORS scoped to localhost.
	previews := make(map[string]*previewProc)
	var previewsMu sync.Mutex

	// --- Playwright trace viewers (one `playwright show-trace` per trace file) ---
	// The trace .zip a run_test_cases run captured is LOCAL to this daemon's box,
	// so the viewer must run here; the backend reverse-proxies it. Reused per
	// trace path while alive; same process-group kill on shutdown as previews.
	traces := make(map[string]*previewProc)
	var tracesMu sync.Mutex

	mux.HandleFunc("/editor/preview", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
			Repo    string `json:"repo"`
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		workdir := strings.TrimSpace(req.WorkDir)
		if workdir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}
		repoDir := resolvePreviewRepoDir(workdir, req.Repo)
		if info, err := os.Stat(repoDir); err != nil || !info.IsDir() {
			http.Error(w, "repo dir does not exist", http.StatusBadRequest)
			return
		}
		command := strings.TrimSpace(req.Command)
		if command == "" {
			command = detectDevCommand(repoDir)
		}
		w.Header().Set("Content-Type", "application/json")
		if command == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"needs_command": true})
			return
		}

		// Auto-install deps on a fresh worktree so the dev server can start.
		if depLog, derr := ensureDeps(repoDir); derr != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "dependency install failed",
				"log":   tailLog(depLog),
			})
			return
		}

		previewsMu.Lock()
		if p, ok := previews[repoDir]; ok && p.running() {
			port, cmd := p.port, p.command
			previewsMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url": fmt.Sprintf("http://127.0.0.1:%d/", port), "port": port, "command": cmd, "running": true,
			})
			return
		}
		pl, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			previewsMu.Unlock()
			http.Error(w, "failed to allocate a port", http.StatusInternalServerError)
			return
		}
		hintPort := pl.Addr().(*net.TCPAddr).Port
		pl.Close()
		p, err := startPreview(repoDir, command, hintPort)
		if err != nil {
			previewsMu.Unlock()
			http.Error(w, "failed to start preview: "+err.Error(), http.StatusInternalServerError)
			return
		}
		previews[repoDir] = p
		previewsMu.Unlock()

		d.logger.Info("launched preview", "repo", repoDir, "command", command, "hint_port", hintPort)
		realPort := scanPreviewPort(p, 40*time.Second)
		if realPort == 0 && !p.running() {
			previewsMu.Lock()
			delete(previews, repoDir)
			previewsMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "the dev server exited — check the command / install dependencies",
				"log":   tailLog(p.buf.String()),
			})
			return
		}
		resp := map[string]any{"command": command, "running": true}
		if realPort != 0 {
			p.port = realPort
			resp["port"] = realPort
			resp["url"] = fmt.Sprintf("http://127.0.0.1:%d/", realPort)
		} else {
			resp["port"] = hintPort
			resp["url"] = fmt.Sprintf("http://127.0.0.1:%d/", hintPort)
			resp["warning"] = "could not detect the port from output; showing the PORT hint"
			resp["log"] = tailLog(p.buf.String())
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/editor/preview/stop", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
			Repo    string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		repoDir := resolvePreviewRepoDir(strings.TrimSpace(req.WorkDir), req.Repo)
		previewsMu.Lock()
		if p, ok := previews[repoDir]; ok {
			killProcessGroup(p.cmd)
			delete(previews, repoDir)
		}
		previewsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"stopped": true})
	})

	mux.HandleFunc("/editor/preview/status", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
			Repo    string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		repoDir := resolvePreviewRepoDir(strings.TrimSpace(req.WorkDir), req.Repo)
		resp := map[string]any{"running": false, "detected": detectDevCommand(repoDir)}
		previewsMu.Lock()
		if p, ok := previews[repoDir]; ok && p.running() {
			resp["running"] = true
			// Re-scan output every poll: a slow first run can print its real
			// Local: URL after the initial scanPreviewPort window timed out,
			// leaving p.port at the (wrong) hint. Pick up the bound port here so
			// the iframe self-heals instead of pointing at a dead hint port.
			if rp := latestDevPort(p.buf.String()); rp != 0 {
				p.port = rp
			}
			resp["port"] = p.port
			resp["url"] = fmt.Sprintf("http://127.0.0.1:%d/", p.port)
			resp["command"] = p.command
		}
		previewsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// --- deterministic QA: run the project's test suite once (non-watch) and
	// return pass/fail + the terminal output. This is the visible, by-exit-code
	// QA the developer watches alongside the preview — it never touches the repo.
	mux.HandleFunc("/editor/test", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			WorkDir string `json:"workdir"`
			Repo    string `json:"repo"`
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		repoDir := resolvePreviewRepoDir(strings.TrimSpace(req.WorkDir), req.Repo)
		command := strings.TrimSpace(req.Command)
		if command == "" {
			command = detectTestCommand(repoDir)
		}
		w.Header().Set("Content-Type", "application/json")
		if command == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"needs_command": true, "detected": ""})
			return
		}
		d.logger.Info("running QA tests", "repo", repoDir, "command", command)
		out, code := runProjectTests(repoDir, command, 5*time.Minute)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"command":   command,
			"exit_code": code,
			"passed":    code == 0,
			"output":    out,
		})
	})

	// --- Playwright trace viewer: `playwright show-trace` for a captured trace ---
	// The backend calls this over 6PN (cloud) or loopback (self-host) to bring up
	// the full Playwright trace viewer for a run's trace .zip, then reverse-proxies
	// it. show-trace ships with the Playwright the box already installed for
	// run_test_cases, so no extra dependency. CORS scoped to localhost (the app).
	mux.HandleFunc("/trace/launch", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			TracePath string `json:"trace_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		tracePath := strings.TrimSpace(req.TracePath)
		if tracePath == "" {
			http.Error(w, "trace_path is required", http.StatusBadRequest)
			return
		}
		if info, err := os.Stat(tracePath); err != nil || info.IsDir() {
			http.Error(w, "trace file does not exist", http.StatusBadRequest)
			return
		}

		tracesMu.Lock()
		defer tracesMu.Unlock()

		// Reuse a still-running viewer for this trace file.
		if tp, ok := traces[tracePath]; ok && tp.running() {
			writeTracePort(w, tp.port)
			return
		}
		delete(traces, tracePath)

		host := editorBindHost()
		pl, err := net.Listen("tcp", fmt.Sprintf("%s:0", host))
		if err != nil {
			http.Error(w, "failed to allocate a port", http.StatusInternalServerError)
			return
		}
		port := pl.Addr().(*net.TCPAddr).Port
		pl.Close()

		// `playwright show-trace` serves the viewer app + the loaded trace over
		// HTTP on the given host:port. Run via a login shell so fnm/nvm-managed
		// node + the box's `npx` are on PATH (same as the preview/test paths).
		command := fmt.Sprintf("npx playwright show-trace --host %s --port %d %s", host, port, shellSingleQuote(tracePath))
		tp, err := startPreview(filepath.Dir(tracePath), command, port)
		if err != nil {
			http.Error(w, "failed to launch trace viewer: "+err.Error(), http.StatusInternalServerError)
			return
		}
		traces[tracePath] = tp
		d.logger.Info("launched trace viewer", "trace", tracePath, "port", port)

		// Wait until the viewer is actually accepting connections before telling
		// the backend the port — otherwise the reverse-proxy's first hit 502s
		// while show-trace is still unzipping/booting. If the process dies first
		// (npx/playwright missing, bad trace), surface its output.
		if !waitTraceReady(tp, host, port, 25*time.Second) {
			if !tp.running() {
				delete(traces, tracePath)
				http.Error(w, "trace viewer exited — is `playwright` installed on the box? "+tailLog(tp.buf.String()), http.StatusBadGateway)
				return
			}
			// Still booting but alive — return the port; the proxy retries.
		}
		writeTracePort(w, port)
	})

	// --- embedded browser (general browser pane: preview URLs + watch automation) ---
	bm := newBrowserManager(d.logger)
	mux.HandleFunc("/editor/browser/start", bm.handleStart)
	mux.HandleFunc("/editor/browser/stop", bm.handleStop)
	mux.HandleFunc("/editor/browser/stream", bm.handleStream)

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
		// Best-effort: stop any editors we spawned.
		editorsMu.Lock()
		for _, e := range editors {
			if e.cmd.Process != nil {
				_ = e.cmd.Process.Kill()
			}
		}
		editorsMu.Unlock()
		previewsMu.Lock()
		for _, p := range previews {
			killProcessGroup(p.cmd)
		}
		previewsMu.Unlock()
		tracesMu.Lock()
		for _, tp := range traces {
			killProcessGroup(tp.cmd)
		}
		tracesMu.Unlock()
		bm.shutdown()
	}()

	d.logger.Info("health server listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		d.logger.Warn("health server error", "error", err)
	}
}

// writeEditorURL replies with the localhost code-server URL + the raw port. The
// url is for self-host (browser hits 127.0.0.1 directly); the port lets the
// cloud backend reverse-proxy to <daemon>.internal:<port> over the private net.
func writeEditorURL(w http.ResponseWriter, port int, workdir string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"url":  fmt.Sprintf("http://127.0.0.1:%d/?folder=%s", port, url.QueryEscape(workdir)),
		"port": port,
	})
}

// writeTracePort replies with the port `playwright show-trace` bound. The
// backend maps it to a reverse-proxy token (self-host and cloud both reach the
// viewer only through the backend proxy, so no direct URL is returned here).
func writeTracePort(w http.ResponseWriter, port int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"port": port})
}

// waitTraceReady polls until the trace viewer accepts a TCP connection on the
// bound port (up to timeout), or the process exits. host may be 0.0.0.0 (the
// cloud bind); we dial loopback in that case since 0.0.0.0 isn't a connect
// target. Returns true once reachable.
func waitTraceReady(tp *previewProc, host string, port int, timeout time.Duration) bool {
	dialHost := host
	if dialHost == "0.0.0.0" || dialHost == "" {
		dialHost = "127.0.0.1"
	}
	addr := net.JoinHostPort(dialHost, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !tp.running() {
			return false
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// shellSingleQuote wraps s in single quotes for safe interpolation into a
// `sh -c` command line (the trace path could contain spaces). Any embedded
// single quote is escaped the POSIX way ('\”).
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- git-diff helpers for the /editor/changes endpoint ---

type changedFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type repoChanges struct {
	Repo   string        `json:"repo"`
	Branch string        `json:"branch"`
	Base   string        `json:"base"`
	Files  []changedFile `json:"files"`
	Diff   string        `json:"diff"`
}

// gitWorktreeChanges finds each git repo directly under the worktree and returns
// the agent's changes vs the merge-base with the default branch.
func gitWorktreeChanges(workdir string) []repoChanges {
	out := []repoChanges{}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		repo := filepath.Join(workdir, e.Name())
		if strings.TrimSpace(runGit(repo, "rev-parse", "--is-inside-work-tree")) != "true" {
			continue
		}
		base := resolveDiffBase(repo)
		branch := strings.TrimSpace(runGit(repo, "rev-parse", "--abbrev-ref", "HEAD"))
		var numstat, diff string
		if base != "" {
			numstat = runGit(repo, "diff", "--numstat", base)
			diff = runGit(repo, "diff", base)
		} else {
			numstat = runGit(repo, "diff", "--numstat")
			diff = runGit(repo, "diff")
		}
		const maxDiff = 400000
		if len(diff) > maxDiff {
			diff = diff[:maxDiff] + "\n\u2026 (diff truncated)"
		}
		out = append(out, repoChanges{
			Repo:   e.Name(),
			Branch: branch,
			Base:   base,
			Files:  parseNumstat(numstat),
			Diff:   diff,
		})
	}
	return out
}

func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func resolveDiffBase(repo string) string {
	if ref := strings.TrimSpace(runGit(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")); ref != "" {
		if b := strings.TrimSpace(runGit(repo, "merge-base", "HEAD", ref)); b != "" {
			return b
		}
	}
	for _, cand := range []string{"origin/main", "origin/master", "main", "master"} {
		if b := strings.TrimSpace(runGit(repo, "merge-base", "HEAD", cand)); b != "" {
			return b
		}
	}
	return ""
}

func parseNumstat(s string) []changedFile {
	files := []changedFile{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		files = append(files, changedFile{Path: parts[2], Additions: add, Deletions: del})
	}
	return files
}

// --- accept/reject (open-pr / discard) helpers for co-code mode ---

type prOpenResult struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	URL     string `json:"url,omitempty"`
	Created bool   `json:"created"`
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

type repoDiscardResult struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// gitReposUnder returns the paths of git work-trees directly under workdir.
func gitReposUnder(workdir string) []string {
	var repos []string
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return repos
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		repo := filepath.Join(workdir, e.Name())
		if strings.TrimSpace(runGit(repo, "rev-parse", "--is-inside-work-tree")) == "true" {
			repos = append(repos, repo)
		}
	}
	return repos
}

// runInDir runs an arbitrary command in dir, returning trimmed combined output.
func runInDir(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// openWorktreePR commits pending edits, pushes the branch, and opens (or reuses
// the existing open) GitHub PR for each repo under the worktree that is ahead of
// its base. Skips the default branch and repos with no changes.
func openWorktreePR(workdir, title, body, base string) []prOpenResult {
	out := []prOpenResult{}
	for _, repo := range gitReposUnder(workdir) {
		branch := strings.TrimSpace(runGit(repo, "rev-parse", "--abbrev-ref", "HEAD"))
		res := prOpenResult{Repo: filepath.Base(repo), Branch: branch}
		// Sprint-worktree mode: a per-task alias (sprint-wt-*) tracking the
		// SHARED origin/<sprintBranch>. Two accept models, selected by env:
		//   - AGORA_SPRINT_PR_MODE off (default): push the task's commits straight
		//     onto the integration branch (no per-task PR).
		//   - AGORA_SPRINT_PR_MODE on: open a PR FROM the task's own branch INTO
		//     the sprint branch, for the squad lead to review + merge. The sprint
		//     branch still merges to main via the sprint-end QA/deploy flow.
		if sprintBranch, ok := sprintUpstreamBranch(repo, branch); ok {
			if sprintPRModeEnabled() {
				out = append(out, openSprintPR(repo, branch, sprintBranch, title, body))
			} else {
				out = append(out, pushToSprintBranch(repo, branch, sprintBranch))
			}
			continue
		}
		defBranch := strings.TrimPrefix(strings.TrimSpace(runGit(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")), "origin/")
		if branch == "" || branch == "HEAD" || (defBranch != "" && branch == defBranch) {
			res.Skipped = "not on a feature branch"
			out = append(out, res)
			continue
		}
		// Commit any uncommitted edits (e.g. the human's live code-server edits).
		if strings.TrimSpace(runGit(repo, "status", "--porcelain")) != "" {
			_ = runGit(repo, "add", "-A")
			_, _ = runInDir(repo, "git", "commit", "-m", "Co-code changes (Agora)")
		}
		baseSHA := resolveDiffBase(repo)
		if baseSHA != "" {
			if ahead := strings.TrimSpace(runGit(repo, "rev-list", "--count", baseSHA+"..HEAD")); ahead == "" || ahead == "0" {
				res.Skipped = "no changes vs base"
				out = append(out, res)
				continue
			}
		}
		if pout, err := runInDir(repo, "git", "push", "-u", "origin", branch); err != nil {
			res.Error = "push failed: " + firstLine(pout)
			out = append(out, res)
			continue
		}
		// Reuse an existing open PR for this head branch if one exists.
		if existing, err := runInDir(repo, "gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url", "-q", ".[0].url"); err == nil && strings.HasPrefix(existing, "http") {
			res.URL = existing
			out = append(out, res)
			continue
		}
		args := []string{"pr", "create", "--head", branch}
		if base != "" {
			args = append(args, "--base", base)
		}
		if title != "" {
			args = append(args, "--title", title, "--body", body)
		} else {
			args = append(args, "--fill")
		}
		cout, err := runInDir(repo, "gh", args...)
		if err != nil {
			res.Error = "gh pr create failed: " + firstLine(cout)
			out = append(out, res)
			continue
		}
		res.URL = lastURL(cout)
		res.Created = true
		out = append(out, res)
	}
	return out
}

// sprintUpstreamBranch returns the SHARED sprint branch a per-task alias tracks,
// when this repo is in sprint-worktree mode. The alias is sprint-wt-<taskID> and
// its upstream is origin/<sprintBranch> (set by ensureSprintBranch); the shared
// branch name is what we push to. ok=false for any non-sprint worktree.
func sprintUpstreamBranch(repo, branch string) (string, bool) {
	if !strings.HasPrefix(branch, "sprint-wt-") {
		return "", false
	}
	up := strings.TrimSpace(runGit(repo, "rev-parse", "--abbrev-ref", branch+"@{upstream}"))
	up = strings.TrimPrefix(up, "origin/")
	if up == "" {
		return "", false
	}
	return up, true
}

// pushToSprintBranch commits pending edits and pushes the task's commits onto the
// SHARED sprint branch. On a non-fast-forward (a teammate pushed first) it
// rebases onto the updated tip and retries once — last writer rebases, never
// force-push (which would clobber teammates). A rebase conflict is surfaced as an
// error so the human/agent resolves it; the daemon never drops or force-pushes.
func pushToSprintBranch(repo, alias, sprintBranch string) prOpenResult {
	res := prOpenResult{Repo: filepath.Base(repo), Branch: sprintBranch}
	// Commit any uncommitted edits (e.g. the human's live code-server edits).
	if strings.TrimSpace(runGit(repo, "status", "--porcelain")) != "" {
		_ = runGit(repo, "add", "-A")
		_, _ = runInDir(repo, "git", "commit", "-m", "Co-code changes (Agora)")
	}
	if baseSHA := strings.TrimSpace(runGit(repo, "merge-base", "HEAD", "origin/"+sprintBranch)); baseSHA != "" {
		if ahead := strings.TrimSpace(runGit(repo, "rev-list", "--count", baseSHA+"..HEAD")); ahead == "" || ahead == "0" {
			res.Skipped = "no changes vs sprint branch"
			return res
		}
	}
	push := func() (string, error) { return runInDir(repo, "git", "push", "origin", "HEAD:"+sprintBranch) }
	if pout, err := push(); err != nil {
		// Non-fast-forward (or other reject): rebase onto the shared tip, retry once.
		_ = runGit(repo, "fetch", "origin", sprintBranch)
		_ = runGit(repo, "rebase", "origin/"+sprintBranch)
		if strings.TrimSpace(runGit(repo, "rev-parse", "--abbrev-ref", "HEAD")) != alias {
			_ = runGit(repo, "rebase", "--abort")
			res.Error = "push rejected; rebase onto origin/" + sprintBranch + " conflicted (resolve before accept): " + firstLine(pout)
			return res
		}
		if pout2, err2 := push(); err2 != nil {
			res.Error = "push failed after rebase: " + firstLine(pout2)
			return res
		}
	}
	// Landed on the integration branch — no PR is opened for a sprint commit.
	return res
}

// sprintPRModeEnabled gates the per-task-PR-into-the-sprint-branch model (Phase 1
// of auto sprint review): a sprint task opens a PR from its own branch INTO the
// sprint branch — for the squad lead to review + merge — instead of pushing its
// commits straight onto the shared branch. Default OFF, so the direct-push model
// (pushToSprintBranch) stays the default and the switch is fully reversible.
func sprintPRModeEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGORA_SPRINT_PR_MODE"))
	return v == "1" || strings.EqualFold(v, "true")
}

// openSprintPR opens (or reuses) a GitHub PR from the task's per-task sprint alias
// INTO the shared sprint branch. Unlike pushToSprintBranch it never writes to the
// shared branch: it pushes the alias as its OWN remote head (WITHOUT -u, so the
// alias keeps tracking origin/<sprintBranch> for pull-before-work + sprint
// detection) and targets --base <sprintBranch>. Idempotent: a re-accept
// force-updates the head (the task owns that branch) and reuses the open PR.
func openSprintPR(repo, alias, sprintBranch, title, body string) prOpenResult {
	res := prOpenResult{Repo: filepath.Base(repo), Branch: alias}
	// Commit any uncommitted edits (e.g. the human's live code-server edits).
	if strings.TrimSpace(runGit(repo, "status", "--porcelain")) != "" {
		_ = runGit(repo, "add", "-A")
		_, _ = runInDir(repo, "git", "commit", "-m", "Co-code changes (Agora)")
	}
	// Nothing to review if the alias is not ahead of the sprint branch.
	if baseSHA := strings.TrimSpace(runGit(repo, "merge-base", "HEAD", "origin/"+sprintBranch)); baseSHA != "" {
		if ahead := strings.TrimSpace(runGit(repo, "rev-list", "--count", baseSHA+"..HEAD")); ahead == "" || ahead == "0" {
			res.Skipped = "no changes vs sprint branch"
			return res
		}
	}
	// Push the alias as its own remote head. force-with-lease covers a re-accept
	// after the pull-before-work rebase rewrote the alias — safe because this
	// branch is the task's own, never the shared sprint branch.
	if pout, err := runInDir(repo, "git", "push", "--force-with-lease", "origin", "HEAD:"+alias); err != nil {
		res.Error = "push failed: " + firstLine(pout)
		return res
	}
	// Reuse an existing open PR for this head branch if one exists.
	if existing, err := runInDir(repo, "gh", "pr", "list", "--head", alias, "--state", "open", "--json", "url", "-q", ".[0].url"); err == nil && strings.HasPrefix(existing, "http") {
		res.URL = existing
		return res
	}
	args := []string{"pr", "create", "--head", alias, "--base", sprintBranch}
	if title != "" {
		args = append(args, "--title", title, "--body", body)
	} else {
		args = append(args, "--fill")
	}
	cout, err := runInDir(repo, "gh", args...)
	if err != nil {
		res.Error = "gh pr create failed: " + firstLine(cout)
		return res
	}
	res.URL = lastURL(cout)
	res.Created = true
	return res
}

// discardWorktreeChanges resets each repo under the worktree to its base and
// removes untracked files. Local only — pushed commits stay on the remote.
func discardWorktreeChanges(workdir string) []repoDiscardResult {
	out := []repoDiscardResult{}
	for _, repo := range gitReposUnder(workdir) {
		res := repoDiscardResult{Repo: filepath.Base(repo)}
		base := resolveDiffBase(repo)
		if base == "" {
			res.Error = "could not resolve base"
			out = append(out, res)
			continue
		}
		if rout, err := runInDir(repo, "git", "reset", "--hard", base); err != nil {
			res.Error = "reset failed: " + firstLine(rout)
			out = append(out, res)
			continue
		}
		_, _ = runInDir(repo, "git", "clean", "-fd")
		res.OK = true
		out = append(out, res)
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// lastURL returns the last http(s) token in s — gh prints the PR URL last.
func lastURL(s string) string {
	fields := strings.Fields(s)
	for i := len(fields) - 1; i >= 0; i-- {
		if strings.HasPrefix(fields[i], "http://") || strings.HasPrefix(fields[i], "https://") {
			return fields[i]
		}
	}
	return strings.TrimSpace(s)
}

// --- live preview (dev server) helpers for co-code mode ---

type previewProc struct {
	port    int
	cmd     *exec.Cmd
	command string
	buf     *syncBuffer
	done    chan struct{}
}

// running reports whether the dev-server process is still alive.
func (p *previewProc) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// syncBuffer is a mutex-guarded buffer the preview's stdout+stderr stream into,
// so we can scan the running dev server's output for the port it bound.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep up to 1MB so a chatty first run (deps re-optimize + HMR) can't reset
	// the bound-port line out of the buffer before the scanner reads it.
	if s.buf.Len() > 1024*1024 {
		s.buf.Reset()
	}
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

var devURLRe = regexp.MustCompile(`(?:https?://)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)`)

// ansiRe strips terminal color/escape sequences. Vite (and others) print the
// server URL with color codes *between* "localhost:" and the port digits — e.g.
// "localhost:\x1b[1m5176\x1b[22m/" — which would otherwise break devURLRe. The
// daemon sets NO_COLOR/FORCE_COLOR, but Vite v8 ignores them, so strip to be safe.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// resolvePreviewRepoDir returns the directory to run the dev server in: the
// named repo under the worktree, the sole git repo if there's exactly one, or
// the worktree itself.
func resolvePreviewRepoDir(workdir, repo string) string {
	if r := strings.TrimSpace(repo); r != "" {
		return filepath.Join(workdir, r)
	}
	if repos := gitReposUnder(workdir); len(repos) == 1 {
		return repos[0]
	}
	return workdir
}

// detectDevCommand returns a best-guess dev-server command, or "". Covers the
// Node ecosystem (the common vibecoder web stack) via package.json + lockfile.
func detectDevCommand(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	script := ""
	for _, cand := range []string{"dev", "start", "serve"} {
		if _, ok := pkg.Scripts[cand]; ok {
			script = cand
			break
		}
	}
	if script == "" {
		return ""
	}
	pm := "npm"
	if _, err := os.Stat(filepath.Join(repoDir, "pnpm-lock.yaml")); err == nil {
		pm = "pnpm"
	} else if _, err := os.Stat(filepath.Join(repoDir, "yarn.lock")); err == nil {
		pm = "yarn"
	}
	return pm + " run " + script
}

// detectTestCommand resolves the project's test command from package.json's
// "test" script. Returns "" when there is no test script (the QA pane then shows
// "no tests configured" instead of failing). CI=1 in runProjectTests forces the
// runner non-watch so it exits with a verdict.
func detectTestCommand(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	if _, ok := pkg.Scripts["test"]; !ok {
		return ""
	}
	pm := "npm"
	if _, err := os.Stat(filepath.Join(repoDir, "pnpm-lock.yaml")); err == nil {
		pm = "pnpm"
	} else if _, err := os.Stat(filepath.Join(repoDir, "yarn.lock")); err == nil {
		pm = "yarn"
	}
	return pm + " run test"
}

// runProjectTests runs the test command once (CI=1 → non-watch, so vitest/jest
// exit instead of hanging in watch mode) and returns the combined output tail +
// exit code. exit 0 = passed; non-zero = failed; -1 = timed out / spawn error.
func runProjectTests(repoDir, command string, timeout time.Duration) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.CommandContext(ctx, shell, "-lc", command)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "CI=1", "FORCE_COLOR=0", "NO_COLOR=1", "BROWSER=none")
	setProcessGroup(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return tailLog(ansiRe.ReplaceAllString(string(out), "")) + "\n\n[timed out]", -1
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return tailLog(ansiRe.ReplaceAllString(string(out), "")), code
}

// ensureDeps installs Node dependencies when node_modules is missing, so the
// dev server can actually start on a fresh worktree. No-op for non-Node repos or
// when deps already exist; the package manager is picked from the lockfile. Runs
// via a login shell so fnm/nvm-managed node is on PATH.
func ensureDeps(repoDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(repoDir, "package.json")); err != nil {
		return "", nil
	}
	if _, err := os.Stat(filepath.Join(repoDir, "node_modules")); err == nil {
		return "", nil
	}
	install := "npm install"
	if _, err := os.Stat(filepath.Join(repoDir, "pnpm-lock.yaml")); err == nil {
		install = "pnpm install"
	} else if _, err := os.Stat(filepath.Join(repoDir, "yarn.lock")); err == nil {
		install = "yarn install"
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-lc", install)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// startPreview runs command in repoDir under a login shell (so fnm/nvm-managed
// node is on PATH), in its own process group so the whole tree can be killed.
func startPreview(repoDir, command string, hintPort int) (*previewProc, error) {
	buf := &syncBuffer{}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-lc", command)
	cmd.Dir = repoDir
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", hintPort),
		"HOST=127.0.0.1",
		"BROWSER=none",
		"FORCE_COLOR=0",
		"NO_COLOR=1",
	)
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pp := &previewProc{port: hintPort, cmd: cmd, command: command, buf: buf, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(pp.done)
	}()
	return pp, nil
}

// latestDevPort returns the most recent localhost:<port> the dev server printed,
// or 0 if none yet. It takes the LAST match so a Vite "Port 5173 is in use,
// trying another one… → Local: http://localhost:5176/" sequence resolves to the
// port the server actually bound (5176), never an earlier in-use one.
func latestDevPort(out string) int {
	out = ansiRe.ReplaceAllString(out, "")
	ms := devURLRe.FindAllStringSubmatch(out, -1)
	if len(ms) == 0 {
		return 0
	}
	if port, err := strconv.Atoi(ms[len(ms)-1][1]); err == nil {
		return port
	}
	return 0
}

// scanPreviewPort polls the captured output for a localhost URL and returns the
// port it advertises, or 0 if none appears before the deadline / the dev server
// exits early. When this times out (a slow first run that re-optimizes deps and
// hunts past in-use ports can print its Local: URL late), /editor/preview/status
// re-scans on every poll, so the UI still recovers the real port.
func scanPreviewPort(p *previewProc, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if port := latestDevPort(p.buf.String()); port != 0 {
			return port
		}
		if !p.running() {
			return 0
		}
		time.Sleep(300 * time.Millisecond)
	}
	return 0
}

// killProcessGroup lives in procgroup_unix.go / procgroup_windows.go so the
// CLI (which embeds this package) cross-compiles for Windows, where the Unix
// process-group syscalls don't exist.

// tailLog returns the last ~2KB of captured output for an error response.
func tailLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}

// editorBindHost is the host code-server binds to. Self-host = 127.0.0.1
// (loopback). Cloud sets AGORA_EDITOR_BIND=0.0.0.0 so the backend can reach the
// spawned code-server over the fly private network (6PN); access is gated by
// the backend proxy + the org-private 6PN, not a public listener.
func editorBindHost() string {
	if v := strings.TrimSpace(os.Getenv("AGORA_EDITOR_BIND")); v != "" {
		return v
	}
	return "127.0.0.1"
}

// editorUserDataDir returns a stable, isolated code-server --user-data-dir for a
// (user, workdir) key, so concurrent editors never share a VS Code profile lock.
// Hashed to keep the path short and filesystem-safe.
func editorUserDataDir(key string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	dir := filepath.Join(home, ".agora", "code-server", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Seed the editor profile on first launch. Two layers (write-if-absent so a
	// human's later in-editor edits persist across relaunches):
	//
	//   User/settings.json + User/keybindings.json — copied VERBATIM from the
	//   host's own VS Code profile when one exists, so the co-code editor opens
	//   with the reviewer's familiar theme/keybinds/settings. Verbatim because
	//   VS Code settings are JSONC (comments, trailing commas) — parsing with
	//   encoding/json would corrupt them. No host profile → minimal "{}".
	//
	//   Machine/settings.json — our co-code invariants. Machine scope overrides
	//   User scope, so they hold regardless of what the copied profile says: no
	//   workspace-trust prompt (the agent authored its own isolated branch — it
	//   is the author, the human is the reviewer), no Getting-Started tab
	//   covering the code, no telemetry.
	userDir := filepath.Join(dir, "User")
	settingsPath := filepath.Join(userDir, "settings.json")
	if _, statErr := os.Stat(settingsPath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(userDir, 0o700); mkErr == nil {
			seeded := false
			if hostDir, ok := hostVSCodeUserDir(); ok {
				if b, rerr := os.ReadFile(filepath.Join(hostDir, "settings.json")); rerr == nil {
					seeded = os.WriteFile(settingsPath, b, 0o600) == nil
				}
				if b, rerr := os.ReadFile(filepath.Join(hostDir, "keybindings.json")); rerr == nil {
					_ = os.WriteFile(filepath.Join(userDir, "keybindings.json"), b, 0o600)
				}
			}
			if !seeded {
				_ = os.WriteFile(settingsPath, []byte("{}\n"), 0o600)
			}
			// Inherit auth/session state from the newest sibling profile.
			// code-server keeps SecretStorage (e.g. the "Sign in with GitHub"
			// session) in User/globalStorage/state.vscdb INSIDE the per-
			// (user, worktree) profile — so without this, every new worktree
			// editor demanded a fresh sign-in. Copying the newest sibling's
			// state.vscdb means: sign in once, every later editor inherits.
			inheritEditorGlobalStorage(filepath.Dir(dir), dir, userDir)
		}
	}
	machineDir := filepath.Join(dir, "Machine")
	machinePath := filepath.Join(machineDir, "settings.json")
	if _, statErr := os.Stat(machinePath); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(machineDir, 0o700); mkErr == nil {
			_ = os.WriteFile(machinePath, []byte(`{
  "security.workspace.trust.enabled": false,
  "workbench.startupEditor": "none",
  "workbench.tips.enabled": false,
  "telemetry.telemetryLevel": "off"
}
`), 0o600)
		}
	}
	return dir, nil
}

// inheritEditorGlobalStorage copies User/globalStorage/state.vscdb from the
// most recently modified sibling editor profile into a NEWLY created one, so a
// GitHub (or any extension) sign-in done once carries into every later
// worktree's editor. Best-effort: no sibling with auth state → fresh profile,
// exactly as before.
func inheritEditorGlobalStorage(root, selfDir, userDir string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var newest string
	var newestMod int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(root, e.Name())
		if cand == selfDir {
			continue
		}
		db := filepath.Join(cand, "User", "globalStorage", "state.vscdb")
		info, serr := os.Stat(db)
		if serr != nil {
			continue
		}
		if m := info.ModTime().UnixNano(); m > newestMod {
			newest, newestMod = db, m
		}
	}
	if newest == "" {
		return
	}
	b, err := os.ReadFile(newest)
	if err != nil {
		return
	}
	gsDir := filepath.Join(userDir, "globalStorage")
	if err := os.MkdirAll(gsDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(gsDir, "state.vscdb"), b, 0o600)
}

// hostVSCodeUserDir locates the host machine's own VS Code user profile so the
// co-code editor can open with the reviewer's familiar settings. Self-host
// daemons run on the developer's machine where this exists; cloud daemons
// simply won't find one (ok=false → minimal seed).
func hostVSCodeUserDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	candidates := []string{
		filepath.Join(home, "Library", "Application Support", "Code", "User"), // macOS
		filepath.Join(home, ".config", "Code", "User"),                        // Linux
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "settings.json")); err == nil {
			return c, true
		}
	}
	return "", false
}
