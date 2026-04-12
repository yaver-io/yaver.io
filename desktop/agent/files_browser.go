package main

// files_browser.go — read-only filesystem browser exposed over
// HTTP so the mobile app can peek into discovered projects from
// the couch. Scoped hard to the set of project roots the agent
// already knows about (via PROJECTS.md / repo discovery) so
// nobody can escape the sandbox and walk /etc/passwd.
//
// No writes. The surface is intentionally tiny:
//
//   GET /files/roots                      list allowed roots
//   GET /files/list?root=<id>&path=<sub>  list a directory
//   GET /files/read?root=<id>&path=<sub>  read a text file
//
// Everything goes through the normal auth() middleware. Guests
// see only the projects their GuestConfigManager.CheckProject
// accepts; owner sees everything.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FileRoot is the public-facing view of a project root.
type FileRoot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// FileEntry is one entry in a directory listing.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// MaxReadableFileSize caps /files/read to 2 MB. Anything larger
// comes down as a truncated preview — mobile isn't the place to
// open 100 MB log files.
const MaxReadableFileSize = 2 * 1024 * 1024

// listFileRoots returns the project roots the current token may
// browse. Owner gets everything; guest gets filtered by
// GuestConfigManager.CheckProject.
func (s *HTTPServer) listFileRoots(r *http.Request) []FileRoot {
	projects := listDiscoveredProjects()
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	out := make([]FileRoot, 0, len(projects))
	for _, p := range projects {
		if p.Path == "" {
			continue
		}
		if guestUID != "" && s.guestConfigMgr != nil {
			if denied := s.guestConfigMgr.CheckProject(guestUID, p.Path); denied != nil {
				continue
			}
		}
		out = append(out, FileRoot{
			ID:   projectFSID(p.Path),
			Name: filepath.Base(p.Path),
			Path: p.Path,
		})
	}
	return out
}

func (s *HTTPServer) handleFilesRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"roots": s.listFileRoots(r),
	})
}

func (s *HTTPServer) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	rootID := r.URL.Query().Get("root")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	root := s.resolveFileRoot(r, rootID)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found or not permitted")
		return
	}
	abs, ok := safeJoin(root.Path, sub)
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	infos, err := os.ReadDir(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	entries := make([]FileEntry, 0, len(infos))
	for _, fi := range infos {
		name := fi.Name()
		if strings.HasPrefix(name, ".") && name != ".env.example" {
			continue
		}
		info, err := fi.Info()
		if err != nil {
			continue
		}
		entries = append(entries, FileEntry{
			Name:  name,
			Path:  filepath.Join(sub, name),
			IsDir: fi.IsDir(),
			Size:  info.Size(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"root":    rootID,
		"path":    sub,
		"entries": entries,
	})
}

func (s *HTTPServer) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	rootID := r.URL.Query().Get("root")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if sub == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveFileRoot(r, rootID)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found or not permitted")
		return
	}
	abs, ok := safeJoin(root.Path, sub)
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if info.IsDir() {
		jsonError(w, http.StatusBadRequest, "is a directory")
		return
	}
	truncated := false
	readSize := info.Size()
	if readSize > MaxReadableFileSize {
		readSize = MaxReadableFileSize
		truncated = true
	}
	f, err := os.Open(abs)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	defer f.Close()
	buf := make([]byte, readSize)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if looksBinary(buf) {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"binary":    true,
			"size":      info.Size(),
			"truncated": truncated,
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"content":   string(buf),
		"size":      info.Size(),
		"truncated": truncated,
	})
}

// handleFilesRaw streams the file bytes with the right Content-Type
// for inline rendering (used by the mobile app's image viewer).
func (s *HTTPServer) handleFilesRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	rootID := r.URL.Query().Get("root")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if sub == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveFileRoot(r, rootID)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found")
		return
	}
	abs, ok := safeJoin(root.Path, sub)
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		jsonError(w, http.StatusNotFound, "not a file")
		return
	}
	// 20MB cap to avoid nuking mobile memory
	const maxRaw = 20 << 20
	if info.Size() > maxRaw {
		jsonError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	ct := "application/octet-stream"
	switch ext {
	case ".png":
		ct = "image/png"
	case ".jpg", ".jpeg":
		ct = "image/jpeg"
	case ".gif":
		ct = "image/gif"
	case ".webp":
		ct = "image/webp"
	case ".svg":
		ct = "image/svg+xml"
	case ".bmp":
		ct = "image/bmp"
	case ".pdf":
		ct = "application/pdf"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, abs)
}

// safeJoin resolves sub against root and verifies the resulting
// absolute path is still inside root. Prevents ../../../etc/passwd
// escapes even when the caller is malicious.
func safeJoin(root, sub string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	joined := filepath.Join(absRoot, filepath.Clean("/"+sub))
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", false
	}
	if absJoined != absRoot && !strings.HasPrefix(absJoined, absRoot+string(os.PathSeparator)) {
		return "", false
	}
	return absJoined, true
}

func looksBinary(buf []byte) bool {
	sample := buf
	if len(sample) > 1024 {
		sample = sample[:1024]
	}
	for _, b := range sample {
		if b == 0 {
			return true
		}
	}
	return false
}

func (s *HTTPServer) resolveFileRoot(r *http.Request, id string) *FileRoot {
	if id == "" {
		return nil
	}
	for _, rt := range s.listFileRoots(r) {
		if rt.ID == id {
			return &rt
		}
	}
	return nil
}

// projectFSID is a stable 8-hex ID derived from the project
// path. Keeps the full local path out of URLs and works without
// pulling crypto/sha1 into this file.
func projectFSID(path string) string {
	var h uint32 = 2166136261
	for _, c := range path {
		h ^= uint32(c)
		h *= 16777619
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = hex[h&0xf]
		h >>= 4
	}
	return string(out)
}
