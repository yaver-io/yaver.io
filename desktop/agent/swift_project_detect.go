package main

import (
	"os"
	"path/filepath"
	"strings"
)

// swift_project_detect.go — which KIND of Swift project is this, and therefore
// where can it run?
//
// "A Swift app" is four different things with four different runtimes, and
// treating them as one is how a capability gets declared that the operation
// cannot deliver:
//
//	tokamak       Tokamak/SwiftWasm → WebAssembly → browser    → LINUX ✅
//	server        Vapor/Hummingbird → it is a web server       → LINUX ✅
//	apple-ui      Apple SwiftUI / UIKit → closed frameworks    → MAC HOST ❌
//	library       no UI at all → compile + swift test          → LINUX, no render
//
// The Swift toolchain itself is officially cross-platform, so EVERY kind
// compiles and tests on Linux. Only RENDERING splits. That distinction is the
// whole point of this file: the previous resolver returned a flat "unsupported"
// for Swift and turned away developers whose logic and test loop would have run
// fine on a 2c/4GB box today.
//
// See docs/architecture/swift-linux-webrtc-audit.md.

// SwiftProjectKind is the runtime a Swift project actually needs.
type SwiftProjectKind string

const (
	SwiftKindTokamak SwiftProjectKind = "tokamak"  // SwiftWasm → browser
	SwiftKindServer  SwiftProjectKind = "server"   // Vapor/Hummingbird
	SwiftKindAppleUI SwiftProjectKind = "apple-ui" // SwiftUI/UIKit — Mac only
	SwiftKindLibrary SwiftProjectKind = "library"  // logic/tests, no render
	SwiftKindUnknown SwiftProjectKind = "unknown"  // never guess a render target
)

// SwiftDetection is the evidence-bearing result.
type SwiftDetection struct {
	Kind SwiftProjectKind `json:"kind"`
	// Evidence is the concrete signal that decided it. Surfaced because a
	// misrouted project ("why is this asking for a Mac?") is otherwise
	// impossible to debug from the outside.
	Evidence string `json:"evidence"`
	// RendersOnLinux is the question everything else hangs off.
	RendersOnLinux bool `json:"rendersOnLinux"`
	// NeedsMacHost is true only for Apple's closed UI frameworks.
	NeedsMacHost bool `json:"needsMacHost"`
}

// Marker files/imports, in the order they are checked. ORDER IS LOAD-BEARING —
// see DetectSwiftProject.
var (
	// Includes the bare package name and the org, because a real Package.swift
	// dependency reads
	//   .package(url: "https://github.com/TokamakUI/Tokamak", from: "0.11.0")
	// and only the .product(...) line mentions TokamakShim/TokamakDOM. Matching
	// only the module names missed actual Tokamak projects — caught by test.
	tokamakMarkers = []string{
		"TokamakUI", "TokamakDOM", "TokamakShim", "TokamakCore", "Tokamak",
		"swiftwasm", "SwiftWasm", "carton",
	}
	serverMarkers  = []string{"vapor", "hummingbird", "swift-nio", "Vapor", "Hummingbird"}
	appleUIImports = []string{"import UIKit", "import SwiftUI", "import AppKit"}
)

// DetectSwiftProject classifies a Swift project from files on disk.
//
// ORDER MATTERS and is deliberate. A Tokamak project is also a SwiftPM package
// and may ALSO carry an .xcodeproj purely for editing convenience while
// targeting WASM. Checking Xcode first would misroute it to a Mac host it does
// not need — so Tokamak and server-side are checked BEFORE Apple UI.
//
// Returns SwiftKindUnknown rather than guessing. Inventing a render target is
// worse than admitting uncertainty: the user would be shown something that is
// not their app.
func DetectSwiftProject(dir string) SwiftDetection {
	if strings.TrimSpace(dir) == "" {
		return SwiftDetection{Kind: SwiftKindUnknown, Evidence: "no directory given"}
	}

	manifest := readFileCapped(filepath.Join(dir, "Package.swift"), 64*1024)
	sources := readSwiftSourcesCapped(dir, 24, 32*1024)
	haystack := manifest + "\n" + sources

	// 1 + 2 — Tokamak / SwiftWasm. Highest priority: it is the one kind that
	// renders on Linux AND is easily mistaken for an Apple project.
	if m := firstMatch(haystack, tokamakMarkers); m != "" {
		return SwiftDetection{
			Kind:           SwiftKindTokamak,
			Evidence:       "found " + m + " (SwiftWasm/Tokamak → WebAssembly → browser)",
			RendersOnLinux: true,
		}
	}

	// 3 — server-side Swift, but ONLY when there is no Apple UI import. A Vapor
	// backend inside an iOS repo must not drag the whole project onto Linux.
	if m := firstMatch(manifest, serverMarkers); m != "" && firstMatch(sources, appleUIImports) == "" {
		return SwiftDetection{
			Kind:           SwiftKindServer,
			Evidence:       "found " + m + " with no Apple UI import (server-side Swift)",
			RendersOnLinux: true,
		}
	}

	// 4 — Apple's closed UI frameworks. Checked AFTER the Linux-capable kinds.
	if m := firstMatch(sources, appleUIImports); m != "" {
		return SwiftDetection{
			Kind:         SwiftKindAppleUI,
			Evidence:     "source contains " + m + " — Apple UI frameworks are macOS-only",
			NeedsMacHost: true,
		}
	}
	if hasAnyEntry(dir, ".xcodeproj", ".xcworkspace") {
		return SwiftDetection{
			Kind:         SwiftKindAppleUI,
			Evidence:     "Xcode project present with no Tokamak/server markers",
			NeedsMacHost: true,
		}
	}

	// 5 — a plain package. Compiles and tests on Linux; nothing to render.
	if manifest != "" {
		return SwiftDetection{
			Kind:     SwiftKindLibrary,
			Evidence: "Package.swift with no UI or server markers (logic/tests only)",
		}
	}

	return SwiftDetection{Kind: SwiftKindUnknown, Evidence: "no Swift project markers found"}
}

