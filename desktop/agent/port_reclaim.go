package main

// port_reclaim.go — best-effort recovery from "address already in use"
// on the agent's HTTP port.
//
// Failure mode this exists for: a previous `yaver serve` orphan
// survives a restart (botched upgrade, systemd lost track of it,
// install script killed only some of them) and stays bound to :18080.
// The new agent boots, fails to bind, logs an actionable message, and
// systemd flips into auto-restart with the same outcome until a human
// SSHes in and runs pkill. For a remote primary that the user only
// reaches from their phone, that's a dead box with no way back.
//
// What we do:
//   1. Detect EADDRINUSE on the agent's port via `errors.Is(syscall.EADDRINUSE)`.
//   2. Look up the listening PID(s) via `lsof -tiTCP:<port> -sTCP:LISTEN`.
//   3. For each holder, verify the process is actually a yaver binary
//      before signalling. Refuse to kill arbitrary user processes.
//   4. Send SIGTERM, wait up to terminateGrace, escalate to SIGKILL.
//   5. Caller retries the bind once; on success we keep going,
//      otherwise we fall back to the existing fatal-log path.
//
// We do NOT touch foreign processes. If the holder is something else
// (postgres, an ssh tunnel, a user's local app) the user keeps the
// pre-self-heal behavior: actionable log + exit. The whole point is
// to recover from yaver-on-yaver conflicts, not to clean up the box.

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	portReclaimTerminateGrace = 3 * time.Second
	portReclaimKillGrace      = 2 * time.Second
)

// isAddrInUseErr reports whether err is the standard "address already
// in use" wrapped at any depth. Goes through syscall.EADDRINUSE first
// and falls back to substring on the rendered message — net package
// wraps the syscall errno but error.Is wiring is reliable across
// Go 1.20+.
func isAddrInUseErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(err.Error(), "address already in use")
}

// reclaimPortFromStaleYaver tries to take :port back from a stale yaver
// holder. Returns true when at least one yaver-owned holder was killed;
// caller should retry binding. Returns false when no holders found, no
// holders are yaver, or kills failed — caller falls back to fatal.
func reclaimPortFromStaleYaver(port int) bool {
	if runtime.GOOS == "windows" {
		// lsof isn't standard on Windows; netstat parsing is messy.
		// Skip — Windows isn't a supported `yaver serve` host today.
		return false
	}
	if port <= 0 {
		return false
	}
	holders := listPortHolders(port)
	if len(holders) == 0 {
		log.Printf("[port-reclaim] no holder PIDs found for :%d (lsof missing or port already free)", port)
		return false
	}
	self := os.Getpid()
	killed := 0
	for _, pid := range holders {
		if pid == self {
			continue
		}
		exe, ok := pidExecutable(pid)
		if !ok {
			log.Printf("[port-reclaim] :%d held by pid=%d but executable path unreadable — skipping", port, pid)
			continue
		}
		if !isYaverBinary(exe) {
			log.Printf("[port-reclaim] :%d held by foreign process pid=%d exe=%s — refusing to kill", port, pid, exe)
			continue
		}
		log.Printf("[port-reclaim] :%d held by stale yaver pid=%d exe=%s — terminating", port, pid, exe)
		if terminatePID(pid) {
			killed++
		}
	}
	return killed > 0
}

// listPortHolders returns PIDs that hold a LISTEN socket on `port`.
// Cross-platform: lsof on macOS + Linux. ss is more reliable on Linux
// but isn't available everywhere lsof is, so we keep one tool.
func listPortHolders(port int) []int {
	portStr := strconv.Itoa(port)
	out, err := exec.Command("lsof", "-tiTCP:"+portStr, "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// pidExecutable returns the absolute executable path for pid. On Linux
// reads /proc/<pid>/exe; on macOS shells out to `ps`. Empty string +
// false when unreadable (process gone, permission denied, …).
func pidExecutable(pid int) (string, bool) {
	if runtime.GOOS == "linux" {
		path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
		if err != nil || path == "" {
			return "", false
		}
		return path, true
	}
	// macOS / BSD path. `ps -o comm=` returns the executable name (or
	// full path on macOS for absolute-launched binaries). Good enough
	// for the isYaverBinary basename check below.
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", false
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", false
	}
	return name, true
}

// isYaverBinary reports whether `exe` is a yaver executable. Matches
// the basename so it works for /usr/local/bin/yaver, /root/.local/bin/yaver,
// ~/.yaver/bin/<version>/<platform>/yaver, etc. Refuses to match the
// substring inside a path like /home/yaver/foo/other-binary.
func isYaverBinary(exe string) bool {
	if exe == "" {
		return false
	}
	base := exe
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		base = exe[i+1:]
	}
	// Strip a trailing " (deleted)" linux annotation that shows up
	// when the binary on disk was replaced after the process started.
	base = strings.TrimSuffix(base, " (deleted)")
	return base == "yaver" || base == "yaver.exe"
}

// terminatePID sends SIGTERM, waits up to portReclaimTerminateGrace,
// escalates to SIGKILL if the process is still alive, then waits up to
// portReclaimKillGrace for the kernel to release the socket. Returns
// true when the process is no longer alive at the end of the wait.
func terminatePID(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// ESRCH means it's already gone; treat as success.
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		log.Printf("[port-reclaim] SIGTERM pid=%d failed: %v", pid, err)
		return false
	}
	if waitForExit(proc, portReclaimTerminateGrace) {
		return true
	}
	log.Printf("[port-reclaim] pid=%d ignored SIGTERM after %s, sending SIGKILL", pid, portReclaimTerminateGrace)
	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		log.Printf("[port-reclaim] SIGKILL pid=%d failed: %v", pid, err)
		return false
	}
	return waitForExit(proc, portReclaimKillGrace)
}

// waitForExit polls signal-0 every 100ms until the process is gone or
// the deadline passes. Cheap on Unix; not used on Windows.
func waitForExit(proc *os.Process, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return errors.Is(err, syscall.ESRCH)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return proc.Signal(syscall.Signal(0)) != nil
}
