package main

// browser_interactive_http.go — HTTP endpoints for the generic interactive /
// human-in-the-loop co-browse feature. A remote UI polls /frame for JPEG
// frames and POSTs raw input to /input so a human can solve a captcha or log
// in, after which automation resumes against the same persistent session.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// profileDirFor returns the on-disk persistent profile directory for a session.
func profileDirFor(id string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".yaver", "browser-profiles", id)
}

// handleBrowserInteractiveStart (POST /browser/interactive/start) opens a
// headful co-browse session, navigates to a URL, optionally prefills fields,
// and returns the frame/input paths for the remote UI.
func (s *HTTPServer) handleBrowserInteractiveStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}

	var req struct {
		SessionID string `json:"session_id"`
		URL       string `json:"url"`
		Profile   string `json:"profile"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Prefill   []struct {
			Selector string `json:"selector"`
			Value    string `json:"value"`
		} `json:"prefill"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, `{"error":"url is required"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("cobrowse-%d", time.Now().UnixMilli()%100000)
	}
	if req.Width <= 0 {
		req.Width = 1280
	}
	if req.Height <= 0 {
		req.Height = 800
	}
	profileDir := req.Profile
	if profileDir == "" {
		profileDir = profileDirFor(req.SessionID)
	}
	_ = os.MkdirAll(profileDir, 0o755)

	if err := s.browserMgr.OpenInteractiveSession(req.SessionID, profileDir, req.Width, req.Height); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	if _, err := s.browserMgr.Navigate(req.SessionID, req.URL); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}

	for _, p := range req.Prefill {
		if p.Selector == "" {
			continue
		}
		// Best-effort: a failed prefill should not abort the co-browse.
		_ = s.browserMgr.Prefill(req.SessionID, p.Selector, p.Value)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id": req.SessionID,
		"frame_path": "/browser/interactive/frame/" + req.SessionID,
		"input_path": "/browser/interactive/input/" + req.SessionID,
		"width":      req.Width,
		"height":     req.Height,
	})
}

// interactiveID extracts the {id} segment after the given prefix.
func interactiveID(r *http.Request, prefix string) string {
	path := strings.TrimPrefix(r.URL.Path, prefix)
	path = strings.TrimPrefix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	return parts[0]
}

// handleBrowserInteractiveFrame (GET /browser/interactive/frame/{id}) returns
// the current page as a JPEG image.
func (s *HTTPServer) handleBrowserInteractiveFrame(w http.ResponseWriter, r *http.Request) {
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}
	id := interactiveID(r, "/browser/interactive/frame")
	if id == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	quality := 0
	if q := r.URL.Query().Get("quality"); q != "" {
		fmt.Sscanf(q, "%d", &quality)
	}
	buf, err := s.browserMgr.FrameJPEG(id, quality)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf)
}

// handleBrowserInteractiveInput (POST /browser/interactive/input/{id}) relays a
// single mouse/keyboard/scroll event into the live session.
func (s *HTTPServer) handleBrowserInteractiveInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}
	id := interactiveID(r, "/browser/interactive/input")
	if id == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	var req struct {
		Type string  `json:"type"` // "click" | "key" | "scroll"
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		Text string  `json:"text"`
		DY   float64 `json:"dy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	var err error
	switch req.Type {
	case "click":
		err = s.browserMgr.InjectClick(id, req.X, req.Y)
	case "key":
		err = s.browserMgr.InjectKeys(id, req.Text)
	case "scroll":
		err = s.browserMgr.InjectScroll(id, req.X, req.Y, req.DY)
	default:
		http.Error(w, `{"error":"type must be one of click|key|scroll"}`, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleBrowserInteractiveStatus (GET /browser/interactive/status/{id}) returns
// the current URL and title.
func (s *HTTPServer) handleBrowserInteractiveStatus(w http.ResponseWriter, r *http.Request) {
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}
	id := interactiveID(r, "/browser/interactive/status")
	if id == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	url, title, err := s.browserMgr.InteractiveStatus(id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url":   url,
		"title": title,
	})
}

// handleBrowserInteractiveStop (POST or DELETE /browser/interactive/stop/{id})
// closes the session. The on-disk profile persists for later automation.
func (s *HTTPServer) handleBrowserInteractiveStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}
	id := interactiveID(r, "/browser/interactive/stop")
	if id == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	if err := s.browserMgr.CloseSession(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "stopped",
		"profile": "persisted",
	})
}
