package main

// swift_toolchain.go — Linux Swift toolchain detection. The
// official swift.org Ubuntu / Fedora / Amazon Linux toolchains can
// build SwiftPM packages with Foundation, Dispatch, Combine
// (open-source bits), and the broader server-side Swift ecosystem
// (Vapor, Hummingbird, swift-nio, swift-async-algorithms, …).
//
// What this file does NOT enable:
//   • UIKit, SwiftUI, AVFoundation, MapKit, etc. — closed-source
//     Apple frameworks. SwiftUI imports fail with "no such module".
//   • `xcodebuild`, `xcrun simctl`, code signing.
//   • Producing .app / .ipa for iOS / iPadOS / watchOS.
//
// The recommended split for an iOS app: factor out a Package.swift
// library target containing pure-logic types (stores, networking,
// models, parsing). Build + test that on Linux for fast TDD; build
// the Xcode app target on macOS for shipping. See the design doc at
// docs/native-webrtc-web-streaming.md §9.

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// SwiftToolchain captures what `yaver swift doctor` discovered on
// the current host. Path is the resolved absolute swift binary
// (LookPath result). Version is the marketing version string —
// e.g. "5.10", "6.0", "5.9.2" — parsed from `swift --version`.
type SwiftToolchain struct {
	Path      string `json:"path"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Notes     string `json:"notes,omitempty"` // human-readable detail (install hint, etc.)
}

// DetectSwiftToolchain probes for `swift --version` on PATH and
// parses the output. Always returns a non-nil *SwiftToolchain so
// callers can read .Available without a nil check; the err return
// is reserved for genuinely-unexpected failures (process exits with
// a non-zero status from a corrupted toolchain, etc.).
//
// The 4-second timeout is deliberately tight — `swift --version`
// should respond in <100 ms on any healthy install. A timeout here
// almost always means a half-broken toolchain (rosetta translation
// loop, or the binary linked against missing libstdc++) rather
// than a slow box.
func DetectSwiftToolchain(ctx context.Context) (*SwiftToolchain, error) {
	tc := &SwiftToolchain{}
	path, err := exec.LookPath("swift")
	if err != nil {
		tc.Notes = "swift not on PATH — install from https://swift.org/install/linux/ or via your package manager"
		return tc, nil
	}
	tc.Path = path

	probe, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probe, path, "--version").CombinedOutput()
	if err != nil {
		tc.Notes = fmt.Sprintf("swift --version failed: %v (%s)", err, strings.TrimSpace(string(out)))
		return tc, err
	}
	tc.Version = parseSwiftVersion(string(out))
	tc.Available = tc.Version != ""
	if !tc.Available {
		tc.Notes = "swift --version returned unparseable output (toolchain installed but possibly damaged)"
	}
	return tc, nil
}

// swiftVersionPattern matches both the Apple form and the Linux form
// of swift's --version output:
//
//	Apple:  "Apple Swift version 6.0 (swiftlang-6.0.0.6.4)"
//	Linux:  "Swift version 5.10 (swift-5.10-RELEASE)"
//	Linux:  "Swift version 5.9.2 (...)"
//
// We capture only the numeric "X.Y" or "X.Y.Z" — operators and the
// dashboard work in marketing-version space, not in toolchain build
// IDs.
var swiftVersionPattern = regexp.MustCompile(`Swift version (\d+\.\d+(?:\.\d+)?)`)

func parseSwiftVersion(out string) string {
	if m := swiftVersionPattern.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	// Defensive fallback: if the format ever shifts (Swift 7? Swift
	// for Wasm?), surface the first line so a human auditor still
	// has something to grep for. Empty input → empty string, which
	// callers interpret as "version unparseable".
	first := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	return first
}
