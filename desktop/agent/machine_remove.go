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

func schedulePermanentMachineRemoval(onShutdown func()) {
	go func() {
		time.Sleep(350 * time.Millisecond)
		if err := performPermanentMachineRemoval(); err != nil {
			log.Printf("[machine/remove] permanent removal failed: %v", err)
		}
		if onShutdown != nil {
			onShutdown()
		}
	}()
}

func performPermanentMachineRemoval() error {
	var errs []string

	if cfg, err := LoadConfig(); err == nil {
		if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" && cfg.DeviceID != "" {
			if err := RemoveDeviceShutdown(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); err != nil {
				errs = append(errs, "convex unregister: "+err.Error())
				if markErr := MarkOffline(cfg.ConvexSiteURL, cfg.AuthToken, cfg.DeviceID); markErr != nil {
					errs = append(errs, "mark offline: "+markErr.Error())
				}
			}
		}
	} else {
		errs = append(errs, "load config: "+err.Error())
	}

	if err := removeInstalledYaverServices(); err != nil {
		errs = append(errs, "remove services: "+err.Error())
	}

	// Strip authorized_keys before nuking ~/.yaver — once the agent's
	// home is gone the OS user might still log in, but any pubkey the
	// previous bootstrap pushed should be gone with the rest of the
	// install. Done here (not after RemoveAll) so even a config-dir
	// resolution failure doesn't strand the ssh entries.
	for _, line := range uninstallExtraCleanup() {
		log.Printf("[machine/remove] %s", line)
	}

	if configDir, err := ConfigDir(); err == nil {
		if err := os.RemoveAll(configDir); err != nil {
			errs = append(errs, "remove ~/.yaver: "+err.Error())
		}
	} else {
		errs = append(errs, "resolve config dir: "+err.Error())
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
