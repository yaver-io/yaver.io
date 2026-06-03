//go:build windows

package main

import "fmt"

// ManagedUnit mirrors the !windows definition so callers compile on Windows.
// Durable per-service OS units are not supported on Windows (no systemd/launchd);
// companion services fall back to the agent-child model.
type ManagedUnit struct {
	Name     string
	ExecPath string
	Args     []string
	WorkDir  string
	Env      map[string]string
	LogDir   string
}

func durableUnitsSupported() bool { return false }

func writeManagedUnit(u ManagedUnit) (string, error) {
	return "", fmt.Errorf("durable OS units unsupported on windows; run as agent-child service")
}

func removeManagedUnit(name string) error { return nil }
