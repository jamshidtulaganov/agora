package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file protects a user's own git checkout when an agent runs inside it
// (local_directory in_place mode). Two guarantees:
//
//   - Pre-existing uncommitted work is snapshotted to a recoverable ref
//     (`git stash create` + refs/agora/backup/<task>) before the agent runs —
//     a zero-touch backup that never moves a file in the working tree.
//   - Agent commits land on a throwaway branch (agent/<name>/<task>), never on
//     the user's branch. If the agent made NO commits (a read-only QA run, an
//     inspection task), the branch is torn down and the user's original branch
//     is restored, so their repo is exactly where they left it. This is why we
//     need no server-side "is this a QA task?" marker: branching by *actual
//     commits* is self-detecting — a task that doesn't commit leaves no trace.
//
// Plain (non-git) local directories skip all of this and keep today's
// behavior; the gate is isGitWorkTree.

var branchNameSanitizer = regexp.MustCompile(`[^a-z0-9._-]+`)

// localDirGit is the captured pre-run state for a local_directory task that
// ran against a real git work tree. nil for plain folders.
type localDirGit struct {
	dir         string // the user's working tree (assignment.AbsPath)
	origRef     string // branch name HEAD was on, or "" when HEAD was detached
	origHead    string // sha HEAD pointed at before the agent ran
	branch      string // agent/<name>/<short-task> we created, "" if none
	snapshotRef string // refs/agora/backup/<short-task> when the tree started dirty
	finalized   bool
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	return strings.TrimSpace(string(out)), err
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	return exec.CommandContext(ctx, "git", full...).Run()
}

// gitHeadRef returns the current branch (empty when detached) and the HEAD sha.
func gitHeadRef(ctx context.Context, dir string) (branch, head string, err error) {
	head, err = gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	// symbolic-ref fails (non-zero) on a detached HEAD — that's not an error
	// for us, it just means there is no branch to restore later.
	branch, _ = gitOutput(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	return branch, head, nil
}

func gitIsDirty(ctx context.Context, dir string) bool {
	out, err := gitOutput(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false
	}
	return out != ""
}

// localAgentBranchName builds agent/<sanitized-name>/<short-task>, mirroring
// the managed-mode worktree branch naming (repocache.CreateWorktree).
func localAgentBranchName(agentName, taskID string) string {
	name := branchNameSanitizer.ReplaceAllString(strings.ToLower(strings.TrimSpace(agentName)), "-")
	name = strings.Trim(name, "-")
	if len(name) > 30 {
		name = strings.TrimRight(name[:30], "-")
	}
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("agent/%s/%s", name, shortID(taskID))
}

// prepareLocalDirGit captures pre-run state and puts the working tree on an
// isolated agent branch. Returns nil (no error) for a non-git directory so the
// caller degrades to today's plain-folder behavior. A snapshot failure is
// non-fatal (logged, backup skipped); a branch-creation failure IS returned —
// running without isolation would let agent commits land on the user's branch,
// which is the exact harm this prevents.
func prepareLocalDirGit(ctx context.Context, dir, agentName, taskID string, log *slog.Logger) (*localDirGit, error) {
	if !isGitWorkTree(ctx, dir) {
		return nil, nil
	}
	branch, head, err := gitHeadRef(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("read git HEAD: %w", err)
	}
	g := &localDirGit{dir: dir, origRef: branch, origHead: head}

	if gitIsDirty(ctx, dir) {
		// `git stash create` builds a commit object for the dirty tracked
		// state WITHOUT touching the working tree, then we pin it to a ref so
		// gc can't reap it. Untracked files are intentionally not captured
		// (documented limitation). Best-effort: a snapshot failure must not
		// block the task.
		if sha, sErr := gitOutput(ctx, dir, "stash", "create", "agora backup "+shortID(taskID)); sErr == nil && sha != "" {
			ref := "refs/agora/backup/" + shortID(taskID)
			if uErr := gitRun(ctx, dir, "update-ref", ref, sha); uErr == nil {
				g.snapshotRef = ref
			} else {
				log.Warn("local_directory: pin dirty-tree snapshot failed (continuing)", "error", uErr)
			}
		} else if sErr != nil {
			log.Warn("local_directory: dirty-tree snapshot failed (continuing)", "error", sErr)
		}
	}

	branchName := localAgentBranchName(agentName, taskID)
	if err := gitRun(ctx, dir, "checkout", "-b", branchName); err != nil {
		// Retry as a plain checkout: a task that was already started once
		// (daemon restart / re-claim) may have left the branch behind.
		if reuseErr := gitRun(ctx, dir, "checkout", branchName); reuseErr != nil {
			return nil, fmt.Errorf("create agent branch %q: %w", branchName, err)
		}
	}
	g.branch = branchName

	// Prune stale dirty-tree snapshots from earlier runs so the backup refs
	// don't accumulate unbounded in the user's repo. Best-effort.
	pruneOldBackupRefs(ctx, dir, backupRefTTL(), log)
	return g, nil
}

const backupRefPrefix = "refs/agora/backup/"

// backupRefTTL is how long a dirty-tree snapshot ref is kept. Override with
// AGORA_LOCAL_BACKUP_TTL_DAYS; 0 disables pruning. Default 14 days.
func backupRefTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("AGORA_LOCAL_BACKUP_TTL_DAYS")); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days >= 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	return 14 * 24 * time.Hour
}

