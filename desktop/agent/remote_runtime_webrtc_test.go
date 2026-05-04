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

func TestApplyWebRTCOffer_VideoTransceiverPicksRTPH264Path(t *testing.T) {
	// The web viewer signals "I can decode H.264 — give me an RTP
	// track" by adding a recv-only video transceiver to the offer.
	// The agent must spot the m=video line, attach a Pion video
	// track, and stamp the negotiated transport on the session
	// payload as `webrtc-rtp-h264-v1`. framesDC must NOT be created
	// in this branch — old viewers that poll on framesDC are not
	// the audience here.
	mgr, sessionID := newPrimedManager(t, "android-emulator")

	// Override the encoder-capability probe so the test doesn't need
	// adb on PATH. The production check still runs in normal flow.
	prev := agentCanEncodeRTPH264
	agentCanEncodeRTPH264 = func(string) bool { return true }
	t.Cleanup(func() { agentCanEncodeRTPH264 = prev })

	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client PC: %v", err)
	}
	defer clientPC.Close()

	if _, err := clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("add video transceiver: %v", err)
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
	if !strings.Contains(finalOffer.SDP, "m=video") {
		t.Fatal("offer missing m=video — test setup wrong")
	}

	updated, answer, err := mgr.ApplyWebRTCOffer(sessionID, finalOffer)
	if err != nil {
		t.Fatalf("ApplyWebRTCOffer: %v", err)
	}
	if updated.FrameTransport != "webrtc-rtp-h264-v1" {
		t.Errorf("FrameTransport = %q, want webrtc-rtp-h264-v1", updated.FrameTransport)
	}
	if !strings.Contains(answer.SDP, "m=video") {
		t.Errorf("answer should echo m=video: %s", answer.SDP)
	}

	live, _ := mgr.getLive(sessionID)
	live.mu.Lock()
	track := live.videoTrack
	frames := live.framesDC
	live.mu.Unlock()
	if track == nil {
		t.Error("videoTrack must be stored on live state for RTP path")
	}
	if frames != nil {
		t.Error("framesDC must NOT be created on RTP path — that's the JPEG-DC code path")
	}
}

func TestApplyWebRTCOffer_VideoTransceiverFallsBackWhenAgentCannotEncode(t *testing.T) {
	// If the viewer asks for RTP but the host can't encode (no adb,
	// or iOS without the MP4 parser), we silently fall back to
	// JPEG-DC. The viewer learns about it from the negotiated
	// transport string in the answer.
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	prev := agentCanEncodeRTPH264
	agentCanEncodeRTPH264 = func(string) bool { return false }
	t.Cleanup(func() { agentCanEncodeRTPH264 = prev })

	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client PC: %v", err)
	}
	defer clientPC.Close()
	if _, err := clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("add video transceiver: %v", err)
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
	updated, _, err := mgr.ApplyWebRTCOffer(sessionID, *clientPC.LocalDescription())
	if err != nil {
		t.Fatalf("ApplyWebRTCOffer: %v", err)
	}
	if updated.FrameTransport != "webrtc-datachannel-jpeg-v1" {
		t.Errorf("FrameTransport = %q, want fallback webrtc-datachannel-jpeg-v1", updated.FrameTransport)
	}
	live, _ := mgr.getLive(sessionID)
	live.mu.Lock()
	track := live.videoTrack
	frames := live.framesDC
	live.mu.Unlock()
	if track != nil {
		t.Error("videoTrack must NOT be created when agent cannot encode")
	}
	if frames == nil {
		t.Error("framesDC must be created on fallback")
	}
}

