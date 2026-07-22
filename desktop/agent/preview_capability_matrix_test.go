package main

// preview_capability_matrix_test.go — the closed loop for "can Yaver preview
// this, here, and over what transport?"
//
// One table, every stack x both host platforms. It exists because the answer
// kept being wrong in ways no single-stack test could catch:
//
//   - native Swift reported UNSUPPORTED on a Mac with a booted simulator,
//     because the stack layer hardcoded "unsupported" and the host layer only
//     upgrades plans that ask for a simulator;
//   - native Kotlin reported UNSUPPORTED on every Mac, because Redroid
//     (Linux-only) was the sole strategy it could route to, while adb and the
//     emulator sat installed on the box;
//   - SwiftWasm reported UNSUPPORTED on Linux — the one Swift runtime that
//     provably works there — because `strings.Contains(stack, "swift")`
//     matched it first.
//
// Each of those was a false NEGATIVE: a capability the box had and the plan
// denied. A matrix makes them visible together, because the bug is always in
// the CELL nobody looked at.

import (
	"os"
	"path/filepath"
	"testing"
)

// wantWebRTC is true when the plan streams pixels — the expensive lane the
// unit economics care about, and the one that must exist for native code.
func planUsesWebRTC(p WorkspacePreviewPlan) bool {
	switch p.Primary {
	case PreviewChromeWebRTC, PreviewRedroidWebRTC, PreviewAndroidEmulator, PreviewIOSSimulator:
		return true
	}
	return false
}

func planCanFallBackToPixels(p WorkspacePreviewPlan) bool {
	if planUsesWebRTC(p) {
		return true
	}
	for _, f := range p.Fallbacks {
		switch f {
		case PreviewChromeWebRTC, PreviewRedroidWebRTC, PreviewAndroidEmulator, PreviewIOSSimulator:
			return true
		}
	}
	return false
}

func TestPreviewCapabilityMatrix(t *testing.T) {
	cases := []struct {
		stack       string
		host        HostPlatform
		wantSupport bool
		wantPrimary PreviewStrategy
		// wantPixelPath asserts a WebRTC lane is reachable (primary or
		// fallback) — "WebRTC for all cases" is about REACHABILITY, not about
		// making it the default.
		wantPixelPath bool
		why           string
	}{
		// ── React Native / Expo ─────────────────────────────────────────
		{"react-native", HostLinux, true, PreviewDirectURL, true,
			"RN web target is browser-renderable; pixels remain as fallback"},
		{"react-native", HostMacOS, true, PreviewDirectURL, true, ""},
		{"expo", HostLinux, true, PreviewDirectURL, true, ""},

		// ── Flutter ─────────────────────────────────────────────────────
		{"flutter", HostLinux, true, PreviewDirectURL, true,
			"Flutter is classed a web dev server; chrome-webrtc backs it"},
		{"flutter", HostMacOS, true, PreviewDirectURL, true, ""},

		// ── Native Kotlin / Android ─────────────────────────────────────
		{"kotlin", HostLinux, true, PreviewRedroidWebRTC, true,
			"Redroid is the dense container path on Linux"},
		// The cell that was broken: a Mac cannot run Redroid, but it CAN run
		// an emulator. Expectation depends on the SDK actually being present,
		// so this case is asserted separately below.

		// ── Native Swift / iOS ──────────────────────────────────────────
		{"swift", HostMacOS, true, PreviewIOSSimulator, true,
			"simulator streams over WebRTC via simctl recordVideo + pion"},
		{"swift", HostLinux, false, PreviewIOSSimulator, true,
			"no simulator exists on Linux; must refuse rather than render something that is not the app"},

		// ── SwiftWasm — the Swift runtime that DOES work on Linux ───────
		{"swiftwasm", HostLinux, true, PreviewDirectURL, true,
			"compiles to wasm and runs in the viewer's browser; chrome-webrtc backs it"},
		{"swiftwasm", HostMacOS, true, PreviewDirectURL, true, ""},

		// ── Plain web ───────────────────────────────────────────────────
		{"next", HostLinux, true, PreviewDirectURL, true, ""},
	}

	for _, tc := range cases {
		plan := ResolvePreviewForHost(ResolveWorkspacePreview(tc.stack, false), tc.host)

		if plan.Supported != tc.wantSupport {
			t.Errorf("%s on %s: Supported=%v want %v (reason: %s)",
				tc.stack, tc.host, plan.Supported, tc.wantSupport, plan.Reason)
		}
		if plan.Primary != tc.wantPrimary {
			t.Errorf("%s on %s: Primary=%q want %q", tc.stack, tc.host, plan.Primary, tc.wantPrimary)
		}
		if got := planCanFallBackToPixels(plan); got != tc.wantPixelPath {
			t.Errorf("%s on %s: a WebRTC lane reachable=%v want %v (primary=%s fallbacks=%v)",
				tc.stack, tc.host, got, tc.wantPixelPath, plan.Primary, plan.Fallbacks)
		}
		// An unsupported plan MUST carry the remedy. A bare false is the thing
		// that sends a user hunting.
		if !plan.Supported && plan.Reason == "" {
			t.Errorf("%s on %s: refused with no reason", tc.stack, tc.host)
		}
	}
}