// pruneOldBackupRefs deletes refs/agora/backup/* whose committer time is older
// than maxAge. A snapshot commit's committer date is when it was created, so
// it doubles as the ref's age. Best-effort: any git failure is logged and
// ignored. maxAge <= 0 disables pruning.
func pruneOldBackupRefs(ctx context.Context, dir string, maxAge time.Duration, log *slog.Logger) {
	if maxAge <= 0 {
		return
	}
	// One line per ref: "<committer-unix-ts> <refname>".
	out, err := gitOutput(ctx, dir, "for-each-ref", "--format=%(committerdate:unix) %(refname)", backupRefPrefix)
	if err != nil || out == "" {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if time.Unix(ts, 0).Before(cutoff) {
			if delErr := gitRun(ctx, dir, "update-ref", "-d", fields[1]); delErr != nil {
				log.Warn("local_directory: prune stale backup ref failed", "ref", fields[1], "error", delErr)
			}
		}
	}
}

// finalizeLocalDirGit is called once after the agent finishes (success,
// failure, or cancellation). It returns a human-readable summary section to
// append to the task's result comment, or "" when there is nothing to say
// (no branch, or the branch was torn down because the agent made no commits).
// Safe to call multiple times — subsequent calls are no-ops.
func finalizeLocalDirGit(ctx context.Context, g *localDirGit, log *slog.Logger) string {
	if g == nil || g.finalized || g.branch == "" {
		if g != nil {
			g.finalized = true
		}
		return ""
	}
	g.finalized = true

	// Best-effort prune of any scratch worktrees a QA baseline step left
	// behind (WS4 has the agent `git worktree add` a merge-base tree); if the
	// agent died before removing it, this keeps the user's .git/worktrees clean.
	_ = gitRun(ctx, g.dir, "worktree", "prune")

	newHead, err := gitOutput(ctx, g.dir, "rev-parse", "HEAD")
	if err != nil {
		log.Warn("local_directory: read post-run HEAD failed", "error", err)
		return ""
	}

	if newHead == g.origHead {
		// The agent made no commits — the branch has no unique history, so
		// tear it down and put the user back on their branch. Uncommitted
		// changes (the agent's or the user's) ride across the checkout
		// untouched because both refs point at the same commit. This is what
		// makes read-only tasks (QA gates, inspections) leave zero trace with
		// no task-kind marker.
		target := g.origRef
		if target == "" {
			target = g.origHead // detached HEAD: restore the exact sha
		}
		if err := gitRun(ctx, g.dir, "checkout", target); err != nil {
			log.Warn("local_directory: restore original branch failed; leaving agent branch checked out", "branch", g.branch, "error", err)
			return ""
		}
		if err := gitRun(ctx, g.dir, "branch", "-D", g.branch); err != nil {
			log.Warn("local_directory: delete empty agent branch failed", "branch", g.branch, "error", err)
		}
		return ""
	}

	// The agent committed. Keep the branch and tell the user what happened and
	// how to get back to where they were.
	return g.buildSummary(ctx, newHead, log)
}

const localDirSummaryLineCap = 60

func (g *localDirGit) buildSummary(ctx context.Context, newHead string, log *slog.Logger) string {
	var b strings.Builder
	b.WriteString("### Agent changes to your local directory\n\n")
	b.WriteString(fmt.Sprintf("The agent's commits are on branch `%s` (your working tree is checked out there now).\n", g.branch))

	orig := g.origRef
	if orig == "" {
		orig = g.origHead[:min(12, len(g.origHead))] + " (detached)"
	}
	b.WriteString(fmt.Sprintf("To return to where you were: `git checkout %s`\n\n", strings.Fields(orig)[0]))

	if stat, err := gitOutput(ctx, g.dir, "diff", "--stat", g.origHead+".."+newHead); err == nil && stat != "" {
		b.WriteString("Commits made:\n```\n")
		b.WriteString(capLines(stat, localDirSummaryLineCap))
		b.WriteString("\n```\n")
	}

	if dirty := gitIsDirty(ctx, g.dir); dirty {
		if count, err := gitOutput(ctx, g.dir, "status", "--porcelain"); err == nil {
			n := len(strings.Split(strings.TrimSpace(count), "\n"))
			b.WriteString(fmt.Sprintf("\n%d uncommitted change(s) remain in the working tree.\n", n))
		}
	}

	if g.snapshotRef != "" {
		b.WriteString(fmt.Sprintf(
			"\nYour uncommitted changes from before this run were backed up to `%s`. Recover them with `git stash apply %s` if needed.\n",
			g.snapshotRef, g.snapshotRef))
	}

	return strings.TrimRight(b.String(), "\n")
}

// capLines truncates a multi-line block to at most n lines, appending a marker
// so a 5000-file diffstat can't blow up the comment.
func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… (%d more lines)", len(lines)-n)
}
