package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	// releaseDevice returns this session's exclusively-claimed simulator/emulator
	// to the pool on close. Nil for targets that hold no device (browser, screen,
	// physical hardware someone else plugged in).
	releaseDevice func()

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
	eventBacklog []map[string]any
	controlAcks  map[string]vibingWebRTCControlAckCacheEntry
	controlOrder []string
	// controlMu permits one DOM operation per session. A broken page cannot
	// accumulate an unbounded queue of 10-second Evaluate/click operations.
	controlMu sync.Mutex

	// lease is the P5 single-writer control lease. Nil-check-safe:
	// callers use ensureLease() which lazily inits with the default
	// idle timeout. Enforced by ExecuteControl and manipulated by
	// runtime_take_control / runtime_release_control MCP verbs.
	lease *ControlLease

	// viewers is the Phase-A shared-session roster: who is watching /
	// participating, keyed by clientId. Populated on offer attach
	// (webrtc) and /frame polling (frame-poll). Presence changes
	// broadcast viewer_joined / viewer_left on the events channel.
	// See remote_runtime_viewers.go.
	viewers map[string]*remoteRuntimeViewer
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
	X2         int `json:"x2,omitempty"`
	Y2         int `json:"y2,omitempty"`
	DurationMs int `json:"durationMs,omitempty"`
	// Scale drives Action == "pinch": >1 zooms in (fingers apart), <1 zooms
	// out. Centred on X,Y. Separate from the swipe end-point because a pinch
	// is two pointers moving symmetrically, not one pointer travelling.
	Scale float64 `json:"scale,omitempty"`

	// URL drives Action == "navigate".
	URL  string `json:"url,omitempty"`
	Text string `json:"text,omitempty"`
	Key  string `json:"key,omitempty"`
	// ClientID is the stable identifier of the surface making the
	// call — used by the P5 control lease to enforce single-writer
	// role split. Empty = anonymous (legacy web viewer). The lease
	// still accepts anonymous callers when nothing else holds it.
	ClientID    string `json:"clientId,omitempty"`
	ClientLabel string `json:"clientLabel,omitempty"`
}

const remoteRuntimeMaxJPEGDataChannelBytes = 60 * 1024
const remoteRuntimeJPEGDataChannelChunkBytes = 12 * 1024
const remoteRuntimeFrameCaptureBudget = 20 * time.Second
const remoteRuntimeEventBacklogMax = 50

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
		if session.TargetID == "browser-window" {
			return m.ensureBrowserWindowNavigated(session, live), nil
		}
		return session, nil
	}

	var (
		deviceID string
		err      error
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var releaseDevice func()
	if session.TargetID == remoteRuntimeAndroidDeviceTargetID && strings.TrimSpace(session.RequestedRealDeviceID) != "" {
		deviceID, err = resolveRegisteredRealDevice(ctx, session.RequestedRealDeviceID)
	} else if tgt, terr := runtimeTargetFor(session.TargetID); terr != nil {
		err = terr
	} else if ex, ok := tgt.(exclusiveAttacher); ok {
		// Simulators and emulators are EXCLUSIVE: one session per device, or two
		// people on one machine end up driving the same screen (the second
		// install replaces the first's app and taps cross over). Claim it against
		// the vibe session so the roster can show who has what.
		deviceID, releaseDevice, err = ex.AttachExclusive(ctx, VibeOwnerForWorkDir(session.WorkDir))
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
	live.releaseDevice = releaseDevice
	live.mu.Unlock()

	if remoteRuntimeNeedsAttachFrameProbe(session.TargetID) {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, _, _, probeErr := live.captureJPEGFrame(probeCtx)
		probeCancel()
		if probeErr != nil {
			live.mu.Lock()
			live.deviceID = ""
			live.releaseDevice = nil
			live.mu.Unlock()
			if releaseDevice != nil {
				releaseDevice()
			}
			updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
				current.Status = "attach-failed"
				current.Note = fmt.Sprintf("Could not capture an initial frame from %s (%s): %v", current.TargetLabel, deviceID, probeErr)
			})
			return updated, fmt.Errorf("initial frame probe failed for %s (%s): %w", session.TargetID, deviceID, probeErr)
		}
	}

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
	if updated.TargetID == "browser-window" {
		updated = m.ensureBrowserWindowNavigated(updated, live)
	}
	return updated, nil
}

