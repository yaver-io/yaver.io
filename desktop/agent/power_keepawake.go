package main

import (
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"runtime"
	"strconv"
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

	pid := os.Getpid()
	cmd := buildHeadlessKeepAwakeCommand(pid)
	if cmd == nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[power] Failed to start keep-awake helper: %v", err)
		return nil
	}
	log.Printf("[power] Headless keep-awake active via %s (PID %d)", cmd.Path, cmd.Process.Pid)
	return func() {
		if cmd.Process != nil {
			_ = terminateProcess(cmd.Process)
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
