package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const machineRemovalPhrase = "delete my machine"

func machineRemovalPhraseValid(phrase string) bool {
	return strings.EqualFold(strings.TrimSpace(phrase), machineRemovalPhrase)
}

func machineRemovalManualSteps() []string {
	return []string{
		"brew uninstall yaver",
		"npm uninstall -g yaver-cli",
		fmt.Sprintf("rm %s", os.Args[0]),
	}
}

// machineRemoveProgress is the callback invoked between every stage
// of performPermanentMachineRemoval. step is a stable identifier
// (convex_dereg, systemd_stop, shell_rc, ssh_keys, linger, config_dir).
// status ∈ {"running","ok","skipped","error"}. detail is a one-line
// human-readable message; err is non-nil only on "error".
//
// nil progress is allowed — old call-sites pre-1.99.163 still work
// without modification.
type machineRemoveProgress func(step, status, detail string, err error)

func schedulePermanentMachineRemoval(onShutdown func(), progress machineRemoveProgress) {
	go func() {
		time.Sleep(350 * time.Millisecond)
		if err := performPermanentMachineRemoval(progress); err != nil {
			log.Printf("[machine/remove] permanent removal failed: %v", err)
		}
		if progress != nil {
			progress("done", "ok", "agent process exiting", nil)
		}
		// SSE clients (web/mobile/CLI) are watching /streams/<machine-remove:...>
		// in a different goroutine. The AppendEvent above is non-blocking
		// and lands in the stream's 256-buffered channel, but the SSE
		// handler still needs to dequeue it, write "data: …" to the
		// socket, and call flusher.Flush() before the agent's HTTP
		// server tears down. Without a brief grace period, onShutdown
		// races the flush and the result event is lost — clients see
		// the connection drop without ever receiving "done", which
		// looks like a partial uninstall to the user.
		time.Sleep(400 * time.Millisecond)
		if onShutdown != nil {
			onShutdown()
		}
	}()
}

func performPermanentMachineRemoval(progress machineRemoveProgress) error {
	emit := func(step, status, detail string, err error) {
		if progress != nil {
			progress(step, status, detail, err)
		}
	}
	var errs []string

	emit("convex_dereg", "running", "deleting device row + cascading sdkTokens / projects / primary pointer", nil)
	if cfg, err := LoadConfig(); err == nil {
		if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" && cfg.DeviceID != "" {
			if err := RemoveDeviceShutdown(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
				errs = append(errs, "convex unregister: "+err.Error())
				emit("convex_dereg", "error", "remote dereg failed; falling back to mark-offline", err)
				if markErr := MarkOffline(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); markErr != nil {
					errs = append(errs, "mark offline: "+markErr.Error())
					emit("convex_dereg", "error", "mark-offline fallback also failed (network?)", markErr)
				} else {
					emit("convex_dereg", "ok", "marked offline (Convex will purge via heartbeat staleness)", nil)
				}
			} else {
				emit("convex_dereg", "ok", "device + sdkTokens + projects + primaryDeviceId cleared", nil)
			}
		} else {
			emit("convex_dereg", "skipped", "no auth token / device id — nothing to dereg", nil)
		}
	} else {
		errs = append(errs, "load config: "+err.Error())
		emit("convex_dereg", "error", "could not load config", err)
	}

	emit("services", "running", "stopping + removing systemd / launchd unit", nil)
	if err := removeInstalledYaverServices(); err != nil {
		errs = append(errs, "remove services: "+err.Error())
		emit("services", "error", "service removal partial", err)
	} else {
		emit("services", "ok", "agent service stopped + unit file removed", nil)
	}

	// Cleanups outside ~/.yaver: shell rc PATH block, ssh
	// authorized_keys yaver-bootstrap entries, linger flag.
	emit("extra_cleanup", "running", "shell rc + ssh authorized_keys + linger", nil)
	for _, line := range uninstallExtraCleanup() {
		log.Printf("[machine/remove] %s", line)
		emit("extra_cleanup", "ok", line, nil)
	}
	emit("extra_cleanup", "ok", "shell rc + ssh keys + linger cleared", nil)

	emit("config_dir", "running", "removing ~/.yaver", nil)
	if configDir, err := ConfigDir(); err == nil {
		if err := os.RemoveAll(configDir); err != nil {
			errs = append(errs, "remove ~/.yaver: "+err.Error())
			emit("config_dir", "error", "~/.yaver removal failed", err)
		} else {
			emit("config_dir", "ok", "~/.yaver removed (auth token, vault, logs, blobs)", nil)
		}
	} else {
		errs = append(errs, "resolve config dir: "+err.Error())
		emit("config_dir", "error", "could not resolve ~/.yaver path", err)
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func removeInstalledYaverServices() error {
	home, _ := os.UserHomeDir()
	var errs []string

	switch runtime.GOOS {
	case "darwin":
		for _, plist := range []string{
			filepath.Join(home, darwinLaunchAgentPath),
			filepath.Join(home, "Library", "LaunchAgents", "io.yaver.agent.plist"),
		} {
			if strings.TrimSpace(plist) == "" {
				continue
			}
			_ = exec.Command("launchctl", "unload", plist).Run()
			if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err.Error())
			}
		}
	case "linux":
		for _, unit := range []string{"yaver", "yaver-agent"} {
			_ = exec.Command("systemctl", "--user", "stop", unit).Run()
			_ = exec.Command("systemctl", "--user", "disable", unit).Run()
		}
		for _, unitPath := range []string{
			filepath.Join(home, ".config", "systemd", "user", "yaver.service"),
			filepath.Join(home, ".config", "systemd", "user", "yaver-agent.service"),
		} {
			if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err.Error())
			}
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	case "windows":
		removeAutoStart()
	default:
		removeAutoStart()
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
