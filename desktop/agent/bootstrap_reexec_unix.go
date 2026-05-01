//go:build !windows

package main

// reexecReplaceProcess replaces the current process image with
// `yaver serve`. Used by runBootstrapServe after a token lands so
// the bootstrap loop seamlessly hands control to the authenticated
// serve loop without spawning a child.
//
// Why this matters: systemd's Type=simple watches the PID of the
// ExecStart process. The previous implementation used
// `osexec.Command(...).Run()` which forked a NEW pid for the
// authenticated serve and then exited the original. systemd saw
// the original PID exit and deactivated the unit, leaving the new
// child as an orphan that would die on the next restart attempt
// (or just sit there with no supervision). Replacing the process
// image keeps the same PID, so systemd's view never changes — the
// "service" stays the same unit, the same PID, the same lifecycle,
// just running a different command line now.
//
// macOS launchd is more forgiving than systemd here, but the same
// approach works: launchd won't notice anything either. The Linux
// (systemd) path is the load-bearing one.

import (
	"os"
	"syscall"
)

func reexecReplaceProcess(execPath string, args []string) error {
	return syscall.Exec(execPath, args, os.Environ())
}
