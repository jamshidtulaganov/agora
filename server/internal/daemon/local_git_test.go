package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitFixture initializes a git repo in a temp dir with one commit on a known
// branch and returns its path. Skips the test when git is unavailable.
func gitFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLocalAgentBranchName(t *testing.T) {
	got := localAgentBranchName("QA Bot!!", "11111111-2222-3333-4444-555555555555")
	if got != "agent/qa-bot/11111111" {
		t.Errorf("got %q", got)
	}
	if localAgentBranchName("", "abcdefgh") != "agent/agent/abcdefgh" {
		t.Errorf("empty name should fall back to 'agent'")
	}
}

func TestPrepareLocalDirGit_NonGitFolder(t *testing.T) {
	dir := t.TempDir() // plain folder, no git
	g, err := prepareLocalDirGit(context.Background(), dir, "agent", "task-1", slog.Default())
	if err != nil {
		t.Fatalf("plain folder should not error: %v", err)
	}
	if g != nil {
		t.Fatal("plain folder should return nil localDirGit")
	}
}

func TestPrepareLocalDirGit_CleanTree(t *testing.T) {
	dir := gitFixture(t)
	g, err := prepareLocalDirGit(context.Background(), dir, "Dev Agent", "task-abcd1234", slog.Default())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if g == nil {
		t.Fatal("git tree should return a localDirGit")
	}
	if g.origRef != "main" {
		t.Errorf("origRef = %q, want main", g.origRef)
	}
	if g.snapshotRef != "" {
		t.Errorf("clean tree should have no snapshot, got %q", g.snapshotRef)
	}
	// The working tree should now be on the agent branch.
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != g.branch {
		t.Errorf("HEAD on %q, want agent branch %q", cur, g.branch)
	}
}

func TestPrepareLocalDirGit_PreferredIssueBranch(t *testing.T) {
	dir := gitFixture(t)
	g, err := prepareLocalDirGit(context.Background(), dir, "Dev Agent", "task-abcd1234", slog.Default(), "feature/issue-12")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if g.branch != "feature/issue-12" {
		t.Fatalf("branch = %q, want feature/issue-12", g.branch)
	}
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != "feature/issue-12" {
		t.Fatalf("HEAD on %q, want feature/issue-12", cur)
	}
}

func TestFinalize_NoCommits_RestoresBranch(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	g, err := prepareLocalDirGit(ctx, dir, "qa", "task-readonly", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agentBranch := g.branch

	// Agent did nothing (read-only QA run). Finalize must restore main and
	// delete the throwaway branch — zero trace.
	summary := finalizeLocalDirGit(ctx, g, slog.Default())
	if summary != "" {
		t.Errorf("no-commit run should produce no summary, got: %s", summary)
	}
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != "main" {
		t.Errorf("HEAD = %q, want main restored", cur)
	}
	branches := gitAt(t, dir, "branch", "--list", agentBranch)
	if branches != "" {
		t.Errorf("agent branch should be deleted, still present: %q", branches)
	}
}

func TestFinalize_WithCommit_KeepsBranchAndSummarizes(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-commits", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agentBranch := g.branch

	// Agent commits on the agent branch.
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "agent work")

	summary := finalizeLocalDirGit(ctx, g, slog.Default())
	if summary == "" {
		t.Fatal("a run with commits should produce a summary")
	}
	if !strings.Contains(summary, agentBranch) {
		t.Errorf("summary should name the agent branch %q: %s", agentBranch, summary)
	}
	if !strings.Contains(summary, "git checkout main") {
		t.Errorf("summary should tell the user how to return to main: %s", summary)
	}
	// Branch is kept and still holds the commit; HEAD stays on it.
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != agentBranch {
		t.Errorf("HEAD = %q, want to stay on agent branch %q", cur, agentBranch)
	}
	// main was never moved.
	mainHead := gitAt(t, dir, "rev-parse", "main")
	if mainHead != g.origHead {
		t.Errorf("main HEAD moved from %q to %q — user's branch must be untouched", g.origHead, mainHead)
	}
}

