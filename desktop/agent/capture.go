package main

// capture.go — content-AGNOSTIC capture-card source. A USB/HDMI capture card on
// the box shows up as a V4L2 device (/dev/videoN on Linux, an AVFoundation index
// on macOS for dev). We run ffmpeg to pull MJPEG off the card into a shared
// latest-frame buffer and serve it as MJPEG (multipart/x-mixed-replace) + a
// single latest frame — exactly the ghost_stream.go pattern, but the source is a
// capture card. The web dashboard / a parked Android head unit embed
// <img src="/capture/stream">; iOS / glasses snapshot-poll /capture/frame.jpg.
//
// Yaver is AGNOSTIC: it does not know or care what the card is fed — a
// satellite/cable box (uydu yayını), a console, a camera, a set-top box, a PC.
// It streams whatever bytes the card provides, to the OWNER's account or an
// explicitly-invited GUEST account only (the "stream" capability scope) — never
// public. It does not inspect, classify, or police the content.
//
// WARNING + USER RESPONSIBILITY: the user is responsible for what they capture
// and stream and for having the rights to it. Yaver attaches a standing warning
// to capture status and never decides for the user. Yaver itself adds NO content
// -protection circumvention — it passes through exactly what the hardware gives.
// Content protection is enforced UPSTREAM: an HDCP source blanks itself, the card
// receives BLACK, and Yaver streams that black unchanged (with an advisory hint
// so the user understands what they see). We never build, document, or assume an
// HDCP stripper — but we also do not block; that call is the user's.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type captureStreamer struct {
	mu     sync.Mutex
	on     bool
	device string
	fps    int
	width  int
	height int
	latest []byte
	cancel context.CancelFunc
	cmd    *exec.Cmd

	lastErr     string
	blackStreak int  // consecutive near-black frames
	hdcpBlocked bool // set when the black streak crosses the threshold
}

var captureStream = &captureStreamer{}

// hdcpBlackStreakThreshold — how many consecutive near-black frames before we
// conclude the source is HDCP-protected (vs. a momentary black scene). At the
// default ~6 fps this is ~2.5s of black, well past any normal fade-to-black.
const hdcpBlackStreakThreshold = 15

func captureClampFps(fps int) int {
	if fps <= 0 {
		return 6
	}
	if fps > 15 {
		return 15
	}
	return fps
}

// ffmpegPath returns the ffmpeg binary or "" if it isn't installed.
func ffmpegPath() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return ""
}

// captureDevices enumerates likely capture devices on this host. On Linux it
// lists /dev/video*; elsewhere it returns an empty list (the capture card is a
// Pi/Linux concern — macOS entries exist only so `capture_start` can be smoke
// -tested on a dev Mac via avfoundation index "0").
func captureDevices() []map[string]interface{} {
	out := []map[string]interface{}{}
	if runtime.GOOS == "linux" {
		entries, _ := filepath.Glob("/dev/video*")
		for _, p := range entries {
			name := p
			// /sys/class/video4linux/<dev>/name holds the human label.
			base := filepath.Base(p)
			if b, err := os.ReadFile(filepath.Join("/sys/class/video4linux", base, "name")); err == nil {
				if n := strings.TrimSpace(string(b)); n != "" {
					name = n
				}
			}
			out = append(out, map[string]interface{}{"path": p, "name": name})
		}
	}
	return out
}

// ffmpegInputArgs builds the platform-specific ffmpeg input for a device.
func ffmpegInputArgs(device string, fps, w, h int) []string {
	size := fmt.Sprintf("%dx%d", w, h)
	switch runtime.GOOS {
	case "linux":
		return []string{
			"-f", "v4l2",
			"-framerate", fmt.Sprintf("%d", fps),
			"-video_size", size,
			"-i", device,
		}
	case "darwin":
		// dev-only: `-f avfoundation -i "<index>"`; device is the AVFoundation index.
		return []string{
			"-f", "avfoundation",
			"-framerate", fmt.Sprintf("%d", fps),
			"-i", device,
		}
	default:
		return []string{"-i", device}
	}
}

func (g *captureStreamer) start(device string, fps, w, h, quality int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on {
		return nil // already running
	}
	ff := ffmpegPath()
	if ff == "" {
		return fmt.Errorf("ffmpeg not found — install it (Debian/Pi: sudo apt install ffmpeg)")
	}
	if strings.TrimSpace(device) == "" {
		if runtime.GOOS == "linux" {
			device = "/dev/video0"
		} else {
			device = "0"
		}
	}
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 720
	}
	if quality < 2 || quality > 31 {
		quality = 7 // ffmpeg -q:v default; lower = better
	}
	g.device = device
	g.fps = captureClampFps(fps)
	g.width, g.height = w, h
	g.lastErr = ""
	g.blackStreak = 0
	g.hdcpBlocked = false

	args := append(ffmpegInputArgs(device, g.fps, w, h),
		"-f", "mjpeg",
		"-q:v", fmt.Sprintf("%d", quality),
		"-an",
		"pipe:1",
	)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, ff, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("ffmpeg stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	g.cancel = cancel
	g.cmd = cmd
	g.on = true
	go g.readLoop(ctx, stdout)
	go func() {
		_ = cmd.Wait()
		g.mu.Lock()
		if g.on && g.lastErr == "" {
			// ffmpeg exited unexpectedly; surface a trimmed stderr tail.
			tail := strings.TrimSpace(stderr.String())
			if len(tail) > 400 {
				tail = tail[len(tail)-400:]
			}
			g.lastErr = "capture ended: " + tail
		}
		g.on = false
		g.mu.Unlock()
	}()
	return nil
}

