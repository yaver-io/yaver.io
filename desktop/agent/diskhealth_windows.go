//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// statfsGB returns total, free disk space in GB for a Windows
// drive root (e.g. "C:\\"). Uses GetDiskFreeSpaceExW from kernel32.
func statfsGB(mount string) (totalGB, freeGB float64, ok bool) {
	mod := syscall.NewLazyDLL("kernel32.dll")
	proc := mod.NewProc("GetDiskFreeSpaceExW")

	pathPtr, err := syscall.UTF16PtrFromString(mount)
	if err != nil {
		return 0, 0, false
	}
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	r1, _, _ := proc.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r1 == 0 {
		return 0, 0, false
	}
	const gb = 1024 * 1024 * 1024
	return float64(totalBytes) / gb, float64(freeBytesAvailable) / gb, true
}