func remoteRuntimeNeedsAttachFrameProbe(targetID string) bool {
	return strings.HasPrefix(targetID, "android-") && preferredCaptureMethod(targetID) == CaptureJPEGScreenshot
}

// exclusiveAttacher is implemented by targets backed by a device that only ONE
// session may hold (iOS/Apple simulators, Android emulators). Optional on
// purpose: a browser window or a desktop screen has nothing to arbitrate.
type exclusiveAttacher interface {
	AttachExclusive(ctx context.Context, owner string) (deviceID string, release func(), err error)
}

func (m *RemoteRuntimeManager) CloseSession(sessionID string) {
	live, ok := m.getLive(sessionID)
	if ok {
		// Give the device back BEFORE tearing down the peer: a leaked claim makes
		// the machine look full to the next session for no reason.
		live.mu.Lock()
		release := live.releaseDevice
		live.releaseDevice = nil
		live.mu.Unlock()
		if release != nil {
			release()
		}
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
	if reason := remoteRuntimeFrameBlockReason(session); reason != "" {
		return session, webrtc.SessionDescription{}, errors.New(reason)
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

	m.mu.RLock()
	iceProvider := m.iceServerProvider
	m.mu.RUnlock()
	iceServers := iceServersForPeer()
	if iceProvider != nil {
		iceCtx, iceCancel := context.WithTimeout(context.Background(), 5*time.Second)
		iceServers = iceProvider(iceCtx)
		iceCancel()
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
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
	eventsDC.OnMessage(func(msg webrtc.DataChannelMessage) {
		m.handleVibingWebRTCControlMessage(sessionID, live, eventsDC, msg.Data)
	})
	if framesDC != nil {
		framesDC.OnOpen(func() {
			streamer.Start(context.Background(), live, m)
		})
		streamer.Start(context.Background(), live, m)
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
			// Phase-A: unregister this peer's viewer so the roster
			// drops them and other viewers see viewer_left.
			if vid := live.viewerIDForPeerLocked(peer); vid != "" {
				live.unregisterViewerLocked(vid)
			}
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
		sendVibingWebRTCHello(eventsDC, sessionID)
		live.flushEventBacklog()
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
		live.mu.Lock()
		targetID := live.targetID
		live.mu.Unlock()
		ticker := time.NewTicker(remoteRuntimeJPEGFrameInterval(targetID))
		defer ticker.Stop()
		type captureResult struct {
			payload       []byte
			width, height int
			err           error
		}
		var captureInFlight atomic.Bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !captureInFlight.CompareAndSwap(false, true) {
					continue
				}
				frameCtx, frameCancel := context.WithTimeout(ctx, remoteRuntimeFrameCaptureBudget)
				resultCh := make(chan captureResult, 1)
				go func() {
					payload, width, height, err := live.captureJPEGFrame(frameCtx)
					resultCh <- captureResult{payload: payload, width: width, height: height, err: err}
				}()
				go func() {
					defer frameCancel()
					select {
					case <-ctx.Done():
						captureInFlight.Store(false)
						return
					case result := <-resultCh:
						captureInFlight.Store(false)
						if result.err != nil {
							live.sendEventJSON(map[string]any{
								"type":  "frame-error",
								"error": result.err.Error(),
							})
							return
						}
						live.mu.Lock()
						dc := live.framesDC
						live.mu.Unlock()
						if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
							return
						}
						chunked, err := sendJPEGDataChannelFrame(dc, result.payload)
						if err != nil {
							live.sendEventJSON(map[string]any{
								"type":  "frame-error",
								"error": fmt.Sprintf("send frame over WebRTC data channel: %v", err),
							})
							return
						}
						live.sendEventJSON(map[string]any{
							"type":    "frame-meta",
							"width":   result.width,
							"height":  result.height,
							"bytes":   len(result.payload),
							"chunked": chunked,
							"ts":      time.Now().UTC().Format(time.RFC3339Nano),
						})
					case <-time.After(remoteRuntimeFrameCaptureBudget + time.Second):
						live.sendEventJSON(map[string]any{
							"type":  "frame-error",
							"error": fmt.Sprintf("frame capture exceeded %.0fs in WebRTC JPEG data-channel pump", remoteRuntimeFrameCaptureBudget.Seconds()),
						})
						return
					}
				}()
			}
		}
	}()
}

