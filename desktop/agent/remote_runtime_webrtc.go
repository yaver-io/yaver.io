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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/yaver-io/agent/testkit"
	xdraw "golang.org/x/image/draw"
)

type remoteRuntimeLiveState struct {
	mu        sync.Mutex
	sessionID string
	targetID  string
	platform  string
	deviceID  string

	pc       *webrtc.PeerConnection
	framesDC *webrtc.DataChannel
	eventsDC *webrtc.DataChannel

	streamCancel context.CancelFunc
	lastFrame    []byte
	lastFrameAt  time.Time
}

type remoteRuntimeControlRequest struct {
	Action string `json:"action"`
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	Text   string `json:"text,omitempty"`
	Key    string `json:"key,omitempty"`
}

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

	switch session.TargetID {
	case "ios-simulator":
		driver := &testkit.IOSSimDriver{DeviceType: "iPhone"}
		deviceID, err = driver.Boot(ctx)
	case "android-emulator":
		driver := &testkit.AndroidEmuDriver{}
		deviceID, err = driver.Boot(ctx)
	default:
		err = fmt.Errorf("unknown remote runtime target %q", session.TargetID)
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

	updated, _ := m.Update(sessionID, func(current *RemoteRuntimeSession) {
		current.DeviceID = deviceID
		current.Status = "control-ready"
		current.Note = fmt.Sprintf("Attached to %s (%s). WebRTC screenshot streaming is ready for signaling.", current.TargetLabel, deviceID)
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

func (live *remoteRuntimeLiveState) closePeer() {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.streamCancel != nil {
		live.streamCancel()
		live.streamCancel = nil
	}
	if live.pc != nil {
		_ = live.pc.Close()
		live.pc = nil
	}
	live.framesDC = nil
	live.eventsDC = nil
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

	live.closePeer()

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return session, webrtc.SessionDescription{}, err
	}
	framesDC, err := pc.CreateDataChannel("frames", nil)
	if err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}
	eventsDC, err := pc.CreateDataChannel("events", nil)
	if err != nil {
		_ = pc.Close()
		return session, webrtc.SessionDescription{}, err
	}

	live.mu.Lock()
	live.pc = pc
	live.framesDC = framesDC
	live.eventsDC = eventsDC
	live.mu.Unlock()

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
			live.startFramePump(m)
			live.sendEventJSON(map[string]any{
				"type":     "session",
				"session":  updated,
				"platform": updated.Platform,
			})
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			live.closePeer()
		}
	})

	framesDC.OnOpen(func() {
		live.sendEventJSON(map[string]any{
			"type":      "ready",
			"sessionId": sessionID,
			"transport": "webrtc-datachannel-jpeg-v1",
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

func (live *remoteRuntimeLiveState) sendEventJSON(payload map[string]any) {
	live.mu.Lock()
	dc := live.eventsDC
	live.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = dc.SendText(string(buf))
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

	switch targetID {
	case "ios-simulator":
		driver := &testkit.IOSSimDriver{}
		if err := driver.Screenshot(ctx, deviceID, pngPath); err != nil {
			return nil, 0, 0, err
		}
	case "android-emulator":
		driver := &testkit.AndroidEmuDriver{}
		if err := driver.Screenshot(ctx, deviceID, pngPath); err != nil {
			return nil, 0, 0, err
		}
	default:
		return nil, 0, 0, fmt.Errorf("unsupported target %q", targetID)
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
	if width > 900 {
		dstW := 900
		dstH := int(float64(height) * (float64(dstW) / float64(width)))
		dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)
		resized = dst
		width = dstW
		height = dstH
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, resized, &jpeg.Options{Quality: 60}); err != nil {
		return nil, 0, 0, err
	}
	payload := out.Bytes()
	live.mu.Lock()
	live.lastFrame = append(live.lastFrame[:0], payload...)
	live.lastFrameAt = time.Now().UTC()
	live.mu.Unlock()
	return payload, width, height, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		return session, fmt.Errorf("missing action")
	}
	switch action {
	case "tap":
		err = live.tap(ctx, req.X, req.Y)
	case "text":
		err = live.text(ctx, req.Text)
	case "back":
		err = live.key(ctx, "back")
	case "home":
		err = live.key(ctx, "home")
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
	switch targetID {
	case "ios-simulator":
		return (&testkit.IOSSimDriver{}).Tap(ctx, deviceID, x, y)
	case "android-emulator":
		return (&testkit.AndroidEmuDriver{}).Tap(ctx, deviceID, x, y)
	default:
		return fmt.Errorf("unsupported target %q", targetID)
	}
}

func (live *remoteRuntimeLiveState) text(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("text is empty")
	}
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	switch targetID {
	case "ios-simulator":
		return (&testkit.IOSSimDriver{}).SendText(ctx, deviceID, text)
	case "android-emulator":
		return (&testkit.AndroidEmuDriver{}).Text(ctx, deviceID, text)
	default:
		return fmt.Errorf("unsupported target %q", targetID)
	}
}

func (live *remoteRuntimeLiveState) key(ctx context.Context, key string) error {
	live.mu.Lock()
	targetID := live.targetID
	deviceID := live.deviceID
	live.mu.Unlock()
	if targetID != "android-emulator" {
		return fmt.Errorf("%s is only supported for Android emulator sessions right now", key)
	}
	driver := &testkit.AndroidEmuDriver{}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "home":
		return driver.KeyEvent(ctx, deviceID, 3)
	case "back":
		return driver.KeyEvent(ctx, deviceID, 4)
	default:
		if code, err := strconv.Atoi(strings.TrimSpace(key)); err == nil {
			return driver.KeyEvent(ctx, deviceID, code)
		}
		return fmt.Errorf("unsupported key %q", key)
	}
}

func (s *HTTPServer) handleRemoteRuntimeSessionRoute(w http.ResponseWriter, r *http.Request) {
	mgr := s.ensureRemoteRuntimeManager()
	path := strings.TrimPrefix(r.URL.Path, "/remote-runtime/sessions/")
	path = strings.Trim(path, "/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "missing session id")
		return
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
			"transport": "webrtc-datachannel-jpeg-v1",
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
