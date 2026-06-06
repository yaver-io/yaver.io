package robot

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// camMu serializes access to the capture device: a verify-loop grab and a
// snapshot/stream poll must not run gst on /dev/video0 concurrently (the second
// gets "device busy" and hangs). All grabs/streams take this so they queue.
var camMu sync.Mutex

// MJPEGBoundary is the multipart boundary for the live stream; viewers set
// Content-Type: multipart/x-mixed-replace; boundary=<this>.
const MJPEGBoundary = "yaverframe"

// Camera grabs a single settled JPEG frame.
type Camera interface {
	Grab(ctx context.Context) ([]byte, error)
	Available() bool
}

// GstCamera captures via gst-launch-1.0 (present on stock Ubuntu desktops; no
// ffmpeg/sudo needed). It grabs several buffers so auto-exposure settles and
// returns the last complete frame. Native V4L2 / Android Camera2 backends
// replace this without changing the protocol.
type GstCamera struct {
	Device  string // /dev/video0
	Buffers int    // frames to grab before keeping the last (exposure settle)
}

func NewGstCamera(device string) *GstCamera {
	if device == "" {
		device = "/dev/video0"
	}
	return &GstCamera{Device: device, Buffers: 8}
}

func (c *GstCamera) Available() bool {
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		return false
	}
	if _, err := os.Stat(c.Device); err != nil {
		return false
	}
	return true
}

func (c *GstCamera) Grab(ctx context.Context) ([]byte, error) {
	camMu.Lock() // serialize with concurrent snapshot/verify grabs (else gst "device busy" hangs)
	defer camMu.Unlock()
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		return nil, fmt.Errorf("gst-launch-1.0 not found: %w", err)
	}
	dir, err := os.MkdirTemp("", "yaver-robot-cam-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	pattern := filepath.Join(dir, "f_%03d.jpg")
	n := c.Buffers
	if n < 2 {
		n = 2
	}
	// videoconvert handles raw (YUYV) cams; jpegenc emits JPEG; multifilesink
	// writes one file per buffer so we can keep the last (settled) frame.
	args := []string{
		"-e", "v4l2src", "device=" + c.Device, fmt.Sprintf("num-buffers=%d", n),
		"!", "videoconvert", "!", "jpegenc", "!", "multifilesink", "location=" + pattern,
	}
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("gst capture failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	frames, _ := filepath.Glob(filepath.Join(dir, "f_*.jpg"))
	if len(frames) == 0 {
		return nil, fmt.Errorf("camera produced no frames")
	}
	sort.Strings(frames)
	return os.ReadFile(frames[len(frames)-1])
}

// StreamMJPEG pipes a live multipart/x-mixed-replace JPEG stream from the
// camera into w (an HTTP response), flushing after each chunk so the viewer
// sees frames immediately. Caps the rate (≈10fps) and quality to keep the
// stream LAN/relay-friendly. Blocks until ctx is cancelled or gst exits — the
// caller sets the Content-Type to "multipart/x-mixed-replace; boundary=" +
// MJPEGBoundary before calling. This is the Yaver-level live camera stream:
// the heavy capture/encode work stays on the edge node; phone/Talos are thin
// <img>/WebView viewers.
func (c *GstCamera) StreamMJPEG(ctx context.Context, w io.Writer, flush func(), fps int) error {
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		return fmt.Errorf("gst-launch-1.0 not found: %w", err)
	}
	if fps <= 0 || fps > 30 {
		fps = 10
	}
	args := []string{
		"-q", "v4l2src", "device=" + c.Device,
		"!", "videoconvert",
		"!", "videorate", "!", fmt.Sprintf("video/x-raw,framerate=%d/1", fps),
		"!", "jpegenc", "quality=70",
		"!", "multipartmux", "boundary=" + MJPEGBoundary,
		"!", "fdsink", "fd=1",
	}
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	buf := make([]byte, 64*1024)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr // viewer disconnected
			}
			if flush != nil {
				flush()
			}
		}
		if rerr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}