// remoteRuntimeJPEGFrameInterval keeps the lightweight browser lane feeling
// interactive without increasing the cost of slow device screenshot paths.
// Chrome's in-process CDP capture normally completes in tens of milliseconds;
// Android/WDA screenshots shell across a device boundary and remain at the
// conservative cadence. captureInFlight above is the hard backpressure guard:
// a slow host skips ticks rather than piling up goroutines or frames.
func remoteRuntimeJPEGFrameInterval(targetID string) time.Duration {
	if targetID == "browser-window" {
		return 125 * time.Millisecond
	}
	return 700 * time.Millisecond
}

func sendJPEGDataChannelFrame(dc *webrtc.DataChannel, payload []byte) (bool, error) {
	if len(payload) <= remoteRuntimeJPEGDataChannelChunkBytes {
		return false, dc.Send(payload)
	}
	frameID := time.Now().UTC().Format("20060102T150405.000000000")
	total := (len(payload) + remoteRuntimeJPEGDataChannelChunkBytes - 1) / remoteRuntimeJPEGDataChannelChunkBytes
	for i := 0; i < total; i++ {
		start := i * remoteRuntimeJPEGDataChannelChunkBytes
		end := start + remoteRuntimeJPEGDataChannelChunkBytes
		if end > len(payload) {
			end = len(payload)
		}
		msg := map[string]any{
			"type":  "jpeg-chunk",
			"id":    frameID,
			"index": i,
			"total": total,
			"data":  base64.StdEncoding.EncodeToString(payload[start:end]),
		}
		buf, err := json.Marshal(msg)
		if err != nil {
			return true, err
		}
		if err := dc.SendText(string(buf)); err != nil {
			return true, err
		}
	}
	return true, nil
}

// sendEventJSON broadcasts payload to every attached viewer's
// events DataChannel. Sends are best-effort: a closed or stuck
// channel is silently skipped — the per-viewer connection-state
// callback handles its own teardown via dropPeerLocked.
func (live *remoteRuntimeLiveState) sendEventJSON(payload map[string]any) {
	live.mu.Lock()
	channels := make([]*webrtc.DataChannel, 0, len(live.peers))
	for _, p := range live.peers {
		if p != nil && p.eventsDC != nil && p.eventsDC.ReadyState() == webrtc.DataChannelStateOpen {
			channels = append(channels, p.eventsDC)
		}
	}
	if len(channels) == 0 {
		live.queueEventLocked(payload)
		live.mu.Unlock()
		return
	}
	live.mu.Unlock()

	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	text := string(buf)
	for _, dc := range channels {
		_ = dc.SendText(text)
	}
}

