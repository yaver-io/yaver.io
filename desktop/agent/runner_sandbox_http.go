package main

// runner_sandbox_http.go — HTTP surface for the SandboxManager
// (RUNNER_DEV.md Phase 2).
//
//   GET    /runner/sandboxes                       List live sandboxes
//   POST   /runner/sandboxes                       Start a new sandbox (SandboxStartOpts)
//   GET    /runner/sandboxes/{id}                  One sandbox
//   DELETE /runner/sandboxes/{id}                  Stop a sandbox
//   POST   /runner/sandboxes/{id}/exec             SandboxExecOpts → SandboxExecResult
//   POST   /runner/sandboxes/{id}/files/read       {path} → {content base64}
//   POST   /runner/sandboxes/{id}/files/write      {path, content base64}  → {ok}
//   GET    /runner/sandboxes/status                Aggregate status (image ready, count)
//
// Phase 2 is owner-only — sandbox is too broad to scope to a guest
// (it's a container shell). Future phases may add a tier where a
// guest can drive a sandbox they themselves started, but the policy
// review around that is its own piece of work.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) ensureSandboxManager() *SandboxManager {
	if s.sandboxMgr == nil {
		s.sandboxMgr = NewSandboxManager(s.containerRunner)
	}
	return s.sandboxMgr
}

// handleRunnerSandboxes — /runner/sandboxes (GET list, POST start) and
// /runner/sandboxes/status (GET aggregate).
func (s *HTTPServer) handleRunnerSandboxes(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/runner/sandboxes/status" {
		s.handleRunnerSandboxStatus(w, r)
		return
	}
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "sandboxes are owner-only in Phase 2"})
		return
	}
	mgr := s.ensureSandboxManager()
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sandbox unavailable — Docker not detected on this agent",
		})
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions := mgr.List("")
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"sessions": sessions,
			"count":    len(sessions),
		})
	case http.MethodPost:
		var opts SandboxStartOpts
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		sess, err := mgr.Start(r.Context(), opts, s.ownerUserID)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleRunnerSandboxStatus — GET /runner/sandboxes/status.
func (s *HTTPServer) handleRunnerSandboxStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	mgr := s.ensureSandboxManager()
	if mgr == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"available": false,
			"image":     sandboxImage,
			"sessions":  []SandboxSession{},
			"count":     0,
		})
		return
	}
	jsonReply(w, http.StatusOK, mgr.Snapshot(""))
}

// handleRunnerSandboxByID dispatches /runner/sandboxes/{id} variants:
// GET, DELETE, POST .../exec, POST .../files/read, POST .../files/write.
func (s *HTTPServer) handleRunnerSandboxByID(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "sandboxes are owner-only in Phase 2"})
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/runner/sandboxes/")
	tail = strings.TrimSuffix(tail, "/")
	if tail == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing sandbox id"})
		return
	}
	parts := strings.SplitN(tail, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	mgr := s.ensureSandboxManager()
	if mgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sandbox unavailable — Docker not detected",
		})
		return
	}
	switch sub {
	case "":
		s.handleSandboxOne(w, r, mgr, id)
	case "exec":
		s.handleSandboxExec(w, r, mgr, id)
	case "files/read":
		s.handleSandboxFileRead(w, r, mgr, id)
	case "files/write":
		s.handleSandboxFileWrite(w, r, mgr, id)
	default:
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource: " + sub})
	}
}

func (s *HTTPServer) handleSandboxOne(w http.ResponseWriter, r *http.Request, mgr *SandboxManager, id string) {
	switch r.Method {
	case http.MethodGet:
		sess, ok := mgr.Get(id, "")
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "sandbox not found"})
			return
		}
		jsonReply(w, http.StatusOK, sess)
	case http.MethodDelete:
		if err := mgr.StopSandbox(id); err != nil {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *HTTPServer) handleSandboxExec(w http.ResponseWriter, r *http.Request, mgr *SandboxManager, id string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var opts SandboxExecOpts
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	res, err := mgr.Exec(r.Context(), id, opts, "")
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, res)
}

// sandboxFileRead body: {"path": "<absolute path inside sandbox>"}.
// Response: {"path": "...", "content": "<base64>"}.
func (s *HTTPServer) handleSandboxFileRead(w http.ResponseWriter, r *http.Request, mgr *SandboxManager, id string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	data, err := mgr.ReadFile(r.Context(), id, body.Path, "")
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"path":    body.Path,
		"content": base64.StdEncoding.EncodeToString(data),
		"bytes":   len(data),
	})
}

// sandboxFileWrite body: {"path": "...", "content": "<base64>"}.
func (s *HTTPServer) handleSandboxFileWrite(w http.ResponseWriter, r *http.Request, mgr *SandboxManager, id string) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Content)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "content must be base64: " + err.Error()})
		return
	}
	if err := mgr.WriteFile(r.Context(), id, body.Path, raw, ""); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "bytes": len(raw)})
}
