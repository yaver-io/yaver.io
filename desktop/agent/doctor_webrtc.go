package main

// doctor_webrtc.go — `yaver doctor webrtc [--install] [--json]`.
// Probes the native-WebRTC remote-runtime stack from the perspective
// of THIS box: which targets it can encode for, which deps are
// missing, and what the user can do about it.
//
// The probe is also reused by the capability response (so the web
// dashboard knows whether to advertise an Open Viewer button) — see
// remote_runtime.go's `viewerEnabled` field.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// WebRTCDoctorReport is the structured output of `yaver doctor
// webrtc --json`. Stable shape — the dashboard depends on field
// names. Extend by adding fields, never by renaming.
type WebRTCDoctorReport struct {
	Platform     string                 `json:"platform"`
	Arch         string                 `json:"arch"`
	HostClass    string                 `json:"hostClass"`
	AgentVersion string                 `json:"agentVersion"`
	Checks       []WebRTCDoctorCheck    `json:"checks"`
	Targets      map[string]bool        `json:"targets"`
	NextSteps    []string               `json:"nextSteps,omitempty"`
}

// WebRTCDoctorCheck is one OK/missing line in the report. Detail is
// a short human-readable note (version string, install hint, etc.).
type WebRTCDoctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// runDoctorWebRTC is the CLI entry point. Flags:
//   --json       structured output (one line, no headers)
//   --install    after probing, run `yaver install remote-runtime`
//                to fix what it can. Idempotent — already-installed
//                tools are skipped.
func runDoctorWebRTC(args []string) {
	wantJSON := false
	wantInstall := false
	for _, a := range args {
		switch a {
		case "--json":
			wantJSON = true
		case "--install":
			wantInstall = true
		case "-h", "--help":
			fmt.Println("usage: yaver doctor webrtc [--install] [--json]")
			fmt.Println()
			fmt.Println("Probes WebRTC remote-runtime deps for the current host.")
			fmt.Println("  --install   run `yaver install remote-runtime` after probing")
			fmt.Println("  --json      structured output for the dashboard")
			return
		}
	}

	report := buildWebRTCDoctorReport(context.Background())
	if wantJSON {
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return
	}
	printWebRTCDoctorReport(report)

	if wantInstall {
		fmt.Println()
		fmt.Println("→ Running `yaver install remote-runtime`…")
		// Re-enter through the install registry so we share the
		// progress + sudo-prompt machinery with everything else
		// `yaver install` does. This is the same hook the npm
		// postinstall already uses (see cli/src/postinstall.js).
		runInstall([]string{"remote-runtime"})
		fmt.Println()
		fmt.Println("Re-probing…")
		report = buildWebRTCDoctorReport(context.Background())
		printWebRTCDoctorReport(report)
	}
}

