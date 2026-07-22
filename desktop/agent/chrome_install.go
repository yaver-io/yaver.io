package main

// chrome_install.go — make sure this box has a browser it can actually drive.
//
// Why the agent installs this itself, rather than printing a hint (the same
// argument as EnsureTmuxInstalled in tmux.go):
//
// A browser is not a nice-to-have on a workspace. `chrome-webrtc` is the
// FALLBACK for every browser-renderable stack — RN, Expo, Flutter, SwiftWasm,
// Next — whenever the viewer cannot reach the dev server or cannot render a
// URL (a TV, a car, a phone on cellular with no relay HTTP proxy). It is also
// the light alternative to Redroid, which forces the `build` machine class at
// ~6.5 GB. So a workspace with no browser silently loses the degraded preview
// path for every stack, and the user sees "preview unavailable" with no idea
// that one apt line stands between them and a working loop.
//
// Two things make this worth code rather than documentation, both verified on
// a clean Ubuntu 24.04 box on 2026-07-22:
//
//  1. `apt-get install -y google-chrome-stable` FAILS. The package is not in
//     the Debian/Ubuntu archives at all — it lives in Google's own repo, which
//     must be added first, with a keyring since apt-key was removed.
//     Our own install hint had prescribed the failing command.
//
//  2. `chromium` has NO apt candidate on Ubuntu, and `chromium-browser` is a
//     SNAP STUB (2:1snap1-0ubuntu2) on BOTH jammy and noble. A snap shim does
//     not run headless in a container, so "apt-get install chromium-browser"
//     reports success and leaves you with something that cannot render.
//     That is a false green of the exact kind this codebase keeps paying for:
//     the inventory says a browser is installed, the operation says no.
//
// Constraints, because this runs unattended inside a daemon — identical to the
// tmux path:
//   - NEVER prompt. On Linux install only as root or when `sudo -n` already
//     works, so serve cannot hang forever on a password prompt.
//   - NEVER fatal. A box with no package manager is still a useful agent; it
//     just cannot serve pixel previews.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// chromeBinaryNames are the browsers the preview pipeline can drive, best
// first. Real Chrome is preferred on Linux precisely because the distro
// "chromium" packages are so often a snap shim.
var chromeBinaryNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
}

// DiscoverChromeBinary returns an absolute path to a usable browser, or "".
//
// It does NOT trust the name alone: a snap stub resolves on PATH and then
// fails to launch, so the candidate must also survive an actual
// `--version` invocation. Probe the operation, not the inventory.
func DiscoverChromeBinary() string {
	for _, name := range chromeBinaryNames {
		path := DiscoverBinary(name)
		if path == "" {
			continue
		}
		if chromeBinaryUsable(path) {
			return path
		}
	}
	return ""
}

// chromeBinaryUsable reports whether the binary actually runs. The snap stub
// on Ubuntu exits non-zero (or prints an install prompt) instead of reporting
// a version, which is exactly the case a PATH lookup cannot distinguish.
func chromeBinaryUsable(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return false
	}
	s := strings.ToLower(string(out))
	return strings.Contains(s, "chrome") || strings.Contains(s, "chromium")
}

// EnsureChromeInstalled installs a browser when none is usable, best-effort, at
// agent startup. Reports whether a browser is usable afterwards.
func EnsureChromeInstalled(ctx context.Context, logf func(format string, v ...interface{})) bool {
	if DiscoverChromeBinary() != "" {
		return true
	}

	run := func(name string, args ...string) bool {
		c, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(c, name, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "NONINTERACTIVE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			logf("Chrome: auto-install via %s failed (non-fatal): %v: %s",
				name, err, strings.TrimSpace(lastLine(string(out))))
			return false
		}
		clearDiscoveryCacheFor("google-chrome")
		clearDiscoveryCacheFor("google-chrome-stable")
		clearDiscoveryCacheFor("chromium")
		clearDiscoveryCacheFor("chromium-browser")
		return DiscoverChromeBinary() != ""
	}

	switch runtime.GOOS {
	case "darwin":
		brew := DiscoverBinary("brew")
		if brew == "" {
			logf("Chrome: not installed and Homebrew is absent — cannot auto-install. %s", ChromeInstallHint())
			return false
		}
		logf("Chrome: not installed — installing it now with brew (pixel previews need it)")
		return run(brew, "install", "--cask", "google-chrome")

	case "linux":
		// Root, or password-less sudo. Anything else would prompt.
		var sudo string
		if os.Geteuid() != 0 {
			s := DiscoverBinary("sudo")
			if s == "" {
				logf("Chrome: not installed and this agent is not root — cannot auto-install. %s", ChromeInstallHint())
				return false
			}
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			ok := exec.CommandContext(probe, s, "-n", "true").Run() == nil
			cancel()
			if !ok {
				logf("Chrome: not installed and sudo needs a password — cannot auto-install. %s", ChromeInstallHint())
				return false
			}
			sudo = s
		}
		sh := func(script string) bool {
			if sudo != "" {
				return run(sudo, "-n", "sh", "-c", script)
			}
			return run("sh", "-c", script)
		}

		if DiscoverBinary("apt-get") != "" {
			logf("Chrome: not installed — adding Google's apt repo and installing (pixel previews need it)")
			// Single shell string: the repo must exist before the install, and
			// `apt-get install google-chrome-stable` without it fails with
			// "Unable to locate package".
			return sh(chromeAptScript)
		}
		if DiscoverBinary("dnf") != "" {
			logf("Chrome: not installed — installing Google Chrome rpm")
			return sh("dnf install -y https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm")
		}
		if DiscoverBinary("pacman") != "" {
			// google-chrome is AUR-only on Arch; chromium there is a real build.
			logf("Chrome: not installed — installing chromium via pacman")
			return sh("pacman -S --noconfirm chromium")
		}
		logf("Chrome: no supported package manager found. %s", ChromeInstallHint())
		return false
	}

	logf("Chrome: auto-install not supported on %s. %s", runtime.GOOS, ChromeInstallHint())
	return false
}

// chromeAptScript adds Google's signed repo and installs Chrome.
//
// Keyring form, not apt-key: apt-key is removed on modern Debian/Ubuntu, and
// the deprecated form fails silently on some releases. `dpkg --print-architecture`
// keeps this correct on arm64 boxes, where the amd64-pinned line installs nothing.
const chromeAptScript = `set -e
install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list
apt-get update
apt-get install -y google-chrome-stable`

// ChromeInstallHint is the one-liner a human can run. Kept next to the
// automation so the two can never drift.
func ChromeInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Install it with: brew install --cask google-chrome"
	case "linux":
		return fmt.Sprintf("Install it with:\n%s", chromeAptScript)
	}
	return "Install Google Chrome from https://www.google.com/chrome/"
}
