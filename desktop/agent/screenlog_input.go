package main

// screenlog_input.go — the input-event companion stream for screenlog:
// keystrokes + mouse clicks/moves/scroll, recorded alongside the frame
// images so a session is a {screenshot, action} trace — exactly the shape
// GUI-agent / computer-use models train on.
//
// ## Data model (deliberately standard, for later AI training)
//
// Each event is one JSON object, appended to `events.jsonl` in the session
// dir. The schema matches the de-facto computer-use action format
// (timestamp + action type + pixel coords + key), so a recorded session
// pairs 1:1 with frames for imitation-learning / RPA replay:
//
//   {"t":1717,"type":"click","x":840,"y":210,"button":"left","screenW":2560,"screenH":1440,"display":0}
//   {"t":1718,"type":"key","key":"Enter"}
//   {"t":1719,"type":"scroll","dx":0,"dy":-3}
//
// Pixel coords are absolute; screenW/H travel with the event so a consumer
// can normalize to 0..1 (the convention most GUI-agent datasets use). This
// is the "replicate the human" trace that feeds the ghost API
// (observe→understand→replicate).
//
// ## Capture is decoupled from storage (producer model)
//
// Global keyboard/mouse hooks are OS-specific + permission-gated + a
// genuine abuse surface, so the agent does NOT ship a built-in global
// keylogger yet. Instead any PRODUCER posts events to
// `POST /screenlog/<id>/events`: a native per-OS helper (macOS CGEventTap,
// Windows SetWindowsHookEx, Linux evdev/XRecord), a browser extension, the
// mobile SDK, etc. The model + storage + redaction + correlation live here
// so producers stay thin. (Native hooks = the next phase; see the doc.)
//
// ## Privacy — this is far more sensitive than screenshots
//
// Keylogging can capture passwords. So input capture is OFF unless BOTH
// ScreenlogConfig.CaptureInput AND ScreenlogPolicy.AllowInputCapture are
// true (a separate, stronger gate than screen capture), it's audited, and
// RedactText (default ON) replaces typed characters with a placeholder —
// preserving the action structure ("a key was pressed", named keys like
// Enter/Tab/Cmd) without storing the secret. Everything stays local.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InputEvent is one recorded input action. JSON tags are the on-disk +
// on-wire schema — keep them stable for training reproducibility.
type InputEvent struct {
	T       int64  `json:"t"`           // unix ms
	Type    string `json:"type"`        // click|mousedown|mouseup|move|scroll|keydown|keyup|key|text
	X       int    `json:"x,omitempty"` // absolute screen px
	Y       int    `json:"y,omitempty"`
	Button  string `json:"button,omitempty"` // left|right|middle
	Key     string `json:"key,omitempty"`    // key name: "a", "Enter", "Cmd", "ArrowLeft"
	Text    string `json:"text,omitempty"`   // committed text (redacted unless disabled)
	DX      int    `json:"dx,omitempty"`     // scroll delta x
	DY      int    `json:"dy,omitempty"`     // scroll delta y
	Display int    `json:"display,omitempty"`
	ScreenW int    `json:"screenW,omitempty"`
	ScreenH int    `json:"screenH,omitempty"`
}

var validInputTypes = map[string]bool{
	"click": true, "mousedown": true, "mouseup": true, "move": true,
	"scroll": true, "keydown": true, "keyup": true, "key": true, "text": true,
}

func screenlogEventsPath(id string) (string, error) {
	dir, err := screenlogSessionDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "events.jsonl"), nil
}

// redactInputEvent strips secret content while preserving the action
// structure: single printable characters → "•", longer text → its length.
// Named keys (Enter, Tab, Cmd, arrows, F-keys) survive — they carry intent,
// not secrets.
func redactInputEvent(e InputEvent) InputEvent {
	if e.Key != "" && isPrintableKey(e.Key) {
		e.Key = "•"
	}
	if e.Text != "" {
		e.Text = fmt.Sprintf("[redacted:%d]", len([]rune(e.Text)))
	}
	return e
}

func isPrintableKey(k string) bool {
	r := []rune(k)
	if len(r) != 1 {
		return false // named key like "Enter"
	}
	return r[0] >= 0x20 && r[0] != 0x7f
}

// ingestInputEvents validates, optionally redacts, and appends a batch to
// the session's companion file. Returns how many were written.
func ingestInputEvents(id string, events []InputEvent, redact bool) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	if _, err := loadScreenlogSession(id); err != nil {
		return 0, fmt.Errorf("session not found: %s", id)
	}
	p, err := screenlogEventsPath(id)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	for _, e := range events {
		if !validInputTypes[e.Type] || e.T <= 0 {
			continue
		}
		if redact {
			e = redactInputEvent(e)
		}
		data, _ := json.Marshal(e)
		if _, err := f.Write(append(data, '\n')); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func readInputEvents(id string, limit int) ([]InputEvent, error) {
	p, err := screenlogEventsPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []InputEvent{}, nil
		}
		return nil, err
	}
	var out []InputEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e InputEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// inputStats summarizes an event stream — the cheap, deterministic "how
// active were the hands" signal that complements the screen activity
// report (clicks/min, keystrokes, scroll, span).
func inputStats(events []InputEvent) map[string]interface{} {
	var clicks, keys, scrolls, moves int
	var first, last int64
	for _, e := range events {
		if first == 0 || e.T < first {
			first = e.T
		}
		if e.T > last {
			last = e.T
		}
		switch e.Type {
		case "click", "mousedown":
			clicks++
		case "key", "keydown":
			keys++
		case "scroll":
			scrolls++
		case "move":
			moves++
		}
	}
	spanSec := 0
	if last > first {
		spanSec = int((last - first) / 1000)
	}
	perMin := 0.0
	if spanSec > 0 {
		perMin = float64(clicks+keys) * 60 / float64(spanSec)
	}
	return map[string]interface{}{
		"total": len(events), "clicks": clicks, "keys": keys,
		"scrolls": scrolls, "moves": moves, "spanSec": spanSec,
		"actionsPerMin": round1Pct(perMin),
	}
}

func round1Pct(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// inputCaptureDriver describes the input-capture story for the drivers
// report. Today it's ingest-only (producers POST events); native global
// hooks are the roadmap.
func inputCaptureDriver() map[string]interface{} {
	return map[string]interface{}{
		"mode":      "ingest", // producers POST to /screenlog/<id>/events
		"native":    false,    // no built-in global keyboard/mouse hook yet
		"roadmap":   "macOS CGEventTap · Windows SetWindowsHookEx (host exe, also from WSL) · Linux evdev/XRecord",
		"schema":    "events.jsonl — {t,type,x,y,button,key,text,dx,dy,display,screenW,screenH}",
		"redaction": "typed characters redacted unless explicitly disabled",
	}
}
