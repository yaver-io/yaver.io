package main

// doctor_surfaces.go — "can this box actually preview EVERY surface Yaver
// claims to support?" Simulators (iOS, iPadOS, watchOS, tvOS, visionOS/AR-VR,
// CarPlay), Android (emulator, device, Redroid), and the browser.
//
// Why this exists as its own probe rather than more lines in doctor_webrtc.go:
// that report answers a narrower question — what can the NATIVE-WebRTC remote
// runtime encode — and its four targets (android-emulator/device,
// ios-simulator/device) are the ones the encoder cares about. A user asking
// "will my watch app preview here?" gets no answer from it, and the honest
// answer differs per surface and per host.
//
// The rule this file exists to enforce: report a surface as available ONLY
// after observing the thing that makes it work. Every entry here is a probe of
// an operation, never of a name:
//
//   - a simulator surface needs BOTH a runtime (`simctl list runtimes`) AND a
//     device of that family (`simctl list devices available`). Xcode installs
//     runtimes without devices and vice versa, so either alone is a false green.
//   - Redroid needs a real Linux kernel AND docker that actually responds —
//     `docker` on PATH with a dead daemon is the classic inventory-yes case.
//   - Chrome must survive `--version`, because Ubuntu's `chromium-browser` is a
//     snap stub that resolves on PATH and then refuses to launch.
//   - CarPlay is not a separate simulator: it is a window on a BOOTED iOS
//     simulator, so it inherits iOS availability and nothing else.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SurfaceCheck is one row of the surfaces report.
type SurfaceCheck struct {
	Surface   string `json:"surface"`
	Available bool   `json:"available"`
	// Detail names what was OBSERVED when available, and the specific remedy
	// when not. Never "check your configuration".
	Detail string `json:"detail"`
	// Transport is how a preview of this surface would reach a viewer, so the
	// report answers "and what will it cost me" in the same glance.
	Transport string `json:"transport"`
}

// SurfacesDoctorReport is the structured result. Stable shape — surfaces are
// added, never renamed.
type SurfacesDoctorReport struct {
	Platform string         `json:"platform"`
	Arch     string         `json:"arch"`
	Host     string         `json:"host"`
	Checks   []SurfaceCheck `json:"checks"`
}

// BuildSurfacesDoctorReport probes every previewable surface on this box.
func BuildSurfacesDoctorReport(ctx context.Context) SurfacesDoctorReport {
	host := DetectHostPlatform()
	rep := SurfacesDoctorReport{
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		Host:     string(host),
	}

	runtimes, devices := appleSimInventory(ctx)

	// ── Apple simulator surfaces ────────────────────────────────────────
	// Each needs a runtime AND a device of that family; Xcode can leave you
	// with one and not the other.
	for _, s := range []struct{ surface, rtKey, devKey, remedy string }{
		{"ios", "iOS", "iPhone", "Xcode ▸ Settings ▸ Components ▸ iOS Simulator"},
		{"ipados", "iOS", "iPad", "Xcode ▸ Settings ▸ Components ▸ iOS Simulator"},
		{"watchos", "watchOS", "Apple Watch", "Xcode ▸ Settings ▸ Components ▸ watchOS Simulator"},
		{"tvos", "tvOS", "Apple TV", "Xcode ▸ Settings ▸ Components ▸ tvOS Simulator"},
		{"visionos", "visionOS", "Vision", "Xcode ▸ Settings ▸ Components ▸ visionOS Simulator"},
	} {
		check := SurfaceCheck{Surface: s.surface + "-simulator", Transport: "webrtc-video"}
		switch {
		case host != HostMacOS:
			check.Detail = "Apple simulators exist only on macOS. Pair a Mac builder (`yaver builder use <alias>`) or preview on a real device with `yaver wire push`."
		case !runtimes[s.rtKey]:
			check.Detail = "no " + s.rtKey + " runtime installed — " + s.remedy
		case !devices[s.devKey]:
			check.Detail = s.rtKey + " runtime present but no " + s.devKey + " device exists; create one in Xcode ▸ Devices, or `xcrun simctl create`"
		default:
			check.Available = true
			check.Detail = s.rtKey + " runtime + " + s.devKey + " device present"
		}
		rep.Checks = append(rep.Checks, check)
	}

	// ── CarPlay ─────────────────────────────────────────────────────────
	// Not its own simulator: it is a window on a booted iOS simulator
	// (Simulator ▸ I/O ▸ External Displays ▸ CarPlay), so it can never be
	// available where iOS is not.
	carplay := SurfaceCheck{Surface: "carplay", Transport: "webrtc-video"}
	if host == HostMacOS && runtimes["iOS"] && devices["iPhone"] {
		carplay.Available = true
		carplay.Detail = "available via a booted iOS simulator — Simulator ▸ I/O ▸ External Displays ▸ CarPlay"
	} else {
		carplay.Detail = "CarPlay renders through an iOS simulator, so it needs the same macOS + iOS runtime"
	}
	rep.Checks = append(rep.Checks, carplay)

	// ── Android ─────────────────────────────────────────────────────────
	adb := DiscoverBinary("adb") != ""
	emu := SurfaceCheck{Surface: "android-emulator", Transport: "webrtc-video"}
	if adb {
		emu.Available = true
		emu.Detail = "adb present; the emulator binary is probed at session start"
	} else {
		emu.Detail = "adb not found — run `yaver install remote-runtime`"
	}
	rep.Checks = append(rep.Checks, emu)

	dev := SurfaceCheck{Surface: "android-device", Transport: "webrtc-video"}
	if adb {
		dev.Available = true
		dev.Detail = "adb present; attach a device and it appears (`yaver wire detect`)"
	} else {
		dev.Detail = "adb not found — run `yaver install remote-runtime`"
	}
	rep.Checks = append(rep.Checks, dev)

	// ── Redroid ─────────────────────────────────────────────────────────
	rd := SurfaceCheck{Surface: "redroid", Transport: "webrtc-video"}
	switch {
	case !HostCanRunRedroid(host):
		rd.Detail = "Redroid shares the host kernel (binder/ashmem) and needs real Linux; on " +
			string(host) + " use a Linux workspace or a paired Android device"
	case !dockerDaemonResponds(ctx):
		// `docker` on PATH with a dead daemon is the textbook false green.
		rd.Detail = "Linux host, but the Docker daemon does not respond — install/start Docker (`yaver install redroid`)"
	default:
		rd.Available = true
		rd.Detail = "Linux kernel + responsive Docker daemon; `yaver install redroid` pulls the image"
	}
	rep.Checks = append(rep.Checks, rd)

	// ── Browser ─────────────────────────────────────────────────────────
	// The one surface that carries EVERY browser-renderable stack, both as the
	// 0-vCPU direct-URL path and as the pixel fallback.
	br := SurfaceCheck{Surface: "browser", Transport: "direct-url | chrome-webrtc"}
	if path := DiscoverChromeBinary(); path != "" {
		br.Available = true
		br.Detail = "usable browser at " + path + " (verified by running it, not by PATH)"
	} else {
		br.Detail = "no usable browser. " + ChromeInstallHint()
	}
	rep.Checks = append(rep.Checks, br)

	return rep
}

