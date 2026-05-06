//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// workDirOwnerLabel returns "uid=<n> gid=<m>" for a Unix FileInfo, or
// the empty string when the platform doesn't expose ownership through
// os.FileInfo.Sys(). Used in the codex sandbox writability error so the
// user sees who owns the dir without having to run `ls -ld`.
func workDirOwnerLabel(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("uid=%d gid=%d", stat.Uid, stat.Gid)
	}
	return ""
}

// codexBwrapWillFail reports whether codex's bwrap sandbox is going to
// hard-fail on this directory even though a host-level write probe just
// succeeded. The probe only catches plain DAC violations; the
// root+CAP_DAC_OVERRIDE case sails right past it because root on the
// host can write anywhere, but bwrap drops CAP_DAC_OVERRIDE before
// invoking the model and is then subject to standard DAC.
//
// Returns true when ALL of:
//   - We're running as root (euid==0), so the host probe lied to us.
//   - The dir is not owned by root.
//   - The dir is not world-writable (so cap-dropped root inside bwrap
//     can't satisfy the perms via the "other" bits).
func codexBwrapWillFail(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	if os.Geteuid() != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if stat.Uid == 0 {
		return false
	}
	if info.Mode().Perm()&0o002 != 0 {
		return false // world-writable; bwrap can write via "other" bits
	}
	return true
}
