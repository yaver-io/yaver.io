package main

// remotedesktop_http.go — HTTP surface for Remote Desktop (see remotedesktop.go
// for the consent model). All routes are behind s.auth (same-account identity);
// this file adds the runtime policy gate + the live-frame plumbing.
//
// Routes:
//   GET  /rd/status      → capabilities + policy + displays + stream state
//   POST /rd/policy      → owner updates view/control toggles
//   GET  /rd/stream      → MJPEG (multipart/x-mixed-replace), owner view
//   GET  /rd/frame.jpg   → single latest frame (poll fallback / thumbnails)
//   POST /rd/input       → batched mouse/keyboard events → injected on the box
//
// Frames are captured + JPEG-encoded locally and streamed straight back over
// the auth'd relay/mesh tunnel; nothing touches Convex (privacy contract).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/yaver-io/agent/ghost"
)

var (
	rdNotifyMu   sync.Mutex
	rdLastNotify time.Time
)

// rdInputEvent is one mouse/keyboard action from a viewer. Points are
// normalized (0..1) fractions of the displayed frame; the agent de-normalizes
// against the live display bounds so it works regardless of retina scaling.
type rdInputEvent struct {
	Type   string   `json:"type"` // move|click|double|drag|scroll|text|key
	NX     float64  `json:"nx"`
	NY     float64  `json:"ny"`
	ToNX   float64  `json:"tonx"`
	ToNY   float64  `json:"tony"`
	Button string   `json:"button"`
	DX     int      `json:"dx"`
	DY     int      `json:"dy"`
	Text   string   `json:"text"`
	Keys   []string `json:"keys"`
}

func rdButton(s string) ghost.Button {
	switch s {
	case "right":
		return ghost.ButtonRight
	case "middle":
		return ghost.ButtonMiddle
	default:
		return ghost.ButtonLeft
	}
}

// rdPrimaryDisplay returns the primary display (or index 0) for coord scaling.
func rdPrimaryDisplay(eng *ghost.Engine) (ghost.Display, error) {
	disps, err := eng.Screen.Displays()
	if err != nil {
		return ghost.Display{}, err
	}
	if len(disps) == 0 {
		return ghost.Display{}, fmt.Errorf("no displays")
	}
	for _, d := range disps {
		if d.Primary {
			return d, nil
		}
	}
	return disps[0], nil
}

