package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExecutionModeForFramework(t *testing.T) {
	cases := []struct {
		framework string
		wantMode  ProjectExecutionMode
		wantSurf  string
	}{
		{"expo", ExecutionModeRNHermes, "hermes"},
		{"react-native", ExecutionModeRNHermes, "hermes"},
		{"nextjs", ExecutionModeWebWebview, "webview"},
		{"swift", ExecutionModeNativeWebRTC, "webrtc"},
		{"kotlin", ExecutionModeNativeWebRTC, "webrtc"},
		// Flutter joins the WebRTC family — see docs/native-webrtc-web-streaming.md §1.
		{"flutter", ExecutionModeNativeWebRTC, "webrtc"},
		// Unity is a generic remote-PC view (no SDK): the player runs on the
		// box's display and the desktop-screen capture streams it. This is the
		// guard that previously shipped ExecutionModeUnsupported — the
		// "inventory says surfaces, operation says unsupported" false green.
		{"unity", ExecutionModeNativeWebRTC, "webrtc"},
		{"desktop", ExecutionModeNativeWebRTC, "webrtc"},
	}
	for _, tc := range cases {
		if got := executionModeForFramework(tc.framework); got != tc.wantMode {
			t.Fatalf("%s mode = %s, want %s", tc.framework, got, tc.wantMode)
		}
		if got := primarySurfaceForFramework(tc.framework); got != tc.wantSurf {
			t.Fatalf("%s surface = %s, want %s", tc.framework, got, tc.wantSurf)
		}
	}
}

// Unity must resolve to a real, streamable target (the desktop-screen grab),
// not ExecutionModeUnsupported and not the web-dev-server downgrade. Break
// the unity case in remoteRuntimeCapabilitiesForProject and this fails.
func TestRemoteRuntimeCapabilitiesForUnityIsRemotePCView(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "unity")
	if !caps.RemoteRuntimeEligible {
		t.Fatalf("unity should be remote-runtime eligible (generic remote-PC view), got eligible=%v", caps.RemoteRuntimeEligible)
	}
	if caps.ExecutionMode != ExecutionModeNativeWebRTC {
		t.Fatalf("unity execution mode = %q, want native-webrtc", caps.ExecutionMode)
	}
	if len(caps.Targets) == 0 {
		t.Fatal("unity must have at least one streamable target")
	}
}

// RN/Expo is Hermes-primary but ALSO simulator-streamable over WebRTC — the
// alternative fast-iteration surface. Eligibility must not have flipped the
// PRIMARY surface (Hermes stays the default), and feedback in this mode is the
// client-shake→remote-sim flow.
func TestRemoteRuntimeCapabilitiesForRNIsWebRTCEligibleButHermesPrimary(t *testing.T) {
	for _, fw := range []string{"expo", "react-native"} {
		caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), fw)
		if !caps.RemoteRuntimeEligible {
			t.Fatalf("%s should be WebRTC-eligible as a secondary surface", fw)
		}
		if caps.PrimarySurface != "hermes" {
			t.Errorf("%s primary surface = %q, want hermes (WebRTC is the alternative, not the default)", fw, caps.PrimarySurface)
		}
		if caps.ExecutionMode != ExecutionModeRNHermes {
			t.Errorf("%s execution mode = %q, want rn-hermes", fw, caps.ExecutionMode)
		}
		if caps.FeedbackSurface != "client-shake-remote-sim" {
			t.Errorf("%s feedback surface = %q, want client-shake-remote-sim", fw, caps.FeedbackSurface)
		}
		if !caps.FeedbackSDKCompatible {
			t.Errorf("%s streamed sim runs the app's own live feedback SDK — must be compatible", fw)
		}
		// Must offer at least an iOS sim + Android emulator target.
		ids := map[string]bool{}
		var browser *RemoteRuntimeTarget
		for _, tg := range caps.Targets {
			ids[tg.ID] = true
			if tg.ID == "browser-window" {
				copy := tg
				browser = &copy
			}
		}
		if !ids["ios-simulator"] || !ids["android-emulator"] {
			t.Errorf("%s should offer ios-simulator + android-emulator targets, got %v", fw, ids)
		}
		if browser == nil {
			t.Errorf("%s should offer browser-window for the RN-web mobile lane, got %v", fw, ids)
		} else {
			if browser.DisplaySurface != "mobile-web" {
				t.Errorf("%s browser-window displaySurface = %q, want mobile-web", fw, browser.DisplaySurface)
			}
			if browser.Viewport == nil || browser.Viewport.Width != 393 || browser.Viewport.Height != 852 {
				t.Errorf("%s browser-window viewport = %+v, want 393x852", fw, browser.Viewport)
			}
		}
	}
}

