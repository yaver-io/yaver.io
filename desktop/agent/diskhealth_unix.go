//go:build !windows

package main

import "syscall"

// statfsGB returns total, free disk space in GB for a mount point.
// Unix path uses syscall.Statfs.
func statfsGB(mount string) (totalGB, freeGB float64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mount, &stat); err != nil {
		return 0, 0, false
	}
	blockSize := uint64(stat.Bsize)
	totalGB = float64(stat.Blocks*blockSize) / (1024 * 1024 * 1024)
	freeGB = float64(stat.Bavail*blockSize) / (1024 * 1024 * 1024)
	return totalGB, freeGB, true
}
