package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetectSprintDepProvider(t *testing.T) {
	mk := func(files ...string) string {
		dir := t.TempDir()
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	cases := []struct {
		name      string
		repo      string
		wantOK    bool
		wantDir   string
		wantInst  string
	}{
		{"pnpm", mk("package.json", "pnpm-lock.yaml"), true, "node_modules", "pnpm install"},
		{"npm", mk("package.json", "package-lock.json"), true, "node_modules", "npm install"},
		{"yarn", mk("package.json", "yarn.lock"), true, "node_modules", "yarn install"},
		{"node-nolock", mk("package.json"), true, "node_modules", "npm install"},
		{"composer", mk("composer.json", "composer.lock"), true, "vendor", "composer install --no-interaction"},
		{"none", mk("go.mod"), false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prov, ok := detectSprintDepProvider(c.repo)
			if ok != c.wantOK {
				t.Fatalf("ok=%v, want %v", ok, c.wantOK)
			}
			if ok && (prov.depDir != c.wantDir || prov.installCmd != c.wantInst) {
				t.Fatalf("got {%s,%s}, want {%s,%s}", prov.depDir, prov.installCmd, c.wantDir, c.wantInst)
			}
		})
	}
}

func TestSprintDepDigest(t *testing.T) {
	dir := t.TempDir()
	lf := []string{"pnpm-lock.yaml"}
	if got := sprintDepDigest(dir, lf); got != "nolock" {
		t.Fatalf("no lockfile → %q, want nolock", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	d1 := sprintDepDigest(dir, lf)
	if d1 == "nolock" || len(d1) != 16 {
		t.Fatalf("digest = %q, want a 16-char hash", d1)
	}
	// Same content → same digest (stable across worktrees → shared install).
	if d2 := sprintDepDigest(dir, lf); d2 != d1 {
		t.Fatalf("digest not stable: %q vs %q", d1, d2)
	}
	// Changed lockfile → new digest (forks a fresh shared dir).
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d3 := sprintDepDigest(dir, lf); d3 == d1 {
		t.Fatal("digest unchanged after lockfile edit — would mix dependency sets")
	}
}

// TestLinkSharedSprintDeps_ShareAcrossWorktrees proves the disk bound: two
// worktrees on one sprint branch share a SINGLE installed-deps dir — install
// runs once, the second worktree symlinks to the first's shared install, and the
// shared dir lives outside any task env root (GC-safe).
func TestLinkSharedSprintDeps_ShareAcrossWorktrees(t *testing.T) {
	var installs int32
	orig := runDepInstall
	runDepInstall = func(repo, install string) (string, error) {
		atomic.AddInt32(&installs, 1)
		pkg := filepath.Join(repo, "node_modules", "left-pad")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			return "", err
		}
		return "", os.WriteFile(filepath.Join(pkg, "index.js"), []byte("module.exports=1"), 0o644)
	}
	t.Cleanup(func() { runDepInstall = orig })

	wsRoot := t.TempDir()
	d := &Daemon{cfg: Config{WorkspacesRoot: wsRoot}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	mkRepo := func(label string) (workdir, repo string) {
		workdir = filepath.Join(t.TempDir(), label)
		repo = filepath.Join(workdir, "demo")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		sprintGit(t, repo, "init")
		if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return workdir, repo
	}
	wdA, repoA := mkRepo("A")
	wdB, repoB := mkRepo("B")

	d.linkSharedSprintDeps(wdA, "ws1", "feat/sprint-x", log)
	d.linkSharedSprintDeps(wdB, "ws1", "feat/sprint-x", log)

	if installs != 1 {
		t.Fatalf("install ran %d times, want exactly 1 (B must reuse A's shared install)", installs)
	}
	tA, errA := os.Readlink(filepath.Join(repoA, "node_modules"))
	tB, errB := os.Readlink(filepath.Join(repoB, "node_modules"))
	if errA != nil || errB != nil {
		t.Fatalf("node_modules not symlinks: A=%v B=%v", errA, errB)
	}
	if tA != tB {
		t.Fatalf("worktrees point at different shared targets: %q vs %q", tA, tB)
	}
	if !fileExists(filepath.Join(repoB, "node_modules", "left-pad", "index.js")) {
		t.Fatal("shared deps not reachable through B's symlink")
	}
	// Shared dir lives under wsRoot/.sprint-deps (branch slashes sanitized),
	// outside task env roots so per-task GC never reclaims it.
	wantSeg := filepath.Join("ws1", ".sprint-deps", "feat-sprint-x")
	if !strings.Contains(tA, wantSeg) {
		t.Fatalf("shared target %q not under %q", tA, wantSeg)
	}
}

// TestLinkSharedSprintDeps_AdoptsExistingInstall covers the case where the
// worktree already has a real node_modules (the agent installed): it is MOVED to
// the shared dir and symlinked back, with no extra install.
func TestLinkSharedSprintDeps_AdoptsExistingInstall(t *testing.T) {
	var installs int32
	orig := runDepInstall
	runDepInstall = func(repo, install string) (string, error) { atomic.AddInt32(&installs, 1); return "", nil }
	t.Cleanup(func() { runDepInstall = orig })

	wsRoot := t.TempDir()
	d := &Daemon{cfg: Config{WorkspacesRoot: wsRoot}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	workdir := filepath.Join(t.TempDir(), "A")
	repo := filepath.Join(workdir, "demo")
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	sprintGit(t, repo, "init")
	_ = os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{}`), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "node_modules", "dep", "x.js"), []byte("1"), 0o644)

	d.linkSharedSprintDeps(workdir, "ws1", "sprint-x", log)

	if installs != 0 {
		t.Fatalf("install ran %d times, want 0 (existing install must be adopted)", installs)
	}
	if _, err := os.Readlink(filepath.Join(repo, "node_modules")); err != nil {
		t.Fatalf("node_modules should be a symlink after adoption: %v", err)
	}
	if !fileExists(filepath.Join(repo, "node_modules", "dep", "x.js")) {
		t.Fatal("adopted install not reachable through symlink")
	}
}
