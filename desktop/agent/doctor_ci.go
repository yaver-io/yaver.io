package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
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
	p, err := exec.LookPath(name)
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