// ResolveSwiftPreview maps a detection onto a preview plan.
//
// Deliberately NOT a flat refusal for the non-rendering kinds. "Your tests run
// here, your UI needs a Mac" is both true and useful; "unsupported" turns away
// a developer who could get real value from the compile/test loop today.
func ResolveSwiftPreview(d SwiftDetection) WorkspacePreviewPlan {
	switch d.Kind {
	case SwiftKindTokamak:
		return WorkspacePreviewPlan{
			Primary:      PreviewChromeWebRTC,
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason: "Tokamak/SwiftWasm compiles to WebAssembly and renders in a browser, so it " +
				"streams from a Linux workspace exactly like RN-web or Flutter. " + d.Evidence +
				". Note: no HMR — an edit is a full WASM rebuild and page reload.",
		}

	case SwiftKindServer:
		return WorkspacePreviewPlan{
			Primary:      PreviewChromeWebRTC,
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason:       "server-side Swift serves HTTP, so the browser is the client. " + d.Evidence,
		}

	case SwiftKindAppleUI:
		return WorkspacePreviewPlan{
			Primary:      PreviewIOSSimulator,
			MachineClass: "standard",
			Feedback:     FeedbackViewerTriggered,
			// NOT supported on THIS workspace — a Linux box cannot run an iOS
			// simulator, and saying so plainly beats a web preview that would
			// render something which is not the user's app.
			Supported: false,
			Reason: "Apple UI frameworks need macOS: an iOS simulator cannot run on a Linux " +
				"workspace. " + d.Evidence + ". Compile and `swift test` DO run here — only the " +
				"UI preview needs a Mac host or your own device (`yaver wire push`).",
		}

	case SwiftKindLibrary:
		return WorkspacePreviewPlan{
			Primary:      PreviewUnsupported,
			MachineClass: "standard",
			Feedback:     FeedbackViewerTriggered,
			// Honest: there is genuinely nothing to render, and that is fine.
			Supported: false,
			Reason: "no UI to render — this package compiles and runs `swift test` on this " +
				"workspace, which is the whole loop for a library. " + d.Evidence,
		}

	default:
		return WorkspacePreviewPlan{
			Primary:      PreviewUnsupported,
			MachineClass: "standard",
			Feedback:     FeedbackViewerTriggered,
			Supported:    false,
			Reason:       "could not identify this Swift project; refusing to guess a render target. " + d.Evidence,
		}
	}
}

// SwiftRunsTestsOnLinux reports whether the compile/test loop works here.
//
// TRUE for every kind including apple-ui: the Swift toolchain is officially
// cross-platform, so only rendering is Mac-bound. This is the fact the old flat
// refusal hid.
func SwiftRunsTestsOnLinux(d SwiftDetection) bool {
	return d.Kind != SwiftKindUnknown
}

// ─── helpers ────────────────────────────────────────────────────────────────

func firstMatch(haystack string, needles []string) string {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return n
		}
	}
	return ""
}

func hasAnyEntry(dir string, suffixes ...string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		for _, s := range suffixes {
			if strings.HasSuffix(e.Name(), s) {
				return true
			}
		}
	}
	return false
}

func readFileCapped(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// readSwiftSourcesCapped samples .swift files. BOUNDED on purpose: detection is
// advisory metadata and must never sit in the critical path of the operation it
// annotates — an unbounded walk of a large repo is the `/tasks` incident again.
func readSwiftSourcesCapped(dir string, maxFiles int, maxBytesEach int64) string {
	var b strings.Builder
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || count >= maxFiles {
			return filepath.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".build", ".git", "node_modules", "Pods", "Carthage":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".swift") {
			return nil
		}
		b.WriteString(readFileCapped(path, maxBytesEach))
		b.WriteByte('\n')
		count++
		return nil
	})
	return b.String()
}
