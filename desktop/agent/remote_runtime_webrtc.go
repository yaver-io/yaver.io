package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	xdraw "golang.org/x/image/draw"
)

// remoteRuntimePeer is one viewer attached to a session. RTP-mode
// sessions can hold many peers in parallel — they all receive the
// same video track via Pion's track-fan-out and each gets its own
// events DataChannel. JPEG-DC mode stays single-viewer (the framesDC
// payload is too large to broadcast efficiently and there are no
// users left who'd benefit from concurrent JPEG viewers anyway).
type remoteRuntimePeer struct {
	pc       *webrtc.PeerConnection
	framesDC *webrtc.DataChannel
	eventsDC *webrtc.DataChannel
}

type remoteRuntimeLiveState struct {
	mu        sync.Mutex
	sessionID string
	targetID  string
	platform  string
	deviceID  string

	// peers is the active subscriber list. Phase-9 multi-viewer
	// fan-out: every RTP-mode offer appends; JPEG-DC mode replaces.
	// The slice is the source of truth — `pc`, `framesDC`,
	// `eventsDC` below are just convenience pointers to the LATEST
	// peer (preserved so existing callers that grab them under the
	// mutex keep compiling without further refactor).
	peers []*remoteRuntimePeer

	pc       *webrtc.PeerConnection
	framesDC *webrtc.DataChannel
	eventsDC *webrtc.DataChannel

	// videoTrack + videoPump are non-nil when the negotiated transport
	// is direct-webrtc-rtp-h264 (browser viewer with a video
	// transceiver). Old viewers (no m=video in their offer) leave
	// these nil and use framesDC for JPEG polling instead. The track
	// outlives any single peer — multi-viewer fan-out adds peers to
	// it without restarting the capture pipeline.
	videoTrack *webrtc.TrackLocalStaticSample
	videoPump  *videoTrackPump

	streamCancel context.CancelFunc
	lastFrame    []byte
	lastFrameAt  time.Time

	// lease is the P5 single-writer control lease. Nil-check-safe:
	// callers use ensureLease() which lazily inits with the default
	// idle timeout. Enforced by ExecuteControl and manipulated by
	// runtime_take_control / runtime_release_control MCP verbs.
	lease *ControlLease
}

// ensureLease lazily creates the control lease on first use so old
// sessions rehydrated from disk (there are none today; forward-compat)
// don't panic on nil deref.
func (live *remoteRuntimeLiveState) ensureLease() *ControlLease {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.lease == nil {
		live.lease = &ControlLease{idleTimeout: defaultControlLeaseIdle}
	}
	return live.lease
}

type remoteRuntimeControlRequest struct {
	Action string `json:"action"`
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	// Swipe end-point + duration. Used when Action == "swipe".
	X2         int    `json:"x2,omitempty"`
	Y2         int    `json:"y2,omitempty"`
	DurationMs int    `json:"durationMs,omitempty"`
	Text       string `json:"text,omitempty"`
	Key        string `json:"key,omitempty"`
	// ClientID is the stable identifier of the surface making the
	// call — used by the P5 control lease to enforce single-writer
	// role split. Empty = anonymous (legacy web viewer). The lease
	// still accepts anonymous callers when nothing else holds it.
	ClientID    string `json:"clientId,omitempty"`
	ClientLabel string `json:"clientLabel,omitempty"`
}

const remoteRuntimeMaxJPEGDataChannelBytes = 60 * 1024

