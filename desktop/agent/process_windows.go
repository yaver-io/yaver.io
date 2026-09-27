//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = modkernel32.NewProc("OpenProcess")
	procCloseHandle = modkernel32.NewProc("CloseHandle")
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
const (
	darwinLaunchdLabel     = "io.yaver.agent"
	darwinLaunchAgentPath  = "Library/LaunchAgents/io.yaver.agent.plist"
	darwinLaunchDaemonPath = "/Library/LaunchDaemons/io.yaver.agent.plist"
)

func isDarwinLaunchDaemonInstalled() bool { return false }

func installLaunchdDaemonService() {
	fmt.Println("macOS LaunchDaemon install is only available on macOS.")
}

func isWSLWindowsScheduledTaskInstalled() bool { return false }

// WSL-autostart stubs for native Windows builds. WSL-from-Windows
// is a non-goal for this target — the real implementations live in
// process_wsl.go (compiled on everything except native Windows).
// Call sites in httpserver.go / main.go / process_unix.go expect
// the names to exist, so we stub them here as no-ops.
func isWSLAutoStartInstalled() bool { return false }
func installAutoStartWSL(exePath, workDir string) (string, error) {
	return "", fmt.Errorf("WSL auto-start is not available on native Windows builds")
}
func removeAutoStartWSL() {}

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

type windowsProcessRecord struct {
	Name            string  `json:"Name"`
	ProcessID       int     `json:"ProcessId"`
	ParentProcessID int     `json:"ParentProcessId"`
	CommandLine     *string `json:"CommandLine"`
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// windowsProcessRecords uses CIM rather than wmic.exe. WMIC is disabled by
// default on current Windows 11 releases and is being removed, so a PATH hit
// or a successful build says nothing about whether process/session discovery
// works on the clean machines that receive the Store installer.
func windowsProcessRecords(binaryNames []string) ([]windowsProcessRecord, error) {
	filter := ""
	if len(binaryNames) > 0 {
		quoted := make([]string, 0, len(binaryNames))
		for _, name := range binaryNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(name), ".exe") {
				name += ".exe"
			}
			quoted = append(quoted, powershellSingleQuote(name))
		}
		if len(quoted) > 0 {
			filter = fmt.Sprintf(" | Where-Object { @(%s) -contains $_.Name }", strings.Join(quoted, ","))
		}
	}
	script := "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);" +
		"$rows=@(Get-CimInstance Win32_Process" + filter +
		" | Select-Object Name,ProcessId,ParentProcessId,CommandLine);" +
		"ConvertTo-Json -Compress -InputObject $rows"
	out, err := osexec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("query Windows processes with CIM: %w — %s", err, strings.TrimSpace(string(out)))
	}
	var rows []windowsProcessRecord
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse Windows CIM process list: %w", err)
	}
	return rows, nil
}

// findRunnerProcesses returns PIDs and command lines of running processes
// matching the given binary name (e.g. "claude").
func findRunnerProcesses(binaryName string) []RunnerProcess {
	rows, err := windowsProcessRecords([]string{binaryName})
	if err != nil {
		return nil
	}
	procs := make([]RunnerProcess, 0, len(rows))
	for _, row := range rows {
		command := row.Name
		if row.CommandLine != nil && strings.TrimSpace(*row.CommandLine) != "" {
			command = strings.TrimSpace(*row.CommandLine)
		}
		procs = append(procs, RunnerProcess{PID: row.ProcessID, Command: command})
	}
	return procs
}

// findAllRunnerSessions scans for all running processes of known agent binaries
// and returns them with their PPID for ancestry checks.
func findAllRunnerSessions(binaryNames []string) []sessionProcess {
	rows, err := windowsProcessRecords(binaryNames)
	if err != nil {
		return nil
	}
	all := make([]sessionProcess, 0, len(rows))
	for _, row := range rows {
		command := row.Name
		if row.CommandLine != nil && strings.TrimSpace(*row.CommandLine) != "" {
			command = strings.TrimSpace(*row.CommandLine)
		}
		all = append(all, sessionProcess{
			PID:        row.ProcessID,
			PPID:       row.ParentProcessID,
			Command:    command,
			BinaryName: strings.TrimSuffix(strings.ToLower(row.Name), ".exe"),
		})
	}
	return all
}

// isDescendantOf checks if a process (by PID) is a descendant of the given ancestor PID.
func isDescendantOf(pid, ancestorPID int) bool {
	rows, err := windowsProcessRecords(nil)
	if err != nil {
		return false
	}
	parents := make(map[int]int, len(rows))
	for _, row := range rows {
		parents[row.ProcessID] = row.ParentProcessID
	}
	current := pid
	for i := 0; i < 20; i++ {
		ppid, ok := parents[current]
		if !ok {
			return false
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
	script := "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);" +
		"$o=Get-CimInstance Win32_OperatingSystem;" +
		"[pscustomobject]@{Total=[int64]$o.TotalVisibleMemorySize;Free=[int64]$o.FreePhysicalMemory}|ConvertTo-Json -Compress"
	out, err := osexec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("query Windows memory with CIM: %w — %s", err, strings.TrimSpace(string(out)))
	}
	var memory struct {
		Total int64 `json:"Total"`
		Free  int64 `json:"Free"`
	}
	if err := json.Unmarshal(out, &memory); err != nil || memory.Total <= 0 || memory.Free < 0 || memory.Free > memory.Total {
		return 0, fmt.Errorf("parse Windows CIM memory response")
	}
	return (memory.Total - memory.Free) / 1024, nil
}

// getCPUPercent returns CPU usage percentage on Windows.
func getCPUPercent() (float64, error) {
	script := "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);" +
		"$p=@(Get-CimInstance Win32_Processor);" +
		"if($p.Count -eq 0){throw 'no processor rows'};" +
		"[double](($p|Measure-Object -Property LoadPercentage -Average).Average)"
	out, err := osexec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("query Windows CPU with CIM: %w — %s", err, strings.TrimSpace(string(out)))
	}
	pct, parseErr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if parseErr != nil {
		return 0, fmt.Errorf("parse Windows CIM CPU response: %w", parseErr)
	}
	return pct, nil
}

// getSystemMemoryMB returns total system memory in MB on Windows.
func getSystemMemoryMB() (int64, error) {
	script := "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);" +
		"[int64](Get-CimInstance Win32_OperatingSystem).TotalVisibleMemorySize"
	out, err := osexec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("query Windows total memory with CIM: %w — %s", err, strings.TrimSpace(string(out)))
	}
	kb, parseErr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if parseErr != nil || kb <= 0 {
		return 0, fmt.Errorf("parse Windows CIM total memory response")
	}
	return kb / 1024, nil
}

// isAutoStartInstalled checks if the Windows Scheduled Task exists.
func isAutoStartInstalled() bool {
	err := osexec.Command("schtasks", "/Query", "/TN", taskName).Run()
	return err == nil
}

// ensureAutoStart registers the agent as a Windows Scheduled Task
// without starting it — the caller already has the agent running.
func ensureAutoStart(exePath, workDir string) string {
	if envTruthy(os.Getenv("YAVER_SKIP_AUTO_START")) {
		return ""
	}
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

// reconcileSystemdBinaryPath is a no-op on Windows — there is no systemd unit
// whose ExecStart could drift. The Unix implementation lives in process_unix.go.
func reconcileSystemdBinaryPath() {}

// reconcileDarwinLaunchdBinaryPath is a no-op on Windows — there is no launchd
// plist whose ProgramArguments could drift. The Darwin implementation lives in
// process_unix.go.
func reconcileDarwinLaunchdBinaryPath() {}
