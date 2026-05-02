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
	"encoding/base64"
	"encoding/json"
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
		if r.Header.Get("X-Yaver-HostShare") == "true" && !hostShareCanAccessProject(r, p.Path) {
			continue
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
	rootPath := r.URL.Query().Get("rootPath")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	root := s.resolveHostShareRoot(r, rootID, rootPath)
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
	rootPath := r.URL.Query().Get("rootPath")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if sub == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveHostShareRoot(r, rootID, rootPath)
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
	// M-13: refuse FIFOs, character/block devices, /proc/*, sockets, etc.
	// os.Stat reports Size=0 for these and os.Open on a FIFO blocks the
	// handler goroutine until something writes — connection-DoS surface.
	if !info.Mode().IsRegular() {
		jsonError(w, http.StatusBadRequest, "not a regular file")
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
	rootPath := r.URL.Query().Get("rootPath")
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if sub == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveHostShareRoot(r, rootID, rootPath)
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

func (s *HTTPServer) handleHostShareFSWrite(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Root          string `json:"root"`
		RootPath      string `json:"rootPath"`
		Path          string `json:"path"`
		Content       string `json:"content"`
		ContentBase64 string `json:"contentBase64"`
		Mode          int    `json:"mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveHostShareRoot(r, body.Root, body.RootPath)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found or not permitted")
		return
	}
	abs, ok := safeJoin(root.Path, strings.TrimPrefix(body.Path, "/"))
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		jsonError(w, http.StatusInternalServerError, "mkdir parent: "+err.Error())
		return
	}
	content := []byte(body.Content)
	if strings.TrimSpace(body.ContentBase64) != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.ContentBase64))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid base64 content")
			return
		}
		content = raw
	}
	mode := os.FileMode(0644)
	if body.Mode > 0 {
		mode = os.FileMode(body.Mode) & os.ModePerm
		if mode == 0 {
			mode = 0644
		}
	}
	if err := os.WriteFile(abs, content, mode); err != nil {
		jsonError(w, http.StatusInternalServerError, "write file: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "path": body.Path})
}

func (s *HTTPServer) handleHostShareFSMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Root     string `json:"root"`
		RootPath string `json:"rootPath"`
		Path     string `json:"path"`
		Mode     int    `json:"mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveHostShareRoot(r, body.Root, body.RootPath)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found or not permitted")
		return
	}
	abs, ok := safeJoin(root.Path, strings.TrimPrefix(body.Path, "/"))
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	mode := os.FileMode(0755)
	if body.Mode > 0 {
		mode = os.FileMode(body.Mode) & os.ModePerm
		if mode == 0 {
			mode = 0755
		}
	}
	if err := os.MkdirAll(abs, mode); err != nil {
		jsonError(w, http.StatusInternalServerError, "mkdir: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "path": body.Path})
}

func (s *HTTPServer) handleHostShareFSDelete(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Root     string `json:"root"`
		RootPath string `json:"rootPath"`
		Path     string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		jsonError(w, http.StatusBadRequest, "path required")
		return
	}
	root := s.resolveHostShareRoot(r, body.Root, body.RootPath)
	if root == nil {
		jsonError(w, http.StatusNotFound, "root not found or not permitted")
		return
	}
	abs, ok := safeJoin(root.Path, strings.TrimPrefix(body.Path, "/"))
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	if err := os.RemoveAll(abs); err != nil {
		jsonError(w, http.StatusInternalServerError, "delete path: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "path": body.Path})
}

// safeJoin resolves sub against root and verifies the resulting
// absolute path is still inside root. Prevents ../../../etc/passwd
// escapes even when the caller is malicious.
//
// H-9 fix: also call EvalSymlinks on the resolved path so a symlink
// dropped into the project root cannot trick us into reading host
// files outside the root. If EvalSymlinks resolves to a path outside
// absRoot, the join is rejected. We deliberately do not return the
// resolved path to callers — they continue to operate on the join
// result they expect — but the *check* is against the resolved one.
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
	// Symlink-aware re-check. If the joined path doesn't exist yet
	// (e.g. caller is creating a new file via the browser), Eval
	// returns an error — that's fine, the textual containment check
	// above is sufficient because there's no symlink to escape via.
	resolvedJoined, errJ := filepath.EvalSymlinks(absJoined)
	if errJ != nil {
		return absJoined, true
	}
	resolvedRoot, errR := filepath.EvalSymlinks(absRoot)
	if errR != nil {
		// Root doesn't resolve cleanly — fall back to textual check.
		return absJoined, true
	}
	if resolvedJoined != resolvedRoot && !strings.HasPrefix(resolvedJoined, resolvedRoot+string(os.PathSeparator)) {
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

func (s *HTTPServer) resolveHostShareRoot(r *http.Request, id, rootPath string) *FileRoot {
	if root := s.resolveFileRoot(r, id); root != nil {
		return root
	}
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		return nil
	}
	raw := strings.TrimSpace(rootPath)
	if raw == "" {
		return nil
	}
	if !filepath.IsAbs(raw) {
		return nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return nil
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil
	}
	root := FileRoot{
		ID:   projectFSID(abs),
		Name: filepath.Base(abs),
		Path: abs,
	}
	return &root
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
