//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// detachProcess sets the child process to run in a new session (Unix: setsid).
func detachProcess(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// isProcessAlive checks if a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// terminateProcess sends SIGTERM to gracefully stop a process.
func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

// killAllClaude kills all running `claude` processes to avoid session conflicts.
func killAllClaude() {
	out, err := osexec.Command("pgrep", "-x", "claude").CombinedOutput()
	if err != nil {
		return // no claude processes
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			proc, err := os.FindProcess(pid)
			if err == nil {
				log.Printf("[startup] Killing leftover claude process PID %d", pid)
				proc.Signal(syscall.SIGTERM)
			}
		}
	}
	// Give processes time to exit
	time.Sleep(500 * time.Millisecond)
}

// findRunnerProcesses returns PIDs and command lines of running processes
// matching the given binary name (e.g. "claude"). Uses pgrep on Unix.
func findRunnerProcesses(binaryName string) []RunnerProcess {
	// Use -x for exact binary name match (avoids matching this process or grep itself)
	out, err := osexec.Command("pgrep", "-x", binaryName).CombinedOutput()
	if err != nil {
		return nil // pgrep returns exit 1 if no match
	}
	var procs []RunnerProcess
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err != nil {
			continue
		}
		// Get the command line for this PID
		cmdOut, cmdErr := osexec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "command=").CombinedOutput()
		cmd := binaryName
		if cmdErr == nil {
			cmd = strings.TrimSpace(string(cmdOut))
		}
		procs = append(procs, RunnerProcess{PID: pid, Command: cmd})
	}
	return procs
}

// findAllRunnerSessions scans for all running processes of known agent binaries
// and returns them with their PPID for ancestry checks.
func findAllRunnerSessions(binaryNames []string) []sessionProcess {
	var all []sessionProcess
	for _, name := range binaryNames {
		out, err := osexec.Command("pgrep", "-x", name).CombinedOutput()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			var pid int
			if _, err := fmt.Sscanf(line, "%d", &pid); err != nil {
				continue
			}
			// Get command line and PPID in one ps call
			psOut, psErr := osexec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "ppid=,command=").CombinedOutput()
			cmd := name
			ppid := 0
			if psErr == nil {
				psLine := strings.TrimSpace(string(psOut))
				// Format: "  1234 /usr/local/bin/claude ..."
				if _, err := fmt.Sscanf(psLine, "%d", &ppid); err == nil {
					// Everything after the first space-separated number is the command
					idx := strings.IndexFunc(psLine, func(r rune) bool { return r != ' ' && (r < '0' || r > '9') })
					if idx > 0 {
						cmd = strings.TrimSpace(psLine[idx:])
					}
				}
			}
			all = append(all, sessionProcess{
				PID:        pid,
				PPID:       ppid,
				Command:    cmd,
				BinaryName: name,
			})
		}
	}
	return all
}

// isDescendantOf checks if a process (by PID) is a descendant of the given ancestor PID.
// Walks the PPID chain up to 20 levels to avoid infinite loops.
func isDescendantOf(pid, ancestorPID int) bool {
	current := pid
	for i := 0; i < 20; i++ {
		out, err := osexec.Command("ps", "-p", fmt.Sprintf("%d", current), "-o", "ppid=").CombinedOutput()
		if err != nil {
			return false
		}
		var ppid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &ppid); err != nil {
			return false
		}
		if ppid == ancestorPID {
			return true
		}
		if ppid <= 1 {
			return false
		}
		current = ppid
	}
	return false
}

// getMemoryUsedMB returns currently used system memory in MB.
func getMemoryUsedMB() (int64, error) {
	// macOS: vm_stat
	out, err := osexec.Command("vm_stat").CombinedOutput()
	if err == nil {
		var active, wired, compressed int64
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Pages active:") {
				fmt.Sscanf(strings.TrimPrefix(line, "Pages active:"), "%d", &active)
			} else if strings.HasPrefix(line, "Pages wired down:") {
				fmt.Sscanf(strings.TrimPrefix(line, "Pages wired down:"), "%d", &wired)
			} else if strings.HasPrefix(line, "Pages occupied by compressor:") {
				fmt.Sscanf(strings.TrimPrefix(line, "Pages occupied by compressor:"), "%d", &compressed)
			}
		}
		// Each page is 16384 bytes on Apple Silicon, 4096 on Intel — check page size
		pageOut, _ := osexec.Command("pagesize").CombinedOutput()
		pageSize := int64(16384) // default Apple Silicon
		fmt.Sscanf(strings.TrimSpace(string(pageOut)), "%d", &pageSize)
		usedBytes := (active + wired + compressed) * pageSize
		return usedBytes / (1024 * 1024), nil
	}
	// Linux fallback: /proc/meminfo
	data, readErr := os.ReadFile("/proc/meminfo")
	if readErr != nil {
		return 0, readErr
	}
	var total, available int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &total)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d kB", &available)
		}
	}
	return (total - available) / 1024, nil
}

