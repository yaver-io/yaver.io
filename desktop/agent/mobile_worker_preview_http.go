package main

import (
	"encoding/json"
	"net/http"
)

// handleMobileWorkerPreviewSession returns the currently selected mobile-worker
// preview target plus whether the worker is online through BlackBox.
// GET /mobile-workers/preview-session
func (s *HTTPServer) handleMobileWorkerPreviewSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	target := s.devServerMgr.PreferredTarget()
	status := s.devServerMgr.Status()
	var workerSession *BlackBoxSession
	if s.blackboxMgr != nil && target.DeviceID != "" {
		workerSession = s.blackboxMgr.GetSession(target.DeviceID)
	}

	resp := map[string]interface{}{
		"hasTarget":          target.DeviceID != "" || target.DeviceName != "" || target.DeviceClass != "",
		"targetDeviceId":     target.DeviceID,
		"targetDeviceName":   target.DeviceName,
		"targetDeviceClass":  target.DeviceClass,
		"workerOnline":       workerSession != nil,
		"devServerRunning":   status != nil && status.Running,
		"framework":          "",
		"workDir":            "",
		"targetCommandScope": "current-device",
	}
	if target.DeviceID != "" {
		resp["targetCommandScope"] = "selected-worker"
	}
	if status != nil {
		resp["framework"] = status.Framework
		resp["workDir"] = status.WorkDir
	}
	if workerSession != nil {
		workerSession.mu.RLock()
		eventCount := len(workerSession.Events)
		workerSession.mu.RUnlock()
		resp["workerPlatform"] = workerSession.Platform
		resp["workerAppName"] = workerSession.AppName
		resp["workerStartedAt"] = workerSession.StartedAt
		resp["workerEventCount"] = eventCount
	}

	jsonReply(w, http.StatusOK, resp)
}

// handleMobileWorkerPreviewCommand sends a targeted command to the selected
// preview worker through the existing BlackBox command channel.
// POST /mobile-workers/preview-session/command
func (s *HTTPServer) handleMobileWorkerPreviewCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}
	if s.blackboxMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "mobile worker commands not available"})
		return
	}

	var req struct {
		Command string                 `json:"command"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Command == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "command required"})
		return
	}

	target := s.devServerMgr.PreferredTarget()
	if target.DeviceID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no mobile worker selected"})
		return
	}

	ok := s.blackboxMgr.SendCommandToDevice(target.DeviceID, BlackBoxCommand{
		Command: req.Command,
		Data:    req.Data,
	})
	if !ok {
		jsonReply(w, http.StatusConflict, map[string]string{"error": "selected mobile worker is offline"})
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":               true,
		"targetDeviceId":   target.DeviceID,
		"targetDeviceName": target.DeviceName,
		"command":          req.Command,
	})
}
