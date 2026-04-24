//go:build !windows

package main

// vault_lock_unix.go — cross-process vault lock via flock(2). The
// in-process mutex in VaultStore is not enough if a user runs
// `yaver vault add X` in two terminals at the same time — each
// process has its own mutex, and the last-saver wins silently. A
// POSIX advisory lock on a sidecar file (~/.yaver/vault.enc.lock)
// serializes write operations across all processes owned by the
// same user.
//
// Scope: held only for the duration of one save(). We don't hold it
// across the entire VaultStore lifetime because a long-running agent
// would block every `yaver vault` invocation for its whole uptime.

import (
	"os"
	"syscall"
)

// vaultFileLock returns a file handle whose advisory lock is held
// exclusively (LOCK_EX). Release by calling .Close() on the handle.
// lockPath is typically <vault-path>.lock.
//
// On any platform where flock(2) isn't available we fall back to a
// no-op — the in-process mutex is still correct; it just doesn't
// protect against sibling processes. The build tag keeps this file
// unix-only; vault_lock_windows.go provides the Windows variant.
func vaultFileLock(lockPath string) (*os.File, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// vaultFileUnlock releases + closes the lock file handle.
func vaultFileUnlock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
