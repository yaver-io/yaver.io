//go:build windows

package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32         = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess     = modkernel32.NewProc("OpenProcess")
	procCloseHandle     = modkernel32.NewProc("CloseHandle")
)

const (
	processQueryLimitedInfo = 0x1000
)

// detachProcess sets the child process to run detached on Windows.
func detachProcess(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// isProcessAlive checks if a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	h, _, _ := procOpenProcess.Call(
		uintptr(processQueryLimitedInfo),
		0,
		uintptr(pid),
	)
	if h == 0 {
		return false
	}
	procCloseHandle.Call(h)
	return true
}

// terminateProcess kills a process on Windows (no graceful SIGTERM equivalent).
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}

const taskName = "YaverAgent"

// installAutoStart creates a Windows Scheduled Task to run the agent at logon.
func installAutoStart(exePath, workDir string) error {
	// Use schtasks to create a logon trigger task
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve exe path: %w", err)
	}
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("resolve work dir: %w", err)
	}

	// Delete existing task if any (ignore errors)
	osexec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()

	// Create task that runs at logon
	cmd := osexec.Command("schtasks", "/Create",
		"/TN", taskName,
		"/TR", fmt.Sprintf(`"%s" serve --debug --work-dir="%s"`, absExe, absWork),
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create scheduled task: %w — %s", err, string(output))
	}
	return nil
}

// killAllClaude kills all running claude processes on Windows.
func killAllClaude() {
	osexec.Command("taskkill", "/F", "/IM", "claude.exe").Run()
	time.Sleep(500 * time.Millisecond)
}

// findRunnerProcesses returns PIDs and command lines of running processes
// matching the given binary name (e.g. "claude"). Uses tasklist on Windows.
func findRunnerProcesses(binaryName string) []RunnerProcess {
	// tasklist /FI "IMAGENAME eq claude.exe" /FO CSV /NH
	exeName := binaryName + ".exe"
	out, err := osexec.Command("tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s", exeName), "/FO", "CSV", "/NH").CombinedOutput()
	if err != nil {
		return nil
	}
	var procs []RunnerProcess
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "No tasks are running") {
			continue
		}
		// CSV format: "claude.exe","1234","Console","1","12,345 K"
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pidStr := strings.Trim(fields[1], "\" ")
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
			continue
		}
		procs = append(procs, RunnerProcess{PID: pid, Command: exeName})
	}
	return procs
}

// findAllRunnerSessions scans for all running processes of known agent binaries
// and returns them with their PPID for ancestry checks.
func findAllRunnerSessions(binaryNames []string) []sessionProcess {
	var all []sessionProcess
	for _, name := range binaryNames {
		exeName := name + ".exe"
		out, err := osexec.Command("wmic", "process", "where",
			fmt.Sprintf("Name='%s'", exeName),
			"get", "ProcessId,ParentProcessId,CommandLine", "/FORMAT:CSV").CombinedOutput()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Node") {
				continue
			}
			// CSV: Node,CommandLine,ParentProcessId,ProcessId
			fields := strings.Split(line, ",")
			if len(fields) < 4 {
				continue
			}
			var pid, ppid int
			fmt.Sscanf(strings.TrimSpace(fields[len(fields)-1]), "%d", &pid)
			fmt.Sscanf(strings.TrimSpace(fields[len(fields)-2]), "%d", &ppid)
			cmd := strings.TrimSpace(fields[1])
			if cmd == "" {
				cmd = exeName
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
func isDescendantOf(pid, ancestorPID int) bool {
	current := pid
	for i := 0; i < 20; i++ {
		out, err := osexec.Command("wmic", "process", "where",
			fmt.Sprintf("ProcessId=%d", current),
			"get", "ParentProcessId", "/VALUE").CombinedOutput()
		if err != nil {
			return false
		}
		var ppid int
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "ParentProcessId=") {
				fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(line), "ParentProcessId="), "%d", &ppid)
			}
		}
		if ppid == ancestorPID {
			return true
		}
		if ppid <= 1 || ppid == 0 {
			return false
		}
		current = ppid
	}
	return false
}

// getMemoryUsedMB returns currently used system memory in MB on Windows.
func getMemoryUsedMB() (int64, error) {
	out, err := osexec.Command("wmic", "OS", "get", "FreePhysicalMemory,TotalVisibleMemorySize", "/Value").CombinedOutput()
	if err != nil {
		return 0, err
	}
	var totalKB, freeKB int64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "TotalVisibleMemorySize=") {
			fmt.Sscanf(line, "TotalVisibleMemorySize=%d", &totalKB)
		} else if strings.HasPrefix(line, "FreePhysicalMemory=") {
			fmt.Sscanf(line, "FreePhysicalMemory=%d", &freeKB)
		}
	}
	return (totalKB - freeKB) / 1024, nil
}

// getCPUPercent returns CPU usage percentage on Windows.
func getCPUPercent() (float64, error) {
	out, err := osexec.Command("wmic", "cpu", "get", "LoadPercentage", "/Value").CombinedOutput()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "LoadPercentage=") {
			var pct float64
			fmt.Sscanf(line, "LoadPercentage=%f", &pct)
			return pct, nil
		}
	}
	return 0, fmt.Errorf("could not determine CPU usage")
}

// getSystemMemoryMB returns total system memory in MB on Windows.
func getSystemMemoryMB() (int64, error) {
	out, err := osexec.Command("wmic", "OS", "get", "TotalVisibleMemorySize", "/Value").CombinedOutput()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "TotalVisibleMemorySize=") {
			var kb int64
			if _, err := fmt.Sscanf(line, "TotalVisibleMemorySize=%d", &kb); err == nil {
				return kb / 1024, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine memory")
}

// isAutoStartInstalled checks if the Windows Scheduled Task exists.
func isAutoStartInstalled() bool {
	err := osexec.Command("schtasks", "/Query", "/TN", taskName).Run()
	return err == nil
}

// ensureAutoStart registers the agent as a Windows Scheduled Task
// without starting it — the caller already has the agent running.
func ensureAutoStart(exePath, workDir string) string {
	if isAutoStartInstalled() {
		return ""
	}
	if err := installAutoStart(exePath, workDir); err != nil {
		return ""
	}
	return "Registered as Windows Scheduled Task (will auto-start on login)."
}

// stopAutoStartService disables the Windows Scheduled Task.
func stopAutoStartService() {
	if isAutoStartInstalled() {
		osexec.Command("schtasks", "/Change", "/TN", taskName, "/Disable").Run()
		fmt.Println("  Scheduled Task disabled (use 'yaver serve' to re-enable).")
	}
}

// removeAutoStart removes the Windows Scheduled Task.
func removeAutoStart() {
	osexec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()
}

// Ensure unsafe is used (required for procOpenProcess.Call)
var _ = unsafe.Pointer(nil)
