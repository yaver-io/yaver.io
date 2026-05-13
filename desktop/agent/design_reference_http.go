package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

const maxDesignReferenceUploadSize = 100 << 20 // 100 MB — references are smaller than feedback bug reports

// handleDesignReferences handles POST /design-references (upload) and
// GET /design-references (list).
func (s *HTTPServer) handleDesignReferences(w http.ResponseWriter, r *http.Request) {
	if s.designRefMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "design references not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, s.designRefMgr.List())

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxDesignReferenceUploadSize)
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart: " + err.Error()})
			return
		}
		metadata := r.FormValue("metadata")
		if metadata == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing metadata field"})
			return
		}
		files := make(map[string][]byte)
		for key, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}
				name := fh.Filename
				if name == "" {
					name = key
				}
				files[name] = data
			}
		}
		ref, err := s.designRefMgr.ReceiveReference(json.RawMessage(metadata), files)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, ref)

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleDesignReferenceByID handles
//
//	GET    /design-references/{id}                  — full record
//	DELETE /design-references/{id}                  — remove + cleanup
//	GET    /design-references/{id}/screenshot/<name> — serve PNG/JPG
//	GET    /design-references/{id}/html             — serve dom.html
//	GET    /design-references/{id}/styles           — serve styles.json
func (s *HTTPServer) handleDesignReferenceByID(w http.ResponseWriter, r *http.Request) {
	if s.designRefMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "design references not available"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/design-references/")
	parts := strings.SplitN(path, "/", 3)
	id := parts[0]
	if id == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing reference id"})
		return
	}
	ref, ok := s.designRefMgr.Get(id)
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "design reference not found"})
		return
	}

	if len(parts) > 1 {
		switch parts[1] {
		case "screenshot":
			if len(parts) < 3 || parts[2] == "" {
				jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing screenshot name"})
				return
			}
			s.serveDesignReferenceFile(w, r, ref, parts[2])
			return
		case "html":
			s.serveDesignReferenceFile(w, r, ref, "dom.html")
			return
		case "styles":
			s.serveDesignReferenceFile(w, r, ref, "styles.json")
			return
		default:
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "unknown sub-route"})
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, ref)
	case http.MethodDelete:
		if err := s.designRefMgr.Delete(id); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *HTTPServer) serveDesignReferenceFile(w http.ResponseWriter, r *http.Request, ref *DesignReference, name string) {
	safe := sanitizeFeedbackUploadName(name)
	if safe == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid filename"})
		return
	}
	refDir := filepath.Join(s.designRefMgr.baseDir, ref.ID)
	filePath := filepath.Join(refDir, safe)
	// Reuse the feedback path-containment helper — it's a general "under
	// baseDir?" check after symlink resolution, not feedback-specific.
	if !pathInsideFeedbackBaseDir(s.designRefMgr.baseDir, filePath) {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "path escapes base dir"})
		return
	}
	http.ServeFile(w, r, filePath)
}
