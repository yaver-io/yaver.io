//go:build !windows

package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
)

// ensureFinalizeSystemdTimer installs the finalize kick timer as a systemd user
// unit. systemd-only (linux); returns early on darwin/other. Lives here (not in
// finalize.go) because systemdUserUnitDir/systemdExecLine are //go:build
// !windows — a windows stub is in finalize_systemd_windows.go.
func ensureFinalizeSystemdTimer() error {
	if runtime.GOOS != "linux" || isWSL() {
		return nil
	}
	dir, err := systemdUserUnitDir()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "yaver"
	}
	servicePath := filepath.Join(dir, "yaver-finalize.service")
	timerPath := filepath.Join(dir, "yaver-finalize.timer")
	service := "[Unit]\nDescription=Yaver finalize tick\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=oneshot\nExecStart=" + systemdExecLine(exe, []string{"finalize", "tick"}) + "\n"
	timer := "[Unit]\nDescription=Yaver finalize periodic kick timer\n\n[Timer]\nOnBootSec=2min\nOnUnitActiveSec=1min\nAccuracySec=15s\nPersistent=true\nUnit=yaver-finalize.service\n\n[Install]\nWantedBy=timers.target\n"
	if err := os.WriteFile(servicePath, []byte(service), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(timerPath, []byte(timer), 0o600); err != nil {
		return err
	}
	_ = osexec.Command("systemctl", "--user", "daemon-reload").Run()
	_ = osexec.Command("systemctl", "--user", "enable", "--now", "yaver-finalize.timer").Run()
	if user := os.Getenv("USER"); user != "" {
		_ = osexec.Command("loginctl", "enable-linger", user).Run()
	}
	return nil
}
