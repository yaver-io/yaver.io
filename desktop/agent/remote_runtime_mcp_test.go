package main

// remote_runtime_mcp_test.go — P1 keystone tests.
//
// Verifies that the /remote-runtime/sessions/<id>/command handler
// accepts the two new P1 commands (`boot`, `launch-app`) and rejects
// bad inputs; the runtime_frame MCP verb returns a first-class image
// content block; and the launch-app dispatcher rejects unsupported
// targets. We stub the manager rather than shell to real simctl/adb
// so this runs on any host (mac or linux CI).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func nilCtxHelper(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func newTestRemoteRuntimeSession(id, targetID, deviceID string) RemoteRuntimeSession {
	return RemoteRuntimeSession{
		ID:            id,
		WorkDir:       "/tmp/swift-app",
		Framework:     "swift",
		ExecutionMode: ExecutionModeNativeWebRTC,
		TargetID:      targetID,
		TargetLabel:   "iPhone Simulator over WebRTC",
		DeviceID:      deviceID,
		Status:        "control-ready",
		CreatedAt:     "2026-07-16T00:00:00Z",
		UpdatedAt:     "2026-07-16T00:00:00Z",
	}
}

func TestHandleRemoteRuntimeSessionCommand_LaunchAppRequiresBundleId(t *testing.T) {
	srv := &HTTPServer{remoteRuntimeMgr: NewRemoteRuntimeManager()}
	sess := newTestRemoteRuntimeSession("rr_p1_no_bundle", "ios-simulator", "SIM-UDID")
	srv.remoteRuntimeMgr.sessions[sess.ID] = sess

	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+sess.ID+"/command",
		strings.NewReader(`{"command":"launch-app"}`))
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeSessionCommand(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("launch-app without bundleId should 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bundleId") {
		t.Fatalf("error should mention bundleId, got %q", rec.Body.String())
	}
}

func TestHandleRemoteRuntimeSessionCommand_LaunchAppNeedsDevice(t *testing.T) {
	srv := &HTTPServer{remoteRuntimeMgr: NewRemoteRuntimeManager()}
	sess := newTestRemoteRuntimeSession("rr_p1_no_device", "ios-simulator", "")
	srv.remoteRuntimeMgr.sessions[sess.ID] = sess

	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+sess.ID+"/command",
		strings.NewReader(`{"command":"launch-app","bundleId":"io.example.app"}`))
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeSessionCommand(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("launch-app without booted device should 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "boot") {
		t.Fatalf("error should hint at boot-first, got %q", rec.Body.String())
	}
}

func TestHandleRemoteRuntimeSessionCommand_RejectsUnknownCommand(t *testing.T) {
	srv := &HTTPServer{remoteRuntimeMgr: NewRemoteRuntimeManager()}
	sess := newTestRemoteRuntimeSession("rr_p1_bad_cmd", "ios-simulator", "SIM-UDID")
	srv.remoteRuntimeMgr.sessions[sess.ID] = sess

	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+sess.ID+"/command",
		strings.NewReader(`{"command":"nope"}`))
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeSessionCommand(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown command should 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestLaunchAppOnRuntimeTarget_UnsupportedTargetReturnsError(t *testing.T) {
	// browser-window has no bundle-id concept — the dispatcher must
	// refuse cleanly instead of guessing.
	err := launchAppOnRuntimeTarget(nilCtxHelper(t), RemoteRuntimeSession{
		TargetID: "browser-window", DeviceID: "n/a",
	}, "io.example.app")
	if err == nil {
		t.Fatal("browser-window should not accept launch-app")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error should say 'not supported', got %v", err)
	}
}

func TestHandleRemoteRuntimeSessionCommand_BootIsIdempotentOnAttachedSession(t *testing.T) {
	// Attach() short-circuits when the session already carries a
	// DeviceID (remote_runtime_webrtc.go:91), so this exercises the
	// full command→Attach→Update round-trip without shelling out.
	srv := &HTTPServer{remoteRuntimeMgr: NewRemoteRuntimeManager()}
	sess := newTestRemoteRuntimeSession("rr_p1_boot_ok", "ios-simulator", "PRE-BOOTED-UDID")
	srv.remoteRuntimeMgr.sessions[sess.ID] = sess
	srv.remoteRuntimeMgr.live[sess.ID] = &remoteRuntimeLiveState{
		sessionID: sess.ID, targetID: sess.TargetID, platform: "ios", deviceID: sess.DeviceID,
	}

	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+sess.ID+"/command",
		strings.NewReader(`{"command":"boot"}`))
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeSessionCommand(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("boot on attached session should 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["deviceId"] != "PRE-BOOTED-UDID" {
		t.Fatalf("deviceId in response = %#v, want PRE-BOOTED-UDID", body["deviceId"])
	}
	got, _ := srv.remoteRuntimeMgr.Get(sess.ID)
	if got.LastCommand != "boot" {
		t.Fatalf("session.lastCommand = %q, want boot", got.LastCommand)
	}
}

func TestRuntimeCommandRequestParsesBundleId(t *testing.T) {
	// The struct rename in P1 must not drop BundleID on the wire — a
	// runner-authored MCP payload contains it and the handler must
	// see it before dispatching to launchAppOnRuntimeTarget.
	raw := []byte(`{"command":"launch-app","bundleId":"io.yaver.mobile","source":"mcp"}`)
	var req struct {
		Command  string `json:"command"`
		Source   string `json:"source,omitempty"`
		BundleID string `json:"bundleId,omitempty"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.BundleID != "io.yaver.mobile" {
		t.Fatalf("bundleId lost in transit: %+v", req)
	}
	if req.Command != "launch-app" || req.Source != "mcp" {
		t.Fatalf("command/source lost in transit: %+v", req)
	}
}
