package main

// ops_vision.go — local-first camera vision (docs/yaver-single-kumanda.md §10c).
// The cheap, on-box gate before any LLM: brightness (is the frame dark?) and
// frame-difference motion. Pure Go (image/jpeg), no model, no cloud — so it runs
// free and offline. The OPTIONAL LLM layer is the agent calling camera_snapshot
// and reasoning over the image; this file is the gate that decides when that's
// worth doing, and it backs the activity engine's closed-loop verify
// (frameIsBlack → "did the TV actually turn on?").

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"strings"
	"time"
)

var errEmptyImage = errors.New("empty image")

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "camera_motion",
		Description: "Local motion check on a camera: grabs two frames and returns {motion, score, dark}. Cheap on-box gate before any LLM. Payload {id} (registered) or {url}.",
		Schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"id":  map[string]interface{}{"type": "string"},
			"url": map[string]interface{}{"type": "string"},
		}},
		Handler: cameraMotionHandler,
	})
}

func cameraMotionHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	url := strings.TrimSpace(p.URL)
	if url == "" && strings.TrimSpace(p.ID) != "" {
		rec, ok := cameraGet(strings.TrimSpace(p.ID))
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "camera not found: " + p.ID}
		}
		url = rec.URL
	}
	if url == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id or url is required"}
	}
	a, err := cameraGrabFrame(c.Ctx, url)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	time.Sleep(600 * time.Millisecond)
	b, err := cameraGrabFrame(c.Ctx, url)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	score, derr := frameDiffScore(a, b)
	if derr != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: derr.Error()}
	}
	const motionThreshold = 0.04
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"motion": score > motionThreshold,
		"score":  score,
		"dark":   frameIsBlack(b),
	}}
}

// lumaGridSize is the sampled grid resolution for cheap whole-frame stats.
const lumaGridSize = 24

// decodeLumaGrid samples a jpeg into an N×N grid of luma values in [0,1].
func decodeLumaGrid(jpegBytes []byte, n int) ([]float64, error) {
	img, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return nil, errEmptyImage
	}
	grid := make([]float64, n*n)
	for gy := 0; gy < n; gy++ {
		for gx := 0; gx < n; gx++ {
			x := b.Min.X + (dx*gx)/n + dx/(2*n)
			y := b.Min.Y + (dy*gy)/n + dy/(2*n)
			grid[gy*n+gx] = lumaAt(img, x, y)
		}
	}
	return grid, nil
}

func lumaAt(img image.Image, x, y int) float64 {
	r, g, b, _ := img.At(x, y).RGBA() // each 0..65535
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
}

func gridMean(grid []float64) float64 {
	if len(grid) == 0 {
		return 0
	}
	var sum float64
	for _, v := range grid {
		sum += v
	}
	return sum / float64(len(grid))
}

// frameMeanLuma returns the average brightness of a jpeg in [0,1] (0 on decode
// failure, treated as dark).
func frameMeanLuma(jpegBytes []byte) float64 {
	grid, err := decodeLumaGrid(jpegBytes, lumaGridSize)
	if err != nil {
		return 0
	}
	return gridMean(grid)
}

// frameIsBlack reports whether a frame is near-black (source dark / HDCP blank /
// device off). Used by the activity engine's closed-loop verify.
func frameIsBlack(jpegBytes []byte) bool {
	return frameMeanLuma(jpegBytes) < 0.06
}

// frameDiffScore returns the mean absolute luma difference between two frames in
// [0,1] — the cheap local motion signal.
func frameDiffScore(a, b []byte) (float64, error) {
	ga, err := decodeLumaGrid(a, lumaGridSize)
	if err != nil {
		return 0, err
	}
	gb, err := decodeLumaGrid(b, lumaGridSize)
	if err != nil {
		return 0, err
	}
	n := len(ga)
	if len(gb) < n {
		n = len(gb)
	}
	if n == 0 {
		return 0, nil
	}
	var sum float64
	for i := 0; i < n; i++ {
		d := ga[i] - gb[i]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return sum / float64(n), nil
}
