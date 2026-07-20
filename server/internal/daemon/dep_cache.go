package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
)

// depCacheDirName is the shared dependency-cache root under WorkspacesRoot. It
// sits beside the bare repo-mirror cache (.repos) and, like it, persists across
// tasks and daemon restarts. The point is that per-task worktrees reuse ONE
// package store instead of reinstalling dependencies from scratch on every run
// — the biggest remaining cold cost once repo materialization is already served
// from the mirror cache.
const depCacheDirName = ".depcache"

// depCacheDirs maps each package-manager env var to its subdir under the shared
// cache root. pnpm reads npmrc-style config from npm_config_* env vars, so
// npm_config_store_dir points its content-addressed store at the shared path —
// which turns `pnpm install` into a hardlink operation (seconds, not a full
// network+build install) because .depcache and the worktrees both live under
// WorkspacesRoot (same filesystem ⇒ hardlinks resolve). The npm/yarn/go/cargo/
// pip caches are all designed for concurrent shared access, so parallel
// worktrees share them safely.
var depCacheDirs = map[string]string{
	"npm_config_cache":     "npm",
	"npm_config_store_dir": "pnpm-store",
	"PNPM_HOME":            "pnpm-home",
	"YARN_CACHE_FOLDER":    "yarn",
	"GOMODCACHE":           "go/mod",
	"GOCACHE":              "go/build",
	"CARGO_HOME":           "cargo",
	"PIP_CACHE_DIR":        "pip",
}

// sharedDepCacheEnv returns environment variables that point common package
// managers at a single persistent, shared cache under workspacesRoot.
//
// Best-effort: a directory it cannot create is simply omitted (logged), never
// fatal — a cache miss must never block a task. Returns an empty map when
// workspacesRoot is unset.
func sharedDepCacheEnv(workspacesRoot string, logger *slog.Logger) map[string]string {
	env := make(map[string]string, len(depCacheDirs))
	if workspacesRoot == "" {
		return env
	}
	root := filepath.Join(workspacesRoot, depCacheDirName)
	for key, sub := range depCacheDirs {
		p := filepath.Join(root, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			if logger != nil {
				logger.Warn("dep-cache: create dir failed (skipping)", "key", key, "path", p, "error", err)
			}
			continue
		}
		env[key] = p
	}
	return env
}