func (m *RemoteRuntimeManager) Attach(sessionID string) (RemoteRuntimeSession, error) {
	session, ok := m.Get(sessionID)
	if !ok {
		return RemoteRuntimeSession{}, fmt.Errorf("remote runtime session not found")
	}
	live, ok := m.getLive(sessionID)
	if !ok {
		return RemoteRuntimeSession{}, fmt.Errorf("remote runtime state missing")
	}
	if strings.TrimSpace(session.DeviceID) != "" {
		return session, nil
	}

	var (
		deviceID string
		err      error
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if tgt, terr := runtimeTargetFor(session.TargetID); terr != nil {
		err = terr
	} else {
		// Boots an AVD/sim, or resolves an already-attached physical
		// serial — see runtimeTarget impls.
		deviceID, err = tgt.Attach(ctx)
	}
	if err != nil {
		updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
			current.Status = "attach-failed"
			current.Note = fmt.Sprintf("Could not attach to %s: %v", current.TargetLabel, err)
		})
		return updated, err
	}

	live.mu.Lock()
	live.deviceID = deviceID
	live.mu.Unlock()

	// Probe the booted device's screen dims now (before signaling
	// starts) so the session payload carries them, and the events
	// channel can emit the same numbers on first connect. Fallback
	// values inside ProbeDeviceDims keep this from blocking session
	// start on transient adb/xcrun failures.
	dimsCtx, dimsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	dims := ProbeDeviceDims(dimsCtx, session.TargetID, deviceID)
	dimsCancel()

	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		current.DeviceID = deviceID
		current.Status = "control-ready"
		current.DeviceDims = &dims
		current.Note = fmt.Sprintf("Attached to %s (%s). Screen %dx%d %s. WebRTC streaming ready for signaling.",
			current.TargetLabel, deviceID, dims.Width, dims.Height, dims.Rotation)
	})
	return updated, nil
}

func (m *RemoteRuntimeManager) CloseSession(sessionID string) {
	live, ok := m.getLive(sessionID)
	if ok {
		live.closePeer()
	}
	m.Delete(sessionID)
}