func (s *HTTPServer) handleRemoteDesktopStatus(w http.ResponseWriter, r *http.Request) {
	pol := loadRemoteDesktopPolicy()
	resp := map[string]interface{}{
		"supported":          ghost.Supported(),
		"viewEnabled":        pol.ViewEnabled,
		"controlEnabled":     pol.ControlEnabled,
		"allowRemoteControl": pol.AllowRemoteControl,
		"notifyOnControl":    pol.NotifyOnControl,
		"streaming":          ghostStream.running(),
		"fps":                ghostStream.curFps(),
		"streamUrl":          "/rd/stream",
		"frameUrl":           "/rd/frame.jpg",
	}
	// Best-effort display enumeration so the client can size/scale. Don't fail
	// status if the engine isn't constructable yet (e.g. missing OS perms) —
	// surface the error string instead so the UI can prompt the user.
	if eng, err := s.ensureGhost(); err == nil {
		if disps, derr := eng.Screen.Displays(); derr == nil {
			resp["displays"] = disps
		} else {
			resp["displaysError"] = derr.Error()
		}
	} else {
		resp["engineError"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleRemoteDesktopPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		ViewEnabled        *bool `json:"viewEnabled"`
		ControlEnabled     *bool `json:"controlEnabled"`
		AllowRemoteControl *bool `json:"allowRemoteControl"`
		NotifyOnControl    *bool `json:"notifyOnControl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	pol := loadRemoteDesktopPolicy()
	if body.ViewEnabled != nil {
		pol.ViewEnabled = *body.ViewEnabled
	}
	if body.ControlEnabled != nil {
		pol.ControlEnabled = *body.ControlEnabled
	}
	if body.AllowRemoteControl != nil {
		pol.AllowRemoteControl = *body.AllowRemoteControl
	}
	if body.NotifyOnControl != nil {
		pol.NotifyOnControl = *body.NotifyOnControl
	}
	if err := saveRemoteDesktopPolicy(pol); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save policy: "+err.Error())
		return
	}
	appendRemoteDesktopAudit(rdAuditEntry{
		Action: "policy",
		Remote: !isLoopbackAddr(r.RemoteAddr),
		Note:   fmt.Sprintf("view=%v control=%v allowRemote=%v", pol.ViewEnabled, pol.ControlEnabled, pol.AllowRemoteControl),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"viewEnabled":        pol.ViewEnabled,
		"controlEnabled":     pol.ControlEnabled,
		"allowRemoteControl": pol.AllowRemoteControl,
		"notifyOnControl":    pol.NotifyOnControl,
	})
}

func (s *HTTPServer) handleRemoteDesktopStream(w http.ResponseWriter, r *http.Request) {
	pol := loadRemoteDesktopPolicy()
	if ok, reason := rdViewEnforce(pol); !ok {
		jsonError(w, http.StatusForbidden, reason)
		return
	}
	if !ghostStream.running() {
		eng, err := s.ensureGhost()
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, "screen capture unavailable: "+err.Error())
			return
		}
		if err := ghostStream.start(eng, 5); err != nil {
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

func (s *HTTPServer) handleRemoteDesktopFrame(w http.ResponseWriter, r *http.Request) {
	pol := loadRemoteDesktopPolicy()
	if ok, reason := rdViewEnforce(pol); !ok {
		jsonError(w, http.StatusForbidden, reason)
		return
	}
	if !ghostStream.running() {
		if eng, err := s.ensureGhost(); err == nil {
			_ = ghostStream.start(eng, 5)
			time.Sleep(400 * time.Millisecond)
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

func (s *HTTPServer) handleRemoteDesktopInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	pol := loadRemoteDesktopPolicy()
	remote := !isLoopbackAddr(r.RemoteAddr) || isRelayBridged(r)
	if ok, reason := rdControlEnforce(pol, remote); !ok {
		appendRemoteDesktopAudit(rdAuditEntry{Action: "deny", Remote: remote, Note: reason})
		jsonError(w, http.StatusForbidden, reason)
		return
	}
	var body struct {
		Events []rdInputEvent `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	eng, err := s.ensureGhost()
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, "input unavailable: "+err.Error())
		return
	}
	disp, err := rdPrimaryDisplay(eng)
	if err != nil {
		jsonError(w, http.StatusServiceUnavailable, "no display: "+err.Error())
		return
	}

	s.maybeNotifyRemoteControl(pol, remote, body.Events)

	applied := 0
	var firstErr error
	for _, ev := range body.Events {
		var e error
		x, y := rdScalePoint(ev.NX, ev.NY, disp)
		switch ev.Type {
		case "move":
			e = eng.Input.Move(x, y)
		case "click":
			e = eng.Input.Click(rdButton(ev.Button), x, y)
		case "double":
			e = eng.Input.DoubleClick(rdButton(ev.Button), x, y)
		case "drag":
			tx, ty := rdScalePoint(ev.ToNX, ev.ToNY, disp)
			e = eng.Input.Drag(rdButton(ev.Button), x, y, tx, ty)
		case "scroll":
			e = eng.Input.Scroll(ev.DX, ev.DY)
		case "text":
			if ev.Text != "" {
				e = eng.Input.TypeText(ev.Text)
			}
		case "key":
			if len(ev.Keys) > 0 {
				e = eng.Input.KeyCombo(ev.Keys...)
			}
		default:
			continue
		}
		if e != nil {
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		applied++
	}
	if firstErr != nil && applied == 0 {
		jsonError(w, http.StatusInternalServerError, "input failed: "+firstErr.Error())
		return
	}
	resp := map[string]interface{}{"ok": true, "applied": applied}
	if firstErr != nil {
		resp["partialError"] = firstErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// maybeNotifyRemoteControl posts a throttled desktop toast + push when a remote
// caller injects a meaningful action (not just hover/move), so the person at the
// keyboard always knows their box is being driven.
func (s *HTTPServer) maybeNotifyRemoteControl(pol RemoteDesktopPolicy, remote bool, events []rdInputEvent) {
	if !remote || !pol.NotifyOnControl {
		return
	}
	meaningful := false
	for _, ev := range events {
		switch ev.Type {
		case "click", "double", "drag", "text", "key":
			meaningful = true
		}
		if meaningful {
			break
		}
	}
	if !meaningful {
		return
	}
	rdNotifyMu.Lock()
	if !rdLastNotify.IsZero() && time.Since(rdLastNotify) < 5*time.Minute {
		rdNotifyMu.Unlock()
		return
	}
	rdLastNotify = time.Now()
	rdNotifyMu.Unlock()

	appendRemoteDesktopAudit(rdAuditEntry{Action: "control", Remote: true})
	const detail = "Another device on your account is controlling this machine via Remote Desktop. Turn it off in Remote Desktop settings if this wasn't you."
	if globalNotifyManager != nil {
		globalNotifyManager.NotifyAgentEvent("Remote Desktop control started", detail)
	}
	defaultDesktopNotify("Yaver: remote control active", "A device on your account is driving this machine.")
}
