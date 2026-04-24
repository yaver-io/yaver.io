//go:build windows

package main

// vault_lock_windows.go — Windows fallback for the cross-process
// vault lock. Uses LockFileEx via x/sys/windows so advisory-like
// exclusion still works. This keeps `yaver vault add` safe against
// concurrent invocations from multiple terminals on Windows too.

import (
	"os"

	"golang.org/x/sys/windows"
)

func vaultFileLock(lockPath string) (*os.File, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	// LOCKFILE_EXCLUSIVE_LOCK — blocking exclusive.
	if err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func vaultFileUnlock(f *os.File) {
	if f == nil {
		return
	}
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
	_ = f.Close()
}
