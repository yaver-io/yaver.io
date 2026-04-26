package main

// vibe_preview_http.go — HTTP handlers for /vibing/preview/*.
//
// Mounted under /vibing/* so the existing guest-vibing scope prefix
// (httpserver.go scopePathPrefixes) covers reads automatically. Mutating
// endpoints (start/stop) still gate on full owner auth.
//
// Phase 1: start, stop, status, snapshot. SSE event stream + binary frame
// fetch land in Phase 2.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// handleVibePreviewStart — POST /vibing/preview/start
//
// Body: {project, targetUrl, mode?, profile?, netMode?}
// On success: 200 + the new session JSON. On already-active project: 409.
// On missing browser/Chromium: 503 with install hint.
func (s *HTTPServer) handleVibePreviewStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}

	var opts VibePreviewStartOpts
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// X-Yaver-NetMode header is the cellular-aware hint from the mobile
	// client. Body field wins if both are set so callers can override.
	if opts.NetMode == "" {
		opts.NetMode = strings.TrimSpace(r.Header.Get("X-Yaver-NetMode"))
	}

	sess, err := s.vibePreviewMgr.Start(opts)
	if err != nil {
		// 409 for "already active", 503 for "no browser", 400 for the rest.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "already active"):
			jsonError(w, http.StatusConflict, msg)
		case strings.Contains(msg, "browser automation unavailable"):
			jsonError(w, http.StatusServiceUnavailable, msg)
		default:
			jsonError(w, http.StatusBadRequest, msg)
		}
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"session": sess,
	})
}

// handleVibePreviewStop — POST /vibing/preview/stop
// Body: {project}
func (s *HTTPServer) handleVibePreviewStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}
	var req struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Project) == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	if err := s.vibePreviewMgr.Stop(req.Project); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleVibePreviewStatus — GET /vibing/preview/status
// Returns every active session (no frame data).
func (s *HTTPServer) handleVibePreviewStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sessions := []*VibePreviewSession{}
	if s.vibePreviewMgr != nil {
		sessions = s.vibePreviewMgr.Status()
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"sessions": sessions,
	})
}

// handleVibePreviewSummaries — GET /vibing/preview/summaries?project=X&limit=N
//
// Returns the most-recent N summaries from the on-disk JSONL log,
// newest first. Default limit 50, max 500.
func (s *HTTPServer) handleVibePreviewSummaries(w http.ResponseWriter, r *http.Request) {
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
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"summaries": s.vibePreviewMgr.ListSummaries(project, limit),
	})
}

// handleVibePreviewSnapshot — POST /vibing/preview/snapshot
// Body: {project}
// Forces one capture and returns the new frame's metadata (no bytes — those
// land via /vibing/preview/frames/:hash in Phase 2).
func (s *HTTPServer) handleVibePreviewSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}
	var req struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Project) == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	rec, err := s.vibePreviewMgr.Snapshot(req.Project)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"seq":        rec.Seq,
		"hash":       rec.Hash,
		"size":       len(rec.Bytes),
		"capturedAt": rec.CapturedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}