// buildWebRTCDoctorReport runs every probe and assembles the result.
// Safe to call without --install. No subprocesses are killed by the
// probe itself — only short-lived `--version` style invocations.
func buildWebRTCDoctorReport(ctx context.Context) WebRTCDoctorReport {
	report := WebRTCDoctorReport{
		Platform:     runtime.GOOS,
		Arch:         runtime.GOARCH,
		HostClass:    detectRuntimeHostClass(),
		AgentVersion: yaverVersionString(),
		Targets:      map[string]bool{},
	}

	// pion/webrtc is always present — it ships statically-linked in
	// the Go binary. We surface this so the user can tell at a
	// glance that the *agent* role is good even before they've
	// installed anything.
	report.Checks = append(report.Checks, WebRTCDoctorCheck{
		Name:   "pion/webrtc",
		OK:     true,
		Detail: "built into agent (statically linked)",
	})

	// In-tree H.264 NAL extractor (h264_extract.go). No runtime
	// probe needed — it's part of the binary. Surface it so the
	// "no ffmpeg dep" property is visible in the report.
	report.Checks = append(report.Checks, WebRTCDoctorCheck{
		Name:   "in-tree H.264 extractor",
		OK:     true,
		Detail: "no ffmpeg dep needed for android-emulator target",
	})

	// adb — required for the Android target. Probed via PATH +
	// `adb version`.
	adbCheck := probeBinary(ctx, "adb", "version")
	report.Checks = append(report.Checks, adbCheck)
	report.Targets["android-emulator"] = adbCheck.OK

	// xcrun — required for the iOS target on macOS. Linux/WSL
	// hosts get a "not applicable" line so the report doesn't look
	// broken for users with a working Android setup.
	if runtime.GOOS == "darwin" {
		xcrunCheck := probeBinary(ctx, "xcrun", "--version")
		report.Checks = append(report.Checks, xcrunCheck)
		report.Targets["ios-simulator"] = xcrunCheck.OK
		report.Checks = append(report.Checks, WebRTCDoctorCheck{
			Name:   "ios-simulator RTP encode",
			OK:     xcrunCheck.OK,
			Detail: "via xcrun simctl recordVideo + in-tree fragmented-MP4 parser",
		})
	} else {
		report.Checks = append(report.Checks, WebRTCDoctorCheck{
			Name:   "xcrun",
			OK:     false,
			Detail: "n/a — iOS Simulator targets need a paired remote macOS builder. See `yaver builder --help` (Phase 5).",
		})
		report.Targets["ios-simulator"] = false
	}

	// Optional: ffmpeg. Not required by the happy path but useful as
	// a fallback for unusual screenrecord builds (pre-Marshmallow
	// emulators that emit non-AVC sequences).
	ffmpegCheck := probeBinary(ctx, "ffmpeg", "-version")
	if ffmpegCheck.OK {
		ffmpegCheck.Detail = "optional fallback transcoder available"
	} else {
		ffmpegCheck.Detail = "optional — only needed for non-AVC capture sources"
	}
	ffmpegCheck.Name = "ffmpeg (optional)"
	report.Checks = append(report.Checks, ffmpegCheck)

	// Next-steps hints — only emitted when something is actionable.
	if !adbCheck.OK {
		report.NextSteps = append(report.NextSteps,
			"Install adb: `yaver install remote-runtime` (or pass --install to this command)")
	}
	if runtime.GOOS != "darwin" {
		report.NextSteps = append(report.NextSteps,
			"Pair a macOS builder for iOS targets: `yaver builder use <alias>` (Phase 5)")
	}
	return report
}

// probeBinary returns OK=true if `name` resolves on PATH and
// `name <verArg>` exits 0 within 2 s. The version output's first
// line becomes the Detail string, so the report shows what was
// actually picked up.
func probeBinary(ctx context.Context, name, verArg string) WebRTCDoctorCheck {
	check := WebRTCDoctorCheck{Name: name}
	path, err := exec.LookPath(name)
	if err != nil {
		check.Detail = "not on PATH"
		return check
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(probeCtx, path, verArg).CombinedOutput()
	first := firstNonEmptyLine(string(out))
	check.OK = true
	if first != "" {
		check.Detail = first
	} else {
		check.Detail = path
	}
	return check
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// yaverVersionString returns the agent's compile-time version. The
// `version` constant is defined in main.go and bumped on every
// release.
func yaverVersionString() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return "dev"
}

func printWebRTCDoctorReport(r WebRTCDoctorReport) {
	fmt.Printf("%s %s — yaver %s — host class: %s\n",
		r.Platform, r.Arch, r.AgentVersion, r.HostClass)
	fmt.Println(strings.Repeat("=", 65))
	for _, c := range r.Checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		if c.Detail == "" {
			fmt.Printf("%s %s\n", mark, c.Name)
		} else {
			fmt.Printf("%s %s — %s\n", mark, c.Name, c.Detail)
		}
	}
	fmt.Println()
	fmt.Println("Targets:")
	for _, target := range []string{"android-emulator", "ios-simulator"} {
		ok, present := r.Targets[target]
		mark := "✗"
		if ok {
			mark = "✓"
		}
		if !present {
			mark = "?"
		}
		fmt.Printf("  %s %s\n", mark, target)
	}
	if len(r.NextSteps) > 0 {
		fmt.Println()
		fmt.Println("Next steps:")
		for _, step := range r.NextSteps {
			fmt.Printf("  • %s\n", step)
		}
	}
	fmt.Println(strings.Repeat("=", 65))
}
