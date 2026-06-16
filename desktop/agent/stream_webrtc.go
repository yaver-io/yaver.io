package main

// stream_webrtc.go — M15 (WebRTC real-time half). A SELF-CONTAINED, one-way
// low-latency WebRTC path for the Yaver stream sources (capture card / screen /
// scene / pushed phone). It reuses the existing pion video-track pump
// (remote_runtime_video_track.go) by registering a "stream-<source>" runtime
// target whose capture is ffmpeg(JPEG buffer → H264 Annex-B). It is decoupled
// from the interactive remote-runtime SESSION machinery (no input, no data
// channels, no session map) — a viewer POSTs an SDP offer, the agent attaches an
// H264 track fed by the source, and answers. Sub-second glass-to-glass vs. the
// snapshot/MJPEG paths.
//
// Egress note (CLAUDE.md): same as the rest of streaming — owner/guest only over
// the authed mesh, neutral tool, user's content + responsibility.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	pionturn "github.com/pion/turn/v4"
	"github.com/pion/webrtc/v4"
)

// iceServersForPeer builds the agent-side ICE config: a public STUN entry
// always, plus the relay's colocated TURN (relay/turn.go) when the operator
// points us at it (YAVER_TURN_URL + the shared TURN_AUTH_SECRET/RELAY_PASSWORD).
// Same inputs as the /stream/webrtc/ice route the browser fetches, so both peers
// agree on the relay candidate — that's what makes remote (CG-NAT) viewing work.
func iceServersForPeer() []webrtc.ICEServer {
	out := []webrtc.ICEServer{}
	stun := strings.TrimSpace(os.Getenv("YAVER_STUN_URL"))
	if stun == "" {
		stun = "stun:stun.l.google.com:19302"
	}
	out = append(out, webrtc.ICEServer{URLs: []string{stun}})
	turnURL := strings.TrimSpace(os.Getenv("YAVER_TURN_URL"))
	secret := turnAuthSecret()
	if turnURL == "" || secret == "" {
		return out // STUN-only; ICE tries its best (works on same-network)
	}
	user, pass, err := pionturn.GenerateLongTermCredentials(secret, turnCredentialTTL)
	if err != nil {
		return out
	}
	out = append(out, webrtc.ICEServer{URLs: []string{turnURL}, Username: user, Credential: pass})
	return out
}

// ---- runtime target: a Yaver stream source as an H264 capture --------------

type streamSourceTarget struct{ source string }

func (t streamSourceTarget) Attach(context.Context) (string, error) { return t.source, nil }
func (streamSourceTarget) Tap(context.Context, string, int, int) error {
	return fmt.Errorf("stream source is view-only")
}
func (streamSourceTarget) Swipe(context.Context, string, int, int, int, int, int) error {
	return fmt.Errorf("stream source is view-only")
}
func (streamSourceTarget) Text(context.Context, string, string) error {
	return fmt.Errorf("stream source is view-only")
}
func (streamSourceTarget) Key(context.Context, string, string) error {
	return fmt.Errorf("stream source is view-only")
}
func (streamSourceTarget) Screenshot(context.Context, string, string) error {
	return fmt.Errorf("stream source uses the H264 path, not PNG screenshots")
}
func (streamSourceTarget) Dims(context.Context, string) DeviceDims {
	return DeviceDims{Width: 1280, Height: 720, Scale: 1.0, Rotation: "landscape"}
}
func (streamSourceTarget) NewNALReader(r io.Reader) (nalSource, error) {
	return NewAnnexBReader(r), nil
}
func (streamSourceTarget) CanEncodeRTPH264() bool { return ffmpegPath() != "" }

// SpawnCapture starts ffmpeg reading the source's JPEG frames (fed on stdin) and
// emitting raw H264 Annex-B on stdout, plus a feeder goroutine that pushes the
// latest source frame at a fixed cadence. deviceID carries the source name.
func (t streamSourceTarget) SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	source := deviceID
	if source == "" {
		source = t.source
	}
	ff := ffmpegPath()
	if ff == "" {
		return nil, nil, fmt.Errorf("ffmpeg not found — required for WebRTC encode")
	}
	const fps = 12
	cmd := exec.CommandContext(ctx, ff,
		"-f", "mjpeg", "-framerate", fmt.Sprintf("%d", fps), "-i", "pipe:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", fmt.Sprintf("%d", fps*2), "-bf", "0",
		"-f", "h264", "pipe:1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	// Feeder: write the latest source JPEG until the context ends or ffmpeg dies.
	go func() {
		defer stdin.Close()
		ticker := time.NewTicker(time.Second / time.Duration(fps))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f := sourceFrameJPEG(source)
				if len(f) == 0 {
					continue
				}
				if _, err := stdin.Write(f); err != nil {
					return
				}
			}
		}
	}()
	return cmd, stdout, nil
}

// ---- self-contained signaling: POST /stream/webrtc/offer -------------------

// liveWebRTCPeers tracks PCs so we can cap concurrent viewers and clean up.
var liveWebRTCPeers sync.Map // *webrtc.PeerConnection -> *videoTrackPump

func (s *HTTPServer) handleStreamWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Source string `json:"source"`
		SDP    string `json:"sdp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SDP) == "" {
		jsonError(w, http.StatusBadRequest, "expected {source, sdp}")
		return
	}
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = "capture"
	}
	if ffmpegPath() == "" {
		jsonError(w, http.StatusServiceUnavailable, "ffmpeg not installed — required for WebRTC encode")
		return
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "peer connection: "+err.Error())
		return
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "yaver-stream-"+source)
	if err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusInternalServerError, "track: "+err.Error())
		return
	}
	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusInternalServerError, "add track: "+err.Error())
		return
	}

	pump := newVideoTrackPump("stream-"+source, source, track, nil)
	liveWebRTCPeers.Store(pc, pump)

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			pump.Start(context.Background())
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			pump.Stop()
			liveWebRTCPeers.Delete(pc)
			_ = pc.Close()
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: body.SDP}); err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusBadRequest, "set remote: "+err.Error())
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusInternalServerError, "answer: "+err.Error())
		return
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusInternalServerError, "local desc: "+err.Error())
		return
	}
	<-gather // non-trickle: one round trip

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"type": "answer",
		"sdp":  pc.LocalDescription().SDP,
	})
}
