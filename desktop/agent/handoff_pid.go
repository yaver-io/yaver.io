package main

// handoff_pid.go — caller-PID detection + cooperative termination for
// `yaver handoff` / MCP `session_handoff`. The contract: when an AI
// agent (Claude Code, Codex, Aider, …) calls Yaver to take over its
// own session, Yaver should be able to actually terminate that calling
// process — not just write a sentinel and hope the agent reads its own
// MCP result and quits.
//
// Three PID sources, in priority order:
//   1. Explicit `caller_pid` arg from the MCP / HTTP / CLI caller.
//      Any wrapper script can set this to $$ before invoking yaver.
//   2. mcpStdioCallerPID — set once at stdio MCP startup to
//      os.Getppid(). Stdio MCP is spawned BY the AI agent, so the
//      parent PID is exactly the agent process.
//   3. Per-HTTP-request peer-port lookup. For loopback connections
//      (Claude Desktop's local MCP via http) lsof maps tcp peer port
//      → owning PID. Cross-platform: macOS + Linux ship lsof; on
//      Windows we fall through to PID=0 and stay cooperative.
//
// Termination: SIGTERM after a configurable grace, SIGKILL `killHard`
// seconds later. Runs in a goroutine so the HTTP/MCP response returns
// to the caller (i.e. the agent itself) BEFORE the kill — otherwise
// the response is lost in transit and the agent's user sees nothing.

import (
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// mcpStdioCallerPID is set once when this process starts in stdio MCP
// mode. Reads from concurrent goroutines are racy in theory but the
// value is written exactly once before any handler runs.
var (
	mcpStdioCallerPID   int
	mcpStdioCallerPIDMu sync.RWMutex
)

func setMCPStdioCallerPID(pid int) {
	mcpStdioCallerPIDMu.Lock()
	mcpStdioCallerPID = pid
	mcpStdioCallerPIDMu.Unlock()
}

func getMCPStdioCallerPID() int {
	mcpStdioCallerPIDMu.RLock()
	defer mcpStdioCallerPIDMu.RUnlock()
	return mcpStdioCallerPID
}

// resolveCallerPID picks the best caller PID for this handoff. Returns
// 0 when no source is available — caller treats that as "stay
// cooperative, just write the sentinel and hope the agent exits".
func resolveCallerPID(explicit int, httpClientAddr string) int {
	if explicit > 0 {
		return explicit
	}
	if pid := getMCPStdioCallerPID(); pid > 0 {
		return pid
	}
	if pid := lookupPIDFromHTTPClientAddr(httpClientAddr); pid > 0 {
		return pid
	}
	return 0
}

// lookupPIDFromHTTPClientAddr resolves a TCP peer address (`127.0.0.1:54321`)
// to the PID that owns the connecting socket. Loopback only — for any
// non-loopback address we return 0, since killing a remote process
// from here would be both impossible and a security hole.
func lookupPIDFromHTTPClientAddr(addr string) int {
	if addr == "" {
		return 0
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	if !isLoopback(host) {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return 0
	}
	if runtime.GOOS == "windows" {
		// netstat / Get-Process route — out of scope for this commit.
		return 0
	}
	// `lsof -nP -iTCP@127.0.0.1:<port> -sTCP:ESTABLISHED -t -F p`
	// emits one or more "p<PID>" lines. We pick the first one that
	// isn't our own PID.
	cmd := exec.Command("lsof", "-nP", "-iTCP@127.0.0.1:"+portStr, "-sTCP:ESTABLISHED", "-t")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	self := os.Getpid()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		return pid
	}
	return 0
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// scheduleCallerTermination sends SIGTERM after graceSec, then SIGKILL
// killHard seconds later if the process is still alive. Runs in its
// own goroutine so the calling HTTP / MCP handler can return its
// response (with the sentinel info) to the agent BEFORE the kill —
// otherwise the agent's last visible state is a broken pipe instead
// of "Yaver took over".
func scheduleCallerTermination(pid, graceSec, killHard int) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	if graceSec <= 0 {
		graceSec = 5
	}
	if killHard <= 0 {
		killHard = 10
	}
	go func() {
		time.Sleep(time.Duration(graceSec) * time.Second)
		proc, err := os.FindProcess(pid)
		if err != nil {
			return
		}
		log.Printf("[handoff] sending SIGTERM to caller pid=%d", pid)
		_ = proc.Signal(syscall.SIGTERM)

		time.Sleep(time.Duration(killHard-graceSec) * time.Second)
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			log.Printf("[handoff] caller pid=%d still alive after %ds, sending SIGKILL", pid, killHard)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}()
}
