package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// devserver_swiftwasm.go — Tokamak / SwiftWasm dev server.
//
// This is what makes a SWIFT project streamable from a LINUX Cloud Workspace.
//
// Tokamak is a SwiftUI-compatible API that compiles through SwiftWasm to
// WebAssembly and renders in a browser. `carton dev` builds the WASM bundle and
// serves it over HTTP — from that point the project is indistinguishable to the
// rest of Yaver from a Vite app, so the existing chrome-webrtc path streams it
// with no further work.
//
// ─── What this does NOT do ──────────────────────────────────────────────────
//
// It does not run iOS apps on Linux. Apple SwiftUI and UIKit are closed
// frameworks; only Tokamak-authored projects work here. swift_project_detect.go
// makes that distinction and routes UIKit/SwiftUI to a Mac host — this server
// is only reached for a project that already detected as SwiftWasm.
//
// ─── The honest limitation: no HMR ──────────────────────────────────────────
//
// SupportsHotReload() returns FALSE, deliberately.
//
// carton watches and rebuilds, but a Swift change means a full WASM recompile
// and a PAGE RELOAD — there is no module swap as with Metro or Vite. Realistic
// edit-to-visible is seconds, not milliseconds, and the reload loses UI state.
//
// Claiming hot reload here would be the inventory-vs-operation error this
// codebase keeps hitting: the caller would preserve state it is about to lose
// and report a fast path that does not exist. Say false, let the UI show a
// rebuild indicator, and let the number be measured rather than assumed.
type SwiftWasmDevServer struct {
	baseDevServer
}

func (s *SwiftWasmDevServer) Name() string { return "swiftwasm" }

// Detect looks for a SwiftPM package that targets WebAssembly.
//
// Requires BOTH a Package.swift and a Tokamak/carton marker: a bare
// Package.swift is far more likely to be an ordinary Swift library, and
// starting carton on one would fail confusingly after a long toolchain spin-up.
func (s *SwiftWasmDevServer) Detect(workDir string) bool {
	if _, err := os.Stat(filepath.Join(workDir, "Package.swift")); err != nil {
		return false
	}
	det := DetectSwiftProject(workDir)
	return det.Kind == SwiftKindTokamak
}

func (s *SwiftWasmDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	s.name = "swiftwasm"
	s.port = opts.Port
	if s.port == 0 {
		// carton's default. Kept distinct from Vite (5173) and Metro (8081) so
		// a workspace can run several previews without collision.
		s.port = 8080
	}

	// Probe the OPERATION, not the inventory. "carton is configured" and
	// "carton runs" are different claims, and only the second serves a bundle.
	// Failing here with a specific remedy beats a 60-second silence followed by
	// an opaque build error.
	if _, err := exec.LookPath("carton"); err != nil {
		return fmt.Errorf("carton not found on PATH — SwiftWasm previews need the SwiftWasm " +
			"toolchain and carton baked into the workspace image (they are ~hundreds of MB, " +
			"so they must be pre-installed, not fetched on first run)")
	}
	if _, err := exec.LookPath("swift"); err != nil {
		return fmt.Errorf("swift not found on PATH — the SwiftWasm toolchain is missing from this workspace image")
	}

	args := []string{
		"dev",
		"--port", fmt.Sprintf("%d", s.port),
		// Bind every interface so the relay tunnel and LAN preview both reach
		// it, same as Vite. The agent fronts /dev/* either way.
		"--host", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", s.port)
	return s.startProcess(ctx, "carton", args, opts.WorkDir, nil, readyURL)
}

// BundleURL — the browser loads the served page; there is no separate bundle
// endpoint as with Metro.
func (s *SwiftWasmDevServer) BundleURL(platform string) string { return "/dev/" }

// SupportsHotReload is FALSE on purpose — see the file comment. A Swift edit is
// a full WASM rebuild plus page reload, not a module swap.
func (s *SwiftWasmDevServer) SupportsHotReload() bool { return false }

// Reload is a no-op: carton's watcher triggers the rebuild and the browser
// reloads itself. There is nothing for the agent to push.
func (s *SwiftWasmDevServer) Reload() error { return nil }

// Kind classes SwiftWasm as WEB.
//
// The rendered surface is a browser page, so it belongs on the Web Reload
// surface and streams through the same chrome-webrtc path as Vite, Next and
// Flutter. Classing it "mobile" would put it in the Hot Reload tab next to
// Hermes, where nothing it does would work.
func (*SwiftWasmDevServer) Kind() DevServerKind { return DevServerKindWeb }

// SwiftWasmToolchainStatus reports whether this workspace can serve SwiftWasm.
//
// Exposed separately from Start so a UI can say "this workspace cannot preview
// Swift yet, and here is why" BEFORE a user waits on a failed start. The
// difference between a ten-second diagnosis and a lost session is usually a
// probe that ran early.
func SwiftWasmToolchainStatus() (ready bool, detail string) {
	var missing []string
	if _, err := exec.LookPath("swift"); err != nil {
		missing = append(missing, "swift")
	}
	if _, err := exec.LookPath("carton"); err != nil {
		missing = append(missing, "carton")
	}
	if len(missing) == 0 {
		return true, "SwiftWasm toolchain present"
	}
	return false, "missing from this workspace image: " + strings.Join(missing, ", ") +
		" — SwiftWasm previews require them to be baked in (hundreds of MB; fetching on " +
		"first run would put minutes of download in the critical path)"
}
