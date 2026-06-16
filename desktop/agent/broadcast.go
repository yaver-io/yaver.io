package main

// broadcast.go — M15 (RTMP broadcast-out half). Go live to an RTMP endpoint
// (Twitch / YouTube / your own server) from any Yaver source — the capture card,
// the screen, a pushed phone camera, or the composited "scene". We feed the
// source's latest JPEG frames into ffmpeg's mjpeg demuxer over a pipe and let
// ffmpeg x264-encode to FLV/RTMP, so there's no capture-device contention with
// the live MJPEG stream and the same composited scene that's on the watch link
// can be broadcast.
//
// Third-party egress (CLAUDE.md "do no harm"): broadcasting hits an external
// service. It is USER-INITIATED ONLY (an owner ops call with an explicit URL),
// never a hidden loop. We identify honestly via the platform's own stream key.
// WebRTC real-time (the other half of M15) is a separate, later effort.

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type broadcaster struct {
	mu      sync.Mutex
	on      bool
	source  string
	audio   bool
	target  string // host only (key masked)
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	lastErr string
}

var bcast = &broadcaster{}

func maskRTMP(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/…"
	}
	return "rtmp://…"
}

func (b *broadcaster) start(source, rtmpURL string, fps int, profile, audioDevice string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.on {
		return fmt.Errorf("already broadcasting (stop first)")
	}
	ff := ffmpegPath()
	if ff == "" {
		return fmt.Errorf("ffmpeg not found — install it to broadcast")
	}
	if !strings.HasPrefix(rtmpURL, "rtmp://") && !strings.HasPrefix(rtmpURL, "rtmps://") {
		return fmt.Errorf("rtmpUrl must be rtmp:// or rtmps://")
	}
	if source == "" {
		source = "capture"
	}
	// Resolve a quality tier for the broadcast (downscale + bitrate); fps from
	// the profile unless explicitly given.
	prof, hasProf := streamProfileTiers[profile]
	if hasProf && profile != "source" && fps <= 0 {
		fps = prof.FPS
	}
	if fps <= 0 || fps > 30 {
		fps = 10
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Video JPEG frames in on stdin (input 0); optional ALSA audio (input 1 —
	// a SEPARATE device from the v4l2 video, so no contention with the capture
	// stream) → x264 + AAC → FLV/RTMP.
	audioDevice = strings.TrimSpace(audioDevice)
	withAudio := audioDevice != "" && runtime.GOOS == "linux"
	args := []string{
		"-f", "mjpeg", "-framerate", fmt.Sprintf("%d", fps), "-i", "pipe:0",
	}
	if withAudio {
		args = append(args, "-f", "alsa", "-i", audioDevice)
	}
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", fmt.Sprintf("%d", fps*2),
	)
	if withAudio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-map", "0:v:0", "-map", "1:a:0")
	} else {
		args = append(args, "-an")
	}
	if hasProf && profile != "source" {
		if prof.MaxWidth > 0 {
			args = append(args, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", prof.MaxWidth))
		}
		if prof.BitrateKbps > 0 {
			args = append(args, "-b:v", fmt.Sprintf("%dk", prof.BitrateKbps),
				"-maxrate", fmt.Sprintf("%dk", prof.BitrateKbps), "-bufsize", fmt.Sprintf("%dk", prof.BitrateKbps*2))
		}
	}
	args = append(args, "-f", "flv", rtmpURL)
	cmd := exec.CommandContext(ctx, ff, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("ffmpeg stdin: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	b.cancel = cancel
	b.cmd = cmd
	b.on = true
	b.source = source
	b.target = maskRTMP(rtmpURL)
	b.audio = withAudio
	b.lastErr = ""

	// Feed loop: write the source's latest JPEG at the target fps.
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
	// Reaper: surface ffmpeg exit.
	go func() {
		_ = cmd.Wait()
		b.mu.Lock()
		if b.on && b.lastErr == "" {
			tail := strings.TrimSpace(stderr.String())
			if len(tail) > 300 {
				tail = tail[len(tail)-300:]
			}
			b.lastErr = "broadcast ended: " + tail
		}
		b.on = false
		b.mu.Unlock()
	}()
	return nil
}

func (b *broadcaster) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.on = false
	b.cmd = nil
}

func (b *broadcaster) status() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := map[string]interface{}{
		"running": b.on,
		"source":  b.source,
		"target":  b.target, // host only; the stream key is never echoed
		"audio":   b.audio,
	}
	if b.lastErr != "" {
		st["error"] = b.lastErr
	}
	return st
}
