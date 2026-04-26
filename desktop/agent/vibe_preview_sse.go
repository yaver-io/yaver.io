package main

// vibe_preview_sse.go — Phase 2 wire protocol: SSE event stream + binary
// frame GET. Mirrors the /dev/events pattern (15 s keepalive, replay on
// subscribe, X-Accel-Buffering: no for Cloudflare).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleVibePreviewEvents — GET /vibing/preview/events?project=<name>
//
// SSE channel of frame / stable / clip_* / summary / lifecycle events for
// one project. Replays the recent event log on subscribe so a late
// dashboard reconnect catches up.
func (s *HTTPServer) handleVibePreviewEvents(w http.ResponseWriter, r *http.Request) {
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Standard SSE headers — same shape as /dev/events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Cloudflare buffer kill
	w.WriteHeader(http.StatusOK)

	ch, history, unsubscribe := s.vibePreviewMgr.Subscribe(project)
	defer unsubscribe()

	// Replay history first so the consumer's timeline lands fully populated.
	for _, ev := range history {
		if err := writeVibePreviewSSE(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	// Live stream + 15 s keepalive ticker.
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case ev, open := <-ch:
			if !open {
				// Manager dropped us (Stop was called) — close cleanly.
				return
			}
			if err := writeVibePreviewSSE(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeVibePreviewSSE writes one event as a single SSE message ("data: <json>\n\n").
func writeVibePreviewSSE(w http.ResponseWriter, ev VibePreviewEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// handleVibePreviewFrame — GET /vibing/preview/frames/<hash>?project=<name>
//
// Binary PNG response. Content-addressed → infinite immutable cache.
// Returns 404 if the project has no record for that hash.
func (s *HTTPServer) handleVibePreviewFrame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}

	// Path: /vibing/preview/frames/<hash>
	hash := strings.TrimPrefix(r.URL.Path, "/vibing/preview/frames/")
	hash = strings.Trim(hash, "/ ")
	if hash == "" || strings.ContainsAny(hash, "/\\.") {
		jsonError(w, http.StatusBadRequest, "invalid frame hash")
		return
	}

	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		jsonError(w, http.StatusBadRequest, "project query param required")
		return
	}

	bytes, err := s.vibePreviewMgr.ReadFrameBytes(project, hash)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bytes)))
	// Content-addressed: identical hash always returns identical bytes,
	// so an aggressive cache-control is safe. The mobile <Image> cache
	// dedup uses the URL, so this is the lever that stops re-downloads.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bytes)
}
