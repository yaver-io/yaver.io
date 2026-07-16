package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	// Five Apple sim surfaces (iPhone/iPad/watch/tv/vision) + ios-device.
	// All disabled on non-macOS — Swift/iOS needs a Mac either way.
	wantIDs := []string{"ios-simulator", "ipados-simulator", "watchos-simulator", "tvos-simulator", "visionos-simulator", "ios-device"}
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
	// Post-P6: android-emulator + Wear/TV/XR/Auto surface variants +
	// android-redroid (Docker) + android-device (physical fallback).
	wantIDs := []string{"android-emulator", "android-wear", "android-tv", "android-xr", "android-auto", "android-redroid", "android-device"}
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
	// Post-P6: Android fan-out (emulator + wear + tv + xr + auto +
	// redroid + device) + Apple sim fan-out (iPhone/iPad/watch/tv/
	// vision) + ios-device = 13 targets.
	wantIDs := map[string]bool{
		"android-emulator": true, "android-wear": true, "android-tv": true,
		"android-xr": true, "android-auto": true, "android-redroid": true,
		"android-device": true, "ios-simulator": true, "ipados-simulator": true,
		"watchos-simulator": true, "tvos-simulator": true, "visionos-simulator": true,
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
