package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
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

	"github.com/jamshidtulaganov/agora/server/internal/daemon/repocache"
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
		host = v // cloud exposes the capability-gated runtime surface over 6PN
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

	// Preview reverse-proxy. The historical path remains wire-compatible with
	// existing agents, but only daemon-tracked preview processes are reachable.
	mux.HandleFunc("/editor/local/", previewLocalProxyHandler)

	// --- live preview: run the project's dev server in the worktree ---
	// The vibecoder's "Verify": see the app RUN, not the diff. Starts the repo's
	// dev server (detected from package.json, or a caller-supplied command),
	// scans its output for the port it bound, and returns a localhost URL the app
	// iframes. One per repo dir; reused while alive. CORS scoped to localhost.
	// (The previews registry lives at package level so previewLocalProxyHandler
	// can proxy a running dev-server's port for cloud mode.)

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
				"url": previewURL(port), "port": port, "command": cmd, "running": true,
				"proxy_path": fmt.Sprintf("/editor/local/%d/", port),
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
			resp["url"] = previewURL(realPort)
			resp["proxy_path"] = fmt.Sprintf("/editor/local/%d/", realPort)
		} else {
			resp["port"] = hintPort
			resp["url"] = previewURL(hintPort)
			resp["proxy_path"] = fmt.Sprintf("/editor/local/%d/", hintPort)
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
			resp["url"] = previewURL(p.port)
			resp["proxy_path"] = fmt.Sprintf("/editor/local/%d/", p.port)
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

	// --- folder picker: list this machine's directories for the web UI ---
	// A browser cannot OS-pick a folder on THIS machine (and <input type="file">
	// never yields an absolute path), so the web "Add local folder" flow walks
	// the filesystem through here instead of making the human type a path.
	// Read-only and grants nothing: see fs_browse.go for the browsable-root
	// gates. Reached via the backend's /browser/proxy/{token} (cloud) or dialed
	// directly on loopback (self-host); CORS scoped to localhost like the rest.
	mux.HandleFunc("/editor/fs/list", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		hidden := r.URL.Query().Get("hidden")
		result, status, err := browseLocalDir(r.URL.Query().Get("path"), hidden == "1" || hidden == "true")
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
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

		host := runtimeProcessBindHost()
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
		// Reap the oldest viewer(s) BEFORE registering this one, capping total
		// live viewers at maxTraceViewers — nothing ever explicitly "closes" a
		// trace viewer from the frontend, so without this the process count
		// grows unbounded across a QA session's worth of "view trace" clicks.
		pruneOldTraceViewers(maxTraceViewers - 1)
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

	// --- trace-viewer reverse-proxy: /trace/local/{port}/* → loopback:{port} ---
	// Same shape as /editor/local/{port}/*: the backend reaches a spawned
	// `playwright show-trace` THROUGH this health listener instead of dialing
	// the viewer port directly, because the viewer binds the DAEMON HOST's
	// loopback — unreachable from a containerized (self-host Docker) or
	// remote-node (cloud) backend that tries to dial it directly. Only ports of
	// live, daemon-tracked trace viewers are proxied.
	mux.HandleFunc("/trace/local/", traceLocalProxyHandler)

	// --- embedded browser (general browser pane: preview URLs + watch automation) ---
	d.registerArtifactHandlers(mux)

	bm := newBrowserManager(d.logger)
	mux.HandleFunc("/editor/browser/start", bm.handleStart)
	mux.HandleFunc("/editor/browser/stop", bm.handleStop)
	mux.HandleFunc("/editor/browser/stream", bm.handleStream)

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
		d.shutdownArtifactPreviews()
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

// previews registers running dev-server processes by repo dir. Package level
// so the local proxy can validate a preview port before forwarding it through
// the cloud backend proxy chain.
var (
	previews   = make(map[string]*previewProc)
	previewsMu sync.Mutex
)

// traces registers running `playwright show-trace` viewers by trace .zip path.
// Package level so traceLocalProxyHandler can gate its per-port reverse proxy
// on live instances — mirrors previews above. The backend reaches a
// tracked viewer THROUGH this health listener (/trace/local/{port}), never by
// dialing the viewer's port directly: the viewer binds the DAEMON HOST's
// loopback, which is unreachable from a containerized (self-host Docker) or
// remote-node (cloud) backend.
var (
	traces   = make(map[string]*previewProc)
	tracesMu sync.Mutex
)

// maxTraceViewers caps how many `playwright show-trace` processes a daemon
// keeps alive at once. Each viewer unzips a trace + serves it indefinitely
// with no natural end-of-life signal (no TTL, no explicit "close" from the
// frontend), so repeated "view trace" clicks across issues/runs would
// otherwise leak processes forever. Small and simple on purpose: oldest is
// killed to make room for a newly launched viewer past the cap.
const maxTraceViewers = 3

// pruneOldTraceViewers keeps at most maxKeep live entries in traces, killing
// the oldest (by startedAt) first. Also drops any entry that already exited on
// its own, so a crashed viewer's slot doesn't count against the cap forever.
// Called right before a newly launched viewer is added, with maxKeep one less
// than the cap so the total stays at maxTraceViewers after the insert.
// Caller must hold tracesMu.
func pruneOldTraceViewers(maxKeep int) {
	for {
		var oldestPath string
		var oldest *previewProc
		count := 0
		for path, tp := range traces {
			if !tp.running() {
				delete(traces, path)
				continue
			}
			count++
			if oldest == nil || tp.startedAt.Before(oldest.startedAt) {
				oldestPath, oldest = path, tp
			}
		}
		if count <= maxKeep || oldest == nil {
			return
		}
		killProcessGroup(oldest.cmd)
		delete(traces, oldestPath)
	}
}

// loopbackHostPort returns "host:port" (IPv6 bracketed) for the loopback
// interface actually accepting TCP on port. Dev servers on macOS frequently
// bind IPv6 [::1] ONLY — Node/Vite resolve "localhost" to ::1 first — so the
// fixed 127.0.0.1 address the daemon used to hand back (and reverse-proxy to)
// was refused with ERR_CONNECTION_REFUSED. Probe IPv4 first (most clients
// prefer it), then IPv6; fall back to 127.0.0.1 when neither answers yet (the
// server is still booting — /editor/preview/status re-probes on the next poll).
func loopbackHostPort(port int) string {
	ps := strconv.Itoa(port)
	for _, h := range []string{"127.0.0.1", "::1"} {
		hp := net.JoinHostPort(h, ps)
		if c, err := net.DialTimeout("tcp", hp, 300*time.Millisecond); err == nil {
			_ = c.Close()
			return hp
		}
	}
	return net.JoinHostPort("127.0.0.1", ps)
}

// previewURL builds the browser-facing dev-server URL for a bound port, using
// whichever loopback stack is actually listening (see loopbackHostPort).
func previewURL(port int) string { return "http://" + loopbackHostPort(port) + "/" }

// previewLocalProxyHandler serves /editor/local/{port}/* for daemon-tracked
// preview processes only. The legacy route name is kept for agent protocol
// compatibility; it is not an editor or an open loopback proxy.
func previewLocalProxyHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/editor/local/")
	portStr, tail, _ := strings.Cut(rest, "/")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		http.Error(w, "invalid preview port", http.StatusBadRequest)
		return
	}
	alive := false
	previewsMu.Lock()
	for _, p := range previews {
		if p.port == port && p.running() {
			alive = true
			break
		}
	}
	previewsMu.Unlock()
	if !alive {
		http.Error(w, "no live preview on this port", http.StatusNotFound)
		return
	}
	target := &url.URL{Scheme: "http", Host: loopbackHostPort(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.URL.Path = "/" + tail
		req.Host = target.Host
	}
	proxy.ServeHTTP(w, r)
}

// traceLocalProxyHandler serves /trace/local/{port}/* by reverse-proxying to
// the `playwright show-trace` viewer bound on loopback:{port}. Gated to ports
// of live, daemon-tracked trace viewers (the traces registry) — never an open
// proxy into the machine. Mirrors previewLocalProxyHandler's shape exactly.
// WebSocket upgrades pass through (httputil.ReverseProxy handles Upgrade
// natively) and show-trace's relative redirect (Location: ./trace/index.html)
// resolves fine under this prefix since we never rewrite Location headers.
func traceLocalProxyHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/trace/local/")
	portStr, tail, _ := strings.Cut(rest, "/")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		http.Error(w, "invalid trace port", http.StatusBadRequest)
		return
	}
	tracesMu.Lock()
	alive := false
	for _, tp := range traces {
		if tp.port == port && tp.running() {
			alive = true
			break
		}
	}
	tracesMu.Unlock()
	if !alive {
		http.Error(w, "no live trace viewer on this port", http.StatusNotFound)
		return
	}
	target := &url.URL{Scheme: "http", Host: loopbackHostPort(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req)
		req.URL.Path = "/" + tail
		req.Host = target.Host
	}
	proxy.ServeHTTP(w, r)
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
func waitTCPReady(running func() bool, host string, port int, timeout time.Duration) bool {
	dialHost := host
	if dialHost == "0.0.0.0" || dialHost == "" {
		dialHost = "127.0.0.1"
	}
	addr := net.JoinHostPort(dialHost, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if running != nil && !running() {
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

func waitTraceReady(tp *previewProc, host string, port int, timeout time.Duration) bool {
	return waitTCPReady(tp.running, host, port, timeout)
}

// shellSingleQuote wraps s in single quotes for safe interpolation into a
// `sh -c` command line (the trace path could contain spaces). Any embedded
// single quote is escaped the POSIX way ('\”).
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- artifact diff helpers ---

type changedFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
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

// --- sprint branch landing helpers ---

type prOpenResult struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	URL     string `json:"url,omitempty"`
	Created bool   `json:"created"`
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
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
	// Commit any uncommitted task edits before updating the sprint branch.
	if strings.TrimSpace(runGit(repo, "status", "--porcelain")) != "" {
		_ = runGit(repo, "add", "-A")
		_, _ = runInDir(repo, "git", "commit", "-m", "Sprint task changes (Agora)")
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

// openSprintPR opens (or reuses) a GitHub PR from the task's per-task sprint alias
// INTO the shared sprint branch. Unlike pushToSprintBranch it never writes to the
// shared branch: it pushes the alias as its OWN remote head (WITHOUT -u, so the
// alias keeps tracking origin/<sprintBranch> for pull-before-work + sprint
// detection) and targets --base <sprintBranch>. Idempotent: a re-accept
// force-updates the head (the task owns that branch) and reuses the open PR.
func openSprintPR(repo, alias, sprintBranch, title, body string) prOpenResult {
	res := prOpenResult{Repo: filepath.Base(repo), Branch: alias}
	// Commit any uncommitted task edits before opening the sprint PR.
	if strings.TrimSpace(runGit(repo, "status", "--porcelain")) != "" {
		_ = runGit(repo, "add", "-A")
		_, _ = runInDir(repo, "git", "commit", "-m", "Sprint task changes (Agora)")
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

// --- live preview (dev server) helpers ---

type previewProc struct {
	port    int
	cmd     *exec.Cmd
	command string
	buf     *syncBuffer
	done    chan struct{}
	// startedAt is used by pruneOldTraceViewers to identify the oldest live
	// trace viewer to reap when the concurrent cap is exceeded. Unused by the
	// preview (dev-server) registry.
	startedAt time.Time
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

// detectDevCommand / detectTestCommand live in detect.go (Node → Makefile →
// PHP tier chain).

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

// ensureDeps installs the repo's dependencies when its dep dir (node_modules /
// vendor) is missing, so the dev server can actually start on a fresh worktree.
// Provider detection is delegated to detectSprintDepProvider (Node + Composer)
// and the install runs via runDepInstall (login shell so fnm/nvm-managed node
// is on PATH; a package var so tests can stub it). No-op for repos without a
// dep provider or when deps already exist.
func ensureDeps(repoDir string) (string, error) {
	prov, ok := detectSprintDepProvider(repoDir)
	if !ok || dirPopulated(filepath.Join(repoDir, prov.depDir)) {
		return "", nil
	}
	return runDepInstall(repoDir, prov.installCmd)
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
	pp := &previewProc{port: hintPort, cmd: cmd, command: command, buf: buf, done: make(chan struct{}), startedAt: time.Now()}
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

// runtimeProcessBindHost is used for daemon-spawned observability helpers such
// as Playwright trace viewer. The historical environment variable name remains
// supported for deployment compatibility.
func runtimeProcessBindHost() string {
	if v := strings.TrimSpace(os.Getenv("AGORA_EDITOR_BIND")); v != "" {
		return v
	}
	return "127.0.0.1"
}
