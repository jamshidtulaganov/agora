package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// This file stress-tests the local_directory git-safety and approval code —
// the parts that operate on a user's REAL repository and consent file. The
// goal is to break invariants under concurrency and adversarial git states:
//   - the allowlist file is never corrupted by concurrent writers;
//   - repeated / concurrent (serialized) task runs never leave the user's repo
//     dirty, on a stray branch, or with leaked refs/worktrees;
//   - weird git states (empty repo, detached HEAD, huge dirty trees, symlinked
//     paths) degrade safely instead of failing or mangling data.
// Run with -race to catch data races in the shared allowlist/lock paths.

// ---- Allowlist under concurrency ---------------------------------------------

func TestStress_ConcurrentAllowlistMutations(t *testing.T) {
	setTestHome(t)
	// Pre-create real dirs so ApproveLocalDir's is-dir check passes.
	dirs := make([]string, 12)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}

	var wg sync.WaitGroup
	var corrupt atomic.Int32
	// Many goroutines approve, revoke, and list the same file concurrently.
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				d := dirs[(g+i)%len(dirs)]
				switch i % 3 {
				case 0:
					_, _, _ = ApproveLocalDir(d)
				case 1:
					_, _, _ = RevokeLocalDir(d)
				case 2:
					// A concurrent reader must never see a torn/invalid file.
					if _, err := loadLocalDirAllowlist(); err != nil {
						corrupt.Add(1)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if n := corrupt.Load(); n != 0 {
		t.Errorf("allowlist load saw %d corrupt/torn reads under concurrency", n)
	}
	// Final file must be valid JSON and every entry a dir we know.
	entries, err := loadLocalDirAllowlist()
	if err != nil {
		t.Fatalf("final allowlist is corrupt: %v", err)
	}
	known := map[string]bool{}
	for _, d := range dirs {
		known[filepath.Clean(d)] = true
	}
	for _, e := range entries {
		if !known[filepath.Clean(e)] {
			t.Errorf("final allowlist has unexpected entry %q", e)
		}
	}
}

// ---- Serialized concurrent task runs on ONE repo -----------------------------

// runOneLocalTask mimics the real handleTask sequence for a git local_directory:
// acquire the path lock, git-prep, do some "agent work", finalize, release.
func runOneLocalTask(t *testing.T, locker *LocalPathLocker, dir, agentName, taskID string, commit bool) {
	t.Helper()
	ctx := context.Background()
	realPath, _ := filepath.EvalSymlinks(dir)
	release, err := locker.Acquire(ctx, realPath, taskID, nil)
	if err != nil {
		t.Errorf("acquire: %v", err)
		return
	}
	defer release()

	g, err := prepareLocalDirGit(ctx, dir, agentName, taskID, slog.Default())
	if err != nil {
		t.Errorf("prepare %s: %v", taskID, err)
		return
	}
	if commit {
		f := filepath.Join(dir, "work-"+taskID+".txt")
		if err := os.WriteFile(f, []byte(taskID), 0o644); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		gitAt(t, dir, "add", ".")
		gitAt(t, dir, "commit", "-q", "-m", "work "+taskID)
	}
	finalizeLocalDirGit(ctx, g, slog.Default())
}

func TestStress_SerializedConcurrentTasksLeaveRepoClean(t *testing.T) {
	dir := gitFixture(t)
	locker := NewLocalPathLocker()
	origMain := gitAt(t, dir, "rev-parse", "main")

	var wg sync.WaitGroup
	// 30 tasks contend for the same repo; half commit, half are read-only.
	// Task IDs must have distinct 8-char shortID prefixes (real ids are UUIDs),
	// so a plain zero-padded counter is the whole shortID.
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runOneLocalTask(t, locker, dir,
				fmt.Sprintf("agent%d", i%4),
				fmt.Sprintf("%08d", i),
				i%2 == 0,
			)
		}(i)
	}
	wg.Wait()

	// Invariant 1: main was never moved by any task (commits go on agent branches).
	if got := gitAt(t, dir, "rev-parse", "main"); got != origMain {
		t.Errorf("main moved from %s to %s — user's branch must never move", origMain, got)
	}
	// Invariant 2: after all tasks, exactly the committing tasks left an agent
	// branch; read-only tasks left none.
	branches := gitAt(t, dir, "branch", "--list", "agent/*", "--format=%(refname:short)")
	committed := 0
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			committed++
		}
	}
	got := 0
	if strings.TrimSpace(branches) != "" {
		got = len(strings.Split(strings.TrimSpace(branches), "\n"))
	}
	if got != committed {
		t.Errorf("agent branches = %d, want %d (one per committing task, none for read-only):\n%s", got, committed, branches)
	}
	// Invariant 3: HEAD is a valid ref and the index has no merge conflicts.
	if _, err := gitOutput(context.Background(), dir, "rev-parse", "HEAD"); err != nil {
		t.Errorf("HEAD unresolved after stress: %v", err)
	}
}

