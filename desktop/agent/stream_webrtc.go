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
	"runtime"
	"strings"
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
	// deviceID is the "source:tier" encode key (fan-out, Q4): the bare source
	// names the frame buffer; the full key selects this tier's profile.
	encodeKey := deviceID
	source := deviceID
	if i := strings.IndexByte(deviceID, ':'); i >= 0 {
		source = deviceID[:i]
	}
	if source == "" {
		source = t.source
		encodeKey = source
	}
	ff := ffmpegPath()
	if ff == "" {
		return nil, nil, fmt.Errorf("ffmpeg not found — required for WebRTC encode")
	}
	// Adaptive encode from the resolved profile (Part H): fps, downscale, bitrate.
	prof := getActiveEncodeProfile(encodeKey)
	fps := prof.FPS
	if fps <= 0 || fps > 30 {
		fps = 12
	}
	args := []string{
		"-f", "mjpeg", "-framerate", fmt.Sprintf("%d", fps), "-i", "pipe:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", fmt.Sprintf("%d", fps*2), "-bf", "0",
	}
	// Downscale to the profile cap (keep aspect, even dims). 0 = source.
	if prof.MaxWidth > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", prof.MaxWidth))
	}
	if prof.BitrateKbps > 0 {
		args = append(args,
			"-b:v", fmt.Sprintf("%dk", prof.BitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", prof.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", prof.BitrateKbps*2))
	}
	args = append(args, "-f", "h264", "pipe:1")
	cmd := exec.CommandContext(ctx, ff, args...)
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

func (s *HTTPServer) handleStreamWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Source      string `json:"source"`
		SDP         string `json:"sdp"`
		DeviceClass string `json:"deviceClass"`
		W           int    `json:"w"`
		H           int    `json:"h"`
		Net         string `json:"net"`
		Profile     string `json:"profile"`     // viewer-requested tier or "auto"
		AudioDevice string `json:"audioDevice"` // ALSA hw:N,0 → add an Opus audio track
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SDP) == "" {
		jsonError(w, http.StatusBadRequest, "expected {source, sdp}")
		return
	}
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = "capture"
	}
	// Resolve the adaptive encode profile: a per-source LOCK wins; else the
	// viewer's requested tier; else compute from declared capabilities.
	lockTier := lockedProfileFor(source)
	wantTier := body.Profile
	if lockTier != "" {
		wantTier = lockTier
	}
	prof := profileForConstraints(body.DeviceClass, body.W, body.H, body.Net, wantTier)
	// Q4 fan-out: get/create the shared encode for this (source, tier). Many
	// viewers at the same tier share one ffmpeg + one track.
	se, err := getOrCreateEncode(source, prof)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "encode: "+err.Error())
		return
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
	// Attach the SHARED track (Pion fans its RTP out to every PC). The encoder
	// pump is owned by the shared encode, started once for this tier.
	if _, err := pc.AddTrack(se.track); err != nil {
		_ = pc.Close()
		jsonError(w, http.StatusInternalServerError, "add track: "+err.Error())
		return
	}
	se.addPC(pc)

	// Optional Opus audio track from an ALSA capture device (Linux). Shared per
	// device, fanned out + refcounted like the video.
	var sa *sharedAudio
	if strings.TrimSpace(body.AudioDevice) != "" && runtime.GOOS == "linux" {
		if a, aerr := getOrCreateAudio(strings.TrimSpace(body.AudioDevice)); aerr == nil {
			if _, e := pc.AddTrack(a.track); e == nil {
				sa = a
				sa.addPC(pc)
			}
		}
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			se.removePC(pc) // last viewer of this tier stops the encoder
			if sa != nil {
				sa.removePC(pc)
			}
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

// Pinch is meaningless for a raw stream source: it is a one-way capture (a
// capture card, a screen, a camera) with no input surface to inject into.
// Navigate is meaningless for a capture/stream source: it is a one-way video
// feed (capture card, screen, camera), not something with an address bar.
func (streamSourceTarget) Navigate(context.Context, string, string) error {
	return fmt.Errorf("%w: a stream source is a one-way capture feed with no URL entry point", errNavigateUnsupported)
}

func (streamSourceTarget) Pinch(context.Context, string, int, int, float64, int) error {
	return fmt.Errorf("%w: a stream source is capture-only, there is nothing to touch", errPinchUnsupported)
}