// closePeer tears down the entire session: every subscriber peer,
// the video track, the JPEG pump. Called from CloseSession + when
// the underlying WebRTC connection state goes Failed/Closed AND no
// other peers remain attached. Multi-viewer fan-out means a single
// PC failure shouldn't kill the session for the rest of the
// audience — see closeOnePeer for the per-peer teardown.
func (live *remoteRuntimeLiveState) closePeer() {
	live.mu.Lock()
	pump := live.videoPump
	peers := live.peers
	live.peers = nil
	live.mu.Unlock()
	// Stop pump *before* taking the mutex so its goroutine can drain
	// without deadlocking against any callbacks that try to grab the
	// lock during shutdown.
	if pump != nil {
		pump.Stop()
	}
	for _, p := range peers {
		closeRemoteRuntimePeer(p)
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.streamCancel != nil {
		live.streamCancel()
		live.streamCancel = nil
	}
	live.pc = nil
	live.framesDC = nil
	live.eventsDC = nil
	live.videoTrack = nil
	live.videoPump = nil
}

// closeRemoteRuntimePeer tears down a single subscriber. Safe to
// call with a nil peer (no-op).
func closeRemoteRuntimePeer(p *remoteRuntimePeer) {
	if p == nil {
		return
	}
	if p.pc != nil {
		_ = p.pc.Close()
	}
}

// dropPeerLocked removes a single peer from live.peers. Caller must
// hold live.mu. Returns true if the peer was actually present so
// the caller can decide whether to log "session ended" (peers==0)
// vs. "viewer disconnected" (peers>0).
func (live *remoteRuntimeLiveState) dropPeerLocked(p *remoteRuntimePeer) bool {
	for i, q := range live.peers {
		if q == p {
			live.peers = append(live.peers[:i], live.peers[i+1:]...)
			// If we just dropped the peer the legacy single-PC
			// pointers reference, repoint them at the new tail
			// (or clear them when the list emptied).
			if live.pc == p.pc {
				live.pc = nil
				live.framesDC = nil
				live.eventsDC = nil
				if n := len(live.peers); n > 0 {
					tail := live.peers[n-1]
					live.pc = tail.pc
					live.framesDC = tail.framesDC
					live.eventsDC = tail.eventsDC
				}
			}
			return true
		}
	}
	return false
}

func (m *RemoteRuntimeManager) ApplyWebRTCOffer(sessionID string, offer webrtc.SessionDescription) (RemoteRuntimeSession, webrtc.SessionDescription, error) {
	session, err := m.Attach(sessionID)
	if err != nil {
		return RemoteRuntimeSession{}, webrtc.SessionDescription{}, err
	}
	if session.TransportMode == "relay-jpeg-poll" {
		return session, webrtc.SessionDescription{}, fmt.Errorf("session %s uses relay-jpeg-poll, not direct WebRTC", sessionID)
	}
	live, ok := m.getLive(sessionID)
	if !ok {
		return RemoteRuntimeSession{}, webrtc.SessionDescription{}, fmt.Errorf("remote runtime state missing")
	}

	// Auto-detect the desired transport through the streamer facade.
	// Browser/headless viewers always use the same signaling surface;
	// the selected streamer decides whether the underlying capture is
	// RTP H.264, WebRTC JPEG data-channel, or a future backend.
	streamer := selectRemoteRuntimeStreamer(session.TargetID, offer.SDP)

	// Phase-9 fan-out: if there's already an active video track AND
	// the new offer also wants RTP, attach this offer as an
	// additional subscriber instead of replacing the running peer.
	// The existing capture pipeline keeps streaming uninterrupted —
	// Pion fans out RTP packets to every PC the track is attached
	// to. JPEG-DC mode stays single-viewer (the framesDC path is
	// not designed for broadcast and the legacy mobile viewer is
	// the only consumer).
	live.mu.Lock()
	existingTrack := live.videoTrack
	live.mu.Unlock()
	if !streamer.UsesRTP() {
		// JPEG-DC offer arriving — close any existing peers (legacy
		// single-viewer behavior). Same as pre-fan-out code path.
		live.closePeer()
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})
	if err != nil {
		return session, webrtc.SessionDescription{}, err
	}

	negotiatedTransport := "webrtc-datachannel-jpeg-v1"

	var (
		framesDC   *webrtc.DataChannel
		videoTrack *webrtc.TrackLocalStaticSample
	)
	videoTrack, framesDC, err = streamer.ConfigurePeer(pc, live, existingTrack)
	if err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}
	negotiatedTransport = streamer.Transport()

	eventsDC, err := pc.CreateDataChannel("events", nil)
	if err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}

	peer := &remoteRuntimePeer{pc: pc, framesDC: framesDC, eventsDC: eventsDC}
	live.mu.Lock()
	live.peers = append(live.peers, peer)
	live.pc = pc
	live.framesDC = framesDC
	live.eventsDC = eventsDC
	live.videoTrack = videoTrack
	live.mu.Unlock()
	// Reflect the negotiated transport on the session so the JSON
	// response carries it back to the viewer (and Convex sees a
	// transport counter that matches reality).
	m.Update(sessionID, func(current *RemoteRuntimeSession) {
		current.FrameTransport = negotiatedTransport
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		status := "signaling"
		switch state {
		case webrtc.PeerConnectionStateConnecting:
			status = "connecting"
		case webrtc.PeerConnectionStateConnected:
			status = "streaming"
		case webrtc.PeerConnectionStateDisconnected:
			status = "disconnected"
		case webrtc.PeerConnectionStateFailed:
			status = "failed"
		case webrtc.PeerConnectionStateClosed:
			status = "closed"
		}
		updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
			current.Status = status
			current.Note = fmt.Sprintf("WebRTC state: %s", state.String())
		})
		if state == webrtc.PeerConnectionStateConnected {
			// Branch on which transport was negotiated. videoTrack !=
			// nil means the viewer offered an m=video transceiver and
			// the agent attached an H.264 track. The pump is shared
			// across viewers — only the FIRST peer for a given
			// session boots it. Subsequent fan-out peers piggy-back
			// on the existing pump.
			live.mu.Lock()
			track := live.videoTrack
			pumpRunning := live.videoPump != nil
			live.mu.Unlock()
			if track != nil && pumpRunning {
				// Already streaming through the shared RTP pump.
			} else {
				streamer.Start(context.Background(), live, m)
			}
			live.sendEventJSON(map[string]any{
				"type":     "session",
				"session":  updated,
				"platform": updated.Platform,
			})
			// Emit `dims` once on connect so the viewer can size its
			// <video> wrapper (CSS aspect-ratio) and scale pointer
			// coordinates back to device space. The session payload
			// already carries these but newer viewers prefer to
			// listen on the events channel — keeps both paths in
			// sync.
			if updated.DeviceDims != nil {
				live.sendEventJSON(map[string]any{
					"type":     "dims",
					"width":    updated.DeviceDims.Width,
					"height":   updated.DeviceDims.Height,
					"scale":    updated.DeviceDims.Scale,
					"rotation": updated.DeviceDims.Rotation,
					"ts":       time.Now().UTC().Format(time.RFC3339Nano),
				})
			}
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			// Phase-9 fan-out: tearing down ONE peer must not kill
			// the session if other viewers are still attached.
			// Drop just this peer; if the list emptied AND we're in
			// JPEG-DC mode, fully close (the pump has nothing to
			// feed). RTP sessions keep the track + pump alive until
			// CloseSession so a momentary disconnect-and-reconnect
			// from the same viewer doesn't restart the encoder.
			live.mu.Lock()
			live.dropPeerLocked(peer)
			remaining := len(live.peers)
			rtpMode := live.videoTrack != nil
			live.mu.Unlock()
			closeRemoteRuntimePeer(peer)
			if remaining == 0 && !rtpMode {
				live.closePeer()
			}
		}
	})

	// `ready` rides on the events channel because that's the one
	// signal both transports always carry. negotiatedTransport tells
	// the viewer whether to expect a video track (rtp-h264-v1) or
	// JPEG payloads on framesDC (datachannel-jpeg-v1).
	eventsDC.OnOpen(func() {
		live.sendEventJSON(map[string]any{
			"type":      "ready",
			"sessionId": sessionID,
			"transport": negotiatedTransport,
		})
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}
	<-gather

	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		current.Status = "signaling"
		current.Note = "WebRTC answer created. Waiting for peer connection."
	})
	return updated, *pc.LocalDescription(), nil
}

