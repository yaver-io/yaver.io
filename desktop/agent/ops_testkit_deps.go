package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ops_testkit_deps.go — one-shot dependency bootstrap for the test runner so a
// user never fails a test run because tooling is missing. Checks + installs
// (idempotently) everything the web/mobile drivers need: ffmpeg (highlight
// clips), a Chromium/Chrome (chromedp), Node + Playwright (the web-playwright
// driver), and Docker + the redroid image (mobile driver). Cross-platform
// best-effort: macOS via Homebrew, Linux via apt-get.
//
// Verbs:
//   testkit_deps_check   — synchronous status of every dependency
//   testkit_deps_install — async job that installs whatever's missing (poll
//                          studio_job_status, then re-check)

type depStatus struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Detail  string `json:"detail,omitempty"`
	How     string `json:"how,omitempty"` // how it would be installed
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("testkit_deps_check",
		"Check the test runner's dependencies on THIS machine (ffmpeg, chromium/chrome, node, playwright, docker, redroid image) so a run never fails on missing tooling. Synchronous → {os, pkgManager, deps:[{name,present,detail}], ready}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			deps, ready := checkTestkitDeps()
			return OpsResult{OK: true, Initial: map[string]any{
				"os": runtime.GOOS, "pkgManager": detectPkgManager(),
				"deps": deps, "ready": ready,
			}}
		})

	reg("testkit_deps_install",
		"Install every missing test-runner dependency once (ffmpeg, chromium, node, playwright+chromium, docker+redroid image), idempotently. Long-running — returns a jobId; poll studio_job_status, then testkit_deps_check. {include? (subset, e.g. [\"ffmpeg\",\"playwright\"]; default all), redroidImage?}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Include      []string `json:"include"`
				RedroidImage string   `json:"redroidImage"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			job, err := studioJobs.startDepsInstall(req.Include, req.RedroidImage)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func detectPkgManager() string {
	switch runtime.GOOS {
	case "darwin":
		if have("brew") {
			return "brew"
		}
		return "none (install Homebrew)"
	case "linux":
		if have("apt-get") {
			return "apt-get"
		}
		if have("dnf") {
			return "dnf"
		}
		if have("pacman") {
			return "pacman"
		}
		return "unknown"
	}
	return runtime.GOOS
}

func chromiumPresent() bool {
	for _, b := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if have(b) {
			return true
		}
	}
	// macOS app bundle
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"); err == nil {
			return true
		}
	}
	return false
}

func playwrightPresent() bool {
	if !have("node") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// resolvable as a module?
	out, _ := exec.CommandContext(ctx, "node", "-e", "try{require.resolve('playwright');console.log('ok')}catch(e){}").CombinedOutput()
	return strings.Contains(string(out), "ok")
}

func redroidImagePresent(image string) bool {
	if !have("docker") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "docker", "images", "-q", image).CombinedOutput()
	return strings.TrimSpace(string(out)) != ""
}

const defaultRedroidImage = "redroid/redroid:13.0.0-latest"

func checkTestkitDeps() ([]depStatus, bool) {
	pm := detectPkgManager()
	deps := []depStatus{
		{Name: "ffmpeg", Present: have("ffmpeg"), How: pm + " install ffmpeg"},
		{Name: "chromium", Present: chromiumPresent(), How: "playwright install chromium / " + pm + " install chromium"},
		{Name: "node", Present: have("node"), How: pm + " install node"},
		{Name: "playwright", Present: playwrightPresent(), How: "npm i -g playwright && npx playwright install chromium"},
		{Name: "docker", Present: have("docker"), How: pm + " install docker (mobile/redroid only)"},
		{Name: "redroid-image", Present: redroidImagePresent(defaultRedroidImage), How: "docker pull " + defaultRedroidImage},
	}
	// "ready" = the web path works: ffmpeg + some chromium. Docker/redroid only
	// needed for mobile specs, so they don't block readiness.
	ready := false
	var ff, ch bool
	for _, d := range deps {
		if d.Name == "ffmpeg" {
			ff = d.Present
		}
		if d.Name == "chromium" {
			ch = d.Present
		}
	}
	ready = ff && ch
	return deps, ready
}

func (m *studioJobManager) startDepsInstall(include []string, redroidImage string) (*studioJob, error) {
	if redroidImage == "" {
		redroidImage = defaultRedroidImage
	}
	want := map[string]bool{}
	for _, n := range include {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	all := len(want) == 0

	job := m.newJob("testkit-deps", "")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.mu.Unlock()

		pm := detectPkgManager()
		run := func(label string, name string, args ...string) {
			job.log(label, "$ "+name+" "+strings.Join(args, " "))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			tail := lastNLines(string(out), 4)
			if err != nil {
				job.log("", label+" failed: "+err.Error()+" "+tail)
			} else {
				job.log("", label+" ok "+tail)
			}
		}
		pmInstall := func(label string, pkgs ...string) {
			switch pm {
			case "brew":
				run(label, "brew", append([]string{"install"}, pkgs...)...)
			case "apt-get":
				run(label, "sudo", append([]string{"apt-get", "install", "-y"}, pkgs...)...)
			case "dnf":
				run(label, "sudo", append([]string{"dnf", "install", "-y"}, pkgs...)...)
			case "pacman":
				run(label, "sudo", append([]string{"pacman", "-S", "--noconfirm"}, pkgs...)...)
			default:
				job.log("", label+": no supported package manager ("+pm+") — install "+strings.Join(pkgs, " ")+" manually")
			}
		}

		// ffmpeg — highlight clips
		if (all || want["ffmpeg"]) && !have("ffmpeg") {
			pmInstall("ffmpeg", "ffmpeg")
		}
		// chromium — chromedp web driver
		if (all || want["chromium"]) && !chromiumPresent() {
			if runtime.GOOS == "darwin" && pm == "brew" {
				run("chromium", "brew", "install", "--cask", "chromium")
			} else {
				pmInstall("chromium", "chromium")
			}
		}
		// node — needed for playwright
		if (all || want["node"] || want["playwright"]) && !have("node") {
			if pm == "brew" {
				run("node", "brew", "install", "node")
			} else {
				pmInstall("node", "nodejs", "npm")
			}
		}
		// playwright + its chromium
		if (all || want["playwright"]) && have("node") && !playwrightPresent() {
			run("playwright", "npm", "install", "-g", "playwright")
			if have("npx") {
				run("playwright-chromium", "npx", "--yes", "playwright", "install", "chromium")
			}
		}
		// docker image for redroid (mobile)
		if (all || want["redroid"] || want["redroid-image"]) && have("docker") && !redroidImagePresent(redroidImage) {
			run("redroid-image", "docker", "pull", redroidImage)
		}

		deps, ready := checkTestkitDeps()
		var missing []string
		for _, d := range deps {
			if !d.Present {
				missing = append(missing, d.Name)
			}
		}
		job.mu.Lock()
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		if len(missing) == 0 {
			job.log("done", "all dependencies present")
		} else {
			job.log("done", fmt.Sprintf("installed what I could; still missing: %s (web ready=%v)", strings.Join(missing, ", "), ready))
		}
	}()
	return job, nil
}

func lastNLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "… " + strings.Join(lines, " | ")
}
