package daemon

import (
	"os"
	"os/exec"
	"testing"
)

func TestResolveLoginShellUsesConfiguredExecutable(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	if got := resolveLoginShell(); got != shell {
		t.Fatalf("resolveLoginShell() = %q, want %q", got, shell)
	}
}

func TestResolveLoginShellFallsBackWhenConfiguredShellIsMissing(t *testing.T) {
	t.Setenv("SHELL", "/definitely/missing/agora-shell")
	got := resolveLoginShell()
	if got == "/definitely/missing/agora-shell" {
		t.Fatal("resolveLoginShell returned a missing configured shell")
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("fallback shell %q is not available: %v", got, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("fallback shell %q is not executable", got)
	}
}
