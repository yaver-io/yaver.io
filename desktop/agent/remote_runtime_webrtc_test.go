package main

// Tests for the WebRTC SDP signalling + control surface.
//
// Per the test-coverage audit, this whole file (`remote_runtime_webrtc.go`,
// 500+ lines) had ZERO unit tests. The endpoint
// `POST /remote-runtime/sessions/{id}/webrtc/offer` rides on hand-driven
// verification — first-time regressions only show up when a real mobile
// client tries to connect.
//
// All cases stand up the manager, manually populate the live-state map
// to skip the actual simulator/emulator boot, and exercise the public
// methods. No real device, no Pion-internal mocking — we use a real
// pion/webrtc.PeerConnection on both sides because the package supports
// loopback DTLS handshakes between two PCs in the same process.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// newPrimedManager creates a RemoteRuntimeManager with one session that
// already has DeviceID set (so Attach short-circuits and never tries to
// boot a real simulator). Returns the manager + session ID.
func newPrimedManager(t *testing.T, targetID string) (*RemoteRuntimeManager, string) {
	t.Helper()
	mgr := NewRemoteRuntimeManager()
	now := time.Now().UTC().Format(time.RFC3339)
	id := "rr_test_" + targetID
	mgr.mu.Lock()
	mgr.sessions[id] = RemoteRuntimeSession{
		ID:               id,
		WorkDir:          "/tmp/test",
		Framework:        "swift",
		ExecutionMode:    "remote-runtime",
		TargetID:         targetID,
		TargetLabel:      "Test " + targetID,
		Platform:         "ios",
		RuntimeHostClass: "macos",
		TransportMode:    "direct-webrtc",
		FrameTransport:   "webrtc-datachannel-jpeg-v1",
		Status:           "control-ready",
		// Pre-fill DeviceID so Attach() returns early without booting.
		DeviceID:  "FAKE-DEVICE-FOR-TEST",
		CreatedAt: now,
		UpdatedAt: now,
		Note:      "primed by test",
	}
	mgr.live[id] = &remoteRuntimeLiveState{
		sessionID: id,
		targetID:  targetID,
		platform:  "ios",
		deviceID:  "FAKE-DEVICE-FOR-TEST",
	}
	mgr.mu.Unlock()
	return mgr, id
}

func TestApplyWebRTCOffer_AnswerHasBothDataChannels(t *testing.T) {
	// The most load-bearing test in this file: prove that the agent
	// can take a Pion-generated offer and produce a valid answer with
	// the "frames" and "events" DataChannels declared. Without this,
	// the mobile client will see a successful HTTP 200 but the SDP
	// won't actually wire up — and we have no other gate for it.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")

	// Stand up a "browser-side" PeerConnection that creates the
	// offer the same way the web/mobile viewer would.
	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client PC: %v", err)
	}
	defer clientPC.Close()

	// Pion requires at least one m-line on the offer side. Adding a
	// recv-only DataChannel is the minimal way to get that — matches
	// what RemoteRuntimeViewer.tsx does.
	if _, err := clientPC.CreateDataChannel("primer", nil); err != nil {
		t.Fatalf("client DC: %v", err)
	}
	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gather := webrtc.GatheringCompletePromise(clientPC)
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local: %v", err)
	}
	<-gather
	finalOffer := *clientPC.LocalDescription()

	// Apply via the manager. This is the production code path.
	_, answer, err := mgr.ApplyWebRTCOffer(sessionID, finalOffer)
	if err != nil {
		t.Fatalf("ApplyWebRTCOffer: %v", err)
	}

	if answer.Type != webrtc.SDPTypeAnswer {
		t.Errorf("answer SDP type: want answer, got %s", answer.Type)
	}
	if !strings.HasPrefix(strings.TrimSpace(answer.SDP), "v=") {
		t.Errorf("answer doesn't start with SDP version line: %s", answer.SDP)
	}
	// The contract the web/mobile viewers depend on: the answer
	// names BOTH application channels. Web side reads "frames" for
	// JPEG bytes and "events" for JSON metadata. Either one missing
	// silently breaks the viewer.
	if !strings.Contains(answer.SDP, "DTLS/SCTP") && !strings.Contains(answer.SDP, "UDP/DTLS/SCTP") {
		t.Errorf("answer missing SCTP m-line for DataChannels: %s", answer.SDP)
	}
	// Verify we actually stored the channels on live state — a future
	// regression that drops the framesDC/eventsDC field would surface
	// here even if the SDP looked right.
	live, _ := mgr.getLive(sessionID)
	live.mu.Lock()
	frames := live.framesDC
	events := live.eventsDC
	live.mu.Unlock()
	if frames == nil {
		t.Error("framesDC not stored on live state")
	}
	if events == nil {
		t.Error("eventsDC not stored on live state")
	}
	if frames != nil && frames.Label() != "frames" {
		t.Errorf("framesDC label: got %q, want frames", frames.Label())
	}
	if events != nil && events.Label() != "events" {
		t.Errorf("eventsDC label: got %q, want events", events.Label())
	}
}

