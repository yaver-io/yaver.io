package main

// Closed loop for "can a Windows machine be a real remote workspace?"
//
// The claim being tested is narrow and checkable: the BROWSER lane — which
// carries Flutter, RN-web, SwiftWasm and every web stack — needs a browser and
// nothing else, so it works on native Windows, WSL1 and WSL2 alike. Everything
// kernel-dependent is probed rather than inferred from "it is Linux", because
// WSL2 IS Linux and still cannot run Redroid on the stock kernel.

import (
	"runtime"
	"strings"
	"testing"
)

// Windows must be able to preview native Android. Excluding it from the
// emulator gate made every Windows box useless for Kotlin even though the
// emulator runs there on WHPX.
func TestWindowsIsAllowedTheAndroidEmulator(t *testing.T) {
	// The gate is platform + probe; assert the PLATFORM half, which is the
	// part that was wrong. The probe half is asserted on the real host below.
	for _, p := range []HostPlatform{HostMacOS, HostLinux, HostWindows} {
		if p == HostOther {
			continue
		}
		// A platform that is categorically excluded can never be true no
		// matter what is installed — that is the bug being pinned.
		if p == HostWindows && !hostPlatformAllowsEmulator(p) {
			t.Error("native Windows must be allowed the Android emulator (WHPX)")
		}
	}
	if hostPlatformAllowsEmulator(HostOther) {
		t.Error("unknown platforms must not claim emulator support")
	}
}

// iOS is never available off Apple hardware — including on Windows and WSL.
func TestIOSIsNeverAvailableOffApple(t *testing.T) {
	for _, p := range []HostPlatform{HostLinux, HostWindows, HostOther} {
		if HostCanRunIOSSimulator(p) {
			t.Errorf("%s must not claim an iOS simulator", p)
		}
	}
	if !HostCanRunIOSSimulator(HostMacOS) {
		t.Error("macOS must offer the iOS simulator")
	}
}

// Redroid must be gated on the KERNEL, not on the OS label. WSL2 is Linux and
// still cannot run it on Microsoft's default kernel.
func TestRedroidIsGatedOnTheKernelNotTheLabel(t *testing.T) {
	if HostCanRunRedroid(HostWindows) || HostCanRunRedroid(HostMacOS) {
		t.Error("Redroid requires Linux")
	}
	ok, reason := HostCanRunRedroidHere()
	if runtime.GOOS != "linux" {
		if ok {
			t.Error("non-Linux host must not claim Redroid")
		}
		if reason == "" {
			t.Error("the refusal must carry a remedy")
		}
		return
	}
	// On Linux the answer depends on binder; either way it must be explained.
	if !ok && reason == "" {
		t.Error("a Linux host that cannot run Redroid must say why")
	}
	if !ok && !strings.Contains(strings.ToLower(reason), "binder") {
		t.Errorf("the Redroid refusal should name binder, got %q", reason)
	}
}

// WSL detection must not depend on an env var that a systemd-started daemon
// never sees.
func TestWSLDetectionUsesProcVersionNotEnv(t *testing.T) {
	w := DetectWSL()
	if runtime.GOOS != "linux" {
		if w.IsWSL {
			t.Error("WSL cannot be detected off Linux")
		}
		return
	}
	if w.IsWSL {
		if w.Version != 1 && w.Version != 2 {
			t.Errorf("a detected WSL host must report a version, got %d", w.Version)
		}
		if w.Detail == "" {
			t.Error("detection must carry its evidence")
		}
		// WSL1 has no kernel, so kernel features must be refused there.
		if w.Version == 1 && HostSupportsLinuxKernelFeatures() {
			t.Error("WSL1 has no Linux kernel — kernel features must be refused")
		}
	}
}

// The load-bearing promise: a Windows machine is a useful remote workspace
// because the browser lane needs only a browser.
func TestBrowserLaneIsHostAgnostic(t *testing.T) {
	// The browser target itself must never be platform-gated.
	target := probeBrowserWindowTarget()
	if target.RuntimeHostClass != "any" || target.HostOS != "any" {
		t.Errorf("browser target must be host-agnostic, got class=%q os=%q",
			target.RuntimeHostClass, target.HostOS)
	}
	// And its availability must track a REAL browser, not a platform guess.
	if HostBrowserLaneAvailable() != (DiscoverChromeBinary() != "") {
		t.Error("browser lane availability must track the verified browser probe")
	}
	if !target.Enabled && target.Reason == "" {
		t.Error("a disabled browser target must name the remedy")
	}
}

func TestDescribeHostForPreviewIsSpecific(t *testing.T) {
	d := DescribeHostForPreview()
	if d == "" {
		t.Fatal("host description must not be empty")
	}
	// It must say something about the browser lane, which is the answer that
	// matters on Windows.
	if !strings.Contains(strings.ToLower(d), "browser") {
		t.Errorf("description should state the browser lane, got %q", d)
	}
}
