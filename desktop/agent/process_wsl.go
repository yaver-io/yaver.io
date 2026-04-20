package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

const yaverWSLAutoStartMarker = "# yaver-wsl-autostart"
const yaverWSLTaskName = "YaverWSLAgent"

func wslAutoStartScriptPath() string {
	dir, err := ConfigDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, "wsl-autostart.sh")
}

func wslShellHookTargets() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}
}

func wslAutoStartCommandPath() string {
	if !isWSL() {
		return ""
	}
	if _, err := osexec.LookPath("cmd.exe"); err != nil {
		return ""
	}
	if _, err := osexec.LookPath("wslpath"); err != nil {
		return ""
	}
	out, err := osexec.Command("cmd.exe", "/C", "echo", "%APPDATA%").CombinedOutput()
	if err != nil {
		return ""
	}
	winAppData := strings.TrimSpace(strings.ReplaceAll(string(out), "\r", ""))
	if winAppData == "" || strings.Contains(winAppData, "%APPDATA%") {
		return ""
	}
	winStartup := winAppData + `\Microsoft\Windows\Start Menu\Programs\Startup\Yaver WSL Agent.cmd`
	converted, err := osexec.Command("wslpath", "-u", winStartup).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(converted))
}

func writeWSLAutoStartScript(exePath, workDir string) error {
	scriptPath := wslAutoStartScriptPath()
	if scriptPath == "" {
		return fmt.Errorf("resolve WSL auto-start script path")
	}
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	content := fmt.Sprintf(`#!/usr/bin/env bash
if [ -z "${WSL_DISTRO_NAME:-}" ]; then
  exit 0
fi
if pgrep -f %q >/dev/null 2>&1; then
  exit 0
fi
nohup %q serve --work-dir=%q >/dev/null 2>&1 &
`, exePath+" serve", exePath, workDir)
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("write WSL auto-start script: %w", err)
	}
	return nil
}

func installWSLShellAutoStart() error {
	scriptPath := wslAutoStartScriptPath()
	if scriptPath == "" {
		return fmt.Errorf("resolve WSL auto-start script path")
	}
	line := fmt.Sprintf(`[ -n "$WSL_DISTRO_NAME" ] && [ -x %q ] && %q >/dev/null 2>&1`, scriptPath, scriptPath)
	for _, target := range wslShellHookTargets() {
		if err := ensureMarkedLine(target, yaverWSLAutoStartMarker, line); err != nil {
			return err
		}
	}
	return nil
}

func installWSLWindowsStartupWrapper() (bool, error) {
	startupCmd := wslAutoStartCommandPath()
	if startupCmd == "" {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(startupCmd), 0o755); err != nil {
		return false, fmt.Errorf("create Windows startup dir bridge: %w", err)
	}
	distro := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
	user := strings.TrimSpace(os.Getenv("USER"))
	if distro == "" || user == "" {
		return false, nil
	}
	content := fmt.Sprintf("@echo off\r\nwsl.exe -d %s -u %s bash -lc \"~/.yaver/wsl-autostart.sh\"\r\n", distro, user)
	if err := os.WriteFile(startupCmd, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("write Windows startup wrapper: %w", err)
	}
	return true, nil
}

func installWSLWindowsScheduledTask() (bool, error) {
	if !isWSL() {
		return false, nil
	}
	if _, err := osexec.LookPath("cmd.exe"); err != nil {
		return false, nil
	}
	distro := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
	user := strings.TrimSpace(os.Getenv("USER"))
	if distro == "" || user == "" {
		return false, nil
	}
	taskCommand := fmt.Sprintf(`wsl.exe -d %s -u %s bash -lc "~/.yaver/wsl-autostart.sh"`, distro, user)
	deleteCmd := osexec.Command("cmd.exe", "/C", "schtasks", "/Delete", "/TN", yaverWSLTaskName, "/F")
	_ = deleteCmd.Run()
	createCmd := osexec.Command("cmd.exe", "/C", "schtasks", "/Create",
		"/TN", yaverWSLTaskName,
		"/TR", taskCommand,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F")
	if out, err := createCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("create Windows scheduled task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return true, nil
}

func installAutoStartWSL(exePath, workDir string) (string, error) {
	if err := writeWSLAutoStartScript(exePath, workDir); err != nil {
		return "", err
	}
	if err := installWSLShellAutoStart(); err != nil {
		return "", err
	}
	taskHooked, taskErr := installWSLWindowsScheduledTask()
	windowsHooked, startupErr := installWSLWindowsStartupWrapper()
	if taskErr != nil && startupErr != nil {
		return "", taskErr
	}
	if startupErr != nil && !taskHooked {
		return "", startupErr
	}
	msg := "Registered WSL startup helper (shell profile hook)."
	if taskHooked {
		msg += " Also registered a Windows Scheduled Task so Yaver can come back after Windows login."
	} else if windowsHooked {
		msg += " Also wrote a Windows Startup wrapper so Yaver can come back after Windows login."
	} else {
		msg += " Add a Windows startup / Task Scheduler wrapper if you want reboot persistence before opening WSL."
	}
	msg += " Note: this still depends on the Windows host staying awake; for unattended remote use, disable Windows sleep and keep Tailscale on the Windows side."
	return msg, nil
}

func isWSLAutoStartInstalled() bool {
	if !isWSL() {
		return false
	}
	scriptPath := wslAutoStartScriptPath()
	if scriptPath == "" {
		return false
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return false
	}
	if taskInstalled := isWSLWindowsScheduledTaskInstalled(); taskInstalled {
		return true
	}
	for _, target := range wslShellHookTargets() {
		data, err := os.ReadFile(target)
		if err == nil && strings.Contains(string(data), yaverWSLAutoStartMarker) {
			return true
		}
	}
	return false
}

func removeAutoStartWSL() {
	for _, target := range wslShellHookTargets() {
		_ = removeMarkedLine(target, yaverWSLAutoStartMarker)
	}
	if scriptPath := wslAutoStartScriptPath(); scriptPath != "" {
		_ = os.Remove(scriptPath)
	}
	if startupCmd := wslAutoStartCommandPath(); startupCmd != "" {
		_ = os.Remove(startupCmd)
	}
	if _, err := osexec.LookPath("cmd.exe"); err == nil {
		_ = osexec.Command("cmd.exe", "/C", "schtasks", "/Delete", "/TN", yaverWSLTaskName, "/F").Run()
	}
}

func isWSLWindowsScheduledTaskInstalled() bool {
	if !isWSL() {
		return false
	}
	if _, err := osexec.LookPath("cmd.exe"); err != nil {
		return false
	}
	out, err := osexec.Command("cmd.exe", "/C", "schtasks", "/Query", "/TN", yaverWSLTaskName).CombinedOutput()
	return err == nil && strings.Contains(strings.ToLower(string(out)), strings.ToLower(yaverWSLTaskName))
}
