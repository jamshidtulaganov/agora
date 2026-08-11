package daemon

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstanceLock prevents two local processes from registering the same
// machine-scoped daemon identity against the same backend at once. Profiles
// intentionally share daemon_id, but their workspace roots and in-process
// path locks do not; without this guard a CLI daemon and Desktop daemon can
// claim the same task concurrently and corrupt each other's runtime state.
type InstanceLock struct {
	file *os.File
	path string
}

// AcquireInstanceLock acquires the machine-wide lock for cfg's backend and
// daemon identity. Different backends remain independent, so local/dev and
// production profiles may still run side by side.
func AcquireInstanceLock(cfg Config) (*InstanceLock, error) {
	server := strings.TrimSpace(cfg.ServerBaseURL)
	daemonID := strings.TrimSpace(cfg.DaemonID)
	if server == "" || daemonID == "" {
		return nil, fmt.Errorf("daemon instance lock requires server URL and daemon ID")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve daemon lock home: %w", err)
	}
	dir := filepath.Join(home, ".agora", "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create daemon lock directory: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(server) + "\x00" + daemonID))
	path := filepath.Join(dir, fmt.Sprintf("daemon-%x.lock", sum[:12]))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon instance lock: %w", err)
	}
	if err := tryLockInstanceFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another Agora daemon is already running for %s on this machine; stop it before starting profile %q: %w", server, cfg.Profile, err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.Seek(0, 0)
		_, _ = fmt.Fprintf(f, "pid=%d\nprofile=%s\nserver=%s\nworkspaces_root=%s\n", os.Getpid(), cfg.Profile, server, cfg.WorkspacesRoot)
		_ = f.Sync()
	}
	return &InstanceLock{file: f, path: path}, nil
}

// Release unlocks the instance. The lock file is intentionally retained so
// another process can safely open the same inode while this process exits.
func (l *InstanceLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockInstanceFile(l.file)
	_ = l.file.Close()
	l.file = nil
}
