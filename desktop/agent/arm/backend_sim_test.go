package arm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeHarness implements the sim harness HTTP contract with stdlib only, so we
// test the Go SimBackend (lifecycle reuse + delegation + sim extras) without
// PyBullet. Mirrors the repo convention: real HTTP server on a real port.
func fakeHarness(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	loaded := 0
	mux := http.NewServeMux()
	describe := map[string]any{
		"model": "Generic 6-DOF (built-in)", "vendor": "Simulator", "dof": 2,
		"hasCartesian": true, "poseFrame": "base", "source": "robot",
		"joints": []map[string]any{
			{"name": "J1", "type": "revolute", "min": -170.0, "max": 170.0, "home": 0.0, "unit": "deg"},
			{"name": "J2", "type": "revolute", "min": -120.0, "max": 120.0, "home": 0.0, "unit": "deg"},
		},
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "engine": "pybullet"})
	})
	mux.HandleFunc("/describe", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(describe)
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"joints": []map[string]any{{"name": "J1", "position": 12.0, "unit": "deg"},
				{"name": "J2", "position": -3.0, "unit": "deg"}},
			"pose": map[string]any{"x": 100, "y": 0, "z": 400, "roll": 0, "pitch": 0, "yaw": 0},
		})
	})
	mux.HandleFunc("/movej", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/enable", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		loaded++
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "info": map[string]any{
			"model": body.Model, "vendor": "Simulator",
			"joints": []map[string]any{{"name": "A", "type": "revolute", "min": -90.0, "max": 90.0, "unit": "deg"}},
		}})
	})
	mux.HandleFunc("/frame.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &loaded
}

func simBackendForServer(t *testing.T, url string) *SimBackend {
	t.Helper()
	port, err := strconv.Atoi(url[strings.LastIndex(url, ":")+1:])
	if err != nil {
		t.Fatalf("parse port from %s: %v", url, err)
	}
	cfg := Config{Driver: "sim"}
	cfg.Sim.Port = port
	cfg.Normalize()
	return NewSimBackend(cfg)
}

func TestSimBackendReusesRunningHarness(t *testing.T) {
	srv, _ := fakeHarness(t)
	sb := simBackendForServer(t, srv.URL)

	ctx := context.Background()
	if err := sb.Connect(ctx); err != nil {
		t.Fatalf("Connect (reuse): %v", err)
	}
	// Connect must NOT have spawned a python process (we reused the live one).
	if sb.spawned {
		t.Error("should not spawn when a harness already answers on the port")
	}

	info, err := sb.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if info.DOF != 2 || info.Joints[0].Name != "J1" {
		t.Errorf("describe = %+v", info)
	}

	st, err := sb.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Backend != "sim" {
		t.Errorf("status backend = %q, want sim", st.Backend)
	}

	if err := sb.MoveJoints(ctx, map[string]float64{"J1": 30}, 50, 50); err != nil {
		t.Fatalf("MoveJoints: %v", err)
	}
}

func TestSimBackendLoadAndReset(t *testing.T) {
	srv, loaded := fakeHarness(t)
	sb := simBackendForServer(t, srv.URL)
	ctx := context.Background()
	if err := sb.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	info, err := sb.LoadModel(ctx, "desc:ur5e")
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if info.DOF != 1 || info.Joints[0].Name != "A" {
		t.Errorf("loaded info = %+v", info)
	}
	if sb.model != "desc:ur5e" {
		t.Errorf("model = %q, want desc:ur5e", sb.model)
	}
	if *loaded != 1 {
		t.Errorf("/load called %d times, want 1", *loaded)
	}
	if err := sb.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

func TestSimBackendFrameURL(t *testing.T) {
	cfg := Config{Driver: "sim"}
	cfg.Sim.Port = 19999
	cfg.Normalize()
	sb := NewSimBackend(cfg)
	if got := sb.FrameURL(); got != "http://127.0.0.1:19999/frame.jpg" {
		t.Errorf("FrameURL = %q", got)
	}
}
