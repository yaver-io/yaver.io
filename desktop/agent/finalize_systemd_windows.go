//go:build windows

package main

// ensureFinalizeSystemdTimer is a no-op on Windows (no systemd). The finalize
// tick still runs via the in-process ticker; only the systemd persistence is
// linux-specific.
func ensureFinalizeSystemdTimer() error { return nil }
