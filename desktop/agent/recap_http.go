package main

// recap_http.go — HTTP surface for recaps.
//
// Modelled on vibe_preview_clip_http.go, which already solved the hard part
// of serving video off a box to every surface: http.ServeContent gives Range,
// If-Modified-Since, and chunked streaming for free. Range matters more here
// than anywhere — tvOS's AVPlayer seeks, VR scrubbing seeks, and a phone on
// cellular must not re-pull an 80-second video to jump to the end.
//
// Everything is bearer-authed. Note the shape this forces on clients: neither
// AsyncImage (tvOS) nor THREE.TextureLoader (VR) can set headers, so those
// surfaces fetch to a blob/AVURLAsset with explicit headers. FeedbackView.swift
// and AppScreenPlane3D.tsx both already do this — a recap client copies them.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

// handleRecaps — GET  /recaps?autorun=<id>&slot=<slot>&tag=<tag>&limit=<n>
//
//	POST /recaps/build
//	POST /recaps/prune
func (s *HTTPServer) handleRecaps(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet:
		recs, err := listRecaps(RecapFilter{
			AutorunID: strings.TrimSpace(r.URL.Query().Get("autorun")),
			Slot:      strings.TrimSpace(r.URL.Query().Get("slot")),
			Tag:       strings.TrimSpace(r.URL.Query().Get("tag")),
			Limit:     atoiOr(r.URL.Query().Get("limit"), 0),
		})
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if recs == nil {
			recs = []*RecapRecord{}
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "recaps": recs})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleRecapBuild — POST /recaps/build
//
// Synchronous by design for an explicit request: the caller asked for a
// recap and wants to know whether it worked. The autorun hook uses BuildRecap
// directly in its own goroutine instead.
func (s *HTTPServer) handleRecapBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var opts RecapBuildOpts
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rec, err := BuildRecap(r.Context(), opts)
	if err != nil {
		// A missing ffmpeg is a 503 with an install hint, not a 400 — the
		// request was fine, the box isn't ready. Same convention as
		// handleVibePreviewClipStart.
		if strings.Contains(err.Error(), "ffmpeg not found") {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "recap": rec})
}

// handleRecap — GET    /recap/<id>              → metadata JSON
//
//	GET    /recap/<id>/video         → MP4 (Range)
//	GET    /recap/<id>/poster        → JPEG
//	GET    /recap/<id>/subtitles.vtt → WebVTT
//	DELETE /recap/<id>               → remove
func (s *HTTPServer) handleRecap(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/recap/")
	tail = strings.Trim(tail, "/ ")
	if tail == "" {
		jsonError(w, http.StatusBadRequest, "recap id required")
		return
	}

	id, sub := tail, ""
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		id, sub = tail[:i], tail[i+1:]
	}
	// recapValidID enforces r_<hex>, so no path component can survive it.
	// This is the only gate between a URL and the filesystem.
	if !recapValidID(id) {
		jsonError(w, http.StatusBadRequest, "invalid recap id")
		return
	}

	if r.Method == http.MethodDelete && sub == "" {
		if err := deleteRecap(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rec, err := loadRecap(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	dir, err := recapDir(id)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	var path, contentType string
	switch sub {
	case "":
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "recap": rec})
		return
	case "video":
		if rec.Status != RecapStatusReady {
			// 409 rather than 404: the recap exists, it just isn't watchable
			// yet. A client can poll instead of giving up.
			jsonError(w, http.StatusConflict, "recap not ready (status: "+rec.Status+")")
			return
		}
		path, contentType = recapVideoPath(dir), "video/mp4"
	case "poster":
		path, contentType = recapPosterPath(dir), "image/jpeg"
	case "subtitles.vtt", "subtitles":
		if !rec.HasSubtitles {
			jsonError(w, http.StatusNotFound, "no subtitles for this recap")
			return
		}
		path, contentType = recapSubtitlesPath(dir), "text/vtt; charset=utf-8"
	default:
		jsonError(w, http.StatusNotFound, "unknown recap artifact")
		return
	}

	f, err := os.Open(path)
	if err != nil {
		jsonError(w, http.StatusNotFound, "recap artifact missing on disk")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "stat recap: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	// Artifacts are immutable once the recap is ready. Private because the
	// bytes are a picture of the user's screen.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	// Range + If-Modified-Since + chunked streaming, all free.
	http.ServeContent(w, r, path, st.ModTime(), f)
}

// handleRecapConfig — GET/POST /recap/config
func (s *HTTPServer) handleRecapConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": loadRecapConfig()})
	case http.MethodPost:
		var cfg RecapConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if err := saveRecapConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": loadRecapConfig()})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// buildRecapAsync runs a build off-request and logs the outcome. Used by the
// autorun hook, where there is no caller to return an error to.
func buildRecapAsync(opts RecapBuildOpts) {
	go func() {
		rec, err := BuildRecap(context.Background(), opts)
		if err != nil {
			// Visible failure over silent retry: a recap that never appears
			// should say why in the log, not just be absent.
			log.Printf("[recap] build failed (autorun=%s tag=%s): %v", opts.AutorunID, opts.Tag, err)
			return
		}
		log.Printf("[recap] built %s (%s, %.0fs, %d frames) for autorun=%s",
			rec.ID, rec.Tag, rec.DurationSec, rec.Frames, opts.AutorunID)
	}()
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
		if n > 100000 {
			return def
		}
	}
	return n
}
