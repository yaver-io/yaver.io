package main

// ghost_stream.go — live "camera view" of the ghost (Bambu-P1S style). The ghost
// captures the Slave screen (which shows the RustDesk-mirrored ERP or a native
// window), JPEG-encodes at a few fps into a shared latest-frame, and serves it as
// MJPEG (multipart/x-mixed-replace) + a single latest frame. The Talos web UI
// proxies this so users can watch operations live. Heavy work (capture/encode)
// is here in Yaver; Talos just embeds an <img>.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"sync"
	"time"

	"github.com/yaver-io/agent/ghost"
)

type ghostStreamer struct {
	mu      sync.Mutex
	on      bool
	fps     int
	quality int
	latest  []byte
	cancel  context.CancelFunc
}

var ghostStream = &ghostStreamer{}

func clampFps(fps int) int {
	if fps <= 0 {
		return 3
	}
	if fps > 10 {
		return 10
	}
	return fps
}

func (g *ghostStreamer) start(eng *ghost.Engine, fps int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if eng == nil || eng.Screen == nil {
		return fmt.Errorf("ghost: no screen to stream on this platform")
	}
	g.fps = clampFps(fps)
	g.quality = 55
	if g.on {
		return nil // already running; fps updated
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.on = true
	go g.loop(ctx, eng)
	return nil
}

func (g *ghostStreamer) loop(ctx context.Context, eng *ghost.Engine) {
	for {
		g.mu.Lock()
		fps, q := g.fps, g.quality
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second / time.Duration(clampFps(fps))):
		}
		img, err := eng.Screen.Capture(0)
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		if jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}) == nil {
			g.mu.Lock()
			g.latest = buf.Bytes()
			g.mu.Unlock()
		}
	}
}

func (g *ghostStreamer) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.on = false
	g.latest = nil
}

func (g *ghostStreamer) running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.on
}

func (g *ghostStreamer) curFps() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return clampFps(g.fps)
}

func (g *ghostStreamer) frame() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.latest
}

func (g *ghostStreamer) status() map[string]interface{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return map[string]interface{}{
		"running":   g.on,
		"fps":       clampFps(g.fps),
		"hasFrame":  len(g.latest) > 0,
		"streamUrl": "/ghost/stream",
		"frameUrl":  "/ghost/frame.jpg",
	}
}

// ── HTTP: MJPEG stream + latest frame (proxied by the Talos web backend) ──────

func (s *HTTPServer) handleGhostStream(w http.ResponseWriter, r *http.Request) {
	if !s.ghostEnabled {
		jsonError(w, http.StatusForbidden, "GUI ghost disabled; start with `yaver serve --ghost`")
		return
	}
	if !ghostStream.running() {
		eng, err := s.ensureGhost()
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, "ghost screen unavailable: "+err.Error())
			return
		}
		if err := ghostStream.start(eng, 3); err != nil {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(time.Second / time.Duration(ghostStream.curFps()))
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			f := ghostStream.frame()
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

func (s *HTTPServer) handleGhostFrame(w http.ResponseWriter, r *http.Request) {
	if !s.ghostEnabled {
		jsonError(w, http.StatusForbidden, "GUI ghost disabled")
		return
	}
	if !ghostStream.running() {
		if eng, err := s.ensureGhost(); err == nil {
			_ = ghostStream.start(eng, 3)
			time.Sleep(500 * time.Millisecond)
		}
	}
	f := ghostStream.frame()
	if len(f) == 0 {
		jsonError(w, http.StatusServiceUnavailable, "no frame yet")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(f)
}

// ── ops verbs (so Talos/Claude can control the stream over the mesh) ──────────

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_stream_start",
		Description: "Start the live ghost screen stream (Bambu-camera style). Optional fps (1-10, default 3). Watch via the agent's /ghost/stream (MJPEG) — the Talos web UI proxies it. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"fps": map[string]interface{}{"type": "integer", "description": "frames per second 1-10 (default 3)"},
		}),
		Handler:    ghostStreamStartHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_stream_stop",
		Description: "Stop the live ghost screen stream. Requires --ghost.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     ghostStreamStopHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_stream_status",
		Description: "Live ghost stream status (running, fps, URLs). Requires --ghost.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     ghostStreamStatusHandler,
		AllowGuest:  false,
	})
}

func ghostStreamStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	var p struct {
		FPS int `json:"fps"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	eng, err := c.Server.ensureGhost()
	if err != nil {
		return OpsResult{OK: false, Code: "unsupported", Error: err.Error()}
	}
	if err := ghostStream.start(eng, p.FPS); err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: ghostStream.status()}
}

func ghostStreamStopHandler(c OpsContext, _ json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	ghostStream.stop()
	return OpsResult{OK: true, Initial: ghostStream.status()}
}

func ghostStreamStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	return OpsResult{OK: true, Initial: ghostStream.status()}
}