func TestPrepareAndSnapshot_DirtyTree(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	// Modify a tracked file so the tree is dirty before the agent runs.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nlocal wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-dirty1234", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if g.snapshotRef == "" {
		t.Fatal("dirty tree should have produced a snapshot ref")
	}
	// The snapshot ref must resolve to a real commit and capture the WIP.
	if sha := gitAt(t, dir, "rev-parse", g.snapshotRef); sha == "" {
		t.Fatal("snapshot ref does not resolve")
	}
	// The WIP is still in the working tree (snapshot is zero-touch).
	content, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "local wip") {
		t.Error("snapshot must not have reverted the working tree")
	}
	// Recovering the snapshot's version of README shows the WIP was captured.
	show := gitAt(t, dir, "show", g.snapshotRef+":README.md")
	if !strings.Contains(show, "local wip") {
		t.Errorf("snapshot did not capture the dirty tracked change: %q", show)
	}
}

func TestPrepareLocalDirGit_DetachedHead(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	head := gitAt(t, dir, "rev-parse", "HEAD")
	gitAt(t, dir, "checkout", "-q", "--detach", head)

	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-detached", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if g.origRef != "" {
		t.Errorf("detached HEAD should record empty origRef, got %q", g.origRef)
	}
	// No-commit finalize on a detached start restores the detached sha.
	finalizeLocalDirGit(ctx, g, slog.Default())
	if cur := gitAt(t, dir, "rev-parse", "HEAD"); cur != head {
		t.Errorf("HEAD = %q, want detached sha %q restored", cur, head)
	}
}

func TestPruneOldBackupRefs(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	head := gitAt(t, dir, "rev-parse", "HEAD")

	// A "fresh" backup ref (points at HEAD, whose committer time is now) and a
	// synthetic "old" one built from a commit dated 30 days ago.
	gitAt(t, dir, "update-ref", "refs/agora/backup/fresh", head)

	thirtyDaysAgo := fmt.Sprintf("@%d +0000", time.Now().Add(-30*24*time.Hour).Unix())
	oldSha := gitCommitAtTime(t, dir, "old snapshot", thirtyDaysAgo)
	gitAt(t, dir, "update-ref", "refs/agora/backup/stale", oldSha)

	pruneOldBackupRefs(ctx, dir, 14*24*time.Hour, slog.Default())

	refs := gitAt(t, dir, "for-each-ref", "--format=%(refname)", "refs/agora/backup/")
	if strings.Contains(refs, "refs/agora/backup/stale") {
		t.Errorf("stale backup ref should have been pruned, still present:\n%s", refs)
	}
	if !strings.Contains(refs, "refs/agora/backup/fresh") {
		t.Errorf("fresh backup ref must be kept, missing:\n%s", refs)
	}

	// TTL 0 disables pruning.
	gitAt(t, dir, "update-ref", "refs/agora/backup/stale2", oldSha)
	pruneOldBackupRefs(ctx, dir, 0, slog.Default())
	if r := gitAt(t, dir, "for-each-ref", "--format=%(refname)", "refs/agora/backup/stale2"); r == "" {
		t.Error("TTL=0 should disable pruning")
	}
}

// gitCommitAtTime creates an empty commit backdated to `when` (a git date
// spec) and returns its sha, without moving any branch.
func gitCommitAtTime(t *testing.T, dir, msg, when string) string {
	t.Helper()
	head := gitAt(t, dir, "rev-parse", "HEAD")
	cmd := exec.Command("git", "-C", dir, "commit-tree", head+"^{tree}", "-p", head, "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestFinalize_Idempotent(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-idem", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	first := finalizeLocalDirGit(ctx, g, slog.Default())
	second := finalizeLocalDirGit(ctx, g, slog.Default())
	if second != "" {
		t.Errorf("second finalize should be a no-op, got: %s", second)
	}
	_ = first
}

// GAP-2: if the human switches the shared checkout to another branch during an
// in_place run, finalize must NOT restore/delete — it leaves the human's move
// intact.
func TestFinalize_HumanSwitchedBranch_LeavesUntouched(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-humanswitch", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	agentBranch := g.branch
	// The human, unaware they're on the agent branch, creates + switches to
	// their own branch mid-run.
	gitAt(t, dir, "checkout", "-q", "-b", "human-hotfix")

	summary := finalizeLocalDirGit(ctx, g, slog.Default())
	if summary != "" {
		t.Errorf("human-switched finalize should be a no-op, got: %s", summary)
	}
	// Still on the human's branch — restore did NOT clobber it.
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != "human-hotfix" {
		t.Errorf("HEAD = %q, want human-hotfix (untouched)", cur)
	}
	// The agent branch was NOT deleted (we didn't touch anything).
	if b := gitAt(t, dir, "branch", "--list", agentBranch); b == "" {
		t.Errorf("agent branch should be left intact when the human intervened")
	}
}
