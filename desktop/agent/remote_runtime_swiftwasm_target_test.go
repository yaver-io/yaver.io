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
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{repo + "/demo/mobile", "react-native", "RN's web target is the cheapest lane and the only one with the in-app SDK"},
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

// RN is the one stack with three honest choices. Adding the browser lane must
// not have cost it the simulators — Hermes lives in its own mode, and the
// simulators must still be reachable for platform-specific work.
func TestRNKeepsAllThreeOptions(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "react-native")
	if _, ok := targetByID(caps, "browser-window"); !ok {
		t.Error("RN must be offered the browser lane")
	}
	if _, ok := targetByID(caps, "ios-simulator"); !ok {
		t.Error("RN must keep the iOS simulator — the browser lane adds, it does not replace")
	}
	if _, ok := targetByID(caps, "android-emulator"); !ok {
		t.Error("RN must keep the Android emulator")
	}
	if caps.Targets[0].ID != "browser-window" {
		t.Errorf("the cheapest lane should lead for RN, got %q", caps.Targets[0].ID)
	}
}

// ── Create → Navigate: the blank-page fix ──────────────────────────────
//
// TestFlight 457 shipped a browser-window session that opened at about:blank
// and stayed there — Create built the session but never told the browser what
// to load, and every subsequent tap/pinch returned 200 while the frame stayed
// empty. The tests below pin the ONE change that closed that gap: after Create
// resolves the browser-window target, it now navigates the session at the
// project's dev-server URL, or explains in session.Note why it could not.
//
// Two dimensions matter and both are tested:
//
//   1. WHICH PORT. Metro (RN/Expo) serves bytecode, not the web app — the RN
//      web bundle lives on a SIBLING Expo Web process whose port is
//      WebPreviewPort(). Flutter / Vite / Next serve the app on the primary
//      DevServerPort(). Picking the wrong one loads a JSON error page (Metro)
//      instead of the RN-Web app.
//
//   2. WHAT HAPPENS WHEN THERE IS NO DEV SERVER. Silent about:blank is the
//      bug being fixed; a blank page WITH an explanation is acceptable, so
//      Create returns a session whose Note names the missing prerequisite.

type fakeDevServer struct {
	running    bool
	workDir    string
	devPort    int // Vite / Next / Flutter — primary
	webPreview int // Expo web sibling — the RN-Web bundle
}

func (f *fakeDevServer) IsRunning() bool { return f.running }
func (f *fakeDevServer) Status() *DevServerStatus {
	if !f.running {
		return nil
	}
	return &DevServerStatus{WorkDir: f.workDir}
}
func (f *fakeDevServer) DevServerPort() int  { return f.devPort }
func (f *fakeDevServer) WebPreviewPort() int { return f.webPreview }

func TestResolveDevServerURLPicksExpoSiblingForRN(t *testing.T) {
	// Metro on 8081 is a decoy — an RN browser-window session must NOT be
	// pointed at DevServerPort. WebPreviewPort (Expo web sibling) is where
	// RN-Web actually lives; that is why WebPreviewPort exists at all.
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/rn", devPort: 8081, webPreview: 8085,
	})
	url, reason := mgr.resolveDevServerURL("/tmp/rn", "react-native")
	if reason != "" {
		t.Fatalf("expected URL with a running Expo web sibling, got reason=%q", reason)
	}
	if !strings.Contains(url, ":8085") {
		t.Errorf("RN must resolve to WebPreviewPort (:8085), got %q — DevServerPort would land on Metro's JSON, not the app", url)
	}
	if strings.Contains(url, ":8081") {
		t.Errorf("RN must NOT resolve to Metro's port (:8081): %q", url)
	}
}

func TestResolveDevServerURLPicksDevPortForFlutter(t *testing.T) {
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/flutter", devPort: 4200,
	})
	url, reason := mgr.resolveDevServerURL("/tmp/flutter", "flutter")
	if reason != "" {
		t.Fatalf("flutter dev server running, but resolve returned reason %q", reason)
	}
	if !strings.Contains(url, ":4200") {
		t.Errorf("flutter must resolve to DevServerPort (:4200), got %q", url)
	}
}

func TestResolveDevServerURLPicksDevPortForWeb(t *testing.T) {
	// Vite / Next serve directly — no sibling process. DevServerPort is
	// correct for them.
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/next", devPort: 3000,
	})
	for _, fw := range []string{"next", "vite", "swift"} {
		url, reason := mgr.resolveDevServerURL("/tmp/next", fw)
		if reason != "" {
			t.Errorf("%s: unexpected reason %q", fw, reason)
			continue
		}
		if !strings.Contains(url, ":3000") {
			t.Errorf("%s: expected DevServerPort (:3000), got %q", fw, url)
		}
	}
}

func TestResolveDevServerURLExplainsMissingDevServer(t *testing.T) {
	// No dev-server manager wired at all — legal, but Create must carry a
	// concrete "why" not a silent about:blank. That reversal is the fix.
	mgr := NewRemoteRuntimeManager()
	url, reason := mgr.resolveDevServerURL("/tmp/x", "flutter")
	if url != "" {
		t.Fatalf("expected no URL when manager is unwired, got %q", url)
	}
	if reason == "" {
		t.Fatal("silent empty reason is the bug being fixed: the viewer must see WHY the browser is blank")
	}

	// Wired but nothing running.
	mgr.SetDevServerManager(&fakeDevServer{running: false})
	url, reason = mgr.resolveDevServerURL("/tmp/x", "flutter")
	if url != "" || reason == "" {
		t.Errorf("no dev server running: want (url==\"\", reason!=\"\"), got (%q, %q)", url, reason)
	}
	if !strings.Contains(strings.ToLower(reason), "dev server") {
		t.Errorf("reason should name the missing prerequisite; got %q", reason)
	}
}