func TestApplyWebRTCOffer_FansOutToSecondViewerWithoutTearingDownFirst(t *testing.T) {
	// Phase-9 multi-viewer fan-out. Two browser tabs (or a tab +
	// the mobile dashboard) hit /webrtc/offer for the same session.
	// Pre-fan-out behavior: the second offer called closePeer() and
	// the first viewer was disconnected. After: both are tracked,
	// both get an answer, both share the same Pion video track.
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	prev := agentCanEncodeRTPH264
	agentCanEncodeRTPH264 = func(string) bool { return true }
	t.Cleanup(func() { agentCanEncodeRTPH264 = prev })

	mkOffer := func(t *testing.T) webrtc.SessionDescription {
		t.Helper()
		client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatalf("client PC: %v", err)
		}
		t.Cleanup(func() { client.Close() })
		if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatalf("add transceiver: %v", err)
		}
		offer, err := client.CreateOffer(nil)
		if err != nil {
			t.Fatalf("create offer: %v", err)
		}
		gather := webrtc.GatheringCompletePromise(client)
		if err := client.SetLocalDescription(offer); err != nil {
			t.Fatalf("set local: %v", err)
		}
		<-gather
		return *client.LocalDescription()
	}

	// First viewer attaches.
	if _, _, err := mgr.ApplyWebRTCOffer(sessionID, mkOffer(t)); err != nil {
		t.Fatalf("first ApplyWebRTCOffer: %v", err)
	}
	live, _ := mgr.getLive(sessionID)
	live.mu.Lock()
	firstTrack := live.videoTrack
	firstPeerCount := len(live.peers)
	live.mu.Unlock()
	if firstTrack == nil {
		t.Fatal("first peer should have created the videoTrack")
	}
	if firstPeerCount != 1 {
		t.Errorf("after first offer: peers=%d, want 1", firstPeerCount)
	}

	// Second viewer attaches. Must NOT tear down the first.
	if _, _, err := mgr.ApplyWebRTCOffer(sessionID, mkOffer(t)); err != nil {
		t.Fatalf("second ApplyWebRTCOffer: %v", err)
	}
	live.mu.Lock()
	secondTrack := live.videoTrack
	secondPeerCount := len(live.peers)
	firstPeerStillThere := false
	for _, p := range live.peers {
		if p != nil && p.pc != nil &&
			p.pc.ConnectionState() != webrtc.PeerConnectionStateClosed {
			firstPeerStillThere = true
		}
	}
	live.mu.Unlock()
	if secondTrack != firstTrack {
		t.Errorf("video track was replaced — fan-out should reuse the same TrackLocalStaticSample")
	}
	if secondPeerCount != 2 {
		t.Errorf("after second offer: peers=%d, want 2 (fan-out)", secondPeerCount)
	}
	if !firstPeerStillThere {
		t.Error("first viewer should not have been closed by second offer")
	}
}

