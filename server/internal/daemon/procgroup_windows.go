//go:build windows

package daemon

import "os/exec"

// The daemon only ever runs on Unix hosts; these Windows stubs exist purely so
// the `agora` CLI (which embeds the daemon package for `agora daemon start`)
// cross-compiles for windows/amd64. Windows lacks Unix process groups, so the
// group semantics degrade to a plain single-process kill — acceptable because
// this code path is never executed on Windows.

// setProcessGroup is a no-op on Windows.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup hard-kills the leader process. Children it spawned are not
// signalled (no process-group equivalent), which is fine for a compile-only
// target.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