// sendEventJSONLocked is sendEventJSON's locked-core variant for callers
// that ALREADY hold live.mu (the viewer-registry broadcasts in
// remote_runtime_viewers.go). It must not call sendEventJSON — that would
// re-lock live.mu and deadlock. Presence events are rare and tiny, so
// holding the lock across SendText here is acceptable; the hot paths
// (frame pumps) keep using sendEventJSON which unlocks before SendText.
func (live *remoteRuntimeLiveState) sendEventJSONLocked(payload map[string]any) {
	channels := make([]*webrtc.DataChannel, 0, len(live.peers))
	for _, p := range live.peers {
		if p != nil && p.eventsDC != nil && p.eventsDC.ReadyState() == webrtc.DataChannelStateOpen {
			channels = append(channels, p.eventsDC)
		}
	}
	if len(channels) == 0 {
		live.queueEventLocked(payload)
		return
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	text := string(buf)
	for _, dc := range channels {
		_ = dc.SendText(text)
	}
}

func (live *remoteRuntimeLiveState) queueEventLocked(payload map[string]any) {
	live.eventBacklog = append(live.eventBacklog, payload)
	if overflow := len(live.eventBacklog) - remoteRuntimeEventBacklogMax; overflow > 0 {
		copy(live.eventBacklog, live.eventBacklog[overflow:])
		live.eventBacklog = live.eventBacklog[:remoteRuntimeEventBacklogMax]
	}
}

func (live *remoteRuntimeLiveState) flushEventBacklog() {
	live.mu.Lock()
	channels := make([]*webrtc.DataChannel, 0, len(live.peers))
	for _, p := range live.peers {
		if p != nil && p.eventsDC != nil && p.eventsDC.ReadyState() == webrtc.DataChannelStateOpen {
			channels = append(channels, p.eventsDC)
		}
	}
	if len(channels) == 0 || len(live.eventBacklog) == 0 {
		live.mu.Unlock()
		return
	}
	backlog := append([]map[string]any(nil), live.eventBacklog...)
	live.eventBacklog = nil
	live.mu.Unlock()

	for _, payload := range backlog {
		buf, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		text := string(buf)
		for _, dc := range channels {
			_ = dc.SendText(text)
		}
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
	// A JPEG is not proof that the requested app rendered. Headless Chrome can
	// screenshot about:blank forever, and this path used to overwrite the
	// manager's truthful waiting-for-dev-server state with "streaming" after the
	// first white JPEG. Every polling surface then showed a false green. Preserve
	// the named session failure and make the frame operation fail until the
	// browser is actually navigated.
	if reason := remoteRuntimeFrameBlockReason(session); reason != "" {
		return session, nil, errors.New(reason)
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
	// ── Our own deadline must never masquerade as a tool crash ──────────────
	//
	// This budget used to be 8s and the error surfaced verbatim as
	//
	//   simctl screenshot: signal: killed
	//
	// which reads as "simctl died" — so the reader goes looking at Xcode, at
	// CoreSimulator, at the device. It was OUR context killing it. Measured
	// 2026-07-25 on the 8 GB Mac mini: four consecutive frame pulls SIGKILLed
	// while the same box was building the guest app.
	//
	// The product already knew this could happen. ops_webrtc_doctor.go warns
	// that "simctl screenshot took %.1fs — CoreSimulator is DEGRADED (healthy is
	// <1s)". A 1s-healthy operation on a loaded box does not fit an 8s budget,
	// and when it does not fit we said the wrong thing about why.
	//
	// So: a budget with headroom for a busy machine, and an error that names the
	// deadline, the elapsed time, and where to look. `signal: killed` tells the
	// user nothing they can act on; "we gave up after 20s while the box was busy"
	// points at load, and at the doctor probe that measures it.
	captureStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), remoteRuntimeFrameCaptureBudget)
	defer cancel()
	payload, _, _, err := live.captureJPEGFrame(ctx)
	if err != nil {
		elapsed := time.Since(captureStart)
		if ctx.Err() == context.DeadlineExceeded || strings.Contains(err.Error(), "signal: killed") {
			return session, nil, fmt.Errorf(
				"screen capture gave up after %.0fs (budget %.0fs) — the box is too busy to screenshot, not broken. "+
					"A healthy simctl screenshot is <1s; run the webrtc doctor to measure CoreSimulator health, "+
					"or wait for the current build to finish: %w",
				elapsed.Seconds(), remoteRuntimeFrameCaptureBudget.Seconds(), err)
		}
		return session, nil, err
	}
	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		if current.TransportMode == "relay-jpeg-poll" && remoteRuntimeFrameBlockReason(*current) == "" {
			current.Status = "streaming"
			current.Note = "Relay frame polling active."
		}
	})
	return updated, payload, nil
}

