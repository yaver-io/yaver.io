package main

// vibe_preview_clip_http.go — HTTP surface for vibe-preview clips.
// Range-aware GET on the MP4 lets mobile players seek without
// re-downloading; that's important on cellular.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// handleVibePreviewClipStart — POST /vibing/preview/clip/start
func (s *HTTPServer) handleVibePreviewClipStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}
	var opts VibeClipStartOpts
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rec, err := s.vibePreviewMgr.StartClip(opts)
	if err != nil {
		// Toolchain missing → 503 so the mobile UI surfaces an
		// install hint, not a "bad request".
		if strings.Contains(err.Error(), "no clip source available") {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"clip": rec,
	})
}

// handleVibePreviewClipStop — POST /vibing/preview/clip/stop
// Body: {clipId}
func (s *HTTPServer) handleVibePreviewClipStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}
	var req struct {
		ClipID string `json:"clipId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ClipID) == "" {
		jsonError(w, http.StatusBadRequest, "clipId is required")
		return
	}
	if err := s.vibePreviewMgr.StopClip(req.ClipID); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	rec := s.vibePreviewMgr.ClipByID(req.ClipID)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "clip": rec})
}

// handleVibePreviewClips — GET /vibing/preview/clips?project=<name>
func (s *HTTPServer) handleVibePreviewClips(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		jsonError(w, http.StatusBadRequest, "project query param required")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"clips": s.vibePreviewMgr.ListClips(project),
	})
}

// handleVibePreviewClip — GET /vibing/preview/clip/<id>
//
// Streams the MP4 with Range support via http.ServeContent. The poster
// suffix returns the JPEG thumbnail.
func (s *HTTPServer) handleVibePreviewClip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}

	tail := strings.TrimPrefix(r.URL.Path, "/vibing/preview/clip/")
	tail = strings.Trim(tail, "/ ")
	if tail == "" {
		jsonError(w, http.StatusBadRequest, "clip id required")
		return
	}

	wantPoster := false
	id := tail
	if strings.HasSuffix(tail, "/poster") {
		id = strings.TrimSuffix(tail, "/poster")
		wantPoster = true
	}
	// Block any path-component shenanigans before we touch the
	// filesystem. Clip IDs are c_<hex> by construction.
	if strings.ContainsAny(id, "/\\.") {
		jsonError(w, http.StatusBadRequest, "invalid clip id")
		return
	}

	rec := s.vibePreviewMgr.ClipByID(id)
	if rec == nil {
		jsonError(w, http.StatusNotFound, "clip not found")
		return
	}
	if rec.Status != "ready" {
		jsonError(w, http.StatusConflict, "clip not ready (status: "+rec.Status+")")
		return
	}

	path := rec.Path
	contentType := "video/mp4"
	if wantPoster {
		path = rec.PosterPath
		contentType = "image/jpeg"
		if path == "" {
			jsonError(w, http.StatusNotFound, "no poster (ffmpeg may be missing)")
			return
		}
	}

	f, err := os.Open(path)
	if err != nil {
		jsonError(w, http.StatusNotFound, "clip artifact missing on disk")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "stat clip: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	// MP4s + posters are immutable once written. Private cache because
	// the path embeds a per-user clip ID.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	// http.ServeContent handles If-Modified-Since, Range, and chunked
	// streaming for us — much better than rolling our own.
	http.ServeContent(w, r, path, st.ModTime(), f)
}
