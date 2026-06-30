package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// sprintDepProvider describes how one package manager's installed-deps dir is
// shared across worktrees on a sprint branch: the dir that holds the deps, the
// lockfiles that key a unique install, and the command that populates it.
type sprintDepProvider struct {
	depDir     string   // node_modules | vendor
	lockFiles  []string // first present one digests the install
	installCmd string   // shell command, run in the worktree, that fills depDir
}

// detectSprintDepProvider picks a repo's provider by its manifest. Only managers
// whose install output is a self-contained, relocatable directory are shareable;
// v1 covers Node (the demo + the Agora repo) and Composer (the SD PHP repos).
// Returns ok=false for repos with no shareable deps (pure-source repos cost no
// extra disk, so there is nothing to share).
func detectSprintDepProvider(repo string) (sprintDepProvider, bool) {
	if fileExists(filepath.Join(repo, "package.json")) {
		install := "npm install"
		switch {
		case fileExists(filepath.Join(repo, "pnpm-lock.yaml")):
			install = "pnpm install"
		case fileExists(filepath.Join(repo, "yarn.lock")):
			install = "yarn install"
		}
		return sprintDepProvider{
			depDir:     "node_modules",
			lockFiles:  []string{"pnpm-lock.yaml", "package-lock.json", "yarn.lock"},
			installCmd: install,
		}, true
	}
	if fileExists(filepath.Join(repo, "composer.json")) {
		return sprintDepProvider{
			depDir:     "vendor",
			lockFiles:  []string{"composer.lock"},
			installCmd: "composer install --no-interaction",
		}, true
	}
	return sprintDepProvider{}, false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sprintDepDigest returns a short key for a unique shared install: the sha256 of
// the first present lockfile, or "nolock" when none exists (deps then share per
// repo+branch). A lockfile change forks a NEW shared dir and the stale one ages
// out, so installs never mix incompatible dependency sets.
func sprintDepDigest(repo string, lockFiles []string) string {
	for _, lf := range lockFiles {
		if b, err := os.ReadFile(filepath.Join(repo, lf)); err == nil {
			sum := sha256.Sum256(b)
			return hex.EncodeToString(sum[:])[:16]
		}
	}
	return "nolock"
}

// sanitizeBranchForPath turns a git branch name into a single safe path segment
// (branches contain "/" and could contain ".."); used for the shared-deps dir.
func sanitizeBranchForPath(b string) string {
	return strings.NewReplacer("/", "-", " ", "-", "..", "-").Replace(b)
}

// runDepInstall runs install in repo via a login shell (so fnm/nvm-managed node
// is on PATH). A package var so tests can stub the real install. Returns trimmed
// combined output.
var runDepInstall = func(repo, install string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cmd := exec.Command(shell, "-lc", install)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// lockForSharedDep returns the mutex guarding a shared dep target path, so two
// concurrent sprint tasks never populate the same shared dir at once.
func (d *Daemon) lockForSharedDep(target string) *sync.Mutex {
	if l, ok := d.sharedDepLocks.Load(target); ok {
		return l.(*sync.Mutex)
	}
	newLock := &sync.Mutex{}
	actual, _ := d.sharedDepLocks.LoadOrStore(target, newLock)
	return actual.(*sync.Mutex)
}

// linkSharedSprintDeps shares each sprint worktree's installed-deps dir across
// all worktrees on the same sprint branch, so N co-editors on one sprint branch
// don't cost N× node_modules/vendor on disk — the reason sprint mode exists.
//
// Per repo the deps live ONCE at
//
//	{wsRoot}/{wsID}/.sprint-deps/{branch}/{repo}/{lockDigest}/{depDir}
//
// a stable path OUTSIDE any task env root (so per-task GC never reclaims it), and
// each worktree's depDir is a symlink to it. The shared dir is populated once,
// under a per-target lock (race-free across concurrent tasks): the first task
// installs in its own worktree and the result is MOVED to the shared dir; later
// tasks symlink to the populated dir and skip install. Best-effort — any failure
// leaves the worktree with a normal per-task install (slower, more disk, but
// correct), never a broken tree.
func (d *Daemon) linkSharedSprintDeps(workdir, wsID, sprintBranch string, log *slog.Logger) {
	sharedRoot := filepath.Join(d.cfg.WorkspacesRoot, wsID, ".sprint-deps", sanitizeBranchForPath(sprintBranch))
	for _, repo := range gitReposUnder(workdir) {
		prov, ok := detectSprintDepProvider(repo)
		if !ok {
			continue
		}
		digest := sprintDepDigest(repo, prov.lockFiles)
		target := filepath.Join(sharedRoot, filepath.Base(repo), digest, prov.depDir)
		worktreeDep := filepath.Join(repo, prov.depDir)

		// Already symlinked to the right target → nothing to do.
		if cur, err := os.Readlink(worktreeDep); err == nil {
			if cur == target {
				continue
			}
			_ = os.Remove(worktreeDep) // stale symlink (a previous digest) — replace
		}

		lock := d.lockForSharedDep(target)
		lock.Lock()
		if err := shareOneDepDir(repo, worktreeDep, target, prov, log); err != nil {
			log.Warn("sprint-worktree: shared deps link failed; falling back to per-task install",
				"repo", filepath.Base(repo), "error", err)
		}
		lock.Unlock()
	}
}

// shareOneDepDir populates the shared target once and symlinks the worktree dep
// dir to it. Must be called with the target's lock held.
func shareOneDepDir(repo, worktreeDep, target string, prov sprintDepProvider, log *slog.Logger) error {
	if dirPopulated(target) {
		// Shared install exists: drop any real worktree dir, symlink to shared.
		return replaceWithSymlink(worktreeDep, target)
	}
	// Not yet shared. Adopt an existing worktree install if present; else install.
	if !dirPopulated(worktreeDep) {
		if out, err := runDepInstall(repo, prov.installCmd); err != nil {
			return fmt.Errorf("install %q: %s: %w", prov.installCmd, firstLine(out), err)
		}
	}
	if !dirPopulated(worktreeDep) {
		return fmt.Errorf("install produced no %s", prov.depDir)
	}
	// Move the fresh install to the shared location, then symlink it back.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Rename(worktreeDep, target); err != nil {
		return fmt.Errorf("move to shared: %w", err) // cross-device/race → caller falls back
	}
	log.Info("sprint-worktree: shared deps populated", "repo", filepath.Base(repo), "target", target)
	return os.Symlink(target, worktreeDep)
}

// replaceWithSymlink removes whatever is at link (real dir or stale symlink) and
// points it at target.
func replaceWithSymlink(link, target string) error {
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			if err := os.RemoveAll(link); err != nil {
				return err
			}
		} else {
			_ = os.Remove(link)
		}
	}
	return os.Symlink(target, link)
}

// dirPopulated reports whether dir exists and has at least one entry.
func dirPopulated(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