// Native Android on macOS: Redroid is impossible, an emulator is not. The
// expectation is conditional on the SDK being installed, because the whole
// point of HostCanRunAndroidEmulator is that it probes rather than assumes.
func TestNativeAndroidOnMacUsesEmulatorWhenAvailable(t *testing.T) {
	plan := ResolvePreviewForHost(ResolveWorkspacePreview("kotlin", false), HostMacOS)

	if HostCanRunAndroidEmulator(HostMacOS) {
		if !plan.Supported {
			t.Fatalf("emulator is installed on this Mac, so Kotlin must be previewable: %+v", plan)
		}
		if plan.Primary != PreviewAndroidEmulator {
			t.Errorf("Primary=%q, want %q — Redroid cannot run here", plan.Primary, PreviewAndroidEmulator)
		}
		if !planUsesWebRTC(plan) {
			t.Error("the emulator path must stream over WebRTC")
		}
	} else {
		if plan.Supported {
			t.Error("no emulator installed — must not claim Kotlin is previewable")
		}
		if plan.Reason == "" {
			t.Error("refusal must name the remedy")
		}
	}
}

// Swift is four runtimes wearing one label. From LINUX, the honest split is:
// wasm previews, native iOS does not.
func TestSwiftOnLinuxPreviewsWasmAndRefusesUIKit(t *testing.T) {
	mk := func(files map[string]string) string {
		dir := t.TempDir()
		for name, body := range files {
			p := filepath.Join(dir, name)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
		return dir
	}

	// A SwiftWasm app: Swift source, JavaScriptKit, builds to wasm. This is
	// the Swift project that CAN be previewed from a Linux workspace.
	wasm := mk(map[string]string{
		"Package.swift": `// swift-tools-version:5.9
import PackageDescription
let package = Package(name: "TodoWasm",
  dependencies: [.package(url: "https://github.com/swiftwasm/JavaScriptKit", from: "0.19.0")])`,
		"Sources/TodoWasm/main.swift": "import JavaScriptKit\n",
	})
	plan := ResolvePreviewForHost(ResolveWorkspacePreviewForDir("swift", wasm, false), HostLinux)
	if !plan.Supported {
		t.Errorf("a SwiftWasm project must be previewable from Linux: %+v", plan)
	}
	if !planCanFallBackToPixels(plan) {
		t.Errorf("a SwiftWasm project must have a WebRTC lane reachable from Linux: %+v", plan)
	}

	// A UIKit app: no amount of Linux makes this render.
	uikit := mk(map[string]string{
		"Sources/App/View.swift":        "import UIKit\nclass V: UIViewController {}\n",
		"App.xcodeproj/project.pbxproj": "// pbxproj",
	})
	plan = ResolvePreviewForHost(ResolveWorkspacePreviewForDir("swift", uikit, false), HostLinux)
	if plan.Supported {
		t.Errorf("a UIKit app cannot be previewed from Linux: %+v", plan)
	}
	if plan.Reason == "" {
		t.Error("the refusal must say what to do instead (Mac host or paired iPhone)")
	}
	// And it must never silently degrade to a web render of something that is
	// not the user's app.
	if plan.Primary == PreviewDirectURL || plan.Primary == PreviewChromeWebRTC {
		t.Errorf("UIKit on Linux must not fall back to a web preview, got %q", plan.Primary)
	}
}

// The same UIKit app on a Mac is fully previewable over WebRTC.
func TestSwiftOnMacPreviewsOverWebRTC(t *testing.T) {
	plan := ResolvePreviewForHost(ResolveWorkspacePreview("swift", false), HostMacOS)
	if !plan.Supported {
		t.Fatalf("native Swift must be previewable on macOS: %+v", plan)
	}
	if plan.Primary != PreviewIOSSimulator {
		t.Errorf("Primary=%q, want ios-simulator", plan.Primary)
	}
	if !planUsesWebRTC(plan) {
		t.Error("the simulator path streams over WebRTC")
	}
}
