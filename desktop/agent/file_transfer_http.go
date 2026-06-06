package main

// file_transfer_http.go — owner-authed absolute-path file get/put for fleet
// file transfer (Fleet SDK Machine.upload/download). Registered with s.auth and
// deliberately absent from every guest/support allowlist, so only the owner
// bearer passes — the same trust level as /exec, which can already read or
// write any file the agent user can. Rides whatever transport the SDK resolved
// (direct-LAN / tunnel / relay / mesh), so transfers work behind NAT with no
// extra plumbing.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// maxFleetFileBytes caps a single transfer to keep a runaway upload from
// filling the disk / OOMing a small edge box. 256 MB covers build artifacts,
// model shards, and project tarballs; larger payloads should be chunked.
const maxFleetFileBytes = 256 << 20

// handleFleetFile dispatches by method: GET downloads, POST/PUT uploads.
func (s *HTTPServer) handleFleetFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.fleetFileGet(w, r)
	case http.MethodPost, http.MethodPut:
		s.fleetFilePut(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET (download) or POST/PUT (upload)")
	}
}

// fleetFileGet streams an absolute-path file back to the caller, advertising
// its permission bits via X-Yaver-File-Mode so the receiver can preserve them.
func (s *HTTPServer) fleetFileGet(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !filepath.IsAbs(p) {
		jsonError(w, http.StatusBadRequest, "absolute path required")
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		jsonError(w, http.StatusNotFound, "not a file")
		return
	}
	if info.Size() > maxFleetFileBytes {
		jsonError(w, http.StatusRequestEntityTooLarge, "file too large (max 256MB; chunk it)")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Yaver-File-Mode", strconv.FormatUint(uint64(info.Mode().Perm()), 8))
	http.ServeFile(w, r, p)
}

// fleetFilePut writes the request body to an absolute path (creating parent
// dirs), preserving the mode from ?mode=<octal> when provided.
func (s *HTTPServer) fleetFilePut(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !filepath.IsAbs(p) {
		jsonError(w, http.StatusBadRequest, "absolute path required")
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		jsonError(w, http.StatusInternalServerError, "mkdir: "+err.Error())
		return
	}
	mode := os.FileMode(0o644)
	if m := r.URL.Query().Get("mode"); m != "" {
		if parsed, err := strconv.ParseUint(m, 8, 32); err == nil && parsed != 0 {
			mode = os.FileMode(parsed).Perm()
		}
	}
	body := http.MaxBytesReader(w, r.Body, maxFleetFileBytes)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "open: "+err.Error())
		return
	}
	defer f.Close()
	n, err := io.Copy(f, body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "write: "+err.Error())
		return
	}
	_ = os.Chmod(p, mode)
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "bytes": n, "path": p, "mode": strconv.FormatUint(uint64(mode), 8)})
}
