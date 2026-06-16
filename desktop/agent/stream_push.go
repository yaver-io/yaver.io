package main

// stream_push.go — M10: a phone (or any client) pushes its OWN camera frames to
// a box, which buffers them and serves them through the same stream plane
// (stream_list / stream_snapshot) — so a phone camera becomes a shareable source
// to the owner's account or a guest watch link, with no inbound connection to
// the phone. Mirrors the robot external-camera push, but lives in the neutral
// stream plane (not robot-framed).
//
// Agnostic, like the rest of the streaming: the box stores and serves whatever
// frames are pushed; what you stream and the right to it is the user's.

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type pushedFrame struct {
	b64  string // base64 JPEG (no data: prefix)
	mime string
	at   time.Time
}

var (
	pushedMu     sync.Mutex
	pushedFrames = map[string]pushedFrame{}
)

// pushedFreshWindow — a pushed source is "live" only while frames keep arriving.
const pushedFreshWindow = 12 * time.Second

func setPushedFrame(name, b64, mime string) {
	if mime == "" {
		mime = "image/jpeg"
	}
	pushedMu.Lock()
	pushedFrames[name] = pushedFrame{b64: b64, mime: mime, at: time.Now()}
	pushedMu.Unlock()
}

// getPushedFrame returns the latest frame for name if it's still fresh.
func getPushedFrame(name string) (pushedFrame, bool) {
	pushedMu.Lock()
	defer pushedMu.Unlock()
	f, ok := pushedFrames[name]
	if !ok || time.Since(f.at) > pushedFreshWindow {
		return pushedFrame{}, false
	}
	return f, true
}

// listFreshPushed returns the names of pushed sources still receiving frames.
func listFreshPushed() []string {
	pushedMu.Lock()
	defer pushedMu.Unlock()
	out := []string{}
	for name, f := range pushedFrames {
		if time.Since(f.at) <= pushedFreshWindow {
			out = append(out, name)
		}
	}
	return out
}

// POST /stream/push?name=<name>  body: {"jpegB64": "...", "mime": "image/jpeg"}
// Owner-auth (the phone is signed in). Stores one frame into the named buffer.
func (s *HTTPServer) handleStreamPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "phone"
	}
	var body struct {
		JpegB64 string `json:"jpegB64"`
		Mime    string `json:"mime"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body); err != nil || body.JpegB64 == "" {
		jsonError(w, http.StatusBadRequest, "expected {jpegB64}")
		return
	}
	setPushedFrame(name, body.JpegB64, body.Mime)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
