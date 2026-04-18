//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// applyDetachSysProcAttr marks the child process as a session leader
// so it survives the parent exiting. Linux/macOS only — Windows has
// its own detach story via cmd /c start, see _windows.go.
func applyDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