// readLoop parses concatenated JPEG frames out of ffmpeg's MJPEG pipe, splitting
// on the SOI (FFD8) / EOI (FFD9) markers, and stores the most recent frame.
func (g *captureStreamer) readLoop(ctx context.Context, stdout io.Reader) {
	br := bufio.NewReaderSize(stdout, 1<<16)
	var frame bytes.Buffer
	inFrame := false
	prev := byte(0)
	frameCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		b, err := br.ReadByte()
		if err != nil {
			return
		}
		if !inFrame {
			if prev == 0xFF && b == 0xD8 {
				inFrame = true
				frame.Reset()
				frame.WriteByte(0xFF)
				frame.WriteByte(0xD8)
			}
			prev = b
			continue
		}
		frame.WriteByte(b)
		if prev == 0xFF && b == 0xD9 {
			// Complete JPEG frame.
			buf := make([]byte, frame.Len())
			copy(buf, frame.Bytes())
			frameCount++
			black := frameCount%4 == 0 && isMostlyBlack(buf) // sample every 4th frame
			g.mu.Lock()
			g.latest = buf
			if frameCount%4 == 0 {
				if black {
					g.blackStreak++
					if g.blackStreak >= hdcpBlackStreakThreshold {
						g.hdcpBlocked = true
					}
				} else {
					g.blackStreak = 0
					g.hdcpBlocked = false
				}
			}
			g.mu.Unlock()
			inFrame = false
		}
		prev = b
	}
}

// isMostlyBlack decodes a JPEG and returns true if its average luma is below a
// small threshold — the signature of an HDCP-blanked capture-card input.
func isMostlyBlack(jpegBytes []byte) bool {
	img, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		return false
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return false
	}
	// Sample a coarse grid to stay cheap.
	const grid = 16
	var sum, n uint64
	for yi := 0; yi < grid; yi++ {
		for xi := 0; xi < grid; xi++ {
			x := b.Min.X + (b.Dx()*xi)/grid
			y := b.Min.Y + (b.Dy()*yi)/grid
			r, gg, bb, _ := img.At(x, y).RGBA()
			// luma approx (values are 16-bit here)
			lum := (uint64(r)*299 + uint64(gg)*587 + uint64(bb)*114) / 1000
			sum += lum >> 8 // back to 8-bit scale
			n++
		}
	}
	if n == 0 {
		return false
	}
	return (sum / n) < 10 // ~4% of full-scale
}

func (g *captureStreamer) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.on = false
	g.latest = nil
	g.cmd = nil
}

func (g *captureStreamer) running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.on
}

func (g *captureStreamer) curFps() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return captureClampFps(g.fps)
}

func (g *captureStreamer) frame() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.latest
}

func (g *captureStreamer) status() map[string]interface{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := map[string]interface{}{
		"running":   g.on,
		"device":    g.device,
		"fps":       captureClampFps(g.fps),
		"width":     g.width,
		"height":    g.height,
		"hasFrame":  len(g.latest) > 0,
		"streamUrl": "/capture/stream",
		"frameUrl":  "/capture/frame.jpg",
		"ffmpeg":    ffmpegPath() != "",
	}
	if g.hdcpBlocked {
		// Terse diagnostic only — we keep streaming the (black) frames anyway.
		// Responsibility framing lives in Yaver policy (CLAUDE.md), not here.
		st["blackHint"] = "persistently black — likely an HDCP source; streamed as-is"
	}
	if g.lastErr != "" {
		st["error"] = g.lastErr
	}
	return st
}

// ── HTTP: MJPEG stream + latest frame (same shape as /ghost/*) ───────────────

func (s *HTTPServer) handleCaptureStream(w http.ResponseWriter, r *http.Request) {
	if !captureStream.running() {
		jsonError(w, http.StatusServiceUnavailable, "capture not running — start with `yaver appletv capture start` or the capture_start verb")
		return
	}
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(time.Second / time.Duration(captureStream.curFps()))
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Agnostic: stream whatever the card provides, including black.
			// If the source is HDCP-protected the frames are black — that's
			// fine, we stream them; the status carries an advisory hint.
			f := captureStream.frame()
			if len(f) == 0 {
				continue
			}
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(f)); err != nil {
				return
			}
			if _, err := w.Write(f); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (g *captureStreamer) hdcpStatus() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hdcpBlocked
}

func (s *HTTPServer) handleCaptureFrame(w http.ResponseWriter, r *http.Request) {
	if !captureStream.running() {
		jsonError(w, http.StatusServiceUnavailable, "capture not running")
		return
	}
	// Agnostic: serve whatever the card provides, including a black/dark frame.
	f := captureStream.frame()
	if len(f) == 0 {
		jsonError(w, http.StatusServiceUnavailable, "no frame yet")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f)
}
