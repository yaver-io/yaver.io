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
	// ios-simulator (default) + ios-device (physical, WDA). Both
	// disabled on non-macOS — Swift/iOS needs a Mac either way.
	if len(caps.Targets) != 2 {
		t.Fatalf("swift targets = %d, want 2 (ios-simulator + ios-device)", len(caps.Targets))
	}
	target := caps.Targets[0]
	if target.ID != "ios-simulator" {
		t.Fatalf("swift target[0] id = %q, want ios-simulator", target.ID)
	}
	if target.RuntimeHostClass != "macos-ios" {
		t.Fatalf("swift runtime host class = %q, want macos-ios", target.RuntimeHostClass)
	}
	if target.Enabled {
		t.Fatal("swift target should be disabled on non-macOS hosts")
	}
	if !strings.Contains(target.Reason, "macOS host") {
		t.Fatalf("swift disabled reason = %q, want macOS host guidance", target.Reason)
	}
	dev := caps.Targets[1]
	if dev.ID != "ios-device" || dev.Enabled || !strings.Contains(dev.Reason, "macOS") {
		t.Fatalf("swift target[1] should be a disabled ios-device w/ macOS reason, got %+v", dev)
	}
}

func TestRemoteRuntimeCapabilitiesForKotlinUseAndroidHostClass(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/kotlin-app", "kotlin")
	// android-emulator (default where the host can run it) +
	// android-device (physical fallback, e.g. linux/arm64).
	if len(caps.Targets) != 2 {
		t.Fatalf("kotlin targets = %d, want 2 (android-emulator + android-device)", len(caps.Targets))
	}
	target := caps.Targets[0]
	if target.ID != "android-emulator" {
		t.Fatalf("kotlin target[0] id = %q, want android-emulator", target.ID)
	}
	if !strings.Contains(target.RuntimeHostClass, "android") {
		t.Fatalf("kotlin runtime host class = %q, want android suffix", target.RuntimeHostClass)
	}
	if target.RequiredCLI != "adb + emulator" {
		t.Fatalf("kotlin required cli = %q", target.RequiredCLI)
	}
	if caps.Targets[1].ID != "android-device" {
		t.Fatalf("kotlin target[1] id = %q, want android-device", caps.Targets[1].ID)
	}
}

func TestRemoteRuntimeCapabilitiesForFlutterExposesBothTargets(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/flutter-app", "flutter")
	if !caps.RemoteRuntimeEligible {
		t.Fatal("flutter should be remote-runtime eligible")
	}
	if len(caps.Targets) != 4 {
		t.Fatalf("flutter targets = %d, want 4 (android-emulator + android-device + ios-simulator + ios-device)", len(caps.Targets))
	}
	ids := []string{}
	wantIDs := map[string]bool{"android-emulator": true, "android-device": true, "ios-simulator": true, "ios-device": true}
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
