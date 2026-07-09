package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree isolation for local_directory (isolation:"worktree"). Instead of
// running the agent directly in the user's folder, the daemon provisions an
// isolated git worktree per repo — cut from the developer's OWN local checkout
// — inside the managed env. Tasks on different issues get different worktrees
// and run in parallel; the user's checkout is never the agent's working tree.
//
// Multi-repo layout: the local_directory local_path is a PARENT folder that
// may contain several repo checkouts as subfolders (backend/, frontend/,
// docs/). A parent that is itself a single git repo (no repo subfolders) is
// the single-repo case. Which repos a task actually edits is up to the agent
// (task-description-driven) — the daemon just makes all of them available as
// worktrees.

// localRepo is one repo the daemon will provision a worktree for.
type localRepo struct {
	Name    string // subfolder name, or the repo basename for a single-repo parent
	SrcPath string // the developer's local checkout to cut the worktree from
	RelPath string // placement under the env workdir: "." for single-repo, else Name
}

// detectLocalRepos enumerates the repos under a local_directory parent. If the
// parent itself is a git work tree it is the single repo (RelPath "."). Else,
// immediate subdirectories that are git work trees are the repos. Returns an
// error only when the parent is unreadable; an empty result means "no git repo
// here" (caller should fail the task with a clear message).
func detectLocalRepos(ctx context.Context, parent string) ([]localRepo, error) {
	if isGitWorkTree(ctx, parent) {
		return []localRepo{{Name: filepath.Base(parent), SrcPath: parent, RelPath: "."}}, nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, fmt.Errorf("read local_directory parent %q: %w", parent, err)
	}
	var repos []localRepo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(parent, e.Name())
		if isGitWorkTree(ctx, sub) {
			repos = append(repos, localRepo{Name: e.Name(), SrcPath: sub, RelPath: e.Name()})
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// provisionedWorktree records a created worktree so it can be cleaned up.
type provisionedWorktree struct {
	SrcRepo string // the developer's checkout the worktree hangs off
	Path    string // the worktree directory inside the env
	Branch  string // agent/<issue>/<repo>
}

// worktreeRun is the result of provisioning: the created worktrees + the
// per-repo branch names, for cleanup and summary.
type worktreeRun struct {
	parent    string
	worktrees []provisionedWorktree
}

// provisionLocalWorktrees is the execenv ProvisionWorkDir hook body: for each
// repo under the parent, `git worktree add <target> -b agent/<issue>/<repo>
// HEAD`, mirroring the parent's subfolder layout inside workDir. issueKey is a
// short, filesystem-safe issue identifier used in the branch name so a repo's
// worktrees are reused per issue (dev → QA → fix).
func provisionLocalWorktrees(ctx context.Context, parent, issueKey, workDir string, log *slog.Logger) (*worktreeRun, error) {
	repos, err := detectLocalRepos(ctx, parent)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("local_directory %q contains no git repositories (worktree isolation needs at least one)", parent)
	}
	run := &worktreeRun{parent: parent}

	// For a single repo placed at "." the worktree IS workDir, which must not
	// pre-exist (the ProvisionWorkDir hook runs in lieu of MkdirAll). For
	// multiple repos, create workDir first and place each under it.
	single := len(repos) == 1 && repos[0].RelPath == "."
	if !single {
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create worktree parent %q: %w", workDir, err)
		}
	}

	for _, repo := range repos {
		target := workDir
		if !single {
			target = filepath.Join(workDir, repo.Name)
		}
		branch := localAgentBranchName(issueKey, repo.Name)
		if err := addWorktree(ctx, repo.SrcPath, target, branch, log); err != nil {
			// Roll back anything already created so a partial failure doesn't
			// leak worktrees in the user's repos.
			cleanupWorktrees(ctx, run, log)
			return nil, err
		}
		run.worktrees = append(run.worktrees, provisionedWorktree{SrcRepo: repo.SrcPath, Path: target, Branch: branch})
	}
	return run, nil
}

// worktreeSidecars are the daemon-written context files that must never be
// committed into the agent's branch (they are runtime scaffolding).
var worktreeSidecars = []string{
	"CLAUDE.md", "AGENTS.md", "GEMINI.md",
	".agent_context/", ".agora/", ".claude/", ".opencode/", ".cursor/", ".pi/", ".agents/",
}

