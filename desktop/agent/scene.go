package main

// scene.go — M14, the "OBS-wrap": a simple, box-side scene compositor. The phone
// is the director; the box does the work. A scene is a list of local frame
// sources (capture card, screen, pushed phone camera) + a layout; a loop grabs
// the latest frame of each, composites them into ONE image (grid / row / pip),
// and publishes it as the "scene" pushed source — so it flows through the exact
// same stream plane (stream_list / stream_snapshot) and guest watch link as any
// other source, no extra plumbing.
//
// Deliberately simple: in-process image compositing (nearest-neighbor scale, no
// ffmpeg filter graph, no external deps). Single-source + multi-tile layouts and
// snapshot cadence — the foundation. Real-time mixing / transitions / encode-to
// -RTMP is a later milestone (WebRTC/ffmpeg). Neutral tool, like OBS: it
// composites whatever the sources provide.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"
	"time"
)

func jpegFromB64(b64 string) ([]byte, error) { return base64.StdEncoding.DecodeString(b64) }
func b64FromBytes(b []byte) string           { return base64.StdEncoding.EncodeToString(b) }

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "scene_set",
		Description: "Configure + start the scene compositor (the box-side 'OBS'). Payload {sources:[\"capture\",\"screen\",\"phone\",…], layout:\"grid|row|pip\", fps?, width?, height?}. The composited result is published as the \"scene\" source — watch/share it like any other stream.",
		Schema: atvSchema(map[string]interface{}{
			"sources": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"layout":  map[string]interface{}{"type": "string", "description": "grid|row|pip"},
			"fps":     map[string]interface{}{"type": "integer"},
			"width":   map[string]interface{}{"type": "integer"},
			"height":  map[string]interface{}{"type": "integer"},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var cfg sceneConfig
			if err := json.Unmarshal(payload, &cfg); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			if len(cfg.Sources) == 0 {
				return OpsResult{OK: false, Code: "bad_payload", Error: "at least one source required"}
			}
			sceneComp.start(cfg)
			return OpsResult{OK: true, StreamID: "scene", Initial: sceneComp.status()}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "scene_stop",
		Description: "Stop the scene compositor.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			sceneComp.stop()
			return OpsResult{OK: true, Initial: sceneComp.status()}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "scene_status",
		Description: "Scene compositor status (running, sources, layout, fps).",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			return OpsResult{OK: true, Initial: sceneComp.status()}
		},
		AllowGuest: true,
	})
}

type sceneConfig struct {
	Sources []string `json:"sources"` // "capture" | "screen" | a pushed name (e.g. "phone")
	Layout  string   `json:"layout"`  // "grid" | "row" | "pip"
	FPS     int      `json:"fps"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
}

type sceneCompositor struct {
	mu     sync.Mutex
	on     bool
	cfg    sceneConfig
	cancel context.CancelFunc
}

var sceneComp = &sceneCompositor{}

// sourceFrameJPEG returns the latest JPEG bytes for a local source, or nil.
func sourceFrameJPEG(name string) []byte {
	switch name {
	case "capture":
		return captureStream.frame()
	case "screen":
		return ghostStream.frame()
	default:
		if f, ok := getPushedFrame(name); ok {
			if dec, err := jpegFromB64(f.b64); err == nil {
				return dec
			}
		}
	}
	return nil
}

func (s *sceneCompositor) start(cfg sceneConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.FPS <= 0 || cfg.FPS > 15 {
		cfg.FPS = 4
	}
	if cfg.Width <= 0 {
		cfg.Width = 1280
	}
	if cfg.Height <= 0 {
		cfg.Height = 720
	}
	if cfg.Layout == "" {
		cfg.Layout = "grid"
	}
	s.cfg = cfg
	if s.on {
		return // reconfigured; loop picks up new cfg
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.on = true
	go s.loop(ctx)
}

func (s *sceneCompositor) loop(ctx context.Context) {
	for {
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second / time.Duration(cfg.FPS)):
		}
		// Decode whatever each source currently has.
		var tiles []image.Image
		for _, name := range cfg.Sources {
			b := sourceFrameJPEG(name)
			if len(b) == 0 {
				continue
			}
			if img, err := jpeg.Decode(bytes.NewReader(b)); err == nil {
				tiles = append(tiles, img)
			}
		}
		if len(tiles) == 0 {
			continue
		}
		canvas := compositeTiles(tiles, cfg.Layout, cfg.Width, cfg.Height)
		var buf bytes.Buffer
		if jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 70}) == nil {
			setPushedFrame("scene", b64FromBytes(buf.Bytes()), "image/jpeg")
		}
	}
}

func (s *sceneCompositor) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.on = false
}

func (s *sceneCompositor) status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{
		"running": s.on,
		"sources": s.cfg.Sources,
		"layout":  s.cfg.Layout,
		"fps":     s.cfg.FPS,
		"output":  "scene", // appears in stream_list/stream_snapshot as source "scene"
	}
}

// compositeTiles lays tiles onto a WxH canvas per layout. Nearest-neighbor scale
// (dep-free); good enough for a monitoring/watch composite.
func compositeTiles(tiles []image.Image, layout string, w, h int) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	n := len(tiles)
	place := func(img image.Image, dx, dy, dw, dh int) {
		dst := image.Rect(dx, dy, dx+dw, dy+dh)
		drawScaled(canvas, dst, img)
	}
	switch layout {
	case "pip":
		place(tiles[0], 0, 0, w, h)
		// up to 3 small insets bottom-right
		pw, ph := w/4, h/4
		for i := 1; i < n && i <= 3; i++ {
			place(tiles[i], w-pw-8, h-(ph+8)*i, pw, ph)
		}
	case "row":
		cw := w / n
		for i, t := range tiles {
			place(t, i*cw, 0, cw, h)
		}
	default: // grid
		cols := 1
		for cols*cols < n {
			cols++
		}
		rows := (n + cols - 1) / cols
		cw, ch := w/cols, h/rows
		for i, t := range tiles {
			place(t, (i%cols)*cw, (i/cols)*ch, cw, ch)
		}
	}
	return canvas
}

// drawScaled scales src into the dst rect of dstImg via nearest-neighbor.
func drawScaled(dstImg *image.RGBA, dst image.Rectangle, src image.Image) {
	sb := src.Bounds()
	dw, dh := dst.Dx(), dst.Dy()
	if dw <= 0 || dh <= 0 || sb.Dx() == 0 || sb.Dy() == 0 {
		return
	}
	tmp := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := sb.Min.Y + y*sb.Dy()/dh
		for x := 0; x < dw; x++ {
			sx := sb.Min.X + x*sb.Dx()/dw
			tmp.Set(x, y, src.At(sx, sy))
		}
	}
	draw.Draw(dstImg, dst, tmp, image.Point{}, draw.Over)
}
