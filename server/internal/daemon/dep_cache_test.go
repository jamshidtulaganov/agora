package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedDepCacheEnv(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()

	env := sharedDepCacheEnv(root, logger)

	// Every declared package-manager var is present and rooted under the shared
	// .depcache dir, and the directory it points at actually exists on disk.
	if len(env) != len(depCacheDirs) {
		t.Fatalf("got %d cache vars, want %d", len(env), len(depCacheDirs))
	}
	wantRoot := filepath.Join(root, depCacheDirName)
	for key := range depCacheDirs {
		p, ok := env[key]
		if !ok {
			t.Errorf("missing cache var %q", key)
			continue
		}
		rel, err := filepath.Rel(wantRoot, p)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) >= 2 && rel[:2] == ".." {
			t.Errorf("%s=%q escapes shared root %q", key, p, wantRoot)
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("%s dir %q not created: %v", key, p, err)
		}
	}
}

func TestSharedDepCacheEnvEmptyRoot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if env := sharedDepCacheEnv("", logger); len(env) != 0 {
		t.Errorf("empty workspacesRoot must yield no cache vars, got %v", env)
	}
}
