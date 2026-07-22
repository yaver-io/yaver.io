package main

import (
	"strings"
	"testing"
)

func TestIOSSimulatorDependsOnHostNotAssumption(t *testing.T) {
	swiftPlan := WorkspacePreviewPlan{Primary: PreviewIOSSimulator}

	// On a Mac the simulator becomes AVAILABLE — this is the case the
	// Linux-only resolver refused outright, turning away the one host that can
	// actually do it.
	mac := ResolvePreviewForHost(swiftPlan, HostMacOS)
	if !mac.Supported {
		t.Fatalf("iOS must be supported on a macOS host: %+v", mac)
	}

	// On Linux it must refuse — and say what DOES work, not just what does not.
	linux := ResolvePreviewForHost(swiftPlan, HostLinux)
	if linux.Supported {
		t.Fatal("iOS simulator cannot run on Linux")
	}
	if !strings.Contains(linux.Reason, "swift test") {
		t.Fatalf("refusal must state what still works here: %q", linux.Reason)
	}
}

func TestRedroidNeedsRealLinuxKernel(t *testing.T) {
	plan := WorkspacePreviewPlan{Primary: PreviewRedroidWebRTC, Supported: true}

	if !ResolvePreviewForHost(plan, HostLinux).Supported {
		t.Fatal("redroid must work on Linux")
	}
	// Docker on macOS runs in a Linux VM, but Redroid needs host kernel
	// modules (binder/ashmem) — promising a container that will not start is
	// worse than refusing.
	mac := ResolvePreviewForHost(plan, HostMacOS)
	if mac.Supported {
		t.Fatal("redroid must not claim support on macOS")
	}
	if !strings.Contains(mac.Reason, "Android device") {
		t.Fatalf("refusal should offer the working alternative: %q", mac.Reason)
	}
}

func TestBrowserStrategiesAreHostAgnostic(t *testing.T) {
	// Chromium and Node run everywhere; the host must not alter these.
	for _, s := range []PreviewStrategy{PreviewChromeWebRTC, PreviewHermesBundle} {
		in := WorkspacePreviewPlan{Primary: s, Supported: true, Reason: "unchanged"}
		for _, h := range []HostPlatform{HostLinux, HostMacOS, HostWindows} {
			out := ResolvePreviewForHost(in, h)
			if !out.Supported || out.Reason != "unchanged" {
				t.Fatalf("%s must be host-agnostic, %s changed it: %+v", s, h, out)
			}
		}
	}
}

func TestDetectHostPlatformIsReal(t *testing.T) {
	// Detected from runtime.GOOS, never configured — a flag would be one more
	// thing that can disagree with reality.
	if p := DetectHostPlatform(); p == "" {
		t.Fatal("host platform must always resolve")
	}
	if HostCanRunIOSSimulator(HostLinux) {
		t.Fatal("linux can never run an iOS simulator")
	}
	if HostCanRunRedroid(HostMacOS) {
		t.Fatal("macOS cannot run redroid")
	}
}
