// Command robotd serves the Yaver Robot Protocol (docs/robot-protocol.md) over
// HTTP, backed by the existing ender_ui bridge + a gst camera. It exists so the
// move-and-verify protocol is live and phone-callable on a box that isn't yet
// running the full agent; the identical logic (the robot package) drops into
// the agent as the robot_* ops verbs.
//
//	YAVER_ROBOT_BRIDGE  ender_ui base URL (default http://127.0.0.1:8330)
//	YAVER_ROBOT_CAMERA  v4l2 device       (default /dev/video0)
//	YAVER_ROBOT_ADDR    listen address    (default :8336)
//	YAVER_ROBOT_TOKEN   optional bearer token required on every request
//	OPENAI_* / GHOST_VISION_*  vision provider (else verify degrades to frames mode)
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/yaver-io/agent/robot"
)

type moveReq struct {
	Axis        string   `json:"axis"`
	Dist        float64  `json:"dist"`
	X           *float64 `json:"x"`
	Y           *float64 `json:"y"`
	Z           *float64 `json:"z"`
	Feed        int      `json:"feed"`
	Axes        string   `json:"axes"`
	On          *bool    `json:"on"`
	Verify      string   `json:"verify"`
	Expectation string   `json:"expectation"`
}

func main() {
	bridge := env("YAVER_ROBOT_BRIDGE", "http://127.0.0.1:8330")
	camDev := env("YAVER_ROBOT_CAMERA", "/dev/video0")
	addr := env("YAVER_ROBOT_ADDR", ":8336")
	token := os.Getenv("YAVER_ROBOT_TOKEN")

	toolMode := env("YAVER_ROBOT_TOOL", "screw") // "fan" = screwdriver on the FAN port (M106/M107)
	toolPin := atoiDef(os.Getenv("YAVER_ROBOT_TOOL_PIN"), 6)

	// Backend selection (docs/yaver-robotics-edge-vibing.md):
	//   YAVER_ROBOT_SERIAL_FD=N  → native Marlin over the termux-usb fd (phone host)
	//   YAVER_ROBOT_SERIAL=/dev/ttyUSB0 → native Marlin over a serial port (no bridge)
	//   else                     → the ender_ui HTTP bridge (default)
	var backend robot.Backend
	switch {
	case os.Getenv("YAVER_ROBOT_SERIAL_FD") != "":
		fd := atoiDef(os.Getenv("YAVER_ROBOT_SERIAL_FD"), -1)
		f := os.NewFile(uintptr(fd), "usbserial")
		if f == nil {
			log.Fatalf("bad YAVER_ROBOT_SERIAL_FD=%s", os.Getenv("YAVER_ROBOT_SERIAL_FD"))
		}
		sb := robot.NewSerialBackend(f, toolMode, toolPin)
		if err := sb.Settle(context.Background()); err != nil {
			log.Printf("serial settle (fd %d): %v", fd, err)
		}
		backend = sb
		log.Printf("backend: marlin serial via termux fd %d (tool=%s)", fd, toolMode)
	case os.Getenv("YAVER_ROBOT_SERIAL") != "":
		dev := os.Getenv("YAVER_ROBOT_SERIAL")
		rw, err := openSerialPort(dev)
		if err != nil {
			log.Fatalf("open serial %s: %v", dev, err)
		}
		sb := robot.NewSerialBackend(rw, toolMode, toolPin)
		sb.Reopen = func() (io.ReadWriteCloser, error) { return openSerialPort(dev) } // USB-glitch recovery
		if err := sb.Settle(context.Background()); err != nil {
			log.Printf("serial settle (%s): %v", dev, err)
		}
		backend = sb
		log.Printf("backend: marlin serial via %s (tool=%s)", dev, toolMode)
	default:
		bb := robot.NewBridgeBackend(bridge)
		bb.ToolMode = toolMode
		backend = bb
		log.Printf("backend: ender_ui bridge %s (tool=%s)", bridge, toolMode)
	}
	ctrl := robot.NewController(backend, robot.NewGstCamera(camDev), robot.VisionConfig{})
	ctrl.StrictEncoder = os.Getenv("YAVER_ROBOT_STRICT") == "1" // e-stop on encoder mismatch

	// Optional torque/GPIO companion MCU on a second serial port
	// (YAVER_ROBOT_COMPANION=/dev/ttyUSB1). Absent → torque-gated screw verbs
	// return {code:"no_companion"} and the rest works unchanged.
	if compDev := os.Getenv("YAVER_ROBOT_COMPANION"); compDev != "" {
		if rw, err := openCompanionSerial(compDev); err != nil {
			log.Printf("companion: cannot open %s: %v", compDev, err)
		} else {
			ctrl.Companion = robot.NewLineCompanion(rw)
			log.Printf("companion: attached on %s", compDev)
		}
	}

	mux := http.NewServeMux()
	guard := func(h func(context.Context, moveReq) any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
				writeJSON(w, 401, map[string]any{"ok": false, "code": "unauthorized", "error": "bad token"})
				return
			}
			var req moveReq
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&req)
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			writeJSON(w, 200, h(ctx, req))
		}
	}

	mux.HandleFunc("/robot/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		st, _ := ctrl.Status(ctx)
		writeJSON(w, 200, st)
	})
	// Single JPEG snapshot — the iOS-robust camera path (WKWebView does NOT
	// render multipart MJPEG in <img>; the mobile CameraStream polls this).
	mux.HandleFunc("/robot/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.URL.Query().Get("token") != token && r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, 401, map[string]any{"ok": false, "code": "unauthorized"})
			return
		}
		cam := robot.NewGstCamera(camDev)
		cam.Buffers = 3 // continuous polling → exposure already settled, grab fast
		if !cam.Available() {
			writeJSON(w, 503, map[string]any{"ok": false, "code": "no_camera"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		jpg, err := cam.Grab(ctx)
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(jpg)
	})
	// Live camera stream (multipart MJPEG). Open from a phone/Talos <img> or
	// WebView: http://<host>:8336/robot/stream
	mux.HandleFunc("/robot/stream", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.URL.Query().Get("token") != token && r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, 401, map[string]any{"ok": false, "code": "unauthorized"})
			return
		}
		cam := robot.NewGstCamera(camDev)
		if !cam.Available() {
			writeJSON(w, 503, map[string]any{"ok": false, "code": "no_camera"})
			return
		}
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+robot.MJPEGBoundary)
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Connection", "close")
		flush := func() {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		_ = cam.StreamMJPEG(r.Context(), w, flush, 10)
	})
	mux.HandleFunc("/robot/home", guard(func(ctx context.Context, q moveReq) any {
		return ctrl.Home(ctx, q.Axes, q.Verify, q.Expectation)
	}))
	mux.HandleFunc("/robot/jog", guard(func(ctx context.Context, q moveReq) any {
		return ctrl.Jog(ctx, q.Axis, q.Dist, q.Feed, q.Verify, q.Expectation)
	}))
	mux.HandleFunc("/robot/move", guard(func(ctx context.Context, q moveReq) any {
		return ctrl.Move(ctx, q.X, q.Y, q.Z, q.Feed, q.Verify, q.Expectation)
	}))
	mux.HandleFunc("/robot/tool", guard(func(ctx context.Context, q moveReq) any {
		on := q.On != nil && *q.On
		return ctrl.Tool(ctx, on)
	}))
	mux.HandleFunc("/robot/verify", guard(func(ctx context.Context, q moveReq) any {
		return ctrl.Verify(ctx, q.Expectation)
	}))
	// Torque-gated screw drive (needs a companion). Body = robot.ScrewParams
	// plus {verify, expectation}.
	mux.HandleFunc("/robot/screw", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, 401, map[string]any{"ok": false, "code": "unauthorized"})
			return
		}
		var body struct {
			robot.ScrewParams
			Verify      string `json:"verify"`
			Expectation string `json:"expectation"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		writeJSON(w, 200, ctrl.DriveScrew(ctx, body.ScrewParams, body.Verify, body.Expectation))
	})
	mux.HandleFunc("/robot/estop", guard(func(ctx context.Context, q moveReq) any {
		_ = ctrl.EStop(ctx)
		return map[string]any{"ok": true, "estopped": true}
	}))
	mux.HandleFunc("/robot/reset", guard(func(ctx context.Context, q moveReq) any {
		ctrl.Reset()
		return map[string]any{"ok": true, "estopped": false}
	}))

	log.Printf("robotd: bridge=%s camera=%s listening on %s", bridge, camDev, addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// openSerialPort raw-configures a tty (115200 8N1) and opens it R/W. No serial
// library: stty + os.OpenFile, the same minimal approach the agent uses. Used
// for both the Marlin port and the companion MCU.
func openSerialPort(dev string) (io.ReadWriteCloser, error) {
	_ = exec.Command("stty", "-F", dev, "115200", "cs8", "-cstopb", "-parenb",
		"-crtscts", "-ixon", "-ixoff", "clocal", "raw", "-echo").Run()
	return os.OpenFile(dev, os.O_RDWR, 0)
}

func openCompanionSerial(dev string) (io.ReadWriteCloser, error) { return openSerialPort(dev) }

func atoiDef(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