func TestResolveDevServerURLRefusesWrongProject(t *testing.T) {
	// A dev server running for project A must NOT be silently used to load
	// the browser-window session for project B — the viewer would think
	// they were looking at B and be wrong.
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/other-project", devPort: 4200,
	})
	url, reason := mgr.resolveDevServerURL("/tmp/this-project", "flutter")
	if url != "" {
		t.Fatalf("must not point session at another project's dev server, got %q", url)
	}
	if !strings.Contains(reason, "other-project") {
		t.Errorf("reason should name the conflicting project so the viewer can act on it, got %q", reason)
	}
}

// ── Create → Navigate (integration seam) ──────────────────────────────
//
// Exercises the Create path with a recording navigator: Create stores the
// session, resolves the dev-server URL, and calls Navigate with it. Chrome is
// not required because the injected navigator returns success without
// launching a real browser — this is the "fake/recording browser target that
// captures the URL" acceptance form named in the task.

type recordingBrowserNav struct {
	attachDeviceID string
	navigatedURL   string
	attachCalls    int
	navigateCalls  int
}

func (r *recordingBrowserNav) Attach(_ context.Context) (string, error) {
	r.attachCalls++
	if r.attachDeviceID == "" {
		r.attachDeviceID = "fake-browser-window"
	}
	return r.attachDeviceID, nil
}
func (r *recordingBrowserNav) Navigate(_ context.Context, deviceID, url string) error {
	r.navigateCalls++
	r.navigatedURL = url
	return nil
}

func TestCreateBrowserWindowNavigatesToResolvedURL(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("browser-window target requires a Chrome binary on this host to be offered by Create")
	}
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/flutter", devPort: 4200,
	})
	rec := &recordingBrowserNav{}
	mgr.SetBrowserNavigator(rec)

	session, err := mgr.Create("/tmp/flutter", "flutter", "browser-window", "direct-webrtc")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if rec.navigateCalls != 1 {
		t.Fatalf("Navigate must be called exactly once for a browser-window Create with a running dev server; got %d", rec.navigateCalls)
	}
	if !strings.Contains(rec.navigatedURL, ":4200") {
		t.Errorf("Navigate got %q; expected the resolved dev-server URL (:4200)", rec.navigatedURL)
	}
	if !strings.Contains(session.Note, "4200") {
		t.Errorf("session.Note should record the destination URL for viewer diagnostics; got %q", session.Note)
	}
	if session.DeviceID != rec.attachDeviceID {
		t.Errorf("session.DeviceID must carry the attached browser id (so the HTTP handler's later Attach short-circuits); got %q, want %q", session.DeviceID, rec.attachDeviceID)
	}
}

func TestCreateBrowserWindowExplainsWhenNoDevServer(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("browser-window target requires a Chrome binary on this host to be offered by Create")
	}
	mgr := NewRemoteRuntimeManager()
	// deliberately no SetDevServerManager
	rec := &recordingBrowserNav{}
	mgr.SetBrowserNavigator(rec)

	session, err := mgr.Create("/tmp/flutter", "flutter", "browser-window", "direct-webrtc")
	if err != nil {
		t.Fatalf("Create must succeed and carry the reason in session.Note, not return an error: %v", err)
	}
	if rec.navigateCalls != 0 {
		t.Errorf("Navigate must not be called when no dev server is available — an about:blank hit is worse than not attaching")
	}
	if session.Note == "" || !strings.Contains(strings.ToLower(session.Note), "dev server") {
		t.Errorf("session.Note must explain WHY the browser is blank; got %q", session.Note)
	}
	if session.Status != "waiting-for-dev-server" {
		t.Errorf("session.Status must signal the missing prerequisite so the viewer can render a call-to-action; got %q", session.Status)
	}
}

// The RN companion of the flutter test above — verifies the WebPreviewPort
// picking is honoured through the full Create seam, not just at the resolver.
// This is the assertion that would have caught the whole class of bug: a green
// "session created" plus a check that the URL Navigate got carries the RIGHT
// port. Metro's port on an RN session is the blank-page-with-extra-steps bug.
func TestCreateBrowserWindowForRNUsesWebPreviewPort(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("browser-window target requires a Chrome binary on this host to be offered by Create")
	}
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true, workDir: "/tmp/rn", devPort: 8081, webPreview: 8085,
	})
	rec := &recordingBrowserNav{}
	mgr.SetBrowserNavigator(rec)

	if _, err := mgr.Create("/tmp/rn", "react-native", "browser-window", "direct-webrtc"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !strings.Contains(rec.navigatedURL, ":8085") {
		t.Errorf("RN Create must navigate to WebPreviewPort (:8085), got %q", rec.navigatedURL)
	}
	if strings.Contains(rec.navigatedURL, ":8081") {
		t.Errorf("RN Create must NOT navigate to Metro (:8081), which serves bytecode: %q", rec.navigatedURL)
	}
}
