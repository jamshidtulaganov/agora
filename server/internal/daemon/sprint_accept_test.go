package daemon

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestSprintUpstreamBranch recognises a sprint alias and resolves the SHARED
// branch it pushes to, while leaving ordinary branches alone.
func TestSprintUpstreamBranch(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	bare, branch := setupSprintRemote(t)
	wd, repo := cloneWorktree(t, bare, "A")
	(&Daemon{}).ensureSprintBranch(wd, branch, "aaaa1111", discardLog())

	if got, ok := sprintUpstreamBranch(repo, "sprint-wt-aaaa1111"); !ok || got != "sprint-x" {
		t.Fatalf("sprintUpstreamBranch = (%q,%v), want (sprint-x,true)", got, ok)
	}
	if _, ok := sprintUpstreamBranch(repo, "main"); ok {
		t.Fatal("a non-sprint branch must not be treated as a sprint alias")
	}
}

// TestPushToSprintBranch_DirectPush proves accept lands the task's commit on the
// SHARED branch (no PR).
func TestPushToSprintBranch_DirectPush(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	bare, branch := setupSprintRemote(t)
	wd, repo := cloneWorktree(t, bare, "A")
	(&Daemon{}).ensureSprintBranch(wd, branch, "aaaa1111", discardLog())

	sprintWrite(t, repo, "newfile.txt", "from A\n")
	sprintGit(t, repo, "add", ".")
	sprintGit(t, repo, "commit", "-m", "A: add newfile")

	res := pushToSprintBranch(repo, "sprint-wt-aaaa1111", branch)
	if res.Error != "" || res.Skipped != "" {
		t.Fatalf("unexpected result: err=%q skipped=%q", res.Error, res.Skipped)
	}
	if files := sprintGit(t, bare, "ls-tree", "--name-only", branch); !strings.Contains(files, "newfile.txt") {
		t.Fatalf("shared branch %s missing newfile.txt; has: %q", branch, files)
	}
}

// TestPushToSprintBranch_RebaseRetryOnNonFastForward proves the CI conflict
// surface: when a teammate pushed first, accept rebases onto the shared tip and
// retries the push (never force-pushes) — both commits end up on the branch.
func TestPushToSprintBranch_RebaseRetryOnNonFastForward(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")

	bare, branch := setupSprintRemote(t)
	wd, repo := cloneWorktree(t, bare, "A")
	(&Daemon{}).ensureSprintBranch(wd, branch, "aaaa1111", discardLog())

	// This task's own commit (on a now-stale base).
	sprintWrite(t, repo, "mine.txt", "mine\n")
	sprintGit(t, repo, "add", ".")
	sprintGit(t, repo, "commit", "-m", "A: mine")

	// A teammate pushes a DIFFERENT file to the shared branch first.
	_, other := cloneWorktree(t, bare, "other")
	sprintGit(t, other, "checkout", "-B", branch, "origin/"+branch)
	sprintWrite(t, other, "theirs.txt", "theirs\n")
	sprintGit(t, other, "add", ".")
	sprintGit(t, other, "commit", "-m", "other: theirs")
	sprintGit(t, other, "push", "origin", "HEAD:"+branch)

	// Accept: first push is non-fast-forward → rebase onto tip → retry → success.
	res := pushToSprintBranch(repo, "sprint-wt-aaaa1111", branch)
	if res.Error != "" {
		t.Fatalf("expected clean rebase-retry, got error: %q", res.Error)
	}
	files := sprintGit(t, bare, "ls-tree", "--name-only", branch)
	if !strings.Contains(files, "mine.txt") || !strings.Contains(files, "theirs.txt") {
		t.Fatalf("shared branch must hold both commits after rebase; has: %q", files)
	}
}

// TestPruneWorktree_ReclaimsStaleSprintAliasesKeepsSharedBranch proves the GC
// sweep removes leaked per-task sprint aliases but never the shared branch and
// never an in-use alias.
func TestPruneWorktree_ReclaimsStaleSprintAliasesKeepsSharedBranch(t *testing.T) {
	t.Parallel()

	d := newGCTestDaemon(t, http.NewServeMux())
	sourceRepo := createGCGitRepo(t)
	barePath := filepath.Join(t.TempDir(), "cache.git")
	runGitForGC(t, "", "clone", "--bare", sourceRepo, barePath)

	activeWT := filepath.Join(t.TempDir(), "active")
	activeAlias := "sprint-wt-live11111111"
	staleAlias := "sprint-wt-stale22222222"
	sharedBranch := "sprint-1" // the integration branch — must survive

	runGitForGC(t, "", "-C", barePath, "worktree", "add", "-b", activeAlias, activeWT, "HEAD")
	runGitForGC(t, "", "-C", barePath, "branch", staleAlias, "HEAD")
	runGitForGC(t, "", "-C", barePath, "branch", sharedBranch, "HEAD")

	d.pruneWorktree(barePath)

	if gitRefExists(t, barePath, "refs/heads/"+staleAlias) {
		t.Fatalf("stale sprint alias %q should be deleted", staleAlias)
	}
	if !gitRefExists(t, barePath, "refs/heads/"+activeAlias) {
		t.Fatalf("in-use sprint alias %q must be preserved", activeAlias)
	}
	if !gitRefExists(t, barePath, "refs/heads/"+sharedBranch) {
		t.Fatalf("shared sprint branch %q must NEVER be deleted", sharedBranch)
	}
}