// appleSimInventory returns which simulator RUNTIMES and which device FAMILIES
// exist. Both matter: a runtime with no device of that family cannot boot, and
// a device whose runtime was removed is listed but unavailable.
func appleSimInventory(ctx context.Context) (runtimes, devices map[string]bool) {
	runtimes, devices = map[string]bool{}, map[string]bool{}
	if runtime.GOOS != "darwin" {
		return
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(c, "xcrun", "simctl", "list", "runtimes").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			// Unavailable runtimes are listed with an explicit marker; skip them
			// so a removed runtime does not read as installed.
			if strings.Contains(line, "unavailable") {
				continue
			}
			for _, k := range []string{"iOS", "watchOS", "tvOS", "visionOS", "xrOS"} {
				if strings.HasPrefix(strings.TrimSpace(line), k) {
					if k == "xrOS" {
						k = "visionOS"
					}
					runtimes[k] = true
				}
			}
		}
	}

	if out, err := exec.CommandContext(c, "xcrun", "simctl", "list", "devices", "available").Output(); err == nil {
		s := string(out)
		for _, k := range []string{"iPhone", "iPad", "Apple Watch", "Apple TV", "Vision"} {
			if strings.Contains(s, k) {
				devices[k] = true
			}
		}
	}
	return
}

// dockerDaemonResponds probes the DAEMON, not the client binary. `docker` on
// PATH proves nothing: the socket is frequently absent on a fresh cloud box,
// and `docker info` is the cheapest call that fails when it is.
func dockerDaemonResponds(ctx context.Context) bool {
	bin := DiscoverBinary("docker")
	if bin == "" {
		return false
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(c, bin, "info").Run() == nil
}

// runDoctorSurfaces is the CLI entry point: `yaver doctor surfaces [--json]`.
func runDoctorSurfaces(args []string) {
	wantJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			wantJSON = true
		case "-h", "--help":
			fmt.Println("usage: yaver doctor surfaces [--json]")
			fmt.Println()
			fmt.Println("Reports which preview surfaces this machine can actually serve:")
			fmt.Println("  iOS / iPadOS / watchOS / tvOS / visionOS simulators, CarPlay,")
			fmt.Println("  Android emulator + device, Redroid, and the browser.")
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep := BuildSurfacesDoctorReport(ctx)

	if wantJSON {
		b, _ := json.Marshal(rep)
		fmt.Println(string(b))
		return
	}

	fmt.Printf("Yaver Surfaces — %s/%s (host: %s)\n\n", rep.Platform, rep.Arch, rep.Host)
	avail := 0
	for _, c := range rep.Checks {
		mark := "✗"
		if c.Available {
			mark = "✓"
			avail++
		}
		fmt.Printf("  %s %-20s %-24s %s\n", mark, c.Surface, c.Transport, c.Detail)
	}
	fmt.Printf("\n  %d of %d surfaces available on this machine.\n", avail, len(rep.Checks))
}
