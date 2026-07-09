package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Worktree integration (stage 3): when an issue's worktree work is accepted
// (qa:pass), land each touched repo's agent branch into a target branch. The
// squash-merge is done in a THROWAWAY worktree of the source repo, so the
// developer's own checkout is never checked out or modified. Sprint mode
// targets the sprint branch; PR mode targets the repo's dev/main branch (the
// push + PR open is layered on top — see stage 3b).

// integrationResult reports what landed where, for the result comment.
type integrationResult struct {
	Repo         string
	AgentBranch  string
	TargetBranch string
	Merged       bool   // false when the agent branch had nothing to land
	Commit       string // squashed commit sha on the target branch
}

// integrateWorktreeIssue commits each worktree's agent changes and squash-merges
// the agent branch into targetBranch (in the source repo, via a temp worktree),
// then removes the issue's worktree env. The developer's checkout is never
// touched. Returns per-repo results.
func integrateWorktreeIssue(ctx context.Context, envDir, targetBranch, agentName string, log *slog.Logger) ([]integrationResult, error) {
	worktrees := discoverEnvWorktrees(ctx, envDir)
	if len(worktrees) == 0 {
		return nil, fmt.Errorf("no worktrees found under %q", envDir)
	}
	var results []integrationResult
	for _, wt := range worktrees {
		src := worktreeSourceRepo(ctx, wt.Path)
		if src == "" {
			log.Warn("local_directory: integrate — cannot resolve source repo", "worktree", wt.Path)
			continue
		}
		// 1. Commit the agent's real changes (excluding sidecars) onto the
		//    agent branch so they're mergeable.
		commitWorktreeChanges(ctx, wt.Path, agentName, log)

		// 2. Squash-merge the agent branch into the target branch.
		res := integrationResult{Repo: filepath.Base(wt.Path), AgentBranch: wt.Branch, TargetBranch: targetBranch}
		commit, merged, err := squashMergeIntoBranch(ctx, src, wt.Branch, targetBranch, agentName, log)
		if err != nil {
			log.Warn("local_directory: integrate — squash merge failed", "repo", res.Repo, "error", err)
			results = append(results, res)
			continue
		}
		res.Merged = merged
		res.Commit = commit
		results = append(results, res)
	}
	// 3. Reclaim the issue's worktree env (agent branches are kept).
	cleanupWorktreeEnvDir(ctx, envDir, log)
	return results, nil
}

// discoverEnvWorktrees reconstructs the worktrees under an issue env from disk:
// each git-work-tree subfolder (or the env itself for a single-repo issue).
func discoverEnvWorktrees(ctx context.Context, envDir string) []provisionedWorktree {
	var out []provisionedWorktree
	if entries, err := os.ReadDir(envDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(envDir, e.Name())
			if isGitWorkTree(ctx, sub) {
				branch, _ := gitOutput(ctx, sub, "symbolic-ref", "--short", "-q", "HEAD")
				out = append(out, provisionedWorktree{Path: sub, Branch: branch})
			}
		}
	}
	if len(out) == 0 && isGitWorkTree(ctx, envDir) {
		branch, _ := gitOutput(ctx, envDir, "symbolic-ref", "--short", "-q", "HEAD")
		out = append(out, provisionedWorktree{Path: envDir, Branch: branch})
	}
	return out
}

// worktreeSourceRepo resolves the developer's source repo a worktree hangs off,
// from the worktree's git-common-dir.
func worktreeSourceRepo(ctx context.Context, worktree string) string {
	common, err := gitOutput(ctx, worktree, "rev-parse", "--git-common-dir")
	if err != nil || common == "" {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(worktree, common)
	}
	return filepath.Dir(common) // .../<repo>/.git → <repo>
}

// commitWorktreeChanges commits the agent's non-sidecar changes in a worktree
// onto its current (agent) branch. No-op when the tree is clean or only
// sidecars changed.
func commitWorktreeChanges(ctx context.Context, worktree, agentName string, log *slog.Logger) {
	if status, _ := gitOutput(ctx, worktree, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		return
	}
	if err := gitRun(ctx, worktree, "add", "-A"); err != nil {
		return
	}
	resetArgs := append([]string{"reset", "-q", "--"}, worktreeSidecars...)
	_ = gitRun(ctx, worktree, resetArgs...)
	if diff, _ := gitOutput(ctx, worktree, "diff", "--cached", "--name-only"); strings.TrimSpace(diff) == "" {
		return
	}
	_ = gitRun(ctx, worktree, "-c", "user.email=agent@agora.local", "-c", "user.name="+agentName,
		"commit", "-m", fmt.Sprintf("agent changes (%s)", agentName))
}

// squashMergeIntoBranch squash-merges agentBranch into targetBranch inside a
// throwaway worktree of src, so src's own checkout is never touched. Creates
// targetBranch from the repo's default HEAD when it does not exist yet. Returns
// the squashed commit sha and whether anything was actually merged.
func squashMergeIntoBranch(ctx context.Context, src, agentBranch, targetBranch, agentName string, log *slog.Logger) (string, bool, error) {
	if strings.TrimSpace(agentBranch) == "" {
		return "", false, fmt.Errorf("empty agent branch")
	}
	// Create the target branch if it doesn't exist yet (first integration).
	if !branchExists(ctx, src, targetBranch) {
		if err := gitRun(ctx, src, "branch", targetBranch); err != nil {
			return "", false, fmt.Errorf("create target branch %q: %w", targetBranch, err)
		}
	}
	tmp, err := os.MkdirTemp("", "agora-integrate-*")
	if err != nil {
		return "", false, err
	}
	defer func() {
		_ = gitRun(ctx, src, "worktree", "remove", "--force", tmp)
		_ = gitRun(ctx, src, "worktree", "prune")
		_ = os.RemoveAll(tmp)
	}()
	if err := gitRun(ctx, src, "worktree", "add", tmp, targetBranch); err != nil {
		return "", false, fmt.Errorf("checkout target %q in temp worktree: %w", targetBranch, err)
	}
	// Squash-merge; a clean squash of already-contained history leaves nothing
	// staged, which we treat as "nothing to merge" rather than an error.
	if err := gitRun(ctx, tmp, "merge", "--squash", agentBranch); err != nil {
		return "", false, fmt.Errorf("squash merge %q: %w", agentBranch, err)
	}
	if staged, _ := gitOutput(ctx, tmp, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) == "" {
		return "", false, nil // nothing to land
	}
	msg := fmt.Sprintf("integrate %s (squash)", agentBranch)
	if err := gitRun(ctx, tmp, "-c", "user.email=agent@agora.local", "-c", "user.name="+agentName, "commit", "-m", msg); err != nil {
		return "", false, fmt.Errorf("commit squash: %w", err)
	}
	sha, _ := gitOutput(ctx, tmp, "rev-parse", "HEAD")
	return sha, true, nil
}

// branchExists reports whether branch exists in src.
func branchExists(ctx context.Context, src, branch string) bool {
	return gitRun(ctx, src, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil
}
