package main

// screenlog_deps.go — make screenlog "just work" after `yaver install` by
// ensuring the OS screen-capture dependency is present.
//
//   - macOS:   none — `screencapture` is built into the OS.
//   - Windows: none — PowerShell `CopyFromScreen` is built in.
//   - WSL:     none — captures the Windows host via powershell.exe interop.
//   - Linux (real display): needs a screenshot CLI. We install `scrot`
//     (tiny, ubiquitous) via the detected package manager.
//
// Two entry points:
//   - installScreenlogDeps()        — explicit, interactive (`yaver screenlog
//     install-deps`); may prompt for sudo.
//   - ensureScreenlogDepsBestEffort()— quiet, non-interactive (sudo -n),
//     called once during `yaver serve` startup on Linux so a fresh install
//     self-provisions where it can, and just logs a hint where it can't.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// screenlogDepStatus reports whether real capture deps are satisfied and,
// if not, what to install. Surfaced in the drivers probe + UI.
func screenlogDepStatus() map[string]interface{} {
	switch runtime.GOOS {
	case "darwin":
		return map[string]interface{}{"needed": false, "satisfied": true, "note": "screencapture is built into macOS"}
	case "windows":
		return map[string]interface{}{"needed": false, "satisfied": true, "note": "PowerShell CopyFromScreen is built in"}
	case "linux":
		if isWSLHost() && lookPathOK("powershell.exe") {
			return map[string]interface{}{"needed": false, "satisfied": true, "note": "WSL captures the Windows host via interop"}
		}
		if linuxScreenshotToolPresent() {
			return map[string]interface{}{"needed": true, "satisfied": true, "tool": linuxScreenshotTool()}
		}
		return map[string]interface{}{
			"needed": true, "satisfied": false, "tool": "scrot",
			"install": "yaver screenlog install-deps",
		}
	}
	return map[string]interface{}{"needed": false, "satisfied": true}
}

func linuxScreenshotToolPresent() bool {
	return lookPathOK("scrot") || lookPathOK("gnome-screenshot") || lookPathOK("import")
}

func linuxScreenshotTool() string {
	switch {
	case lookPathOK("scrot"):
		return "scrot"
	case lookPathOK("gnome-screenshot"):
		return "gnome-screenshot"
	case lookPathOK("import"):
		return "imagemagick"
	}
	return ""
}

// linuxScreenshotInstallCmd returns the (binary, args) to install scrot via
// the first available package manager, prefixed with sudo when not root.
func linuxScreenshotInstallCmd(nonInteractive bool) ([]string, bool) {
	type pm struct {
		bin  string
		args []string
	}
	order := []pm{
		{"apt-get", []string{"install", "-y", "scrot"}},
		{"dnf", []string{"install", "-y", "scrot"}},
		{"pacman", []string{"-S", "--noconfirm", "scrot"}},
		{"zypper", []string{"install", "-y", "scrot"}},
		{"apk", []string{"add", "scrot"}},
	}
	for _, p := range order {
		if DiscoverBinary(p.bin) == "" {
			continue
		}
		cmd := append([]string{p.bin}, p.args...)
		if os.Geteuid() != 0 {
			if DiscoverBinary("sudo") == "" {
				return cmd, false // can't elevate; caller reports
			}
			sudo := []string{"sudo"}
			if nonInteractive {
				sudo = append(sudo, "-n")
			}
			cmd = append(sudo, cmd...)
		}
		return cmd, true
	}
	return nil, false
}

// installScreenlogDeps installs the capture dependency for this OS. Returns
// a human message. interactive=true allows sudo to prompt.
func installScreenlogDeps(interactive bool) (string, error) {
	st := screenlogDepStatus()
	if sat, _ := st["satisfied"].(bool); sat {
		if note, _ := st["note"].(string); note != "" {
			return "nothing to install — " + note, nil
		}
		return "already installed (" + fmt.Sprint(st["tool"]) + ")", nil
	}
	if runtime.GOOS != "linux" {
		return "no installable dependency for " + runtime.GOOS, nil
	}
	cmd, ok := linuxScreenshotInstallCmd(!interactive)
	if !ok {
		return "", fmt.Errorf("no supported package manager found — install scrot (or gnome-screenshot / imagemagick) manually")
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	if interactive {
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return "", fmt.Errorf("install failed (%s): %v", strings.Join(cmd, " "), err)
		}
	} else {
		out, err := c.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("install failed (%s): %v: %s", strings.Join(cmd, " "), err, strings.TrimSpace(string(out)))
		}
	}
	if !linuxScreenshotToolPresent() {
		return "", fmt.Errorf("ran %q but scrot still not on PATH", strings.Join(cmd, " "))
	}
	return "installed scrot via " + cmd[0], nil
}

// ensureScreenlogDepsBestEffort is the quiet auto-provision hook for
// `yaver serve` on Linux: non-interactive, non-fatal. Installs where
// passwordless sudo (or root) allows, otherwise logs the one-liner.
func ensureScreenlogDepsBestEffort() {
	if runtime.GOOS != "linux" {
		return
	}
	st := screenlogDepStatus()
	if sat, _ := st["satisfied"].(bool); sat {
		return
	}
	if msg, err := installScreenlogDeps(false); err == nil {
		fmt.Fprintf(os.Stderr, "[screenlog] %s\n", msg)
	} else {
		fmt.Fprintf(os.Stderr, "[screenlog] screen capture needs a screenshot tool — run `yaver screenlog install-deps` (or `sudo apt-get install -y scrot`)\n")
	}
}