// ---- Re-claim: branch already exists -----------------------------------------

func TestStress_ReclaimReusesExistingBranch(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	// First prep creates agent/dev/<task>.
	g1, err := prepareLocalDirGit(ctx, dir, "dev", "task-reclaim01", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: no finalize. Put HEAD back on main to mimic a fresh
	// re-claim starting point, leaving the branch behind.
	gitAt(t, dir, "checkout", "-q", "main")

	// Second prep with the SAME task id must reuse the branch, not error.
	g2, err := prepareLocalDirGit(ctx, dir, "dev", "task-reclaim01", slog.Default())
	if err != nil {
		t.Fatalf("re-claim prepare should reuse existing branch, got: %v", err)
	}
	if g1.branch != g2.branch {
		t.Errorf("branch names differ across re-claim: %q vs %q", g1.branch, g2.branch)
	}
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != g2.branch {
		t.Errorf("HEAD = %q, want reused branch %q", cur, g2.branch)
	}
}

// ---- Huge dirty tree ---------------------------------------------------------

func TestStress_HugeDirtyTreeSnapshotAndSummaryCap(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	// Seed 300 tracked files, commit, then modify all of them → a big dirty tree.
	for i := 0; i < 300; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("v1"), 0o644)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "seed 300")
	for i := 0; i < 300; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("v2-wip"), 0o644)
	}

	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-huge0001", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if g.snapshotRef == "" {
		t.Fatal("huge dirty tree must be snapshotted")
	}
	// Agent commits a change across all files → a 300-line diffstat.
	for i := 0; i < 300; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("v3-agent"), 0o644)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "agent touched all")

	summary := finalizeLocalDirGit(ctx, g, slog.Default())
	if summary == "" {
		t.Fatal("expected summary")
	}
	// Summary must be capped, not a 300-line dump.
	if lines := strings.Count(summary, "\n"); lines > localDirSummaryLineCap+20 {
		t.Errorf("summary not capped: %d lines", lines)
	}
	if !strings.Contains(summary, "more lines") {
		t.Errorf("capped summary should note truncation:\n%s", summary)
	}
	// The pre-run WIP is still recoverable from the snapshot.
	if v := gitAt(t, dir, "show", g.snapshotRef+":f000.txt"); v != "v2-wip" {
		t.Errorf("snapshot lost the pre-run WIP: %q", v)
	}
}

// ---- Repeated cycles: no ref/branch leak -------------------------------------

func TestStress_RepeatedReadOnlyCyclesNoLeak(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		g, err := prepareLocalDirGit(ctx, dir, "qa", fmt.Sprintf("task-cycle%04d", i), slog.Default())
		if err != nil {
			t.Fatalf("cycle %d prepare: %v", i, err)
		}
		finalizeLocalDirGit(ctx, g, slog.Default())
	}
	// No agent branches should survive read-only cycles.
	if b := gitAt(t, dir, "branch", "--list", "agent/*"); strings.TrimSpace(b) != "" {
		t.Errorf("read-only cycles leaked agent branches:\n%s", b)
	}
	// Still on main, tree clean.
	if cur := gitAt(t, dir, "symbolic-ref", "--short", "HEAD"); cur != "main" {
		t.Errorf("HEAD drifted to %q after cycles", cur)
	}
	if st := gitAt(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("repo dirty after read-only cycles:\n%s", st)
	}
}

// ---- Adversarial git states --------------------------------------------------