func (live *remoteRuntimeLiveState) startFramePump(mgr *RemoteRuntimeManager) {
	live.mu.Lock()
	if live.streamCancel != nil {
		live.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	live.streamCancel = cancel
	live.mu.Unlock()

	go func() {
		ticker := time.NewTicker(700 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				payload, width, height, err := live.captureJPEGFrame(ctx)
				if err != nil {
					live.sendEventJSON(map[string]any{
						"type":  "frame-error",
						"error": err.Error(),
					})
					continue
				}
				live.mu.Lock()
				dc := live.framesDC
				live.mu.Unlock()
				if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
					continue
				}
				if err := dc.Send(payload); err != nil {
					continue
				}
				live.sendEventJSON(map[string]any{
					"type":   "frame-meta",
					"width":  width,
					"height": height,
					"ts":     time.Now().UTC().Format(time.RFC3339Nano),
				})
			}
		}
	}()
}

// sendEventJSON broadcasts payload to every attached viewer's
// events DataChannel. Sends are best-effort: a closed or stuck
// channel is silently skipped — the per-viewer connection-state
// callback handles its own teardown via dropPeerLocked.
func (live *remoteRuntimeLiveState) sendEventJSON(payload map[string]any) {
	live.mu.Lock()
	channels := make([]*webrtc.DataChannel, 0, len(live.peers))
	for _, p := range live.peers {
		if p != nil && p.eventsDC != nil {
			channels = append(channels, p.eventsDC)
		}
	}
	live.mu.Unlock()

	if len(channels) == 0 {
		return
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	text := string(buf)
	for _, dc := range channels {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			continue
		}
		_ = dc.SendText(text)
	}
}

