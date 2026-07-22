package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSwiftProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectTokamakRendersOnLinux(t *testing.T) {
	dir := writeSwiftProject(t, map[string]string{
		"Package.swift":         `.package(url: "https://github.com/TokamakUI/Tokamak", from: "0.11.0")`,
		"Sources/App/App.swift": "import TokamakDOM\nstruct TodoApp {}\n",
	})
	d := DetectSwiftProject(dir)
	if d.Kind != SwiftKindTokamak || !d.RendersOnLinux {
		t.Fatalf("tokamak: %+v", d)
	}
	p := ResolveSwiftPreview(d)
	if p.Primary != PreviewChromeWebRTC || !p.Supported || p.MachineClass != "standard" {
		t.Fatalf("tokamak plan: %+v", p)
	}
}

func TestTokamakWithXcodeprojStillRoutesToLinux(t *testing.T) {
	// The ordering trap: a Tokamak project may carry an .xcodeproj purely for
	// editing. Checking Xcode first would misroute it to a Mac it does not need.
	dir := writeSwiftProject(t, map[string]string{
		"Package.swift":           "import PackageDescription // TokamakShim",
		"App.xcodeproj/x.pbxproj": "// xcode",
	})
	d := DetectSwiftProject(dir)
	if d.Kind != SwiftKindTokamak {
		t.Fatalf("xcodeproj must not outrank tokamak markers: %+v", d)
	}
	if d.NeedsMacHost {
		t.Fatal("tokamak must never require a mac host")
	}
}

func TestDetectAppleUINeedsMac(t *testing.T) {
	dir := writeSwiftProject(t, map[string]string{
		"Sources/App/View.swift": "import SwiftUI\nstruct V: View { var body: some View { Text(\"hi\") } }\n",
	})
	d := DetectSwiftProject(dir)
	if d.Kind != SwiftKindAppleUI || !d.NeedsMacHost || d.RendersOnLinux {
		t.Fatalf("apple-ui: %+v", d)
	}
	p := ResolveSwiftPreview(d)
	if p.Supported {
		t.Fatal("apple UI must not claim support on a Linux workspace")
	}
	// But it must NOT be a bare refusal — tests still run here, and saying so
	// is the difference between a useful answer and turning a dev away.
	if !strings.Contains(p.Reason, "swift test") || !strings.Contains(p.Reason, "Mac host") {
		t.Fatalf("apple-ui reason must explain what DOES work: %s", p.Reason)
	}
	if !SwiftRunsTestsOnLinux(d) {
		t.Fatal("apple-ui still compiles and tests on Linux")
	}
}

func TestServerSideSwiftRendersOnLinux(t *testing.T) {
	dir := writeSwiftProject(t, map[string]string{
		"Package.swift":          `.package(url: "https://github.com/vapor/vapor", from: "4.0.0")`,
		"Sources/App/main.swift": "import Vapor\n",
	})
	d := DetectSwiftProject(dir)
	if d.Kind != SwiftKindServer || !d.RendersOnLinux {
		t.Fatalf("server: %+v", d)
	}
}

func TestVaporInsideIOSAppDoesNotHijackRouting(t *testing.T) {
	// A Vapor backend living inside an iOS repo must not drag the whole
	// project onto Linux and hide the fact the UI needs a Mac.
	dir := writeSwiftProject(t, map[string]string{
		"Package.swift":          `.package(url: "https://github.com/vapor/vapor", from: "4.0.0")`,
		"Sources/App/View.swift": "import UIKit\n",
	})
	d := DetectSwiftProject(dir)
	if d.Kind != SwiftKindAppleUI {
		t.Fatalf("UIKit presence must win over a vapor dependency: %+v", d)
	}
}

func TestUnknownNeverGuesses(t *testing.T) {
	d := DetectSwiftProject(t.TempDir())
	if d.Kind != SwiftKindUnknown {
		t.Fatalf("empty dir: %+v", d)
	}
	p := ResolveSwiftPreview(d)
	if p.Supported || p.Primary == PreviewChromeWebRTC {
		t.Fatal("unknown must never be given a render target")
	}
}

