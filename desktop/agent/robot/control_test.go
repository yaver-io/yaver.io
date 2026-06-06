package robot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeBridge is a stand-in for the ender_ui server: a real HTTP server on a
// random port (repo convention: no mocks). It tracks a position that updates on
// move/jog and answers M114/M400 like Marlin via the bridge.
type fakeBridge struct {
	mu       sync.Mutex
	x, y, z  float64
	homed    bool
	lastTool bool
	estop    bool
	server   *httptest.Server
}

func newFakeBridge() *fakeBridge {
	f := &fakeBridge{z: 0}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/home", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.x, f.y, f.z, f.homed = 0, 0, 0, true
		f.mu.Unlock()
		ok(w)
	})
	mux.HandleFunc("/api/move", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]float64
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		if v, k := p["x"]; k {
			f.x = v
		}
		if v, k := p["y"]; k {
			f.y = v
		}
		if v, k := p["z"]; k {
			f.z = v
		}
		f.mu.Unlock()
		ok(w)
	})
	mux.HandleFunc("/api/jog", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		axis, _ := p["axis"].(string)
		dist, _ := p["dist"].(float64)
		f.mu.Lock()
		switch axis {
		case "X":
			f.x += dist
		case "Y":
			f.y += dist
		case "Z":
			f.z += dist
		}
		f.mu.Unlock()
		ok(w)
	})
	mux.HandleFunc("/api/screw", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		f.lastTool = p["on"]
		f.mu.Unlock()
		ok(w)
	})
	mux.HandleFunc("/api/estop", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.estop = true
		f.mu.Unlock()
		ok(w)
	})
	mux.HandleFunc("/api/gcode", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p["line"] == "M114" {
			f.mu.Lock()
			reply := fmt.Sprintf("X:%.2f Y:%.2f Z:%.2f E:0.00 Count X:0 Y:0 Z:0\nok", f.x, f.y, f.z)
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"ok": true, "reply": reply})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "reply": "ok"}) // M400 etc.
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		st := map[string]any{"ok": true, "state": map[string]any{
			"connected": true, "homed": f.homed, "screw": "off",
			"position": map[string]any{"x": f.x, "y": f.y, "z": f.z},
		}}
		f.mu.Unlock()
		writeJSON(w, 200, st)
	})
	f.server = httptest.NewServer(mux)
	return f
}

func ok(w http.ResponseWriter) { writeJSON(w, 200, map[string]any{"ok": true, "reply": "ok"}) }
func writeJSON(w http.ResponseWriter, c int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c)
	_ = json.NewEncoder(w).Encode(v)
}

type fakeCam struct{}

func (fakeCam) Grab(ctx context.Context) ([]byte, error) { return []byte("\xff\xd8\xff\xd9"), nil }
func (fakeCam) Available() bool                          { return true }

func newTestController(t *testing.T) (*Controller, *fakeBridge) {
	t.Helper()
	fb := newFakeBridge()
	t.Cleanup(fb.server.Close)
	c := NewController(NewBridgeBackend(fb.server.URL), fakeCam{}, VisionConfig{})
	return c, fb
}

func TestMoveCrossCheckAgrees(t *testing.T) {
	c, _ := newTestController(t)
	ctx := context.Background()
	// frames mode → no vision call needed.
	x, y, z := 110.0, 110.0, 25.0
	resp := c.Move(ctx, &x, &y, &z, 3000, "frames", "carriage to center, Z up")
	if !resp.OK {
		t.Fatalf("move failed: %s", resp.Error)
	}
	if resp.Position == nil || resp.Position.Z != 25 {
		t.Fatalf("position not read back: %+v", resp.Position)
	}
	if resp.Cross == nil || !resp.Cross.Agree {
		t.Fatalf("cross-check should agree: %+v", resp.Cross)
	}
	if resp.Frames == nil || resp.Frames.After == "" {
		t.Fatalf("frames should be attached in frames mode")
	}
}

func TestMoveOutOfRangeRefused(t *testing.T) {
	c, _ := newTestController(t)
	z := 999.0
	resp := c.Move(context.Background(), nil, nil, &z, 0, "off", "")
	if resp.OK || resp.Code != "out_of_range" {
		t.Fatalf("expected out_of_range refusal, got %+v", resp)
	}
}

func TestJogRequiresHoming(t *testing.T) {
	c, _ := newTestController(t)
	resp := c.Jog(context.Background(), "Z", 10, 600, "off", "up")
	if resp.OK || resp.Code != "not_homed" {
		t.Fatalf("expected not_homed, got %+v", resp)
	}
}

func TestHomeThenJog(t *testing.T) {
	c, _ := newTestController(t)
	ctx := context.Background()
	if h := c.Home(ctx, "", "off", ""); !h.OK {
		t.Fatalf("home failed: %s", h.Error)
	}
	resp := c.Jog(ctx, "Z", 10, 600, "off", "carriage up 10mm")
	if !resp.OK {
		t.Fatalf("jog failed: %s", resp.Error)
	}
	if resp.Cross == nil || !resp.Cross.Agree || resp.Cross.ObservedDelta["z"] != 10 {
		t.Fatalf("jog cross-check wrong: %+v", resp.Cross)
	}
}

func TestEStopLatches(t *testing.T) {
	c, _ := newTestController(t)
	ctx := context.Background()
	_ = c.Home(ctx, "", "off", "")
	if err := c.EStop(ctx); err != nil {
		t.Fatalf("estop: %v", err)
	}
	z := 50.0
	resp := c.Move(ctx, nil, nil, &z, 0, "off", "")
	if resp.OK || resp.Code != "estopped" {
		t.Fatalf("expected estopped refusal, got %+v", resp)
	}
	c.Reset()
	_ = c.Home(ctx, "", "off", "") // reset re-requires homing
	resp = c.Move(ctx, nil, nil, &z, 0, "off", "")
	if !resp.OK {
		t.Fatalf("after reset+home, move should work: %+v", resp)
	}
}