func TestApplyWebRTCOffer_JPEGModeStaysSingleViewer(t *testing.T) {
	// JPEG-DC mode predates fan-out and the framesDC payload isn't
	// designed for broadcast. A second JPEG offer for the same
	// session must still close the first peer (legacy behavior),
	// otherwise we'd quietly start two competing JPEG pumps.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")

	mkJpegOffer := func(t *testing.T) webrtc.SessionDescription {
		t.Helper()
		client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		if _, err := client.CreateDataChannel("primer", nil); err != nil {
			t.Fatal(err)
		}
		offer, err := client.CreateOffer(nil)
		if err != nil {
			t.Fatal(err)
		}
		gather := webrtc.GatheringCompletePromise(client)
		if err := client.SetLocalDescription(offer); err != nil {
			t.Fatal(err)
		}
		<-gather
		return *client.LocalDescription()
	}

	if _, _, err := mgr.ApplyWebRTCOffer(sessionID, mkJpegOffer(t)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := mgr.ApplyWebRTCOffer(sessionID, mkJpegOffer(t)); err != nil {
		t.Fatalf("second: %v", err)
	}
	live, _ := mgr.getLive(sessionID)
	live.mu.Lock()
	peerCount := len(live.peers)
	live.mu.Unlock()
	if peerCount != 1 {
		t.Errorf("JPEG-DC mode should stay single-viewer, got %d peers", peerCount)
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

func TestAndroidKeycodeForName_KnownAliases(t *testing.T) {
	// Spot-check the canonical map and its alias spellings. This is
	// the protocol the web viewer relies on — a typo here silently
	// breaks a hardware-button click in the browser, which is
	// frustrating to debug without a regression gate.
	cases := []struct {
		in   string
		want int
	}{
		{"home", 3},
		{"HOME", 3},
		{"back", 4},
		{"menu", 82},
		{"recents", 187},
		{"app_switch", 187},
		{"app-switch", 187},
		{"AppSwitch", 187},
		{"overview", 187},
		{"volume_up", 24},
		{"volumeup", 24},
		{"Volume Up", 24},
		{"volume_down", 25},
		{"mute", 164},
		{"power", 26},
		{"wake", 224},
		{"sleep", 223},
		{"enter", 66},
		{"tab", 61},
		{"esc", 111},
		{"escape", 111},
		{"backspace", 67},
		{"delete", 67},
		{"search", 84},
		{"camera", 27},
		{"play_pause", 85},
		{"playpause", 85},
		{"next", 87},
		{"previous", 88},
	}
	for _, c := range cases {
		got, ok := androidKeycodeForName(c.in)
		if !ok {
			t.Errorf("androidKeycodeForName(%q): expected hit, got miss", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("androidKeycodeForName(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAndroidKeycodeForName_UnknownReturnsFalse(t *testing.T) {
	for _, in := range []string{"", "  ", "nonsense", "   xxxx   "} {
		if _, ok := androidKeycodeForName(in); ok {
			t.Errorf("androidKeycodeForName(%q) should miss", in)
		}
	}
}

func TestExecuteControl_NumericKeycodeStillWorks(t *testing.T) {
	// Escape hatch: even when the friendly name doesn't exist, the
	// numeric KEYCODE_* string ("82" for KEYCODE_MENU, "187" for
	// KEYCODE_APP_SWITCH) must still flow through. Lets a power
	// user drive any keycode without waiting for the alias map to
	// catch up. The test stops at the request validation layer —
	// without an actual device, the adb call can't succeed, so we
	// only verify the "missing action" / "unsupported" gates don't
	// fire.
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{
		Action: "key", Key: "999999", // numeric, but no real adb device
	})
	// We expect an error from the underlying adb exec, not from the
	// validation layer. The error message should NOT be the
	// "unsupported key" string that earlier code emitted before this
	// change.
	if err != nil && strings.Contains(err.Error(), "unsupported key") {
		t.Errorf("numeric keycode should bypass the unknown-name error, got %q", err.Error())
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

func TestExecuteControl_SwipeRejectsNegativeCoords(t *testing.T) {
	// Same defense-in-depth as tap. Negative swipe coords would slip
	// past adb input swipe and produce undefined behavior.
	mgr, sessionID := newPrimedManager(t, "android-emulator")
	cases := []struct{ x1, y1, x2, y2 int }{
		{-1, 100, 200, 200},
		{100, -1, 200, 200},
		{100, 100, -1, 200},
		{100, 100, 200, -1},
	}
	for _, c := range cases {
		_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{
			Action: "swipe", X: c.x1, Y: c.y1, X2: c.x2, Y2: c.y2,
		})
		if err == nil {
			t.Errorf("swipe(%d,%d → %d,%d) should be rejected", c.x1, c.y1, c.x2, c.y2)
		}
	}
}

func TestExecuteControl_SwipeUnsupportedOnIOS(t *testing.T) {
	// iOS Simulator has no built-in swipe primitive in xcrun. Until
	// a WDA / cliclick path lands in a follow-up phase the handler
	// must error so the viewer can either fall back to short taps or
	// surface the limitation to the user.
	mgr, sessionID := newPrimedManager(t, "ios-simulator")
	_, err := mgr.ExecuteControl(sessionID, remoteRuntimeControlRequest{
		Action: "swipe", X: 100, Y: 200, X2: 100, Y2: 600, DurationMs: 300,
	})
	if err == nil {
		t.Fatal("expected swipe-on-iOS to error pending Phase 6+ support")
	}
	if !strings.Contains(err.Error(), "ios-simulator") {
		t.Errorf("error should call out the platform: %v", err)
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
