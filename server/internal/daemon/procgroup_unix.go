//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the entire tree it
// spawns (a dev server + the workers it forks, a headless Chrome + its zygotes)
// can be signalled at once via killProcessGroup. Unix-only; the Windows build
// (compile-only — the daemon never runs there) is a no-op.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup terminates cmd and every child it spawned by signalling the
// whole process group (negative pid), then hard-kills the leader as a backstop.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = cmd.Process.Kill()
}