// Web apps remain WebView-primary on clients that have a browser, but TV,
// vision and watch surfaces must still be able to stream that browser window
// over WebRTC. Returning eligible=false here was the tvOS "data is missing"
// failure: the nil target slice also serialized as JSON null.
func TestRemoteRuntimeCapabilitiesForWebOffersBrowserWebRTC(t *testing.T) {
	for _, fw := range []string{"nextjs", "next", "vite", "react"} {
		caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), fw)
		if !caps.RemoteRuntimeEligible {
			t.Fatalf("%s should expose the browser-window WebRTC lane", fw)
		}
		if caps.ExecutionMode != ExecutionModeWebWebview || caps.PrimarySurface != "webview" {
			t.Fatalf("%s changed primary routing: mode=%q surface=%q", fw, caps.ExecutionMode, caps.PrimarySurface)
		}
		if len(caps.Targets) != 1 || caps.Targets[0].ID != "browser-window" {
			t.Fatalf("%s targets = %+v, want browser-window", fw, caps.Targets)
		}
	}
}

func TestRemoteRuntimeIneligibleTargetsEncodeAsArray(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "unknown-framework")
	if caps.Targets == nil {
		t.Fatal("ineligible capabilities must carry [] targets, not null")
	}
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"targets":[]`) {
		t.Fatalf("targets wire shape = %s, want []", raw)
	}
}

// A native (non-RN) framework keeps in-app-sdk feedback and its WebRTC-primary
// surface — the RN change must not have leaked into it.
func TestRemoteRuntimeNativeFeedbackSurfaceUnchanged(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/swift-app", "swift")
	if caps.FeedbackSurface != "in-app-sdk" {
		t.Errorf("swift feedback surface = %q, want in-app-sdk", caps.FeedbackSurface)
	}
	if caps.PrimarySurface != "webrtc" {
		t.Errorf("swift primary surface = %q, want webrtc", caps.PrimarySurface)
	}
}

// injectSimulatorShake must refuse a target it cannot drive rather than claim
// success — the caller degrades to the events-channel path on error.
func TestInjectSimulatorShakeRejectsUnknownTarget(t *testing.T) {
	err := injectSimulatorShake(context.Background(), RemoteRuntimeSession{TargetID: "browser-window"})
	if err == nil {
		t.Fatal("shake into a browser target must error, not silently succeed")
	}
}

func TestRemoteRuntimeCapabilitiesForSwiftIncludesFeedbackProtocol(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/swift-app", "swift")
	if !caps.RemoteRuntimeEligible {
		t.Fatal("swift should be remote-runtime eligible")
	}
	if !caps.FeedbackSDKCompatible {
		t.Fatal("swift remote runtime should mark feedback sdk compatible")
	}
	if caps.FeedbackControlProtocol != "remote-runtime-feedback-v1" {
		t.Fatalf("feedback protocol = %q", caps.FeedbackControlProtocol)
	}
	if len(caps.SupportedTransports) == 0 {
		t.Fatal("expected supported transports")
	}
	if caps.Targets[0].RuntimeHostClass == "" {
		t.Fatal("expected runtime host class on target")
	}
}

func TestRemoteRuntimeCapabilitiesForSwiftOnLinuxRequiresMacHost(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux-only expectation")
	}
	caps := remoteRuntimeCapabilitiesForProject("/tmp/swift-app", "swift")
	// Mobile/tablet Apple targets only. watchOS/tvOS/visionOS need project
	// code markers; a Swift label alone is not an Apple TV/watch app.
	wantIDs := []string{"ios-simulator", "ipados-simulator", "ios-device"}
	if len(caps.Targets) != len(wantIDs) {
		t.Fatalf("swift targets = %d, want %d (%v)", len(caps.Targets), len(wantIDs), wantIDs)
	}
	for i, want := range wantIDs {
		tg := caps.Targets[i]
		if tg.ID != want {
			t.Fatalf("swift target[%d] id = %q, want %q", i, tg.ID, want)
		}
		if tg.RuntimeHostClass != "macos-ios" {
			t.Fatalf("swift target[%d] runtime host class = %q, want macos-ios", i, tg.RuntimeHostClass)
		}
		if tg.Enabled {
			t.Fatalf("swift target[%d] should be disabled on non-macOS hosts", i)
		}
		if !strings.Contains(tg.Reason, "macOS") {
			t.Fatalf("swift target[%d] disabled reason = %q, want macOS guidance", i, tg.Reason)
		}
	}
}

func TestRemoteRuntimeCapabilitiesForKotlinUseAndroidHostClass(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/kotlin-app", "kotlin")
	// Phone/tablet Android targets only. Wear/TV/XR/Auto need project code
	// markers; host inventory alone must not advertise them.
	wantIDs := []string{"android-emulator", "android-redroid", "android-device"}
	if len(caps.Targets) != len(wantIDs) {
		t.Fatalf("kotlin targets = %d, want %d (%v)", len(caps.Targets), len(wantIDs), wantIDs)
	}
	for i, want := range wantIDs {
		tg := caps.Targets[i]
		if tg.ID != want {
			t.Fatalf("kotlin target[%d] id = %q, want %q", i, tg.ID, want)
		}
		// redroid legitimately reports its host class as `linux-redroid`
		// (it's a Docker container, not the emulator suite).
		if tg.ID != "android-redroid" && !strings.Contains(tg.RuntimeHostClass, "android") {
			t.Fatalf("kotlin target[%d] runtime host class = %q, want android suffix", i, tg.RuntimeHostClass)
		}
	}
	if caps.Targets[0].RequiredCLI != "adb + emulator" {
		t.Fatalf("kotlin required cli = %q", caps.Targets[0].RequiredCLI)
	}
}

func TestRemoteRuntimeCapabilitiesForFlutterExposesBothTargets(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/flutter-app", "flutter")
	if !caps.RemoteRuntimeEligible {
		t.Fatal("flutter should be remote-runtime eligible")
	}
	// Browser first, then ordinary mobile/tablet targets. Specialized
	// watch/TV/XR/car targets require code markers.
	wantIDs := map[string]bool{
		"browser-window":   true,
		"android-emulator": true, "android-redroid": true,
		"android-device": true, "ios-simulator": true, "ipados-simulator": true,
		"ios-device": true,
	}
	if len(caps.Targets) != len(wantIDs) {
		t.Fatalf("flutter targets = %d, want %d", len(caps.Targets), len(wantIDs))
	}
	ids := []string{}
	for _, tg := range caps.Targets {
		ids = append(ids, tg.ID)
		if !wantIDs[tg.ID] {
			t.Fatalf("unexpected flutter target id %q (got %v)", tg.ID, ids)
		}
		delete(wantIDs, tg.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("flutter caps missing targets %v (got %v)", wantIDs, ids)
	}
}

func TestRemoteRuntimeCapabilitiesOnlyOffersDeclaredSpecialClientSurfaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ios", "App.xcodeproj"), 0o755); err != nil {
		t.Fatalf("mkdir xcodeproj: %v", err)
	}
	pbx := strings.Join([]string{
		"SDKROOT = watchos;",
		"SDKROOT = appletvos;",
		"SDKROOT = xros;",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "ios", "App.xcodeproj", "project.pbxproj"), []byte(pbx), 0o600); err != nil {
		t.Fatalf("write pbxproj: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "android", "wear", "src", "main"), 0o755); err != nil {
		t.Fatalf("mkdir wear manifest: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "android", "wear", "src", "main", "AndroidManifest.xml"),
		[]byte(`<manifest><uses-feature android:name="android.hardware.type.watch"/></manifest>`),
		0o600,
	); err != nil {
		t.Fatalf("write wear manifest: %v", err)
	}
	for _, marker := range []string{"android-tv", "android-xr", "android-auto"} {
		if err := os.MkdirAll(filepath.Join(dir, marker), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", marker, err)
		}
	}

	caps := remoteRuntimeCapabilitiesForProject(dir, "flutter")
	want := map[string]bool{
		"watchos-simulator":  true,
		"android-wear":       true,
		"tvos-simulator":     true,
		"android-tv":         true,
		"visionos-simulator": true,
		"android-xr":         true,
		"android-auto":       true,
	}
	for _, target := range caps.Targets {
		delete(want, target.ID)
	}
	if len(want) != 0 {
		t.Fatalf("declared special surfaces missing: %v", want)
	}
}

func TestRemoteRuntimeAllSurfacesOverrideExposesAndroidSpecialTargets(t *testing.T) {
	t.Setenv("YAVER_REMOTE_RUNTIME_ALL_SURFACES", "1")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "android", "app", "src", "main"), 0o755); err != nil {
		t.Fatalf("mkdir android app: %v", err)
	}

	caps := remoteRuntimeCapabilitiesForProject(dir, "react-native")
	want := map[string]bool{
		"android-wear":    true,
		"android-tv":      true,
		"android-xr":      true,
		"android-auto":    true,
		"android-redroid": true,
	}
	for _, target := range caps.Targets {
		delete(want, target.ID)
	}
	if len(want) != 0 {
		t.Fatalf("all-surfaces override did not expose Android special targets: %v", want)
	}
}

func TestAndroidSpecialSurfaceCapabilityNamesMissingAVD(t *testing.T) {
	t.Setenv("YAVER_REMOTE_RUNTIME_ALL_SURFACES", "1")
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	for _, name := range []string{"adb", "emulator"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("PATH", bin)

	caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), "react-native")
	target, ok := targetByIDForTest(caps, "android-wear")
	if !ok {
		t.Fatal("android-wear target missing")
	}
	if target.Enabled {
		t.Fatalf("android-wear was enabled without a wear AVD: %+v", target)
	}
	if !strings.Contains(target.Reason, "AVD \"wear\" is not configured") {
		t.Fatalf("missing AVD reason was not named:\n%s", target.Reason)
	}
}

func targetByIDForTest(caps RemoteRuntimeCapabilities, id string) (RemoteRuntimeTarget, bool) {
	for _, target := range caps.Targets {
		if target.ID == id {
			return target, true
		}
	}
	return RemoteRuntimeTarget{}, false
}

func TestRemoteRuntimeSessionCarriesDeviceDims(t *testing.T) {
	// The DeviceDims field should round-trip through JSON unscathed
	// so the web viewer can pick it up directly from the session
	// payload without waiting for the events channel.
	session := RemoteRuntimeSession{
		ID:         "rr_dims",
		Framework:  "kotlin",
		Status:     "streaming",
		DeviceDims: &DeviceDims{Width: 1080, Height: 2400, Scale: 3, Rotation: "portrait"},
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded RemoteRuntimeSession
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.DeviceDims == nil {
		t.Fatal("decoded session missing DeviceDims")
	}
	if decoded.DeviceDims.Width != 1080 || decoded.DeviceDims.Height != 2400 {
		t.Fatalf("decoded dims = %+v, want 1080x2400", decoded.DeviceDims)
	}
	if decoded.DeviceDims.Rotation != "portrait" {
		t.Fatalf("decoded rotation = %q", decoded.DeviceDims.Rotation)
	}
}

func TestHandleRemoteRuntimeSessionCommandLaunchFeedback(t *testing.T) {
	srv := &HTTPServer{remoteRuntimeMgr: NewRemoteRuntimeManager()}
	session := RemoteRuntimeSession{
		ID:            "rr_test",
		WorkDir:       "/tmp/swift-app",
		Framework:     "swift",
		ExecutionMode: ExecutionModeNativeWebRTC,
		TargetID:      "ios-simulator",
		TargetLabel:   "iOS Simulator over WebRTC",
		Status:        "control-ready",
		CreatedAt:     "2026-04-30T00:00:00Z",
		UpdatedAt:     "2026-04-30T00:00:00Z",
		Note:          "initial",
	}
	srv.remoteRuntimeMgr.sessions[session.ID] = session
	req := httptest.NewRequest(http.MethodPost, "/remote-runtime/sessions/"+session.ID+"/command", strings.NewReader(`{"command":"launch-feedback","source":"shake"}`))
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeSessionCommand(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["protocol"] != "remote-runtime-feedback-v1" {
		t.Fatalf("protocol = %#v, want remote-runtime-feedback-v1", body["protocol"])
	}
	gotSession, ok := srv.remoteRuntimeMgr.Get(session.ID)
	if !ok {
		t.Fatal("session missing after command")
	}
	if gotSession.Status != "feedback-pending" {
		t.Fatalf("session status = %q, want feedback-pending", gotSession.Status)
	}
	if gotSession.LastCommand != "launch-feedback" {
		t.Fatalf("last command = %q, want launch-feedback", gotSession.LastCommand)
	}
	if !strings.Contains(gotSession.Note, "shake") {
		t.Fatalf("session note = %q, expected source", gotSession.Note)
	}
}

// The iOS simulator build must use FIRST-PARTY tools (xcodebuild + simctl, no
// expo CLI): a GENERIC simulator destination (a specific-udid destination fails
// to enumerate a simctl-booted device on Xcode 26.4), a single HOST arch (the
// x86_64 slice fails to compile on Apple Silicon), Debug config (keeps Metro Fast
// Refresh), and no code signing.
func TestIOSSimBuildArgs(t *testing.T) {
	args := iosSimBuildArgs("/p/ios/Talos.xcworkspace", "Talos", "/tmp/dd", "arm64")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"xcodebuild",
		"-workspace /p/ios/Talos.xcworkspace",
		"-scheme Talos",
		"-configuration Debug",           // Fast Refresh, not release
		"generic/platform=iOS Simulator", // NOT id=<udid> — that fails on 26.4
		"ARCHS=arm64",                    // single host slice; x86_64 fails on Apple Silicon
		"CODE_SIGNING_ALLOWED=NO",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("iosSimBuildArgs missing %q\ngot: %s", want, joined)
		}
	}
	// Must never target a specific udid (the enumeration-failure form).
	if strings.Contains(joined, "id=") {
		t.Error("build must use the generic destination, never a specific udid")
	}
}

func TestHostSimulatorArch(t *testing.T) {
	got := hostSimulatorArch()
	if got != "arm64" && got != "x86_64" {
		t.Errorf("host sim arch = %q, want arm64 or x86_64", got)
	}
}

// The Android build is first-party gradle (assembleDebug) so it runs on the Linux
// Cloud Workspace too — the Apple-client / Linux-server redroid case. Debug keeps
// Metro Fast Refresh.
func TestAndroidGradleAssembleArgs(t *testing.T) {
	got := strings.Join(androidGradleAssembleArgs(), " ")
	if got != "./gradlew :app:assembleDebug" {
		t.Errorf("android build = %q, want ./gradlew :app:assembleDebug", got)
	}
}

func TestIsRNSimulatorTarget(t *testing.T) {
	for _, ok := range []string{"ios-simulator", "tvos-simulator", "android-emulator", "android-redroid"} {
		if !isRNSimulatorTarget(ok) {
			t.Errorf("%s should be an RN sim target", ok)
		}
	}
	for _, no := range []string{"browser-window", "desktop-screen", ""} {
		if isRNSimulatorTarget(no) {
			t.Errorf("%s should NOT be an RN sim target", no)
		}
	}
}