func (live *remoteRuntimeLiveState) captureJPEGFrame(ctx context.Context) ([]byte, int, int, error) {
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	if strings.TrimSpace(deviceID) == "" {
		return nil, 0, 0, fmt.Errorf("device is not attached yet")
	}
	tmpDir, err := os.MkdirTemp("", "yaver-rr-*")
	if err != nil {
		return nil, 0, 0, err
	}
	defer os.RemoveAll(tmpDir)
	pngPath := filepath.Join(tmpDir, "frame.png")

	tgt, terr := runtimeTargetFor(targetID)
	if terr != nil {
		return nil, 0, 0, fmt.Errorf("unsupported target %q", targetID)
	}
	if err := tgt.Screenshot(ctx, deviceID, pngPath); err != nil {
		return nil, 0, 0, err
	}

	raw, err := os.ReadFile(pngPath)
	if err != nil {
		return nil, 0, 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, err
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	resized := img
	if width > 720 {
		dstW := 720
		dstH := int(float64(height) * (float64(dstW) / float64(width)))
		dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)
		resized = dst
		width = dstW
		height = dstH
	}

	payload, err := encodeRemoteRuntimeJPEG(resized)
	if err != nil {
		return nil, 0, 0, err
	}
	live.mu.Lock()
	live.lastFrame = append(live.lastFrame[:0], payload...)
	live.lastFrameAt = time.Now().UTC()
	live.mu.Unlock()
	return payload, width, height, nil
}

func encodeRemoteRuntimeJPEG(img image.Image) ([]byte, error) {
	quality := 55
	for {
		var out bytes.Buffer
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if out.Len() <= remoteRuntimeMaxJPEGDataChannelBytes || quality <= 35 {
			return out.Bytes(), nil
		}
		quality -= 10
	}
}

func (m *RemoteRuntimeManager) CaptureFrame(sessionID string) (RemoteRuntimeSession, []byte, error) {
	session, err := m.Attach(sessionID)
	if err != nil {
		return RemoteRuntimeSession{}, nil, err
	}
	live, ok := m.getLive(sessionID)
	if !ok {
		return RemoteRuntimeSession{}, nil, fmt.Errorf("remote runtime state missing")
	}
	live.mu.Lock()
	if len(live.lastFrame) > 0 && time.Since(live.lastFrameAt) < 350*time.Millisecond {
		cached := append([]byte(nil), live.lastFrame...)
		live.mu.Unlock()
		return session, cached, nil
	}
	live.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	payload, _, _, err := live.captureJPEGFrame(ctx)
	if err != nil {
		return session, nil, err
	}
	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		if current.TransportMode == "relay-jpeg-poll" {
			current.Status = "streaming"
			current.Note = "Relay frame polling active."
		}
	})
	return updated, payload, nil
}

func (m *RemoteRuntimeManager) ExecuteControl(sessionID string, req remoteRuntimeControlRequest) (RemoteRuntimeSession, error) {
	session, err := m.Attach(sessionID)
	if err != nil {
		return RemoteRuntimeSession{}, err
	}
	live, ok := m.getLive(sessionID)
	if !ok {
		return RemoteRuntimeSession{}, fmt.Errorf("remote runtime state missing")
	}
	// P5 single-writer control lease: reject strangers when the
	// session is held. The gate keeps the lease alive as a
	// last-activity marker; take/release is via TakeControl below.
	if err := live.ensureLease().CheckAndRefresh(req.ClientID, time.Now()); err != nil {
		return session, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		return session, fmt.Errorf("missing action")
	}
	switch action {
	case "tap":
		err = live.tap(ctx, req.X, req.Y)
	case "swipe":
		err = live.swipe(ctx, req.X, req.Y, req.X2, req.Y2, req.DurationMs)
	case "text":
		err = live.text(ctx, req.Text)
	case "back":
		err = live.key(ctx, "back")
	case "home":
		err = live.key(ctx, "home")
	case "key":
		// Generic hardware-key path. The viewer can send a friendly
		// name ("recents", "volume_up", "menu") or a raw KEYCODE_*
		// integer as a string ("187"). androidKeycodeForName
		// resolves the name; the numeric escape hatch lives inside
		// live.key().
		if strings.TrimSpace(req.Key) == "" {
			err = fmt.Errorf("missing key")
		} else {
			err = live.key(ctx, req.Key)
		}
	default:
		err = fmt.Errorf("unsupported control action %q", req.Action)
	}
	if err != nil {
		updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
			current.Status = "control-error"
			current.LastCommand = action
			current.Note = err.Error()
		})
		return updated, err
	}
	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		current.Status = "streaming"
		current.LastCommand = action
		switch action {
		case "tap":
			current.Note = fmt.Sprintf("Tapped %d,%d on %s", req.X, req.Y, current.TargetLabel)
		case "swipe":
			current.Note = fmt.Sprintf("Swiped %d,%d → %d,%d on %s", req.X, req.Y, req.X2, req.Y2, current.TargetLabel)
		case "text":
			current.Note = fmt.Sprintf("Sent text to %s", current.TargetLabel)
		default:
			current.Note = fmt.Sprintf("Sent %s to %s", action, current.TargetLabel)
		}
	})
	return updated, nil
}

