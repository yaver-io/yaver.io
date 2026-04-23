package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) hostShareSessionIDForRequest(r *http.Request) string {
	if r.Header.Get("X-Yaver-HostShare") == "true" {
		return strings.TrimSpace(r.Header.Get("X-Yaver-HostShareSessionID"))
	}
	if r.Header.Get("X-Yaver-Guest") == "true" {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("sessionId"))
}

func (s *HTTPServer) hostShareGuestDeviceIDForRequest(r *http.Request) string {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-Yaver-HostShareGuestDeviceID"))
}

func (s *HTTPServer) handleHostShareWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.hostShareWorkspaceMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "host-share workspace manager unavailable")
		return
	}
	sessionID := s.hostShareSessionIDForRequest(r)
	if sessionID == "" {
		jsonError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	ws, err := s.hostShareWorkspaceMgr.EnsureWorkspace(sessionID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "ensure host-share workspace: "+err.Error())
		return
	}
	if refreshed, err := s.hostShareWorkspaceMgr.RefreshCounts(sessionID); err == nil && refreshed != nil {
		ws = refreshed
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"workspace": ws,
	})
}

func (s *HTTPServer) handleHostShareWorkspaceBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") == "true" {
		jsonError(w, http.StatusForbidden, "host-share guests cannot bootstrap workspaces")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.hostShareWorkspaceMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "host-share workspace manager unavailable")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
		SourceDir string `json:"sourceDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.SessionID) == "" || strings.TrimSpace(body.SourceDir) == "" {
		jsonError(w, http.StatusBadRequest, "sessionId and sourceDir are required")
		return
	}
	ws, err := s.hostShareWorkspaceMgr.BootstrapFromDir(body.SessionID, body.SourceDir)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bootstrap host-share workspace: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"workspace": ws,
	})
}

func (s *HTTPServer) handleHostShareWorkspaceAttachRepo(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sessionID := s.hostShareSessionIDForRequest(r)
	deviceID := s.hostShareGuestDeviceIDForRequest(r)
	if sessionID == "" || deviceID == "" {
		jsonError(w, http.StatusBadRequest, "host-share session binding is incomplete")
		return
	}
	var body struct {
		RootID   string `json:"rootId"`
		RootPath string `json:"rootPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ws, root, stats, err := hostShareImportGuestRootIntoWorkspace(sessionID, deviceID, body.RootID, body.RootPath)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"workspace": ws,
		"root":      root,
		"stats": map[string]any{
			"files": stats.Files,
			"dirs":  stats.Dirs,
		},
	})
}

func (s *HTTPServer) handleHostShareWorkspacePullFromGuest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sessionID := s.hostShareSessionIDForRequest(r)
	deviceID := s.hostShareGuestDeviceIDForRequest(r)
	if sessionID == "" || deviceID == "" {
		jsonError(w, http.StatusBadRequest, "host-share session binding is incomplete")
		return
	}
	ws, root, stats, err := hostShareImportGuestRootIntoWorkspace(sessionID, deviceID, "", "")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"workspace": ws,
		"root":      root,
		"stats": map[string]any{
			"files": stats.Files,
			"dirs":  stats.Dirs,
		},
	})
}

func (s *HTTPServer) handleHostShareWorkspacePushToGuest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-HostShare") != "true" {
		jsonError(w, http.StatusForbidden, "host-share session required")
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	sessionID := s.hostShareSessionIDForRequest(r)
	deviceID := s.hostShareGuestDeviceIDForRequest(r)
	if sessionID == "" || deviceID == "" {
		jsonError(w, http.StatusBadRequest, "host-share session binding is incomplete")
		return
	}
	ws, stats, err := hostShareExportWorkspaceToGuest(sessionID, deviceID, "")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"workspace": ws,
		"stats": map[string]any{
			"files":   stats.Files,
			"dirs":    stats.Dirs,
			"deleted": stats.Deleted,
		},
	})
}