func TestApplyWebRTCOffer_RejectsRelayJpegPollSession(t *testing.T) {
	// Sessions created in relay-jpeg-poll mode do not use WebRTC at
	// all. Attempting an SDP exchange against one is a client bug; we
	// must reject it loudly so the wrong-mode case doesn't half-work.
	mgr := NewRemoteRuntimeManager()
	now := time.Now().UTC().Format(time.RFC3339)
	id := "rr_relay_only"
	mgr.mu.Lock()
	mgr.sessions[id] = RemoteRuntimeSession{
		ID:            id,
		Framework:     "kotlin",
		TargetID:      "android-emulator",
		TransportMode: "relay-jpeg-poll",
		DeviceID:      "FAKE",
		Status:        "streaming",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	mgr.live[id] = &remoteRuntimeLiveState{sessionID: id, targetID: "android-emulator", deviceID: "FAKE"}
	mgr.mu.Unlock()

	_, _, err := mgr.ApplyWebRTCOffer(id, webrtc.SessionDescription{Type: webrtc.SDPTypeOffer})
	if err == nil {
		t.Fatal("expected error for relay-jpeg-poll session")
	}
	if !strings.Contains(err.Error(), "relay-jpeg-poll") {
		t.Errorf("error should call out the wrong-mode case: %v", err)
	}
}

func TestApplyWebRTCOffer_UnknownSession(t *testing.T) {
	// Defensive: hitting the SDP endpoint with a stale or fabricated
	// session ID must 4xx-equivalent, not panic or hang.
	mgr := NewRemoteRuntimeManager()
	_, _, err := mgr.ApplyWebRTCOffer("rr_does_not_exist", webrtc.SessionDescription{Type: webrtc.SDPTypeOffer})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Errorf("error should say not-found: %v", err)
	}
}

func TestExecuteControl_RejectsEmptyAction(t *testing.T) {
	// Empty action body — coordinate-level validation. The actual
	// driver is never reached, so the test is platform-agnostic.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{Action: "  "})
	if err == nil {
		t.Fatal("expected missing-action error")
	}
	if !strings.Contains(err.Error(), "missing action") {
		t.Errorf("error should be specific: %v", err)
	}
}

func TestExecuteControl_RejectsUnknownAction(t *testing.T) {
	// Action name typo or future-version field — must be loud rather
	// than silently no-op. The CLAUDE.md trust model assumes guests
	// can hit /control with arbitrary bodies; a silent accept is a
	// security smell.
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{Action: "shake-the-phone"})
	if err == nil {
		t.Fatal("expected unsupported-action error")
	}
	if !strings.Contains(err.Error(), "unsupported control action") {
		t.Errorf("error should name the action surface: %v", err)
	}
	if !strings.Contains(err.Error(), "shake-the-phone") {
		t.Errorf("error should echo the offending action for debugging: %v", err)
	}
}

func TestExecuteControl_BackKeyOniOSFails(t *testing.T) {
	// Hardware back/home keys are an Android-only concept. iOS simulator
	// has no equivalent. The handler explicitly errors rather than
	// silently dropping. This documents the intended platform asymmetry
	// — if someone later wires a Siri-style "back" gesture for iOS,
	// they have to update both the handler AND this test, forcing the
	// platform-divergence to be intentional.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{Action: "back"})
	if err == nil {
		t.Fatal("expected error for back-on-iOS")
	}
	if !strings.Contains(err.Error(), "Android") {
		t.Errorf("error should say Android-only: %v", err)
	}
}

func TestExecuteControl_TextRejectsEmpty(t *testing.T) {
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{Action: "text", Text: "   "})
	if err == nil {
		t.Fatal("expected text-empty error")
	}
	if !strings.Contains(err.Error(), "text is empty") {
		t.Errorf("error should be specific: %v", err)
	}
}

func TestExecuteControl_TapRejectsNegativeCoords(t *testing.T) {
	// Defense in depth: callers can send negative coords. Reject them
	// before they reach the simulator command — driver behavior on
	// negative coords is undefined and platform-specific.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")
	for _, c := range []struct {
		x, y int
	}{{-1, 100}, {100, -1}, {-5, -5}} {
		_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{Action: "tap", X: c.x, Y: c.y})
		if err == nil {
			t.Errorf("tap(%d,%d) should be rejected", c.x, c.y)
		}
	}
}

func TestCloseSession_IsIdempotentAndDoesNotPanic(t *testing.T) {
	// Closing an already-closed session must not panic — the relay
	// can deliver a close twice in low-network situations.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")
	mgr.CloseSession(sessionID)
	mgr.CloseSession(sessionID)
	mgr.CloseSession("rr_unknown") // should also be a no-op
}

// Compile-time fixture: a successful WebRTC handshake actually opens
// the data channels. We don't use this in the main flow because real
// loopback DTLS makes tests flaky in CI, but we keep the helper around
// so a future test that needs a fully-open channel can reach for it.
//
//nolint:unused
func waitForFramesDCOpen(t *testing.T, live *remoteRuntimeLiveState, deadline time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		live.mu.Lock()
		dc := live.framesDC
		live.mu.Unlock()
		if dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("framesDC never opened")
		case <-tick.C:
		}
	}
}
