package main

import (
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"runtime"
	"strconv"
	"time"
)

func shouldEnableHeadlessKeepAwake(cfg *Config) bool {
	if isWSL() {
		return false
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	if cfg != nil && cfg.HeadlessKeepAwake != nil {
		return *cfg.HeadlessKeepAwake
	}
	return true
}

func applyDefaultHeadlessKeepAwake(cfg *Config) bool {
	if cfg == nil || cfg.HeadlessKeepAwake != nil {
		return false
	}
	if isWSL() || (runtime.GOOS != "darwin" && runtime.GOOS != "linux") {
		return false
	}
	enabled := true
	cfg.HeadlessKeepAwake = &enabled
	return true
}

func startHeadlessKeepAwake(cfg *Config) func() {
	if !shouldEnableHeadlessKeepAwake(cfg) {
		if isWSL() && cfg != nil && cfg.HeadlessKeepAwake != nil && *cfg.HeadlessKeepAwake {
			log.Printf("[power] WSL detected: runtime sleep inhibition is not supported; rely on the WSL startup helper and Windows power settings instead")
		}
		return nil
	}
	if buildHeadlessKeepAwakeCommand(os.Getpid()) == nil {
		return nil // no inhibitor available on this platform
	}
	stop := make(chan struct{})
	go superviseKeepAwake(os.Getpid(), stop)
	return func() { close(stop) }
}

// superviseKeepAwake keeps the sleep-inhibitor (caffeinate on macOS,
// systemd-inhibit on Linux) alive for the WHOLE agent lifetime, RESPAWNING it if
// it dies while the agent is still running.
//
// Incident (2026-07-21): the remote mac-mini dropped off the network — Tailscale
// "offline, last seen 32m ago". Root cause class: the keep-awake helper was
// spawned ONCE and never re-checked, so the moment it exited (a broad `pkill`
// during a build, an OOM, a crash) the system was free to idle-sleep and the box
// vanished, even though `yaver serve` was still running. A remote box that sleeps
// is a remote box that's gone. So: as long as the agent runs, an inhibitor MUST
// be running — if it dies, bring it back. Bounded (a fixed delay between
// respawns, never a tight loop) per the no-hammer rule.
func superviseKeepAwake(pid int, stop <-chan struct{}) {
	superviseKeepAwakeWith(func() *osexec.Cmd { return buildHeadlessKeepAwakeCommand(pid) }, 15*time.Second, stop)
}

// superviseKeepAwakeWith is the testable core: `build` yields a fresh inhibitor
// command (nil = platform has none), `respawnDelay` is the bounded wait between
// respawns. Split out so the respawn/stop behavior is unit-tested without a real
// caffeinate.
func superviseKeepAwakeWith(build func() *osexec.Cmd, respawnDelay time.Duration, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		cmd := build()
		if cmd == nil {
			return
		}
		if err := cmd.Start(); err != nil {
			log.Printf("[power] keep-awake helper failed to start: %v; retrying in 15s", err)
		} else {
			log.Printf("[power] headless keep-awake active via %s (PID %d)", cmd.Path, cmd.Process.Pid)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-stop:
				if cmd.Process != nil {
					_ = terminateProcess(cmd.Process)
				}
				return
			case werr := <-done:
				// The helper exited while the agent is STILL running — it was
				// killed/crashed, not because we're shutting down. Respawn so the
				// box can't drift into sleep.
				log.Printf("[power] keep-awake helper exited (%v) while agent is running — respawning so the box stays awake/reachable", werr)
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(respawnDelay):
		}
	}
}

func buildHeadlessKeepAwakeCommand(pid int) *osexec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		if _, err := osexec.LookPath("caffeinate"); err != nil {
			log.Printf("[power] macOS caffeinate not found; continuing without sleep inhibition")
			return nil
		}
		return osexec.Command("caffeinate", "-dimsu", "-w", strconv.Itoa(pid))
	case "linux":
		if isWSL() {
			return nil
		}
		if _, err := osexec.LookPath("systemd-inhibit"); err != nil {
			log.Printf("[power] systemd-inhibit not found; continuing without sleep inhibition")
			return nil
		}
		script := fmt.Sprintf("while kill -0 %d 2>/dev/null; do sleep 30; done", pid)
		return osexec.Command("systemd-inhibit", "--what=sleep", "--why=Yaver headless agent", "--mode=block", "sh", "-lc", script)
	default:
		return nil
	}
}
