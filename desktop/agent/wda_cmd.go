package main

// wda_cmd.go — `yaver install wda` one-liner for the iOS Simulator.
//
// Without WebDriverAgent, yaver-test-sdk's ios-sim target can only
// tap by coordinate. With WDA booted on the simulator, tap-by-
// selector lights up — the dev writes `click: 'accessibility-id=
// submit'` and it Just Works.
//
// The dev never has to open Xcode. This command:
//
//  1. Checks we're on macOS with a booted iOS Simulator (xcrun
//     simctl list devices booted). Bails with a clear hint if not.
//  2. Installs appium-xcuitest-driver globally via npm — that
//     package ships the WebDriverAgent xcodeproj on disk, which is
//     the supported path per the Appium team.
//  3. Locates the WDA xcodeproj inside the npm install tree.
//  4. Runs `xcodebuild build-for-testing` + `xcrun simctl install`
//     to deploy WDA to the booted sim.
//  5. Launches WDA in the background on port 8100 and waits for
//     the /status endpoint to answer.
//
// Runs entirely from source the user already trusts (Appium on npm,
// xcodebuild from Xcode). We never download random binaries.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// runInstallWDA is the `yaver install wda` entry point.
func runInstallWDA(args []string) {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "WDA install requires macOS (the iOS Simulator only runs on Mac).")
		os.Exit(2)
	}
	fmt.Println("=> yaver install wda — booting WebDriverAgent on the iOS Simulator")
	fmt.Println()

	// Step 1: verify xcrun + xcodebuild + a booted simulator.
	if _, err := exec.LookPath("xcrun"); err != nil {
		fmt.Fprintln(os.Stderr, "   xcrun not found — install Xcode from the App Store, then run `xcode-select --install`.")
		os.Exit(2)
	}
	if _, err := exec.LookPath("xcodebuild"); err != nil {
		fmt.Fprintln(os.Stderr, "   xcodebuild not found — open Xcode once and accept the license, then rerun.")
		os.Exit(2)
	}
	udid, deviceName, err := bootedSimUDID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "   no booted iOS Simulator found: %v\n", err)
		fmt.Fprintln(os.Stderr, "   Start one first: `xcrun simctl boot \"iPhone 15\"` (or open Simulator.app)")
		os.Exit(2)
	}
	fmt.Printf("   booted simulator: %s (%s)\n", deviceName, udid)

	// Step 2: npm + appium-xcuitest-driver.
	if _, err := exec.LookPath("npm"); err != nil {
		fmt.Fprintln(os.Stderr, "   npm not found — run `yaver install node` first.")
		os.Exit(2)
	}
	npmRoot := strings.TrimSpace(runShellCapture("npm", "root", "-g"))
	if npmRoot == "" {
		fmt.Fprintln(os.Stderr, "   could not resolve global npm root")
		os.Exit(2)
	}
	wdaProj := filepath.Join(npmRoot, "appium-xcuitest-driver", "node_modules", "appium-webdriveragent", "WebDriverAgent.xcodeproj")
	if _, serr := os.Stat(wdaProj); serr != nil {
		fmt.Println("   appium-xcuitest-driver not present globally — installing via npm")
		runShellInteractive("npm install -g appium-xcuitest-driver")
		if _, serr := os.Stat(wdaProj); serr != nil {
			fmt.Fprintf(os.Stderr, "   WDA xcodeproj still missing at %s after npm install\n", wdaProj)
			os.Exit(2)
		}
	}
	wdaDir := filepath.Dir(wdaProj)
	fmt.Printf("   WDA source: %s\n", wdaDir)

	// Step 3: build WDA for the booted simulator.
	fmt.Println("   building WebDriverAgentRunner for the booted simulator (this takes 1-2 minutes on first run)...")
	buildCmd := exec.Command("xcodebuild",
		"-project", wdaProj,
		"-scheme", "WebDriverAgentRunner",
		"-destination", fmt.Sprintf("id=%s", udid),
		"CODE_SIGNING_ALLOWED=NO",
		"build-for-testing",
	)
	buildCmd.Dir = wdaDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "   xcodebuild build-for-testing failed: %v\n", err)
		os.Exit(2)
	}

	// Step 4: launch WDA with `xcodebuild test-without-building`.
	// We spawn it in the background — the dev keeps their terminal
	// free and WDA stays up for future `yaver test run` invocations
	// until they kill the process or shut down the simulator.
	fmt.Println("   launching WebDriverAgentRunner in the background...")
	launchCmd := exec.Command("xcodebuild",
		"-project", wdaProj,
		"-scheme", "WebDriverAgentRunner",
		"-destination", fmt.Sprintf("id=%s", udid),
		"CODE_SIGNING_ALLOWED=NO",
		"test-without-building",
	)
	launchCmd.Dir = wdaDir
	// Detach stdout/stderr to a log file so the dev can tail it later.
	logDir := filepath.Join(os.TempDir(), "yaver-wda")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("wda-%s.log", udid))
	logFile, lerr := os.Create(logPath)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "   could not open log file %s: %v\n", logPath, lerr)
		os.Exit(2)
	}
	launchCmd.Stdout = logFile
	launchCmd.Stderr = logFile
	if err := launchCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "   could not start WDA: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("   WDA pid=%d log=%s\n", launchCmd.Process.Pid, logPath)

	// Step 5: wait for the /status endpoint to answer on the default
	// WDA port (8100). We probe every 500ms for up to 60s; first-run
	// launches can be slow because the sim warms up indexing.
	fmt.Println("   waiting for WDA to answer on http://127.0.0.1:8100/status ...")
	if err := waitForWDAStatus(60 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "   WDA did not come up: %v\n", err)
		fmt.Fprintf(os.Stderr, "   check the log: tail -f %s\n", logPath)
		os.Exit(2)
	}

	fmt.Println()
	fmt.Println("✓ WebDriverAgent is live on http://127.0.0.1:8100")
	fmt.Println("  yaver-test-sdk's ios-sim target now supports tap-by-selector.")
	fmt.Println()
	fmt.Println("  To stop WDA later:")
	fmt.Printf("    kill %d    # or shut down the simulator\n", launchCmd.Process.Pid)
}

// bootedSimUDID returns the UDID + name of the currently booted iOS
// Simulator, or an error if zero / multiple sims are booted. Parses
// `xcrun simctl list devices booted --json`.
func bootedSimUDID() (string, string, error) {
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "booted", "--json").Output()
	if err != nil {
		return "", "", fmt.Errorf("xcrun simctl list: %w", err)
	}
	var payload struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if jerr := json.Unmarshal(out, &payload); jerr != nil {
		return "", "", fmt.Errorf("parse simctl output: %w", jerr)
	}
	var booted []struct {
		UDID, Name string
	}
	for _, list := range payload.Devices {
		for _, d := range list {
			if d.State == "Booted" {
				booted = append(booted, struct{ UDID, Name string }{d.UDID, d.Name})
			}
		}
	}
	switch len(booted) {
	case 0:
		return "", "", fmt.Errorf("no booted simulator")
	case 1:
		return booted[0].UDID, booted[0].Name, nil
	default:
		return "", "", fmt.Errorf("multiple simulators booted; shut down all but one and retry")
	}
}

// waitForWDAStatus polls http://127.0.0.1:8100/status until it
// responds or deadline fires.
func waitForWDAStatus(deadline time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		resp, err := client.Get("http://127.0.0.1:8100/status")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", deadline)
}

// runShellCapture runs a command and returns its trimmed stdout.
// Errors are swallowed so the caller can decide how to fall back —
// the npm-root lookup above is the canonical consumer.
func runShellCapture(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
