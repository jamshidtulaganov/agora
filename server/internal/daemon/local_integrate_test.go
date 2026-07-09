package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSquashMergeIntoBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	makeRepo(t, src)
	ctx := context.Background()

	// An agent branch with two commits off main.
	gitAt(t, src, "checkout", "-q", "-b", "agent/issue/repo")
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	gitAt(t, src, "add", ".")
	gitAt(t, src, "commit", "-q", "-m", "c1")
	os.WriteFile(filepath.Join(src, "b.txt"), []byte("b"), 0o644)
	gitAt(t, src, "add", ".")
	gitAt(t, src, "commit", "-q", "-m", "c2")
	gitAt(t, src, "checkout", "-q", "main") // leave src on main (simulates untouched dev checkout)
	mainBefore := gitAt(t, src, "rev-parse", "main")

	sha, merged, err := squashMergeIntoBranch(ctx, src, "agent/issue/repo", "sprint-1", "dev", slog.Default())
	if err != nil || !merged {
		t.Fatalf("squash merge failed: merged=%v err=%v", merged, err)
	}
	// The two agent commits became ONE squashed commit on sprint-1.
	if n := gitAt(t, src, "rev-list", "--count", "main..sprint-1"); n != "1" {
		t.Errorf("expected 1 squashed commit on sprint-1, got %s", n)
	}
	// sprint-1 has both files.
	if gitAt(t, src, "show", "sprint-1:a.txt") != "a" || gitAt(t, src, "show", "sprint-1:b.txt") != "b" {
		t.Error("sprint-1 missing the agent's files")
	}
	if gitAt(t, src, "rev-parse", "sprint-1") != sha {
		t.Error("returned sha != sprint-1 tip")
	}
	// The developer's checkout is untouched: still on main, clean, main unmoved.
	if b := gitAt(t, src, "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Errorf("src checkout moved to %q — must stay on main", b)
	}
	if gitAt(t, src, "rev-parse", "main") != mainBefore {
		t.Error("main moved during integration")
	}
	if s := gitAt(t, src, "status", "--porcelain"); s != "" {
		t.Errorf("src dirty after integration: %q", s)
	}
	// No dangling temp worktree.
	if strings.Contains(gitAt(t, src, "worktree", "list"), "agora-integrate") {
		t.Error("temp integration worktree leaked")
	}
}

func TestSquashMergeIntoBranch_NothingToMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	makeRepo(t, src)
	ctx := context.Background()
	// Agent branch identical to main → nothing to land.
	gitAt(t, src, "branch", "agent/issue/repo")
	_, merged, err := squashMergeIntoBranch(ctx, src, "agent/issue/repo", "sprint-1", "dev", slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged {
		t.Error("should report nothing merged when the agent branch adds nothing")
	}
}

func TestIntegrateWorktreeIssue_MultiRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "backend"))
	makeRepo(t, filepath.Join(parent, "frontend"))
	ctx := context.Background()
	env := filepath.Join(t.TempDir(), "wt", "issue-i")

	run, err := provisionLocalWorktrees(ctx, parent, "issue-integ", env, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	// Agent edits BOTH repos (uncommitted — integrate must commit them).
	for _, r := range []string{"backend", "frontend"} {
		os.WriteFile(filepath.Join(env, r, "feature.txt"), []byte(r+" work"), 0o644)
	}
	_ = run

	results, err := integrateWorktreeIssue(ctx, env, "sprint-42", "dev-agent", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 repo results, got %d", len(results))
	}
	for _, res := range results {
		if !res.Merged {
			t.Errorf("%s: expected merged", res.Repo)
		}
		src := filepath.Join(parent, res.Repo)
		// sprint-42 has the agent's file; the dev checkout stayed on main clean.
		if gitAt(t, src, "show", "sprint-42:feature.txt") != res.Repo+" work" {
			t.Errorf("%s: sprint-42 missing the work", res.Repo)
		}
		if b := gitAt(t, src, "symbolic-ref", "--short", "HEAD"); b != "main" {
			t.Errorf("%s: source moved to %q", res.Repo, b)
		}
		if s := gitAt(t, src, "status", "--porcelain"); s != "" {
			t.Errorf("%s: source dirty", res.Repo)
		}
	}
	// The worktree env is reclaimed.
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Error("worktree env should be removed after integration")
	}
}
