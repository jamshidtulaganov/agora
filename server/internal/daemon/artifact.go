package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ArtifactRepoRef struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch,omitempty"`
	BaseSHA     string `json:"base_sha"`
	HeadSHA     string `json:"head_sha"`
	MergeStatus string `json:"merge_status"`
}

type ArtifactCapabilityGrant struct {
	ID          string            `json:"id"`
	ArtifactID  string            `json:"artifact_id"`
	Purpose     string            `json:"purpose"`
	WorkspaceID string            `json:"workspace_id"`
	IssueID     string            `json:"issue_id"`
	RunID       string            `json:"run_id"`
	StepID      string            `json:"step_id"`
	RuntimeID   string            `json:"runtime_id,omitempty"`
	DaemonID    string            `json:"daemon_id,omitempty"`
	SourceRoot  string            `json:"source_root"`
	Repos       []ArtifactRepoRef `json:"repos"`
	// Live: the grant points at a local_directory's LIVE working tree — no
	// orchestration step, no frozen base/head SHAs. changes/file read the
	// folder's current (possibly uncommitted) state; preview proxies to
	// PreviewURL (the developer's own dev server) instead of spawning one.
	Live       bool      `json:"live,omitempty"`
	PreviewURL string    `json:"preview_url,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type artifactCapabilityRequest struct {
	Capability string `json:"capability"`
	Repo       string `json:"repo,omitempty"`
	Path       string `json:"path,omitempty"`
}

func artifactCORS(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (d *Daemon) artifactGrant(w http.ResponseWriter, r *http.Request, purpose string) (ArtifactCapabilityGrant, artifactCapabilityRequest, bool) {
	var req artifactCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Capability) == "" {
		http.Error(w, "capability is required", http.StatusBadRequest)
		return ArtifactCapabilityGrant{}, req, false
	}
	grant, err := d.client.VerifyArtifactCapability(r.Context(), strings.TrimSpace(req.Capability), purpose)
	if err != nil {
		http.Error(w, "invalid or expired artifact capability", http.StatusForbidden)
		return ArtifactCapabilityGrant{}, req, false
	}
	if grant.Purpose != purpose || grant.ArtifactID == "" || grant.SourceRoot == "" || len(grant.Repos) == 0 {
		http.Error(w, "artifact capability is incomplete", http.StatusForbidden)
		return ArtifactCapabilityGrant{}, req, false
	}
	return grant, req, true
}

func artifactSelectedRepo(grant ArtifactCapabilityGrant, requested string) (ArtifactRepoRef, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" && len(grant.Repos) == 1 {
		return grant.Repos[0], nil
	}
	for _, ref := range grant.Repos {
		if ref.Repo == requested {
			return ref, nil
		}
	}
	if requested == "" {
		return ArtifactRepoRef{}, fmt.Errorf("repo is required for a multi-repository artifact")
	}
	return ArtifactRepoRef{}, fmt.Errorf("repo is not part of this artifact")
}

func artifactRepoPath(ctx context.Context, grant ArtifactCapabilityGrant, ref ArtifactRepoRef) (string, error) {
	root := filepath.Clean(strings.TrimSpace(grant.SourceRoot))
	if root == "." || root == "" {
		return "", fmt.Errorf("artifact source is unavailable")
	}
	if len(grant.Repos) == 1 && isGitWorkTree(ctx, root) {
		return root, nil
	}
	if filepath.Base(ref.Repo) != ref.Repo || ref.Repo == "." || ref.Repo == ".." {
		return "", fmt.Errorf("invalid repository name")
	}
	repo := filepath.Join(root, ref.Repo)
	if !isGitWorkTree(ctx, repo) {
		return "", fmt.Errorf("artifact repository is unavailable")
	}
	return repo, nil
}

func artifactGitBytes(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

type artifactRepoChanges struct {
	Repo      string        `json:"repo"`
	BaseSHA   string        `json:"base_sha"`
	HeadSHA   string        `json:"head_sha"`
	Files     []changedFile `json:"files"`
	Tree      []string      `json:"tree"`
	Diff      string        `json:"diff"`
	Truncated bool          `json:"truncated"`
}

func artifactGitLines(output []byte) []string {
	raw := strings.TrimSuffix(string(output), "\n")
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, "\n")
}

func artifactChanges(ctx context.Context, grant ArtifactCapabilityGrant) ([]artifactRepoChanges, error) {
	result := make([]artifactRepoChanges, 0, len(grant.Repos))
	for _, ref := range grant.Repos {
		repo, err := artifactRepoPath(ctx, grant, ref)
		if err != nil {
			return nil, err
		}
		if _, err := artifactGitBytes(ctx, repo, "cat-file", "-e", ref.BaseSHA+"^{commit}"); err != nil {
			return nil, fmt.Errorf("artifact base commit is unavailable for %s", ref.Repo)
		}
		if _, err := artifactGitBytes(ctx, repo, "cat-file", "-e", ref.HeadSHA+"^{commit}"); err != nil {
			return nil, fmt.Errorf("artifact head commit is unavailable for %s", ref.Repo)
		}
		numstat, err := artifactGitBytes(ctx, repo, "diff", "--no-ext-diff", "--numstat", ref.BaseSHA, ref.HeadSHA, "--")
		if err != nil {
			return nil, fmt.Errorf("read artifact diff stats for %s: %w", ref.Repo, err)
		}
		diffBytes, err := artifactGitBytes(ctx, repo, "diff", "--no-ext-diff", "--no-color", ref.BaseSHA, ref.HeadSHA, "--")
		if err != nil {
			return nil, fmt.Errorf("read artifact diff for %s: %w", ref.Repo, err)
		}
		truncated := false
		const maxDiff = 400_000
		if len(diffBytes) > maxDiff {
			diffBytes = append(diffBytes[:maxDiff], []byte("\n… (diff truncated)")...)
			truncated = true
		}
		treeBytes, err := artifactGitBytes(ctx, repo, "ls-tree", "-r", "--name-only", ref.HeadSHA)
		if err != nil {
			return nil, fmt.Errorf("read artifact tree for %s: %w", ref.Repo, err)
		}
		tree := artifactGitLines(treeBytes)
		if len(tree) > 20_000 {
			tree = tree[:20_000]
		}
		result = append(result, artifactRepoChanges{
			Repo: ref.Repo, BaseSHA: ref.BaseSHA, HeadSHA: ref.HeadSHA,
			Files: parseNumstat(string(numstat)), Tree: tree, Diff: string(diffBytes), Truncated: truncated,
		})
	}
	return result, nil
}

func validArtifactFilePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.ContainsRune(path, '\x00') {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

type artifactFileResponse struct {
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	HeadSHA   string `json:"head_sha"`
	Content   string `json:"content,omitempty"`
	Encoding  string `json:"encoding"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

func artifactFile(ctx context.Context, grant ArtifactCapabilityGrant, requestedRepo, path string) (artifactFileResponse, error) {
	ref, err := artifactSelectedRepo(grant, requestedRepo)
	if err != nil {
		return artifactFileResponse{}, err
	}
	path = strings.TrimSpace(path)
	if !validArtifactFilePath(path) {
		return artifactFileResponse{}, fmt.Errorf("invalid artifact file path")
	}
	repo, err := artifactRepoPath(ctx, grant, ref)
	if err != nil {
		return artifactFileResponse{}, err
	}
	object := ref.HeadSHA + ":" + path
	sizeBytes, err := artifactGitBytes(ctx, repo, "cat-file", "-s", object)
	if err != nil {
		return artifactFileResponse{}, fmt.Errorf("artifact file not found")
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(string(sizeBytes)), 10, 64)
	content, err := artifactGitBytes(ctx, repo, "show", object)
	if err != nil {
		return artifactFileResponse{}, fmt.Errorf("artifact file not found")
	}
	response := artifactFileResponse{Repo: ref.Repo, Path: path, HeadSHA: ref.HeadSHA, Size: size, Encoding: "utf-8"}
	if strings.IndexByte(string(content), 0) >= 0 {
		response.Binary = true
		response.Encoding = "base64"
		const binaryLimit = 256_000
		if len(content) > binaryLimit {
			content = content[:binaryLimit]
			response.Truncated = true
		}
		response.Content = base64.StdEncoding.EncodeToString(content)
		return response, nil
	}
	const textLimit = 1_000_000
	if len(content) > textLimit {
		content = content[:textLimit]
		response.Truncated = true
	}
	response.Content = string(content)
	return response, nil
}

type artifactRuntime struct {
	root    string
	workDir string
	run     *worktreeRun
	once    sync.Once
}

var artifactWorktreeMu sync.Mutex

func (runtime *artifactRuntime) cleanup(logContext context.Context, logger *slog.Logger) {
	if runtime == nil {
		return
	}
	runtime.once.Do(func() {
		artifactWorktreeMu.Lock()
		cleanupWorktrees(logContext, runtime.run, logger)
		artifactWorktreeMu.Unlock()
		if err := os.RemoveAll(runtime.root); err != nil && logger != nil {
			logger.Warn("artifact runtime cleanup failed", "root", runtime.root, "error", err)
		}
	})
}

func (d *Daemon) provisionArtifactRuntime(ctx context.Context, grant ArtifactCapabilityGrant) (*artifactRuntime, error) {
	if info, err := os.Stat(grant.SourceRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("artifact source runtime is unavailable")
	}
	root, err := os.MkdirTemp("", "agora-artifact-"+grant.ArtifactID+"-")
	if err != nil {
		return nil, err
	}
	runtime := &artifactRuntime{root: root, workDir: filepath.Join(root, "worktree")}
	refs := make([]OrchestrationGitHead, 0, len(grant.Repos))
	for _, ref := range grant.Repos {
		refs = append(refs, OrchestrationGitHead{Repo: ref.Repo, HeadSHA: ref.HeadSHA})
	}
	artifactWorktreeMu.Lock()
	runtime.run, err = provisionLocalWorktreesAt(ctx, grant.SourceRoot, "artifact-"+grant.ArtifactID, runtime.workDir, refs, true, d.logger)
	artifactWorktreeMu.Unlock()
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	for index, worktree := range runtime.run.worktrees {
		ref := ArtifactRepoRef{}
		if len(grant.Repos) == 1 {
			ref = grant.Repos[0]
		} else {
			for _, candidate := range grant.Repos {
				if candidate.Repo == filepath.Base(worktree.SrcRepo) {
					ref = candidate
					break
				}
			}
		}
		if ref.HeadSHA == "" || index >= len(runtime.run.worktrees) {
			runtime.cleanup(context.Background(), d.logger)
			return nil, fmt.Errorf("artifact repository mapping is incomplete")
		}
		head, headErr := gitOutput(ctx, worktree.Path, "rev-parse", "HEAD")
		status, statusErr := gitOutput(ctx, worktree.Path, "status", "--porcelain")
		if headErr != nil || statusErr != nil || !strings.EqualFold(head, ref.HeadSHA) || strings.TrimSpace(status) != "" {
			runtime.cleanup(context.Background(), d.logger)
			return nil, fmt.Errorf("artifact runtime failed exact clean HEAD verification")
		}
		if exec.CommandContext(ctx, "git", "-C", worktree.Path, "symbolic-ref", "--short", "HEAD").Run() == nil {
			runtime.cleanup(context.Background(), d.logger)
			return nil, fmt.Errorf("artifact runtime must be detached")
		}
	}
	return runtime, nil
}

func artifactRuntimeRepo(runtime *artifactRuntime, grant ArtifactCapabilityGrant, ref ArtifactRepoRef) (string, error) {
	if runtime == nil || runtime.run == nil || len(runtime.run.worktrees) == 0 {
		return "", fmt.Errorf("artifact runtime unavailable")
	}
	if len(grant.Repos) == 1 {
		return runtime.run.worktrees[0].Path, nil
	}
	for _, worktree := range runtime.run.worktrees {
		if filepath.Base(worktree.SrcRepo) == ref.Repo {
			return worktree.Path, nil
		}
	}
	return "", fmt.Errorf("artifact repository unavailable")
}

type artifactPreview struct {
	key     string
	repoDir string
	runtime *artifactRuntime
	proc    *previewProc
	once    sync.Once
}

var artifactPreviews = struct {
	sync.Mutex
	items map[string]*artifactPreview
}{items: make(map[string]*artifactPreview)}

func artifactPreviewKey(artifactID, repo string) string { return artifactID + "\x00" + repo }

func (d *Daemon) closeArtifactPreview(preview *artifactPreview, kill bool) {
	if preview == nil {
		return
	}
	preview.once.Do(func() {
		artifactPreviews.Lock()
		if artifactPreviews.items[preview.key] == preview {
			delete(artifactPreviews.items, preview.key)
		}
		artifactPreviews.Unlock()
		previewsMu.Lock()
		if previews[preview.repoDir] == preview.proc {
			delete(previews, preview.repoDir)
		}
		previewsMu.Unlock()
		if kill && preview.proc != nil {
			killProcessGroup(preview.proc.cmd)
		}
		preview.runtime.cleanup(context.Background(), d.logger)
	})
}

func (d *Daemon) shutdownArtifactPreviews() {
	artifactPreviews.Lock()
	items := make([]*artifactPreview, 0, len(artifactPreviews.items))
	for _, preview := range artifactPreviews.items {
		items = append(items, preview)
	}
	artifactPreviews.Unlock()
	for _, preview := range items {
		d.closeArtifactPreview(preview, true)
	}
}

func artifactPreviewJSON(preview *artifactPreview) map[string]any {
	response := map[string]any{
		"artifact_id": strings.Split(preview.key, "\x00")[0],
		"command":     preview.proc.command, "running": preview.proc.running(),
		"port": preview.proc.port, "url": previewURL(preview.proc.port),
		"proxy_path": fmt.Sprintf("/editor/local/%d/", preview.proc.port),
	}
	return response
}

func (d *Daemon) registerArtifactHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/artifact/changes", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, _, ok := d.artifactGrant(w, r, "changes")
		if !ok {
			return
		}
		changes, err := artifactChanges(r.Context(), grant)
		if err != nil {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"artifact_id": grant.ArtifactID, "repos": changes})
	})

	mux.HandleFunc("/artifact/file", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, req, ok := d.artifactGrant(w, r, "file")
		if !ok {
			return
		}
		file, err := artifactFile(r.Context(), grant, req.Repo, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(file)
	})

	mux.HandleFunc("/artifact/preview", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, req, ok := d.artifactGrant(w, r, "preview")
		if !ok {
			return
		}
		ref, err := artifactSelectedRepo(grant, req.Repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		key := artifactPreviewKey(grant.ArtifactID, ref.Repo)
		artifactPreviews.Lock()
		existing := artifactPreviews.items[key]
		artifactPreviews.Unlock()
		if existing != nil && existing.proc.running() {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(artifactPreviewJSON(existing))
			return
		}

		runtime, err := d.provisionArtifactRuntime(r.Context(), grant)
		if err != nil {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		repoDir, err := artifactRuntimeRepo(runtime, grant, ref)
		if err != nil {
			runtime.cleanup(context.Background(), d.logger)
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		command := detectDevCommand(repoDir)
		if command == "" {
			runtime.cleanup(context.Background(), d.logger)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"needs_command": true, "artifact_id": grant.ArtifactID})
			return
		}
		if depLog, depErr := ensureDeps(repoDir); depErr != nil {
			runtime.cleanup(context.Background(), d.logger)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "dependency install failed", "log": tailLog(depLog)})
			return
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			runtime.cleanup(context.Background(), d.logger)
			http.Error(w, "failed to allocate a port", http.StatusInternalServerError)
			return
		}
		hintPort := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		proc, err := startPreview(repoDir, command, hintPort)
		if err != nil {
			runtime.cleanup(context.Background(), d.logger)
			http.Error(w, "failed to start preview: "+err.Error(), http.StatusInternalServerError)
			return
		}
		preview := &artifactPreview{key: key, repoDir: repoDir, runtime: runtime, proc: proc}
		artifactPreviews.Lock()
		if winner := artifactPreviews.items[key]; winner != nil && winner.proc.running() {
			artifactPreviews.Unlock()
			killProcessGroup(proc.cmd)
			runtime.cleanup(context.Background(), d.logger)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(artifactPreviewJSON(winner))
			return
		}
		artifactPreviews.items[key] = preview
		artifactPreviews.Unlock()
		previewsMu.Lock()
		previews[repoDir] = proc
		previewsMu.Unlock()
		go func() {
			<-proc.done
			d.closeArtifactPreview(preview, false)
		}()
		if realPort := scanPreviewPort(proc, 40*time.Second); realPort != 0 {
			proc.port = realPort
		}
		if !proc.running() {
			log := tailLog(proc.buf.String())
			d.closeArtifactPreview(preview, false)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "the dev server exited", "log": log})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(artifactPreviewJSON(preview))
	})

	mux.HandleFunc("/artifact/preview/status", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, req, ok := d.artifactGrant(w, r, "preview")
		if !ok {
			return
		}
		ref, err := artifactSelectedRepo(grant, req.Repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		artifactPreviews.Lock()
		preview := artifactPreviews.items[artifactPreviewKey(grant.ArtifactID, ref.Repo)]
		artifactPreviews.Unlock()
		response := map[string]any{"artifact_id": grant.ArtifactID, "running": false}
		if preview != nil && preview.proc.running() {
			if realPort := latestDevPort(preview.proc.buf.String()); realPort != 0 {
				preview.proc.port = realPort
			}
			response = artifactPreviewJSON(preview)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})

	mux.HandleFunc("/artifact/preview/stop", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, req, ok := d.artifactGrant(w, r, "preview")
		if !ok {
			return
		}
		ref, err := artifactSelectedRepo(grant, req.Repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		artifactPreviews.Lock()
		preview := artifactPreviews.items[artifactPreviewKey(grant.ArtifactID, ref.Repo)]
		artifactPreviews.Unlock()
		d.closeArtifactPreview(preview, true)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"artifact_id": grant.ArtifactID, "stopped": true})
	})

	mux.HandleFunc("/artifact/checks", func(w http.ResponseWriter, r *http.Request) {
		if !artifactCORS(w, r) {
			return
		}
		grant, req, ok := d.artifactGrant(w, r, "checks")
		if !ok {
			return
		}
		ref, err := artifactSelectedRepo(grant, req.Repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		runtime, err := d.provisionArtifactRuntime(r.Context(), grant)
		if err != nil {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		defer runtime.cleanup(context.Background(), d.logger)
		repoDir, err := artifactRuntimeRepo(runtime, grant, ref)
		if err != nil {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		command := detectTestCommand(repoDir)
		w.Header().Set("Content-Type", "application/json")
		if command == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"artifact_id": grant.ArtifactID, "head_sha": ref.HeadSHA, "needs_command": true})
			return
		}
		if depLog, depErr := ensureDeps(repoDir); depErr != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"artifact_id": grant.ArtifactID, "head_sha": ref.HeadSHA, "command": command, "passed": false, "exit_code": -1, "output": tailLog(depLog), "error": "dependency install failed"})
			return
		}
		output, exitCode := runProjectTests(repoDir, command, 5*time.Minute)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artifact_id": grant.ArtifactID, "head_sha": ref.HeadSHA,
			"command": command, "exit_code": exitCode, "passed": exitCode == 0, "output": output,
		})
	})
}
