package main

// droid_interactive_http.go — HTTP endpoints for the generic interactive /
// human-in-the-loop Android device control feature. A remote UI polls /droid/frame
// for PNG frames and POSTs raw input to /droid/input so a human can log in (e.g.
// enter an SMS OTP) on a paired phone, after which automation drives the same
// device. Mirrors browser_interactive_http.go in JSON/error conventions.
//
// GENERIC: knows no specific app. adb is reached via the shared helpers in
// droid_interactive.go (which shell out to the `adb` binary on PATH).

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// droidResolveDevice returns the requested device serial, falling back to the
// first attached `device`-state serial. Empty result means no usable device.
func droidResolveDevice(requested string) string {
	if requested != "" {
		return requested
	}
	return droidPickDevice()
}

// handleDroidStatus (GET /droid/status[?device=]) returns the current device,
// screen size, and focused activity. Gracefully returns {device:null} with 200
// when no device is attached (parallels browser status conventions).
func (s *HTTPServer) handleDroidStatus(w http.ResponseWriter, r *http.Request) {
	serial := droidResolveDevice(r.URL.Query().Get("device"))
	w.Header().Set("Content-Type", "application/json")
	if serial == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"device": nil,
			"w":      0,
			"h":      0,
			"focus":  "",
		})
		return
	}
	wpx, hpx := droidSize(serial)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device": serial,
		"w":      wpx,
		"h":      hpx,
		"focus":  droidFocus(serial),
	})
}

// handleDroidFrame (GET /droid/frame[?device=]) returns the live screen as PNG.
func (s *HTTPServer) handleDroidFrame(w http.ResponseWriter, r *http.Request) {
	serial := droidResolveDevice(r.URL.Query().Get("device"))
	if serial == "" {
		http.Error(w, `{"error":"no android device attached"}`, http.StatusServiceUnavailable)
		return
	}
	buf, err := droidFrame(serial)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf)
}

// handleDroidInput (POST /droid/input) relays a single tap/text/key/swipe event.
func (s *HTTPServer) handleDroidInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Type    string `json:"type"` // "tap" | "text" | "key" | "swipe"
		X       int    `json:"x"`
		Y       int    `json:"y"`
		Text    string `json:"text"`
		Keycode int    `json:"keycode"`
		X1      int    `json:"x1"`
		Y1      int    `json:"y1"`
		X2      int    `json:"x2"`
		Y2      int    `json:"y2"`
		Dur     int    `json:"dur"`
		Device  string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	serial := droidResolveDevice(req.Device)
	if serial == "" {
		http.Error(w, `{"error":"no android device attached"}`, http.StatusServiceUnavailable)
		return
	}
	var err error
	switch req.Type {
	case "tap":
		err = droidTap(serial, req.X, req.Y)
	case "text":
		err = droidText(serial, req.Text)
	case "key":
		err = droidKey(serial, req.Keycode)
	case "swipe":
		err = droidSwipe(serial, req.X1, req.Y1, req.X2, req.Y2, req.Dur)
	default:
		http.Error(w, `{"error":"type must be one of tap|text|key|swipe"}`, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleDroidUI (GET /droid/ui[?device=]) returns the on-screen text values via
// a uiautomator dump — useful for reading login fields / labels.
func (s *HTTPServer) handleDroidUI(w http.ResponseWriter, r *http.Request) {
	serial := droidResolveDevice(r.URL.Query().Get("device"))
	if serial == "" {
		http.Error(w, `{"error":"no android device attached"}`, http.StatusServiceUnavailable)
		return
	}
	texts, err := droidUITexts(serial)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	if texts == nil {
		texts = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"texts": texts})
}

// handleDroidLaunch (POST /droid/launch) launches an installed app whose package
// id contains {package}, via the LAUNCHER intent.
func (s *HTTPServer) handleDroidLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Package string `json:"package"`
		Device  string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Package == "" {
		http.Error(w, `{"error":"package is required"}`, http.StatusBadRequest)
		return
	}
	serial := droidResolveDevice(req.Device)
	if serial == "" {
		http.Error(w, `{"error":"no android device attached"}`, http.StatusServiceUnavailable)
		return
	}
	pkg, err := droidLaunchPackage(serial, req.Package)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"package":  pkg,
		"launched": true,
	})
}
