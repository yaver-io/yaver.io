package main

// remote_runtime_video_track.go — pumps H.264 NAL units from a
// platform-native capture subprocess (adb screenrecord on Android,
// xcrun simctl on iOS) into a Pion TrackLocalStaticSample. The
// browser-side <video> element decodes the resulting RTP stream
// natively, so the only system dep on the agent box is `adb`
// (covered by `yaver install remote-runtime`).
//
// Lifecycle is owned by remoteRuntimeLiveState: Start when the
// PeerConnection enters Connected, Stop when it transitions to
// Failed/Closed/Disconnected. Auto-restart is built into runOnce
// because adb screenrecord caps each call at 3 minutes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// nalSource is the abstraction the pump consumes — both AnnexBReader
// (Android raw H.264) and MP4AnnexBReader (iOS fragmented MP4) satisfy
// it. Defined locally as a tiny interface so neither concrete reader
// has to import the other.
type nalSource interface {
	Next(ctx context.Context) (NALUnit, error)
}

const (
	// 4 Mbps is the comfortable default for 1080p screenrecord —
	// well under the relay's per-response cap and high enough that
	// text rendering stays readable. Adjust via Bandwidth feedback
	// once Phase 7 (TWCC) lands.
	androidScreenrecordBitrate = 4_000_000
	// adb screenrecord has a hard 180-second cap baked into the
	// tool. We restart slightly before that so the viewer never sees
	// dead air.
	androidScreenrecordTimeLimit = 170
	// Sample duration drives RTP timestamp deltas. 42 ms ≈ 24 fps,
	// matching screenrecord's default output. Pion's H.264
	// packetizer fragments larger NALs into FU-A units automatically.
	rtpFrameDuration = 42 * time.Millisecond
)