// getCPUPercent returns a rough CPU usage percentage (sampled over 1 second).
func getCPUPercent() (float64, error) {
	// macOS: top -l 2 -n 0 — second sample gives accurate reading
	out, err := osexec.Command("top", "-l", "2", "-n", "0", "-s", "1").CombinedOutput()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		// Find the last "CPU usage:" line (second sample)
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.Contains(lines[i], "CPU usage:") {
				var user, sys float64
				fmt.Sscanf(lines[i], "CPU usage: %f%% user, %f%% sys,", &user, &sys)
				return user + sys, nil
			}
		}
	}
	// Linux fallback: /proc/stat (instant snapshot — less accurate but fast)
	data, readErr := os.ReadFile("/proc/stat")
	if readErr != nil {
		return 0, readErr
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "cpu ") {
		fields := strings.Fields(lines[0])
		if len(fields) >= 5 {
			var user, nice, system, idle int64
			fmt.Sscanf(fields[1], "%d", &user)
			fmt.Sscanf(fields[2], "%d", &nice)
			fmt.Sscanf(fields[3], "%d", &system)
			fmt.Sscanf(fields[4], "%d", &idle)
			total := float64(user + nice + system + idle)
			if total > 0 {
				return float64(user+nice+system) / total * 100, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine CPU usage")
}

// getSystemMemoryMB returns total system memory in MB.
func getSystemMemoryMB() (int64, error) {
	// macOS: sysctl hw.memsize
	out, err := osexec.Command("sysctl", "-n", "hw.memsize").CombinedOutput()
	if err == nil {
		var bytes int64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &bytes); err == nil {
			return bytes / (1024 * 1024), nil
		}
	}
	// Linux fallback: /proc/meminfo
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			var kb int64
			if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &kb); err == nil {
				return kb / 1024, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine memory")
}

// installAutoStart registers the agent to start on login.
// macOS: launchd plist, Linux: systemd user service.
func installAutoStart(exePath, workDir string) error {
	switch runtime.GOOS {
	case "darwin":
		return installAutoStartDarwin(exePath, workDir)
	case "linux":
		return installAutoStartLinux(exePath, workDir)
	default:
		return fmt.Errorf("auto-start not supported on %s", runtime.GOOS)
	}
}

func installAutoStartDarwin(exePath, workDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	logDir := filepath.Join(home, ".yaver")
	os.MkdirAll(logDir, 0700)

	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(plistDir, 0755)
	plistPath := filepath.Join(plistDir, "io.yaver.agent.plist")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.yaver.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
        <string>--debug</string>
        <string>--work-dir=%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, exePath, workDir,
		filepath.Join(logDir, "launchd-stdout.log"),
		filepath.Join(logDir, "launchd-stderr.log"))

	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Unload first in case it's already loaded (ignore errors)
	osexec.Command("launchctl", "unload", plistPath).Run()

	if out, err := osexec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %s: %w", string(out), err)
	}

	fmt.Printf("LaunchAgent installed: %s\n", plistPath)
	fmt.Println("Yaver will start automatically on login.")
	return nil
}