// An empty repo (git init, no commits, unborn HEAD) must NOT hard-fail the
// task — it should degrade to plain in-place behavior (no branch), like a
// non-git folder, rather than erroring on `rev-parse HEAD`.
// A repo reached through a symlinked path (the daemon resolves RealPath for
// locking, but git-prep runs on AbsPath) must still branch/snapshot correctly.
func TestEdge_SymlinkedRepoPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	real := gitFixture(t)
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "repo-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ctx := context.Background()
	g, err := prepareLocalDirGit(ctx, link, "dev", "task-symlink01", slog.Default())
	if err != nil {
		t.Fatalf("symlinked repo prepare: %v", err)
	}
	if g == nil || g.branch == "" {
		t.Fatal("symlinked git repo should get branch isolation")
	}
	// A no-commit run restores cleanly through the symlink.
	finalizeLocalDirGit(ctx, g, slog.Default())
	if cur := gitAt(t, real, "symbolic-ref", "--short", "HEAD"); cur != "main" {
		t.Errorf("HEAD = %q via symlink, want main restored", cur)
	}
}

// Concurrent approve+revoke of the SAME path must never corrupt the file or
// leave the daemon unable to load it (the atomic temp+rename is the guard).
func TestStress_ConcurrentApproveRevokeSamePath(t *testing.T) {
	setTestHome(t)
	proj := t.TempDir()
	var wg sync.WaitGroup
	var loadErrs atomic.Int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				if (g+i)%2 == 0 {
					_, _, _ = ApproveLocalDir(proj)
				} else {
					_, _, _ = RevokeLocalDir(proj)
				}
				if _, _, _, err := ListLocalDirs(); err != nil {
					loadErrs.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if n := loadErrs.Load(); n != 0 {
		t.Errorf("ListLocalDirs saw %d corrupt reads under concurrent approve/revoke", n)
	}
	// File must end valid (approved or not, but never torn).
	if _, _, _, err := ListLocalDirs(); err != nil {
		t.Fatalf("final list corrupt: %v", err)
	}
}

// A leftover backup ref from a crashed prior run with the same task id must not
// break the snapshot step (update-ref overwrites it).
func TestEdge_SnapshotOverExistingBackupRef(t *testing.T) {
	dir := gitFixture(t)
	ctx := context.Background()
	head := gitAt(t, dir, "rev-parse", "HEAD")
	// Pre-seed the backup ref this task id would use.
	gitAt(t, dir, "update-ref", backupRefPrefix+shortID("task-dup00001"), head)

	// Dirty the tree so a fresh snapshot is taken.
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\nwip\n"), 0o644)
	g, err := prepareLocalDirGit(ctx, dir, "dev", "task-dup00001", slog.Default())
	if err != nil {
		t.Fatalf("prepare over existing backup ref: %v", err)
	}
	if g.snapshotRef == "" {
		t.Fatal("expected a fresh snapshot")
	}
	// The ref now points at the fresh snapshot (captures the wip), not the old head.
	if show := gitAt(t, dir, "show", g.snapshotRef+":README.md"); !strings.Contains(show, "wip") {
		t.Errorf("backup ref not overwritten with fresh snapshot: %q", show)
	}
}

func TestEdge_EmptyRepoNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitAt(t, dir, "init", "-q", "-b", "main")

	g, err := prepareLocalDirGit(context.Background(), dir, "dev", "task-empty001", slog.Default())
	if err != nil {
		t.Fatalf("empty repo should degrade gracefully, not error: %v", err)
	}
	// Unborn HEAD → degrade to plain in-place (no git state), exactly like a
	// non-git folder. finalize on nil is a safe no-op.
	if g != nil {
		t.Errorf("unborn-HEAD repo should degrade to nil localDirGit, got %+v", g)
	}
	if s := finalizeLocalDirGit(context.Background(), g, slog.Default()); s != "" {
		t.Errorf("finalize on degraded repo should be empty, got %q", s)
	}
	// The repo must be untouched — still unborn, no stray branches.
	if b := gitAt(t, dir, "branch", "--list"); strings.TrimSpace(b) != "" {
		t.Errorf("unborn repo should have no branches after degrade:\n%s", b)
	}
}