func remoteRuntimeFrameBlockReason(session RemoteRuntimeSession) string {
	if session.TargetID != "browser-window" {
		return ""
	}
	switch strings.TrimSpace(session.Status) {
	case "waiting-for-dev-server", "attach-failed", "navigate-failed", "failed":
		if note := strings.TrimSpace(session.Note); note != "" {
			return note
		}
		return fmt.Sprintf("browser runtime cannot stream while session status is %s", session.Status)
	default:
		return ""
	}
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
	textInputFocused := false
	if action == "" {
		return session, fmt.Errorf("missing action")
	}
	switch action {
	case "tap":
		err = live.tap(ctx, req.X, req.Y)
		if err == nil {
			live.mu.Lock()
			targetID, deviceID := live.targetID, live.deviceID
			live.mu.Unlock()
			if targetID == "browser-window" {
				// The click has completed; ask the browser which element owns
				// focus. Failure leaves the optional UX hint false without
				// turning a successful tap into a failed control operation.
				textInputFocused, _ = browserWindowTextInputFocused(ctx, deviceID)
			}
		}
	case "swipe":
		err = live.swipe(ctx, req.X, req.Y, req.X2, req.Y2, req.DurationMs)
	case "pinch", "zoom":
		// "zoom" is accepted as an alias because that is what the gesture is
		// called in every UI that will send it; the mechanism is a pinch.
		err = live.pinch(ctx, req.X, req.Y, req.Scale, req.DurationMs)
	case "navigate":
		err = live.navigate(ctx, req.URL)
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
		current.TextInputFocused = textInputFocused
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

func (live *remoteRuntimeLiveState) pinch(ctx context.Context, x, y int, scale float64, durationMs int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("pinch centre must be non-negative")
	}
	if scale <= 0 {
		return fmt.Errorf("pinch scale must be > 0 (>1 zooms in, <1 zooms out)")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		return fmt.Errorf("unsupported target %q", targetID)
	}
	return tgt.Pinch(ctx, deviceID, x, y, scale, durationMs)
}

// navigate points the session's target at a URL. Scheme validation lives in
// validateNavigateURL, on the target side, so every entry point gets it.
func (live *remoteRuntimeLiveState) navigate(ctx context.Context, url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("navigate requires a url")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	tgt, err := runtimeTargetFor(targetID)
	if err != nil {
		return fmt.Errorf("unsupported target %q", targetID)
	}
	return tgt.Navigate(ctx, deviceID, url)
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
	// P6: directional / D-pad — Android TV, Wear, Auto navigation.
	case "up", "dpad_up":
		return 19, true
	case "down", "dpad_down":
		return 20, true
	case "left", "dpad_left":
		return 21, true
	case "right", "dpad_right":
		return 22, true
	case "select", "dpad_center", "ok":
		return 23, true
	// Wear OS Digital Crown scroll — SCROLL wheel synthesised via
	// PAGE_UP/DOWN. Not perfect (real crown emits precise deltas)
	// but close enough for scrolling the list in every stock Wear app.
	case "crown_up", "page_up":
		return 92, true
	case "crown_down", "page_down":
		return 93, true
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
	case strings.HasSuffix(path, "/leave"):
		sessionID := strings.TrimSuffix(path, "/leave")
		sessionID = strings.Trim(sessionID, "/")
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var req struct {
			ClientID string `json:"clientId,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if strings.TrimSpace(req.ClientID) == "" {
			jsonError(w, http.StatusBadRequest, "clientId is required to leave a shared session")
			return
		}
		updated, closed, err := mgr.leaveViewer(sessionID, req.ClientID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{
			"ok":        true,
			"closed":    closed,
			"session":   updated,
			"sessionId": sessionID,
		})
		return
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
		// Phase-A: a relay-jpeg-poll client pulling frames IS a viewer —
		// register it so the roster shows who is watching and viewerCount
		// reflects them. ?clientId= is the stable identity; without it the
		// pull still works but is anonymous (counted, not addressable).
		if cid := strings.TrimSpace(r.URL.Query().Get("clientId")); cid != "" {
			if live, ok := mgr.getLive(sessionID); ok {
				live.mu.Lock()
				live.registerViewerLocked(remoteRuntimeViewer{
					ID:      cid,
					Surface: string(surfaceFromRequest(r)),
					Kind:    "frame-poll",
				})
				live.mu.Unlock()
			}
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
			// ClientID identifies the attaching viewer in the shared-session
			// roster (e.g. "tvos-<uuid>"). Empty = legacy anonymous viewer.
			ClientID string `json:"clientId,omitempty"`
			// Surface names the X-Yaver-Surface of the attaching viewer.
			Surface string `json:"surface,omitempty"`
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
		// Phase-A: attribute + roster the attaching viewer, then stamp the
		// refreshed viewerCount on the response session so the caller (and
		// Convex) sees the real audience. The offer already attached the
		// peer; registering here ties that peer to a clientId for `leave`.
		if req.ClientID != "" {
			if live, ok := mgr.getLive(sessionID); ok {
				live.mu.Lock()
				live.registerViewerLocked(remoteRuntimeViewer{
					ID:      req.ClientID,
					Surface: string(normalizeSurface(req.Surface)),
					Kind:    "webrtc",
					peer:    live.latestPeerLocked(),
				})
				live.mu.Unlock()
			}
			answerSession, _ = mgr.Get(sessionID)
			answerSession = mgr.stampViewerCount(answerSession)
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
			"note":      "WebRTC uses the host's measured ICE configuration, including short-lived TURN credentials when configured.",
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
			jsonReply(w, http.StatusOK, mgr.stampViewerCount(session))
		case http.MethodDelete:
			// A client deleting "its" session is the legacy single-viewer
			// stop. For shared sessions the client should call /leave with
			// its clientId so other viewers survive; DELETE remains the
			// explicit force-stop (owner's "end the room").
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
