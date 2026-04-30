package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
