package main

// Closed loop on the PRODUCTION path.
//
// remoteRuntimeCapabilitiesForProject is what the mobile app actually calls
// (GET /remote-runtime/capabilities -> getRemoteRuntimeCapabilities in
// quic.ts). The preview-plan layer (ResolveWorkspacePreview*) has no
// production consumer, so a test against it proves nothing about what a user
// sees — which is exactly how this bug survived a green matrix.
//
// The bug: target selection switches on framework, and "swift" always went to
// the Apple arm — ios/ipados/watchos/tvos/visionos simulators + iOS device,
// every one gated on macOS + Xcode. browser-window, whose own probe declares
// RuntimeHostClass "any" / HostOS "any" and is gated only on a Chrome binary,
// lived under case "browser" and was never offered to a Swift project. So a
// SwiftWasm app on a Linux workspace saw nothing but "Requires a macOS host".

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeSwiftWasmProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Package.swift", `// swift-tools-version:5.9
import PackageDescription
let package = Package(
  name: "TodoWasm",
  dependencies: [
    .package(url: "https://github.com/swiftwasm/JavaScriptKit", from: "0.19.0"),
  ],
  targets: [.executableTarget(name: "TodoWasm", dependencies: ["JavaScriptKit"])]
)`)
	write("Sources/TodoWasm/main.swift", "import JavaScriptKit\nlet d = JSObject.global.document\n")
	return dir
}

func targetByID(caps RemoteRuntimeCapabilities, id string) (RemoteRuntimeTarget, bool) {
	for _, t := range caps.Targets {
		if t.ID == id {
			return t, true
		}
	}
	return RemoteRuntimeTarget{}, false
}

// A SwiftWasm project must be OFFERED the browser target — the one that can
// stream it over WebRTC from a Linux box.
func TestSwiftWasmProjectIsOfferedTheBrowserTarget(t *testing.T) {
	dir := writeSwiftWasmProject(t)

	if kind := DetectSwiftProject(dir).Kind; kind != SwiftKindTokamak {
		t.Fatalf("fixture must detect as SwiftWasm/Tokamak, got %q", kind)
	}

	caps := remoteRuntimeCapabilitiesForProject(dir, "swift")
	browser, ok := targetByID(caps, "browser-window")
	if !ok {
		var ids []string
		for _, tg := range caps.Targets {
			ids = append(ids, tg.ID)
		}
		t.Fatalf("SwiftWasm project was not offered browser-window; targets=%v — this is the routing that stranded Swift on Linux", ids)
	}

	// The browser target must not be macOS-gated: that is the entire point.
	if browser.RuntimeHostClass != "any" && browser.HostOS != "any" {
		t.Errorf("browser target must be host-agnostic, got runtimeHostClass=%q hostOS=%q",
			browser.RuntimeHostClass, browser.HostOS)
	}

	// On a box with Chrome it must be ENABLED — capability, not just presence.
	// Where Chrome is absent it must say so, rather than being silently unusable.
	if DiscoverChromeBinary() != "" {
		if !browser.Enabled {
			t.Errorf("Chrome is installed here, so browser-window must be enabled: reason=%q", browser.Reason)
		}
	} else if browser.Reason == "" {
		t.Error("no Chrome on this box — the target must carry the remedy")
	}
}

// A native Apple-UI Swift project must NOT be offered the browser target: a
// headless Chrome tab would render a blank page, which is worse than an honest
// "this needs a Mac".
func TestNativeAppleSwiftIsNotOfferedTheBrowserTarget(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Sources", "App"), 0o755)
	os.WriteFile(filepath.Join(dir, "Sources", "App", "View.swift"),
		[]byte("import SwiftUI\nstruct V: View { var body: some View { Text(\"hi\") } }\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "App.xcodeproj"), 0o755)
	os.WriteFile(filepath.Join(dir, "App.xcodeproj", "project.pbxproj"), []byte("// pbxproj"), 0o644)

	if kind := DetectSwiftProject(dir).Kind; kind == SwiftKindTokamak {
		t.Fatalf("SwiftUI fixture must not detect as wasm, got %q", kind)
	}

	caps := remoteRuntimeCapabilitiesForProject(dir, "swift")
	if _, ok := targetByID(caps, "browser-window"); ok {
		t.Error("a SwiftUI app must not be offered a browser target — it would render nothing")
	}
	if _, ok := targetByID(caps, "ios-simulator"); !ok {
		t.Error("a SwiftUI app must still be offered the iOS simulator")
	}
}

// The macOS half of the pair: on a Mac, native Swift must reach the simulator
// target and it must be enabled when a runtime + device exist.
func TestNativeSwiftOnMacReachesTheSimulatorTarget(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only: asserts the simulator target on a real Mac")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Sources", "App"), 0o755)
	os.WriteFile(filepath.Join(dir, "Sources", "App", "View.swift"),
		[]byte("import SwiftUI\n"), 0o644)

	caps := remoteRuntimeCapabilitiesForProject(dir, "swift")
	sim, ok := targetByID(caps, "ios-simulator")
	if !ok {
		t.Fatal("native Swift on macOS must be offered ios-simulator")
	}
	// Enabled depends on an installed iOS runtime; when it is not enabled the
	// reason must name the fix rather than leaving the user guessing.
	if !sim.Enabled && sim.Reason == "" {
		t.Error("a disabled simulator target must carry its remedy")
	}
}

// Flutter renders as a web dev server on the box, so the browser target must
// be offered — and lead. It is the only path where the in-app yaver_feedback
// SDK (pub.dev) applies, because the app is real rather than a video of one.
func TestFlutterIsOfferedTheBrowserTargetFirst(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "flutter")
	if len(caps.Targets) == 0 {
		t.Fatal("flutter must be offered targets")
	}
	if caps.Targets[0].ID != "browser-window" {
		var ids []string
		for _, tg := range caps.Targets {
			ids = append(ids, tg.ID)
		}
		t.Errorf("browser-window must lead for flutter; got %v", ids)
	}
}

// Closed loop for the browser lane, against REAL project directories.
//
// Scope is deliberate. Web stacks (next/vite/astro) are NOT here: they resolve
// to ExecutionModeWebWebview (executionModeForFramework, remote_runtime.go:131)
// and preview through the WebView/DevPreview path, so an empty remote-runtime
// target list is correct for them, not a bug. I asserted otherwise first and
// was wrong — recorded so the next reader does not "fix" it back.
//
// RN is the real gap: it resolves to ExecutionModeRNHermes but is made
// eligible for targets via the rnSim path, and comes back with twelve
// simulator/emulator targets and no browser lane — the cheapest one, and the
// only one where the in-app feedback SDK applies. Left failing-by-omission
// rather than papered over; see BROWSER_VIBING_AUDIT.md §3.
func TestBrowserLaneForNativeBrowserRenderableStacks(t *testing.T) {
	repo := "/Users/kivanccakmak/Workspace/yaver.io"
	cases := []struct{ dir, framework, why string }{
		{repo + "/demo/yaver-todo-swift-wasm", "swift", "SwiftWasm compiles to wasm and runs in a browser"},
		{repo + "/demo", "flutter", "Flutter is classed a web dev server (devserver_kind.go)"},
	}
	for _, c := range cases {
		if _, err := os.Stat(c.dir); err != nil {
			t.Logf("skip %s — not in this checkout", c.dir)
			continue
		}
		caps := remoteRuntimeCapabilitiesForProject(c.dir, c.framework)
		browser, ok := targetByID(caps, "browser-window")
		if !ok {
			var ids []string
			for _, tg := range caps.Targets {
				ids = append(ids, tg.ID)
			}
			t.Errorf("%s (%s): no browser-window — %s. targets=%v", c.dir, c.framework, c.why, ids)
			continue
		}
		if DiscoverChromeBinary() != "" && !browser.Enabled {
			t.Errorf("%s (%s): browser present but target disabled: %q", c.dir, c.framework, browser.Reason)
		}
		if !browser.Enabled && browser.Reason == "" {
			t.Errorf("%s (%s): disabled with no remedy", c.dir, c.framework)
		}
	}
}
