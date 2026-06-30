package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sprintGit runs a git command in dir with a deterministic identity, failing the
// test on error. Returns trimmed combined output.
func sprintGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %s: %v", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out))
}

func sprintWrite(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func sprintHead(t *testing.T, repo string) string {
	t.Helper()
	return sprintGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
}

func sprintHas(t *testing.T, repo, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(repo, name))
	return err == nil
}

// setupSprintRemote builds a bare "origin" carrying a sprint branch with one
// seed commit, and returns (barePath, sprintBranch). Clones of bare therefore
// see origin/<sprintBranch>.
func setupSprintRemote(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	sprintGit(t, root, "init", "--bare", bare)
	sprintGit(t, root, "init", seed) // commit on git's default branch, whatever it is named
	sprintWrite(t, seed, "shared.txt", "v1\n")
	sprintGit(t, seed, "add", ".")
	sprintGit(t, seed, "commit", "-m", "seed")
	sprintGit(t, seed, "remote", "add", "origin", bare)
	sprintGit(t, seed, "checkout", "-b", "sprint-x")
	sprintGit(t, seed, "push", "origin", "sprint-x")
	// Point the bare's HEAD at the sprint branch so clones check it out cleanly
	// (no "remote HEAD refers to nonexistent ref" and a populated working tree).
	sprintGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/sprint-x")
	return bare, "sprint-x"
}

// cloneWorktree clones bare into <tmp>/<label>/repo and returns the workdir
// (the dir CONTAINING repo, which is what ensureSprintBranch walks).
func cloneWorktree(t *testing.T, bare, label string) (workdir, repo string) {
	t.Helper()
	workdir = filepath.Join(t.TempDir(), label)
	repo = filepath.Join(workdir, "repo")
	sprintGit(t, t.TempDir(), "clone", bare, repo)
	return workdir, repo
}

// TestEnsureSprintBranch_SharedBranchCoexistAndRebase proves the slice-1
// guarantee: N worktrees check out ONE shared sprint branch (each via its own
// per-task local alias, since git rejects two worktrees on one branch name),
// each tracks origin/<sprintBranch>, and a later run rebases a teammate's pushed
// commit onto this task's own work (pull-before-work).
func TestEnsureSprintBranch_SharedBranchCoexistAndRebase(t *testing.T) {
	// runGit (used inside ensureSprintBranch) inherits the process env; rebase
	// creates commits, so a committer identity must be present.
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	bare, branch := setupSprintRemote(t)
	d := &Daemon{}
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	wdA, repoA := cloneWorktree(t, bare, "taskA")
	wdB, repoB := cloneWorktree(t, bare, "taskB")

	// Both tasks land on the shared branch via distinct aliases.
	d.ensureSprintBranch(wdA, branch, "aaaa1111", log)
	d.ensureSprintBranch(wdB, branch, "bbbb2222", log)

	if got := sprintHead(t, repoA); got != "sprint-wt-aaaa1111" {
		t.Fatalf("worktree A HEAD = %q, want sprint-wt-aaaa1111", got)
	}
	if got := sprintHead(t, repoB); got != "sprint-wt-bbbb2222" {
		t.Fatalf("worktree B HEAD = %q, want sprint-wt-bbbb2222", got)
	}
	// Each alias tracks the shared remote branch (so commits/pulls target it).
	if up := sprintGit(t, repoA, "rev-parse", "--abbrev-ref", "sprint-wt-aaaa1111@{upstream}"); up != "origin/sprint-x" {
		t.Fatalf("A upstream = %q, want origin/sprint-x", up)
	}

	// Task A commits and pushes to the shared branch (slice-3 accept, done by
	// hand here since slice 1 still opens a PR).
	sprintWrite(t, repoA, "a.txt", "from A\n")
	sprintGit(t, repoA, "add", ".")
	sprintGit(t, repoA, "commit", "-m", "A: add a.txt")
	sprintGit(t, repoA, "push", "origin", "HEAD:"+branch)

	// Task B does its own work, then re-syncs: pull-before-work must rebase B's
	// commit ONTO A's pushed commit — B ends up with both files, no conflict.
	sprintWrite(t, repoB, "b.txt", "from B\n")
	sprintGit(t, repoB, "add", ".")
	sprintGit(t, repoB, "commit", "-m", "B: add b.txt")
	d.ensureSprintBranch(wdB, branch, "bbbb2222", log)

	if !sprintHas(t, repoB, "a.txt") {
		t.Fatal("worktree B missing a.txt — A's pushed commit was not rebased in (pull-before-work failed)")
	}
	if !sprintHas(t, repoB, "b.txt") {
		t.Fatal("worktree B lost b.txt — rebase dropped this task's own commit")
	}
	if got := sprintHead(t, repoB); got != "sprint-wt-bbbb2222" {
		t.Fatalf("worktree B HEAD after rebase = %q, want sprint-wt-bbbb2222", got)
	}
}

// TestEnsureSprintBranch_ConflictAbortsNonDestructively proves a rebase conflict
// is surfaced, not silently resolved: the rebase aborts, the worktree keeps THIS
// task's own commit (no detached HEAD, no dropped work), and a warning is logged.
func TestEnsureSprintBranch_ConflictAbortsNonDestructively(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	bare, branch := setupSprintRemote(t)
	d := &Daemon{}
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	wd, repo := cloneWorktree(t, bare, "taskC")
	d.ensureSprintBranch(wd, branch, "cccc3333", log)

	// This task edits the shared file.
	sprintWrite(t, repo, "shared.txt", "C-change\n")
	sprintGit(t, repo, "add", ".")
	sprintGit(t, repo, "commit", "-m", "C: edit shared.txt")

	// A teammate pushes a CONFLICTING edit to the same file on the shared branch.
	_, other := cloneWorktree(t, bare, "other")
	sprintGit(t, other, "checkout", "-B", "sprint-x", "origin/sprint-x")
	sprintWrite(t, other, "shared.txt", "REMOTE-change\n")
	sprintGit(t, other, "add", ".")
	sprintGit(t, other, "commit", "-m", "other: edit shared.txt")
	sprintGit(t, other, "push", "origin", "HEAD:"+branch)

	// Re-sync: the rebase conflicts. It must abort, not leave a detached HEAD.
	d.ensureSprintBranch(wd, branch, "cccc3333", log)

	if got := sprintHead(t, repo); got != "sprint-wt-cccc3333" {
		t.Fatalf("HEAD after conflict = %q, want sprint-wt-cccc3333 (rebase not aborted cleanly)", got)
	}
	content, err := os.ReadFile(filepath.Join(repo, "shared.txt"))
	if err != nil {
		t.Fatalf("read shared.txt: %v", err)
	}
	if strings.TrimSpace(string(content)) != "C-change" {
		t.Fatalf("shared.txt = %q, want this task's own C-change (conflict abort dropped work)", strings.TrimSpace(string(content)))
	}
	if !strings.Contains(logBuf.String(), "conflict") {
		t.Fatalf("expected a conflict warning in the log, got: %s", logBuf.String())
	}
}