// addWorktree creates one worktree, reusing an existing branch on retry (a
// re-claimed task may have left the branch behind).
func addWorktree(ctx context.Context, srcRepo, target, branch string, log *slog.Logger) error {
	if err := gitRun(ctx, srcRepo, "worktree", "add", target, "-b", branch, "HEAD"); err != nil {
		// Branch may already exist (re-claim); add the worktree on it.
		if reuse := gitRun(ctx, srcRepo, "worktree", "add", target, branch); reuse != nil {
			return fmt.Errorf("git worktree add for %q: %w", srcRepo, err)
		}
	}
	return nil
}

// finalizeWorktrees commits any real (non-sidecar) agent changes in each
// worktree onto its agent branch so the work persists after the worktree is
// removed, then cleans the worktrees up. Returns a short human summary of what
// landed on which branch (empty when nothing changed). The developer's source
// checkout is never touched — commits go only onto the agent branches.
func finalizeWorktrees(ctx context.Context, run *worktreeRun, agentName string, log *slog.Logger) string {
	if run == nil {
		return ""
	}
	var lines []string
	for _, wt := range run.worktrees {
		status, _ := gitOutput(ctx, wt.Path, "status", "--porcelain")
		if strings.TrimSpace(status) == "" {
			continue // agent left this repo untouched
		}
		if err := gitRun(ctx, wt.Path, "add", "-A"); err != nil {
			log.Warn("local_directory: worktree add -A failed", "path", wt.Path, "error", err)
			continue
		}
		// Unstage the daemon's sidecar scaffolding so it never lands in the
		// agent's commit (a linked worktree does not consult a per-worktree
		// exclude file, so we reset explicitly rather than rely on .gitignore).
		resetArgs := append([]string{"reset", "-q", "--"}, worktreeSidecars...)
		_ = gitRun(ctx, wt.Path, resetArgs...)
		// Nothing staged after unstaging sidecars (only scaffolding changed) → skip.
		if diff, _ := gitOutput(ctx, wt.Path, "diff", "--cached", "--name-only"); strings.TrimSpace(diff) == "" {
			continue
		}
		msg := fmt.Sprintf("agent changes (%s)", agentName)
		if err := gitRun(ctx, wt.Path, "-c", "user.email=agent@agora.local", "-c", "user.name="+agentName, "commit", "-m", msg); err != nil {
			log.Warn("local_directory: worktree commit failed", "path", wt.Path, "branch", wt.Branch, "error", err)
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`: committed on branch `%s`", filepath.Base(wt.Path), wt.Branch))
	}
	cleanupWorktrees(ctx, run, log)
	if len(lines) == 0 {
		return ""
	}
	return "### Agent changes (worktree isolation)\n\nYour source checkout was untouched. The agent's work is committed on these branches:\n\n" + strings.Join(lines, "\n") + "\n"
}

// sanitizeResourcesForWorktree drops the source local_path (and daemon_id) from
// the local_directory resource when worktree mode is on, so the agent context
// never advertises the developer's checkout as an editable path. Returns the
// input unchanged when worktreeMode is false. The label is kept for display.
func sanitizeResourcesForWorktree(resources []ProjectResourceData, worktreeMode bool) []ProjectResourceData {
	if !worktreeMode {
		return resources
	}
	out := make([]ProjectResourceData, len(resources))
	copy(out, resources)
	for i := range out {
		if out[i].ResourceType != localDirectoryResourceType {
			continue
		}
		var ref localDirectoryRef
		_ = json.Unmarshal(out[i].ResourceRef, &ref)
		// Keep only the label; the agent works in its cwd worktrees, and the
		// source path must not be reachable via the context.
		safe, _ := json.Marshal(map[string]string{"label": ref.Label, "isolation": "worktree"})
		out[i].ResourceRef = safe
	}
	return out
}

// cleanupWorktrees removes every worktree in the run and prunes the parent
// repos' worktree metadata. Best-effort; the agent branches are intentionally
// kept (they hold the agent's commits). Safe to call more than once.
func cleanupWorktrees(ctx context.Context, run *worktreeRun, log *slog.Logger) {
	if run == nil {
		return
	}
	for _, wt := range run.worktrees {
		if err := gitRun(ctx, wt.SrcRepo, "worktree", "remove", "--force", wt.Path); err != nil {
			log.Warn("local_directory: worktree remove failed", "path", wt.Path, "error", err)
		}
	}
	pruned := map[string]bool{}
	for _, wt := range run.worktrees {
		if pruned[wt.SrcRepo] {
			continue
		}
		pruned[wt.SrcRepo] = true
		_ = gitRun(ctx, wt.SrcRepo, "worktree", "prune")
	}
}
