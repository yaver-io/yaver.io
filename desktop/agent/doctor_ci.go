package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// osStat is split out so test files can stub it. Avoids importing
// `os` directly into the helper at every call site.
func osStat(p string) (interface{}, error) { return os.Stat(p) }

// doctor_ci.go contains the small helpers `runDoctor` uses for the
// "Local CI integrations" section. They live in their own file so the
// massive runDoctor function in main.go stays a little easier to read.

// detectChromeForCI looks for a Chromium-family browser in the
// places chromedp itself looks. Returns ("", "") if none found.
func detectChromeForCI() (path, version string) {
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "linux":
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}
	// Also check $PATH for the usual names.
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, p)
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := exec.LookPath(c); err == nil || pathExists(c) {
			ver := probeBrowserVersion(c)
			return c, ver
		}
	}
	return "", ""
}

func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := osStat(p)
	return err == nil
}

// probeBrowserVersion runs `<bin> --version` with a 2s timeout. Returns
// "unknown" on any error so the caller still gets a usable label.
func probeBrowserVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return firstLine(strings.TrimSpace(string(out)))
}

// detectBinaryWithVersion looks up `name` in PATH and asks it for its
// version. Returns ("", "") if the binary isn't on PATH.
func detectBinaryWithVersion(name, versionFlag string) (path, version string) {
	p, err := lookPathWithRuntimes(name)
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, versionFlag).CombinedOutput()
	if err != nil {
		return p, "unknown"
	}
	return p, firstLine(strings.TrimSpace(string(out)))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// testkitHostStatus is a thin wrapper so doctor doesn't import testkit
// at the top of main.go (which would force a longer build dep cycle).
func testkitHostStatus() testkit.HostStatus {
	return testkit.SnapshotHost()
}

const autoBootManualURL = "https://yaver.io/manuals/auto-boot"

type headlessPowerStatus struct {
	ok      bool
	summary string
	details []string
}

func detectHeadlessPowerStatus() *headlessPowerStatus {
	switch runtime.GOOS {
	case "darwin":
		return detectDarwinHeadlessPowerStatus()
	case "linux":
		return detectLinuxHeadlessPowerStatus()
	default:
		return nil
	}
}

func detectDarwinHeadlessPowerStatus() *headlessPowerStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "-g").CombinedOutput()
	if err != nil {
		return &headlessPowerStatus{
			ok:      false,
			summary: "pmset unavailable",
			details: []string{"Install/enable macOS power tools or verify manually: " + autoBootManualURL},
		}
	}
	values := parseKeyValueLines(string(out))
	var problems []string
	if v := pmsetIntValue(values, "sleep"); v != 0 {
		problems = append(problems, fmt.Sprintf("sleep=%d (expected 0 for headless serve)", v))
	}
	if v := pmsetIntValue(values, "disksleep"); v != 0 {
		problems = append(problems, fmt.Sprintf("disksleep=%d (expected 0)", v))
	}
	if v := pmsetIntValue(values, "autorestart"); v != 1 {
		problems = append(problems, fmt.Sprintf("autorestart=%d (expected 1 after power loss)", v))
	}
	if v, ok := values["powernap"]; ok && strings.TrimSpace(v) != "0" {
		problems = append(problems, fmt.Sprintf("powernap=%s (expected 0)", strings.TrimSpace(v)))
	}
	if cfg, _ := LoadConfig(); shouldEnableHeadlessKeepAwake(cfg) {
		problems = append(problems, "runtime keep-awake handled by Yaver while `yaver serve` runs")
	}
	summary := fmt.Sprintf("sleep=%d, disksleep=%d, autorestart=%d", pmsetIntValue(values, "sleep"), pmsetIntValue(values, "disksleep"), pmsetIntValue(values, "autorestart"))
	if len(problems) == 0 {
		return &headlessPowerStatus{ok: true, summary: summary}
	}
	if len(problems) == 1 && strings.Contains(problems[0], "runtime keep-awake handled by Yaver") {
		return &headlessPowerStatus{ok: true, summary: summary, details: problems}
	}
	return &headlessPowerStatus{
		ok:      false,
		summary: summary,
		details: append(problems, "Manual: "+autoBootManualURL),
	}
}

func detectLinuxHeadlessPowerStatus() *headlessPowerStatus {
	var details []string
	ok := true
	linger := "unknown"
	if user := strings.TrimSpace(os.Getenv("USER")); user != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "loginctl", "show-user", user, "-p", "Linger").CombinedOutput()
		if err == nil {
			parts := strings.Split(strings.TrimSpace(string(out)), "=")
			if len(parts) == 2 {
				linger = strings.TrimSpace(parts[1])
				if !strings.EqualFold(linger, "yes") {
					ok = false
					details = append(details, "linger disabled (run: sudo loginctl enable-linger $USER)")
				}
			}
		}
	}
	if !isAutoStartInstalled() {
		ok = false
		details = append(details, "auto-start service missing (run `yaver serve` once)")
	}
	if cfg, _ := LoadConfig(); shouldEnableHeadlessKeepAwake(cfg) {
		details = append(details, "runtime keep-awake handled by Yaver while `yaver serve` runs")
	}
	details = append(details, "Firmware auto-reboot after power loss is manual: "+autoBootManualURL)
	return &headlessPowerStatus{
		ok:      ok,
		summary: fmt.Sprintf("auto-start=%t, linger=%s", isAutoStartInstalled(), linger),
		details: details,
	}
}

func parseKeyValueLines(out string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		values[fields[0]] = fields[len(fields)-1]
	}
	return values
}

func pmsetIntValue(values map[string]string, key string) int {
	raw, ok := values[key]
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return -1
	}
	return n
}
