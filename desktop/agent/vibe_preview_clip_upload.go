package main

// vibe_preview_clip_upload.go — Phase 5 agent side: receive an MP4 from
// the developer's own phone (recorded via ReplayKit on iOS or
// MediaProjection on Android by the Yaver mobile app's super-host) and
// land it as a clip ready for playback.
//
// Flow:
//   1. Mobile calls /vibing/preview/clip/start with source="phone".
//      Agent allocates clip ID + path; the placeholder recorder
//      (currently a sleep) starts the lifecycle so the existing
//      watchClipRecorder path applies.
//   2. Mobile records to a local file (RPScreenRecorder/AVAssetWriter
//      on iOS, MediaProjection+MediaRecorder on Android).
//   3. When recording finishes, mobile POSTs the MP4 here.
//   4. Agent writes bytes to clip.Path, kills the sleep placeholder,
//      lets watchClipRecorder finalize (mark ready, extract poster,
//      emit clip_ready).
//
// Auth: owner-only (mutating). The mobile app already has the owner
// token after pair flow.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// vibeClipUploadMaxBytes caps the upload request body. Phone clips are
// 8-15 s at 720p; 50 MB is more than generous and well below the relay
// 100 MB request cap. A larger limit risks OOM if a buggy client streams
// forever.
const vibeClipUploadMaxBytes = 50 * 1024 * 1024

// handleVibePreviewClipUpload — POST /vibing/preview/clip/upload
//
// Headers:
//   X-Yaver-Clip-ID:    required, must match an existing recording clip
//   Content-Type:       video/mp4 (or octet-stream — we write bytes either way)
//
// Body: raw MP4 bytes. Multipart is overkill for a single-file upload;
// raw body keeps the mobile native code simpler (one URLSession upload
// task, no body framing).
func (s *HTTPServer) handleVibePreviewClipUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vibePreviewMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "vibe preview not initialised")
		return
	}

	clipID := strings.TrimSpace(r.Header.Get("X-Yaver-Clip-ID"))
	if clipID == "" {
		// Allow the clip ID via query param too — easier to debug from
		// curl, and matches the GET endpoint shape.
		clipID = strings.TrimSpace(r.URL.Query().Get("clipId"))
	}
	if clipID == "" {
		jsonError(w, http.StatusBadRequest, "X-Yaver-Clip-ID header (or ?clipId=) required")
		return
	}

	rec := s.vibePreviewMgr.ClipByID(clipID)
	if rec == nil {
		jsonError(w, http.StatusNotFound, "clip not found — call /clip/start first")
		return
	}
	if rec.Source != string(VibeClipSourcePhone) {
		jsonError(w, http.StatusConflict,
			fmt.Sprintf("clip source is %q; only phone-source clips accept uploads", rec.Source))
		return
	}
	if rec.Status != "recording" {
		jsonError(w, http.StatusConflict,
			fmt.Sprintf("clip status is %q; can only upload to a recording clip", rec.Status))
		return
	}

	// Path discipline: clip records are created by StartClip with a
	// derived path; refuse if it points anywhere we wouldn't write to.
	dir := filepath.Dir(rec.Path)
	if dir == "" || dir == "/" || strings.HasPrefix(dir, "/proc") || strings.HasPrefix(dir, "/sys") {
		jsonError(w, http.StatusInternalServerError, "clip path looks invalid")
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		jsonError(w, http.StatusInternalServerError, "mkdir clip dir: "+err.Error())
		return
	}

	// Cap the body so a misbehaving client can't OOM the agent.
	body := http.MaxBytesReader(w, r.Body, int64(vibeClipUploadMaxBytes))
	defer body.Close()

	f, err := os.OpenFile(rec.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "open mp4: "+err.Error())
		return
	}
	written, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil {
		// MaxBytesReader returns *http.MaxBytesError on cap; surface 413.
		if strings.Contains(copyErr.Error(), "request body too large") {
			jsonError(w, http.StatusRequestEntityTooLarge, "clip exceeds 50 MB cap")
			return
		}
		jsonError(w, http.StatusInternalServerError, "write mp4: "+copyErr.Error())
		return
	}
	if closeErr != nil {
		jsonError(w, http.StatusInternalServerError, "close mp4: "+closeErr.Error())
		return
	}
	if written == 0 {
		jsonError(w, http.StatusBadRequest, "empty body")
		return
	}

	// Stop the placeholder recorder so watchClipRecorder fires the
	// finalize path (extracts poster, marks ready, emits clip_ready).
	// StopClip is idempotent + tolerates an already-finished recorder.
	_ = s.vibePreviewMgr.StopClip(clipID)

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"clipId":    clipID,
		"sizeBytes": written,
	})
}