// videoTrackPump owns the capture subprocess and feeds NAL units
// into the Pion track. One pump per session.
type videoTrackPump struct {
	targetID string
	deviceID string
	track    *webrtc.TrackLocalStaticSample

	// onEvent is called for lifecycle signals the viewer should see
	// on the events DataChannel (e.g. "capture restarted",
	// "encoder gave up"). Optional; nil disables the surface.
	onEvent func(map[string]any)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newVideoTrackPump(targetID, deviceID string, track *webrtc.TrackLocalStaticSample, onEvent func(map[string]any)) *videoTrackPump {
	return &videoTrackPump{
		targetID: targetID,
		deviceID: deviceID,
		track:    track,
		onEvent:  onEvent,
	}
}

// Start kicks off the capture loop in a goroutine. Idempotent — a
// second Start while a pump is already running is a no-op.
func (p *videoTrackPump) Start(parent context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(ctx)
}

// Stop tears down the capture and waits for the goroutine to exit.
// Safe to call multiple times; safe to call before Start (no-op).
func (p *videoTrackPump) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (p *videoTrackPump) run(ctx context.Context) {
	defer func() {
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()
	backoff := time.Second
	restartCount := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		// runOnce returning nil means screenrecord exited cleanly
		// (hit its 3-minute cap). Restart immediately. Returning
		// non-nil means something broke; back off so we don't spin.
		if err == nil {
			restartCount++
			if p.onEvent != nil {
				p.onEvent(map[string]any{
					"type":  "capture_restart",
					"count": restartCount,
					"ts":    time.Now().UTC().Format(time.RFC3339Nano),
				})
			}
			backoff = time.Second
			continue
		}
		if p.onEvent != nil {
			p.onEvent(map[string]any{
				"type":  "capture_error",
				"error": err.Error(),
				"ts":    time.Now().UTC().Format(time.RFC3339Nano),
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

func (p *videoTrackPump) runOnce(ctx context.Context) error {
	cmd, stdout, err := p.spawnCapture(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// stdout pipe is owned by cmd; closing it is implied by
		// process exit. Best-effort kill ensures we don't leak the
		// subprocess if Pion's WriteSample bails mid-stream.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	reader, err := p.newNALReader(stdout)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		nal, err := reader.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// Pion's H.264 packetizer wants Annex-B input — it scans for
		// start codes to find NAL boundaries before fragmenting into
		// RTP STAP-A / FU-A as size dictates. We feed one NAL per
		// Sample with a 3-byte start code restored.
		buf := make([]byte, 0, 3+len(nal.Data))
		buf = append(buf, 0x00, 0x00, 0x01)
		buf = append(buf, nal.Data...)
		if err := p.track.WriteSample(media.Sample{
			Data:     buf,
			Duration: rtpFrameDuration,
		}); err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
	}
}

// newNALReader picks the right NAL extractor for the capture format.
// adb screenrecord emits raw Annex-B, xcrun simctl recordVideo emits
// fragmented MP4. Both satisfy nalSource so the pump loop is the
// same on top.
func (p *videoTrackPump) newNALReader(r io.Reader) (nalSource, error) {
	switch p.targetID {
	case "android-emulator", "android-device":
		return NewAnnexBReader(r), nil
	case "ios-simulator":
		return MP4ToAnnexB(r)
	}
	return nil, fmt.Errorf("no NAL reader for target %q", p.targetID)
}

func (p *videoTrackPump) spawnCapture(ctx context.Context) (*exec.Cmd, io.ReadCloser, error) {
	switch p.targetID {
	case "android-emulator", "android-device":
		return spawnAdbScreenrecord(ctx, p.deviceID)
	case "ios-simulator":
		return spawnXcrunRecordVideo(ctx, p.deviceID)
	}
	return nil, nil, fmt.Errorf("unsupported target %q for rtp-h264 capture", p.targetID)
}

// spawnXcrunRecordVideo starts `xcrun simctl io <udid> recordVideo
// --codec=h264 -` with stdout piped back. xcrun emits a fragmented
// MP4 (ISO BMFF moof+mdat) that MP4AnnexBReader unpacks into NAL
// units. SIGINT (sent during runOnce cleanup) makes xcrun finalize
// the trailing fragment cleanly so the next restart's first frame
// has a valid avcC.
//
// Note: xcrun has no equivalent of adb screenrecord's --time-limit,
// so the iOS pump runs the subprocess until the session ends. We
// don't auto-restart the way Android does — there's nothing to
// restart. If xcrun ever crashes, runOnce's parent loop will pick
// it back up after the standard backoff.
func spawnXcrunRecordVideo(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	if runtime.GOOS != "darwin" {
		return nil, nil, fmt.Errorf("ios-simulator capture requires macOS")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil, nil, fmt.Errorf("xcrun not on PATH — install Xcode Command Line Tools (`xcode-select --install`)")
	}
	target := deviceID
	if target == "" {
		target = "booted"
	}
	cmd := exec.CommandContext(ctx, "xcrun", "simctl", "io", target,
		"recordVideo", "--codec=h264", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, stdout, nil
}

func spawnAdbScreenrecord(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil, nil, fmt.Errorf("adb not on PATH — run `yaver install remote-runtime` to provision android-sdk")
	}
	args := []string{}
	if deviceID != "" {
		args = append(args, "-s", deviceID)
	}
	// `exec-out` (not `shell`) avoids CR/LF mangling that would
	// corrupt the H.264 byte stream on its way back to the host.
	// `--output-format=h264` emits raw Annex-B straight to stdout.
	args = append(args, "exec-out", "screenrecord",
		"--output-format=h264",
		fmt.Sprintf("--bit-rate=%d", androidScreenrecordBitrate),
		fmt.Sprintf("--time-limit=%d", androidScreenrecordTimeLimit),
		"-")
	cmd := exec.CommandContext(ctx, "adb", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, stdout, nil
}

// agentCanEncodeRTPH264 reports whether the agent host can produce
// an RTP H.264 video track for the given target. Called at offer-
// acceptance time so the agent only adds a video track when it
// actually has a fighting chance of feeding it.
//
// Exposed as a package-level var (not a plain func) so tests can
// override it deterministically without faking adb on PATH. Production
// keeps the real probe.
var agentCanEncodeRTPH264 = func(targetID string) bool {
	switch targetID {
	case "android-emulator", "android-device":
		_, err := exec.LookPath("adb")
		return err == nil
	case "ios-simulator":
		// Xcode 26's simctl no longer supports recordVideo to stdout
		// ("rendering to standard out is no longer supported"). The
		// RTP pump needs a streaming pipe, so keep iOS on WebRTC JPEG
		// data-channel frames until we replace this with a file-backed
		// fragment tailer or another live capture source.
		return false
	}
	return false
}