func (live *remoteRuntimeLiveState) tap(ctx context.Context, x, y int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("tap coordinates must be non-negative")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		return fmt.Errorf("unsupported target %q", targetID)
	}
	return tgt.Tap(ctx, deviceID, x, y)
}

// swipe drags from (x1,y1) to (x2,y2) over durationMs. Used by the
// web viewer's pointer-drag handler. iOS Simulator has no built-in
// swipe primitive in xcrun, so iOS sessions return a clear "not
// implemented" error rather than silently no-oping — the viewer can
// then either fall back to a series of small taps or surface the
// limitation to the user.
func (live *remoteRuntimeLiveState) swipe(ctx context.Context, x1, y1, x2, y2, durationMs int) error {
	if x1 < 0 || y1 < 0 || x2 < 0 || y2 < 0 {
		return fmt.Errorf("swipe coordinates must be non-negative")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		return fmt.Errorf("unsupported target %q", targetID)
	}
	return tgt.Swipe(ctx, deviceID, x1, y1, x2, y2, durationMs)
}

func (live *remoteRuntimeLiveState) text(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("text is empty")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		return fmt.Errorf("unsupported target %q", targetID)
	}
	return tgt.Text(ctx, deviceID, text)
}

func (live *remoteRuntimeLiveState) key(ctx context.Context, key string) error {
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		// Unknown/iOS targets keep the old "Android only" message —
		// the iOS impl returns the same string.
		return fmt.Errorf("%s is only supported for Android sessions right now", key)
	}
	return tgt.Key(ctx, deviceID, key)
}

// androidKeycodeForName maps the friendly key names the web viewer
// sends ("home", "back", "recents", "menu", "volume_up", …) to
// Android's KEYCODE_* integer constants. Constants from
// https://developer.android.com/reference/android/view/KeyEvent —
// stable across every API level we care about.
//
// Names are normalized to lowercase + underscores so the protocol
// is forgiving of "VolumeUp", "volume-up", "Volume_Up", etc.
func androidKeycodeForName(name string) (int, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "home":
		return 3, true
	case "back":
		return 4, true
	case "menu":
		return 82, true
	case "recents", "app_switch", "appswitch", "overview":
		return 187, true
	case "volume_up", "volumeup":
		return 24, true
	case "volume_down", "volumedown":
		return 25, true
	case "volume_mute", "mute":
		return 164, true
	case "power":
		return 26, true
	case "wake", "wakeup":
		return 224, true
	case "sleep":
		return 223, true
	case "enter":
		return 66, true
	case "tab":
		return 61, true
	case "escape", "esc":
		return 111, true
	case "delete", "backspace":
		return 67, true
	case "search":
		return 84, true
	case "camera":
		return 27, true
	case "media_play_pause", "playpause", "play_pause":
		return 85, true
	case "media_next", "next":
		return 87, true
	case "media_previous", "previous", "prev":
		return 88, true
	}
	return 0, false
}

