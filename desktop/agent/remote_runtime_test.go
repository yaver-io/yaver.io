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
	if len(caps.Targets) != 1 {
		t.Fatalf("swift targets = %d, want 1", len(caps.Targets))
	}
	target := caps.Targets[0]
	if target.ID != "ios-simulator" {
		t.Fatalf("swift target id = %q, want ios-simulator", target.ID)
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
}

func TestRemoteRuntimeCapabilitiesForKotlinUseAndroidHostClass(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/kotlin-app", "kotlin")
	if len(caps.Targets) != 1 {
		t.Fatalf("kotlin targets = %d, want 1", len(caps.Targets))
	}
	target := caps.Targets[0]
	if target.ID != "android-emulator" {
		t.Fatalf("kotlin target id = %q, want android-emulator", target.ID)
	}
	if !strings.Contains(target.RuntimeHostClass, "android") {
		t.Fatalf("kotlin runtime host class = %q, want android suffix", target.RuntimeHostClass)
	}
	if target.RequiredCLI != "adb + emulator" {
		t.Fatalf("kotlin required cli = %q", target.RequiredCLI)
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
