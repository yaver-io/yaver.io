package main

// develop_for_test.go — P2 orchestration verb tests.
//
// The runner-auth gate and HTTP calls are behind var seams so the
// tests can drive the full RunDevelopFor loop without shelling out or
// spinning a real agent daemon. Covers:
//   * happy path (returns session id + mechanism + first frame)
//   * runner-auth gate (no authed runner → error, no session created)
//   * missing surface / missing framework guard rails.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func withDevelopForStubs(t *testing.T, gate func(string) error,
	rt func(method, path string, body any) ([]byte, int, error),
	frame func(sessionID string) ([]byte, int, error)) func() {
	t.Helper()
	origGate := developForRunnerAuthGate
	origRT := developForRuntimeCall
	origFrame := developForFrameCall
	developForRunnerAuthGate = gate
	developForRuntimeCall = rt
	developForFrameCall = frame
	return func() {
		developForRunnerAuthGate = origGate
		developForRuntimeCall = origRT
		developForFrameCall = origFrame
	}
}

func TestRunDevelopFor_HappyPathReturnsSessionAndFrame(t *testing.T) {
	// Fake session the /remote-runtime/sessions POST would return.
	fakeSession := RemoteRuntimeSession{
		ID:            "rr_dev_for_happy",
		WorkDir:       "/tmp/talos",
		Framework:     "expo",
		ExecutionMode: ExecutionModeRNHermes,
		TargetID:      "watchos-simulator",
		TargetLabel:   "Apple Watch Simulator over WebRTC",
		Status:        "control-ready",
		DeviceID:      "SIM-WATCH-UDID",
	}
	sessionBody, _ := json.Marshal(fakeSession)

	seen := map[string]bool{}
	rt := func(method, path string, body any) ([]byte, int, error) {
		seen[method+" "+path] = true
		if method == "POST" && path == "/remote-runtime/sessions" {
			return sessionBody, 200, nil
		}
		if method == "POST" && strings.HasSuffix(path, "/command") {
			return []byte(`{"ok":true}`), 200, nil
		}
		return nil, 404, nil
	}
	fram := func(sessionID string) ([]byte, int, error) {
		if sessionID != fakeSession.ID {
			t.Fatalf("frame called with %q, want %q", sessionID, fakeSession.ID)
		}
		return []byte{0xff, 0xd8, 0xff, 0xe0}, 200, nil
	}
	cleanup := withDevelopForStubs(t, func(_ string) error { return nil }, rt, fram)
	defer cleanup()

	res, err := RunDevelopFor(context.Background(), DevelopForRequest{
		Project: "talos", Framework: "expo", Surface: "watch", Platform: "ios",
		BundleID: "io.example.talos", WorkDir: "/tmp/talos",
	})
	if err != nil {
		t.Fatalf("RunDevelopFor errored: %v", err)
	}
	if res.SessionID != fakeSession.ID {
		t.Fatalf("SessionID = %q, want %q", res.SessionID, fakeSession.ID)
	}
	if res.Mechanism != "native-rebuild" {
		t.Fatalf("Mechanism = %q, want native-rebuild", res.Mechanism)
	}
	if res.TargetID != "watchos-simulator" {
		t.Fatalf("TargetID = %q, want watchos-simulator", res.TargetID)
	}
	if res.FirstFrameJPEG == "" {
		t.Fatal("FirstFrameJPEG should be populated when frame call succeeds")
	}
	if !seen["POST /remote-runtime/sessions"] {
		t.Fatalf("expected session create call, saw %v", seen)
	}
	if !seen["POST /remote-runtime/sessions/"+fakeSession.ID+"/command"] {
		t.Fatalf("expected launch-app command call, saw %v", seen)
	}
}

func TestRunDevelopFor_RunnerAuthGateFails(t *testing.T) {
	rt := func(method, path string, body any) ([]byte, int, error) {
		t.Fatalf("HTTP proxy must not fire when runner-auth gate errors (saw %s %s)", method, path)
		return nil, 0, nil
	}
	fram := func(string) ([]byte, int, error) {
		t.Fatal("frame proxy must not fire when gate errors")
		return nil, 0, nil
	}
	cleanup := withDevelopForStubs(t,
		func(_ string) error { return errors.New("no authed runner on this machine — run `yaver runner auth`") },
		rt, fram)
	defer cleanup()

	_, err := RunDevelopFor(context.Background(), DevelopForRequest{
		Project: "talos", Framework: "expo", Surface: "phone",
	})
	if err == nil {
		t.Fatal("gate failure must surface as an error")
	}
	if !strings.Contains(err.Error(), "no authed runner") {
		t.Fatalf("error should propagate the gate message, got %v", err)
	}
}

func TestRunDevelopFor_RequiresSurface(t *testing.T) {
	cleanup := withDevelopForStubs(t, func(_ string) error { return nil },
		func(string, string, any) ([]byte, int, error) { return nil, 0, nil },
		func(string) ([]byte, int, error) { return nil, 0, nil })
	defer cleanup()
	_, err := RunDevelopFor(context.Background(), DevelopForRequest{
		Project: "talos", Framework: "expo",
	})
	if err == nil {
		t.Fatal("missing surface must error before any HTTP call")
	}
	if !strings.Contains(err.Error(), "surface required") {
		t.Fatalf("error should mention surface, got %v", err)
	}
}

func TestRunDevelopFor_AutoFrameworkFallsBackWhenUnknown(t *testing.T) {
	cleanup := withDevelopForStubs(t, func(_ string) error { return nil },
		func(string, string, any) ([]byte, int, error) { return nil, 0, nil },
		func(string) ([]byte, int, error) { return nil, 0, nil })
	defer cleanup()
	_, err := RunDevelopFor(context.Background(), DevelopForRequest{
		Project: "unknown-app-name", Surface: "phone",
	})
	if err == nil {
		t.Fatal("unknown project without framework should error, not silently use a wrong framework")
	}
	if !strings.Contains(err.Error(), "framework required") {
		t.Fatalf("error should mention framework, got %v", err)
	}
}
