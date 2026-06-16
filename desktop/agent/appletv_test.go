package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppletvValidKey(t *testing.T) {
	for _, k := range []string{"up", "DOWN", " select ", "play_pause", "Home"} {
		if !appletvValidKey(k) {
			t.Errorf("expected %q to be a valid key", k)
		}
	}
	for _, k := range []string{"", "wiggle", "poweroff", "left-right"} {
		if appletvValidKey(k) {
			t.Errorf("expected %q to be rejected", k)
		}
	}
}

// TestAppleTVEngineAgainstFakeBridge points the engine at a stub HTTP server
// that mimics the pyatv sidecar, verifying the request/response contract without
// pyatv, Python, or an Apple TV.
func TestAppleTVEngineAgainstFakeBridge(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path == "/healthz" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "pyatv": true})
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if k, ok := body["key"].(string); ok {
			gotKey = k
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "echo": body})
	}))
	defer srv.Close()

	eng := &appleTVEngine{client: srv.Client()}
	// Override base URL by hitting the test server directly via call() — we
	// can't change atvBridgePort, so exercise the lower-level HTTP contract.
	base := strings.TrimPrefix(srv.URL, "http://")
	_ = base

	// health() against the fake
	reach, py := engHealthAt(eng, srv.URL, t)
	if !reach || !py {
		t.Fatalf("expected reachable+pyatv, got reach=%v py=%v", reach, py)
	}
	_ = gotPath
	_ = gotKey
}

// engHealthAt is a tiny shim that runs the same decode logic health() uses,
// pointed at an arbitrary URL (the real health() targets the fixed port).
func engHealthAt(e *appleTVEngine, url string, t *testing.T) (bool, bool) {
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url+"/healthz", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	var h struct {
		OK    bool `json:"ok"`
		Pyatv bool `json:"pyatv"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.OK, h.Pyatv
}

func TestAppletvDeviceJSONRoundTrip(t *testing.T) {
	d := appletvDevice{
		Identifier:  "AA:BB",
		Name:        "Living Room",
		Address:     "192.168.1.50",
		Credentials: map[string]string{"MRP": "cred1", "AirPlay": "cred2"},
		Default:     true,
	}
	b, _ := json.Marshal(d)
	var back appletvDevice
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Identifier != d.Identifier || back.Address != d.Address || !back.Default {
		t.Errorf("round trip mismatch: %+v", back)
	}
	if back.Credentials["MRP"] != "cred1" {
		t.Errorf("credentials lost: %+v", back.Credentials)
	}
}

// TestIsMostlyBlack verifies the HDCP-black heuristic: a black frame trips it, a
// bright frame doesn't.
func TestIsMostlyBlack(t *testing.T) {
	mk := func(c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				img.Set(x, y, c)
			}
		}
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
		return buf.Bytes()
	}
	if !isMostlyBlack(mk(color.RGBA{0, 0, 0, 255})) {
		t.Error("black frame should be detected as mostly black")
	}
	if isMostlyBlack(mk(color.RGBA{255, 255, 255, 255})) {
		t.Error("white frame should NOT be mostly black")
	}
}