func TestResolveForDirBeatsLabelOnlyForSwift(t *testing.T) {
	// Label alone => conservative: Swift needs a Mac, so a LINUX host refuses.
	label := ResolvePreviewForHost(ResolveWorkspacePreview("swift", false), HostLinux)
	if label.Supported {
		t.Fatal("label-only swift should stay conservative on a Linux host")
	}
	// With the directory, a Tokamak project routes to Linux + chrome-webrtc.
	dir := writeSwiftProject(t, map[string]string{
		"Package.swift": "// TokamakDOM",
	})
	withDir := ResolveWorkspacePreviewForDir("swift", dir, false)
	if !withDir.Supported || withDir.Primary != PreviewChromeWebRTC {
		t.Fatalf("tokamak dir should route to chrome-webrtc: %+v", withDir)
	}
	// A real UIKit project still requires a Mac even with the directory.
	uikit := writeSwiftProject(t, map[string]string{
		"Sources/V.swift": "import UIKit\n",
	})
	if p := ResolveWorkspacePreviewForDir("swift", uikit, false); p.Supported {
		t.Fatalf("UIKit must not become supported on Linux: %+v", p)
	}
	// Non-Swift stacks are untouched by this path.
	if p := ResolveWorkspacePreviewForDir("react-native", dir, false); p.Primary != PreviewDirectURL {
		t.Fatalf("non-swift routing changed: %+v", p)
	}
}

func TestSwiftWasmDevServerDetection(t *testing.T) {
	srv := &SwiftWasmDevServer{}
	// A Tokamak package is detected.
	tok := writeSwiftProject(t, map[string]string{
		"Package.swift": `.package(url: "https://github.com/TokamakUI/Tokamak", from: "0.11.0")`,
	})
	if !srv.Detect(tok) {
		t.Fatal("tokamak package should be detected")
	}
	// A BARE Package.swift must NOT be — starting carton on an ordinary Swift
	// library fails confusingly after a long toolchain spin-up.
	plain := writeSwiftProject(t, map[string]string{
		"Package.swift": "// just a library",
	})
	if srv.Detect(plain) {
		t.Fatal("a plain Swift package must not be treated as SwiftWasm")
	}
	// Not a Swift project at all.
	if srv.Detect(t.TempDir()) {
		t.Fatal("empty dir must not detect")
	}
	// It is a WEB surface — it streams via chrome-webrtc, not the Hermes tab.
	if srv.Kind() != DevServerKindWeb {
		t.Fatalf("swiftwasm must class as web, got %q", srv.Kind())
	}
	// HMR must be reported FALSE: a Swift edit is a full WASM rebuild plus a
	// page reload. Claiming otherwise would make the caller preserve state it
	// is about to lose.
	if srv.SupportsHotReload() {
		t.Fatal("swiftwasm has no HMR and must not claim it")
	}
}

// TestTokamakFixtureRoutesEndToEnd pins the whole chain the demo depends on:
// the real fixture on disk -> detection -> preview plan -> dev server.
//
// Kept as a permanent test rather than a one-off probe because every link is a
// place the routing could silently regress: widen a marker, reorder detection,
// or rename the fixture, and the Swift demo stops working with no compile error.
func TestTokamakFixtureRoutesEndToEnd(t *testing.T) {
	const fixture = "../../demo/yaver-todo-swift-wasm"
	if _, err := os.Stat(filepath.Join(fixture, "Package.swift")); err != nil {
		t.Skip("fixture not present in this checkout")
	}
	d := DetectSwiftProject(fixture)
	if d.Kind != SwiftKindTokamak || !d.RendersOnLinux {
		t.Fatalf("fixture must detect as tokamak and render on Linux: %+v", d)
	}
	p := ResolveSwiftPreview(d)
	if !p.Supported || p.Primary != PreviewChromeWebRTC {
		t.Fatalf("fixture must route to chrome-webrtc: %+v", p)
	}
	if p.MachineClass != "standard" {
		t.Fatalf("fixture must fit the 2c/4GB default class, got %q", p.MachineClass)
	}
	if !(&SwiftWasmDevServer{}).Detect(fixture) {
		t.Fatal("SwiftWasmDevServer must claim the fixture")
	}
}