func installAutoStartLinux(exePath, workDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(unitDir, 0755)
	unitPath := filepath.Join(unitDir, "yaver.service")

	unit := fmt.Sprintf(`[Unit]
Description=Yaver Agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s serve --debug --work-dir=%s
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, exePath, workDir)

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	cmds := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "yaver"},
		{"systemctl", "--user", "start", "yaver"},
	}
	for _, c := range cmds {
		if out, err := osexec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %s: %w", strings.Join(c, " "), string(out), err)
		}
	}

	// Enable linger so user services run without login
	user := os.Getenv("USER")
	if user != "" {
		osexec.Command("loginctl", "enable-linger", user).Run()
	}

	fmt.Printf("Systemd user service installed: %s\n", unitPath)
	fmt.Println("Yaver will start automatically on login.")
	return nil
}

// isAutoStartInstalled checks if the auto-start service file exists.
func isAutoStartInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.yaver.agent.plist"))
		return err == nil
	case "linux":
		_, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "yaver.service"))
		return err == nil
	}
	return false
}

// ensureAutoStart registers the agent as a system service (launchd/systemd)
// without starting it — the caller already has the agent running.
// Returns a user-facing message describing what was set up, or "" if skipped.
func ensureAutoStart(exePath, workDir string) string {
	if isAutoStartInstalled() {
		return "" // already registered
	}
	switch runtime.GOOS {
	case "darwin":
		msg, _ := ensureAutoStartDarwin(exePath, workDir)
		return msg
	case "linux":
		msg, _ := ensureAutoStartLinux(exePath, workDir)
		return msg
	}
	return ""
}

func ensureAutoStartDarwin(exePath, workDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	logDir := filepath.Join(home, ".yaver")
	os.MkdirAll(logDir, 0700)

	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(plistDir, 0755)
	plistPath := filepath.Join(plistDir, "io.yaver.agent.plist")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.yaver.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
        <string>--debug</string>
        <string>--work-dir=%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, exePath, workDir,
		filepath.Join(logDir, "launchd-stdout.log"),
		filepath.Join(logDir, "launchd-stderr.log"))

	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return "", err
	}

	// Don't load/start — the agent is already running from the fork.
	// launchd will pick it up on next login/reboot.
	return "Registered as macOS LaunchAgent (will auto-start on login).", nil
}

func ensureAutoStartLinux(exePath, workDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(unitDir, 0755)
	unitPath := filepath.Join(unitDir, "yaver.service")

	unit := fmt.Sprintf(`[Unit]
Description=Yaver Agent — AI coding from your phone
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s serve --debug --work-dir=%s
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, exePath, workDir)

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return "", err
	}

	// Reload and enable but don't start — the agent is already running.
	osexec.Command("systemctl", "--user", "daemon-reload").Run()
	osexec.Command("systemctl", "--user", "enable", "yaver").Run()

	// Enable linger so user services run without login
	user := os.Getenv("USER")
	if user != "" {
		osexec.Command("loginctl", "enable-linger", user).Run()
	}

	return "Registered as systemd user service (will auto-start on login/boot).", nil
}

// stopAutoStartService stops the system service (but doesn't uninstall it).
// Used by `yaver stop` to prevent systemd/launchd from restarting the agent.
func stopAutoStartService() {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		if home != "" {
			plistPath := filepath.Join(home, "Library", "LaunchAgents", "io.yaver.agent.plist")
			if _, err := os.Stat(plistPath); err == nil {
				osexec.Command("launchctl", "unload", plistPath).Run()
				fmt.Println("  LaunchAgent unloaded (use 'yaver serve' to re-enable).")
			}
		}
	case "linux":
		home, _ := os.UserHomeDir()
		if home != "" {
			unitPath := filepath.Join(home, ".config", "systemd", "user", "yaver.service")
			if _, err := os.Stat(unitPath); err == nil {
				osexec.Command("systemctl", "--user", "stop", "yaver").Run()
				osexec.Command("systemctl", "--user", "disable", "yaver").Run()
				fmt.Println("  Systemd service stopped and disabled (use 'yaver serve' to re-enable).")
			}
		}
	}
}

// removeAutoStart removes auto-start registration.
func removeAutoStart() {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		plistPath := filepath.Join(home, "Library", "LaunchAgents", "io.yaver.agent.plist")
		osexec.Command("launchctl", "unload", plistPath).Run()
		os.Remove(plistPath)
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		osexec.Command("systemctl", "--user", "stop", "yaver").Run()
		osexec.Command("systemctl", "--user", "disable", "yaver").Run()
		unitPath := filepath.Join(home, ".config", "systemd", "user", "yaver.service")
		os.Remove(unitPath)
		osexec.Command("systemctl", "--user", "daemon-reload").Run()
	}
}