func (s *HTTPServer) handleRemoteRuntimeSessionRoute(w http.ResponseWriter, r *http.Request) {
	mgr := s.ensureRemoteRuntimeManager()
	path := strings.TrimPrefix(r.URL.Path, "/remote-runtime/sessions/")
	path = strings.Trim(path, "/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "missing session id")
		return
	}

	// Phase-5 closer: if the session is dispatched to a paired
	// builder, every per-session HTTP call is forwarded verbatim.
	// Path suffixes (`/command`, `/frame`, `/webrtc/offer`,
	// `/control`, "" for GET/DELETE) flow through unchanged so the
	// builder's handler shape and ours stay in lockstep without
	// special-casing each.
	sessionID, suffix := splitSessionRoutePath(path)
	if sessionID != "" {
		if proxy := mgr.proxiedFor(sessionID); proxy != nil {
			if r.Method == http.MethodDelete {
				// Tear down the local mapping after forwarding so a
				// subsequent GET returns 404 here just like it does
				// after a normal Delete.
				defer mgr.Delete(sessionID)
			}
			forwardSessionRequest(w, r, proxy, suffix)
			return
		}
	}
	switch {
	case strings.HasSuffix(path, "/command"):
		s.handleRemoteRuntimeSessionCommand(w, r)
		return
	case strings.HasSuffix(path, "/frame"):
		sessionID := strings.TrimSuffix(path, "/frame")
		sessionID = strings.Trim(sessionID, "/")
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		session, payload, err := mgr.CaptureFrame(sessionID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("X-Yaver-Remote-Session", session.ID)
		w.Header().Set("X-Yaver-Remote-Transport", session.FrameTransport)
		_, _ = w.Write(payload)
		return
	case strings.HasSuffix(path, "/webrtc/offer"):
		sessionID := strings.TrimSuffix(path, "/webrtc/offer")
		sessionID = strings.Trim(sessionID, "/")
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var req struct {
			SDP  string `json:"sdp"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		answerSession, answer, err := mgr.ApplyWebRTCOffer(sessionID, webrtc.SessionDescription{
			Type: webrtc.NewSDPType(req.Type),
			SDP:  req.SDP,
		})
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{
			"session": answerSession,
			"answer": map[string]any{
				"type": answer.Type.String(),
				"sdp":  answer.SDP,
			},
			// Mirror the field we just stamped on the session so the
			// viewer can read it from either place. Old viewers that
			// only inspect this top-level "transport" string still
			// see a sensible value.
			"transport": answerSession.FrameTransport,
			"note":      "Current WebRTC phase uses direct/Tailscale-reachable candidates from the host machine. TURN is not wired yet.",
		})
		return
	case strings.HasSuffix(path, "/control"):
		sessionID := strings.TrimSuffix(path, "/control")
		sessionID = strings.Trim(sessionID, "/")
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var req remoteRuntimeControlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		updated, err := mgr.ExecuteControl(sessionID, req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{
			"ok":      true,
			"session": updated,
		})
		return
	default:
		sessionID := strings.Trim(path, "/")
		switch r.Method {
		case http.MethodGet:
			session, ok := mgr.Get(sessionID)
			if !ok {
				jsonError(w, http.StatusNotFound, "remote runtime session not found")
				return
			}
			jsonReply(w, http.StatusOK, session)
		case http.MethodDelete:
			mgr.CloseSession(sessionID)
			jsonReply(w, http.StatusOK, map[string]any{"ok": true, "sessionId": sessionID})
		default:
			jsonError(w, http.StatusMethodNotAllowed, "use GET or DELETE")
		}
	}
}

func remoteRuntimeViewerBootstrapJSON(baseURL string, headers map[string]string, session RemoteRuntimeSession) string {
	payload := map[string]any{
		"baseUrl":   strings.TrimRight(baseURL, "/"),
		"headers":   headers,
		"session":   session,
		"transport": "webrtc-datachannel-jpeg-v1",
	}
	buf, _ := json.Marshal(payload)
	return base64.StdEncoding.EncodeToString(buf)
}
