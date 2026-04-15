package main

// autodev_detach.go — turns `yaver autodev sfmg` into a "set and
// forget" command. The CLI fork-execs itself as a detached child
// (own session, no controlling terminal) that runs the actual
// kick loop. The original CLI then tails the daemon-hosted log
// stream over SSE so the user sees live output exactly as today —
// but Ctrl-C just detaches the tail; the loop keeps running. Same
// applies if the terminal closes, the SSH session drops, the laptop
// lid closes after re-open, etc. The loop is parented to PID 1.
//
// IPC layout:
//   parent CLI  --(spawn)-->  detached autodev process  --(stream)-->  daemon
//        `--(SSE tail)<--------------------------------------'
//
// State files:
//   /tmp/yaver/autodev-<loop>.pid   — pid of the detached process
//   /tmp/yaver/autodev_<loop>-*.log — per-run log (timestamped)
//   /tmp/yaver/autodev_<loop>-latest.log — symlink to most recent

import (
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const autodevDetachEnv = "YAVER_AUTODEV_DETACHED"

// autodevDetachActive is true when this process IS the detached
// child (i.e. our env contains YAVER_AUTODEV_DETACHED=1). When
// true, runAutodevOrTest skips the fork dance and just runs the
// loop directly.
func autodevDetachActive() bool {
	return os.Getenv(autodevDetachEnv) == "1"
}

// spawnDetachedAutodev fork-execs the current binary as a session
// leader (setsid) so the kick loop survives parent exit, terminal
// close, ssh disconnect. Returns the detached child's PID and the
// stream name the parent should tail. Best-effort: any failure
// here returns ("", "") so the caller falls back to running the
// loop in the foreground.
func spawnDetachedAutodev(kind string, args []string, loopName string) (int, string) {
	exe, err := os.Executable()
	if err != nil {
		return 0, ""
	}
	streamName := fmt.Sprintf("autodev:%s", loopName)

	if err := os.MkdirAll("/tmp/yaver", 0o755); err != nil {
		return 0, ""
	}
	pidFile := autodevPIDFile(loopName)

	// Refuse to spawn a duplicate. If an existing pid is alive, just
	// return it so the parent attaches to its stream instead of
	// double-running the loop.
	if existing, ok := readAutodevPID(loopName); ok {
		fmt.Fprintf(os.Stderr, "[autodev] loop %q already running (pid %d) — attaching to its stream\n", loopName, existing)
		return existing, streamName
	}

	logPath := fmt.Sprintf("/tmp/yaver/autodev-%s-%s.fork.log",
		safeStreamName(streamName), time.Now().Format("20060102-150405"))
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, ""
	}

	// Re-exec ourselves with the same kind + args, marked detached.
	childArgs := append([]string{kind}, args...)
	cmd := osexec.Command(exe, childArgs...)
	cmd.Env = append(os.Environ(), autodevDetachEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logF.Close()
		return 0, ""
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644)
	// Don't Wait — the child outlives us. Release the *os.Process so
	// the runtime stops tracking it; any Process.Release error is
	// non-fatal.
	_ = cmd.Process.Release()
	logF.Close()

	fmt.Fprintf(os.Stderr, "[autodev] loop running detached as pid %d (fork log: %s)\n", pid, logPath)
	fmt.Fprintf(os.Stderr, "[autodev] tailing live stream — Ctrl-C to detach (loop keeps running)\n\n")
	return pid, streamName
}

func autodevPIDFile(loopName string) string {
	return "/tmp/yaver/autodev-" + safeFileSegment(loopName) + ".pid"
}

func safeFileSegment(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(s)
}

func safeStreamName(s string) string {
	return strings.ReplaceAll(s, ":", "_")
}

func readAutodevPID(loopName string) (int, bool) {
	data, err := os.ReadFile(autodevPIDFile(loopName))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	// Liveness check: signal 0 returns nil if the process exists.
	if err := syscall.Kill(pid, 0); err != nil {
		_ = os.Remove(autodevPIDFile(loopName))
		return 0, false
	}
	return pid, true
}

// tailDetachedAutodev attaches to the given stream over SSE and
// prints lines to stdout. Blocks until the user hits Ctrl-C, the
// stream closes (loop ended), or the daemon goes away. Detaching
// (Ctrl-C) does NOT kill the detached loop process — the user can
// re-attach later with `yaver stream autodev:<loop>`.
func tailDetachedAutodev(streamName string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "[autodev] not authenticated — can't tail; loop is still running detached.")
		return
	}
	url := "http://127.0.0.1:18080/streams/" + streamName
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Accept", "text/event-stream")

	// Wait briefly for the detached process to publish anything so
	// the SSE history snapshot has something to replay.
	time.Sleep(700 * time.Millisecond)

	tailStream(streamName)
}

// findAutodevForkLogPath returns the most recent fork log for a
// loop, or "" if none. Used by the help/status commands.
func findAutodevForkLogPath(loopName string) string {
	matches, _ := filepath.Glob(fmt.Sprintf("/tmp/yaver/autodev-%s-*.fork.log",
		safeStreamName("autodev:"+loopName)))
	if len(matches) == 0 {
		return ""
	}
	// Sort lexicographically — timestamps in filenames are
	// chronological.
	last := matches[0]
	for _, m := range matches[1:] {
		if m > last {
			last = m
		}
	}
	return last
}
