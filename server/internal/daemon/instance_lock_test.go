package daemon

import (
	"strings"
	"testing"
)

func TestAcquireInstanceLockRejectsDuplicateBackendIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Config{
		ServerBaseURL:  "https://agora.example.test",
		DaemonID:       "019ec85a-d8e7-78e5-8376-ade01f1a81d9",
		Profile:        "desktop-agora.example.test",
		WorkspacesRoot: "/tmp/agora-desktop",
	}
	first, err := AcquireInstanceLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Release)

	if _, err := AcquireInstanceLock(cfg); err == nil || !strings.Contains(err.Error(), "another Agora daemon is already running") {
		t.Fatalf("duplicate lock error = %v", err)
	}

	first.Release()
	second, err := AcquireInstanceLock(cfg)
	if err != nil {
		t.Fatalf("lock was not reusable after release: %v", err)
	}
	second.Release()
}
