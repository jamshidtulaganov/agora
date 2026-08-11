package daemon

import (
	"os"
	"os/exec"
	"strings"
)

// resolveLoginShell returns an executable login shell for daemon-owned
// commands. Desktop daemons normally inherit SHELL, while container workers
// often do not. Prefer the configured shell, then fall back to the common
// Unix shells in platform-friendly order.
func resolveLoginShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		if path, err := exec.LookPath(shell); err == nil {
			return path
		}
	}
	for _, shell := range []string{"zsh", "bash", "sh"} {
		if path, err := exec.LookPath(shell); err == nil {
			return path
		}
	}
	return "/bin/sh"
}
