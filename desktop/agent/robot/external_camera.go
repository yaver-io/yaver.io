package robot

// external_camera.go — camera sources for an edge box that has NO local
// /dev/video0:
//
//   - ExternalCamera: a PUSH buffer. A producer (the Android Yaver app capturing
//     its OWN camera, a frame grabber, a PLC/machinery vision feed) calls
//     SetFrame with the latest JPEG over loopback; the move-and-verify loop,
//     robot_snapshot and the MJPEG stream read it back. This is how a phone that
//     is ITSELF the box supplies the eye — Android exposes the camera through
//     Camera2/CameraX, not a V4L2 node, so GstCamera cannot capture it.
//
//   - HTTPCamera: a PULL source. Fetches a single JPEG from a snapshot URL (an IP
//     camera, a phone-as-webcam app, a Fairino/PLC cell's network camera). For
//     boxes that have no local camera but can reach one over the LAN.
//
// Both implement Camera, so they drop into Controller with no protocol change —
// the verify-and-gate loop, encoder cross-check and e-stop are unchanged.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ExternalCamera is a thread-safe latest-frame buffer fed by SetFrame.
type ExternalCamera struct {
	mu     sync.RWMutex
	latest []byte
	at     time.Time
	// MaxAge: a frame older than this is treated as stale (the producer stopped
	// pushing) so Available()/Grab() degrade instead of serving a frozen image
	// into an obstruction verdict. 0 → defaultMaxAge.
	MaxAge time.Duration
	// nowFn is injectable for deterministic tests.
	nowFn func() time.Time
}

const defaultExternalCamMaxAge = 5 * time.Second

func NewExternalCamera() *ExternalCamera {
	return &ExternalCamera{MaxAge: defaultExternalCamMaxAge}
}

func (c *ExternalCamera) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

func (c *ExternalCamera) maxAge() time.Duration {
	if c.MaxAge <= 0 {
		return defaultExternalCamMaxAge
	}
	return c.MaxAge
}

// SetFrame stores the latest JPEG. Safe for concurrent producers (last writer
// wins); a copy is taken so the caller may reuse its buffer.
func (c *ExternalCamera) SetFrame(jpeg []byte) {
	cp := make([]byte, len(jpeg))
	copy(cp, jpeg)
	c.mu.Lock()
	c.latest = cp
	c.at = c.now()
	c.mu.Unlock()
}

// Available reports whether a fresh (non-stale) frame is present.
func (c *ExternalCamera) Available() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.latest) > 0 && c.now().Sub(c.at) <= c.maxAge()
}

// Grab returns the latest frame. It errors when none has arrived yet or the
// feed went stale, so a frozen image never silently feeds a safety verdict.
func (c *ExternalCamera) Grab(ctx context.Context) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.latest) == 0 {
		return nil, fmt.Errorf("external camera: no frame received yet (is the box pushing frames?)")
	}
	if age := c.now().Sub(c.at); age > c.maxAge() {
		return nil, fmt.Errorf("external camera: last frame is stale (%s old)", age.Round(time.Millisecond))
	}
	out := make([]byte, len(c.latest))
	copy(out, c.latest)
	return out, nil
}

// AgeMs is the age of the latest frame in ms, or -1 if none has arrived.
func (c *ExternalCamera) AgeMs() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.latest) == 0 {
		return -1
	}
	return c.now().Sub(c.at).Milliseconds()
}

// StreamMJPEG re-emits the latest pushed frame as a multipart/x-mixed-replace
// stream at fps, so the existing MJPEG viewer works against a push-fed camera
// too. Skips ticks where no new frame arrived. Blocks until ctx is cancelled or
// the writer errors; the caller sets Content-Type to
// "multipart/x-mixed-replace; boundary=" + MJPEGBoundary first.
func (c *ExternalCamera) StreamMJPEG(ctx context.Context, w io.Writer, flush func(), fps int) error {
	if fps <= 0 || fps > 30 {
		fps = 10
	}
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	var lastAt time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		c.mu.RLock()
		frame := c.latest
		at := c.at
		c.mu.RUnlock()
		if len(frame) == 0 || at.Equal(lastAt) {
			continue // nothing new since last emit
		}
		lastAt = at
		hdr := fmt.Sprintf("\r\n--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", MJPEGBoundary, len(frame))
		if _, err := io.WriteString(w, hdr); err != nil {
			return err
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
}

// HTTPCamera pulls a single JPEG from a snapshot URL each Grab. Covers a box
// with no local camera but a reachable network camera (IP cam, phone-as-webcam
// app, a cobot/PLC cell's camera).
type HTTPCamera struct {
	URL    string
	client *http.Client
}

func NewHTTPCamera(url string) *HTTPCamera {
	return &HTTPCamera{URL: url, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *HTTPCamera) Available() bool { return c.URL != "" }

func (c *HTTPCamera) Grab(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http camera GET %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http camera %s: status %d", c.URL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if !looksJPEG(b) {
		return nil, fmt.Errorf("http camera %s: response is not a JPEG", c.URL)
	}
	return b, nil
}

// looksJPEG checks the SOI marker (0xFFD8) so we never push a non-image into a
// vision verdict.
func looksJPEG(b []byte) bool {
	return len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF
}
