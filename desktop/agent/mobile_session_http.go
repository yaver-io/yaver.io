package main

// Mobile-app session enumeration + remote-trigger control plane.
//
// The mobile app already speaks to the agent through:
//   - /blackbox/stream            (event ingestion, opens a session)
//   - /blackbox/command-stream    (SSE — receives reload + open-app commands)
//
// This file adds the surfaces that let an operator (CLI, MCP) ask
// "which mobile devices are currently connected to me?" and
// "tell device X to open app Y in Hot Reload":
//
//   GET  /mobile/sessions               list connected devices
//   POST /mobile/insert {app, deviceId} send open_app command
//
// Both endpoints are owner-auth and not exposed to guests. Guests
// pushing arbitrary apps onto a mobile device would be an obvious
// scope-escalation foothold.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// mobileInsertRequest is the body of POST /mobile/insert.
//
// `app` is the project name as it appears in the agent's project
// list (case-insensitive match). `deviceId` is the mobile session
// id reported by /mobile/sessions. Empty deviceId broadcasts to all
// connected mobile sessions — handy when there's only one paired
// phone and the operator doesn't want to copy a UUID.
type mobileInsertRequest struct {
	App      string `json:"app"`
	DeviceID string `json:"deviceId"`
}

func (s *HTTPServer) handleMobileSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.blackboxMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": []interface{}{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": s.blackboxMgr.ListSessions(),
	})
}

func (s *HTTPServer) handleMobileInsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req mobileInsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	app := strings.TrimSpace(req.App)
	if app == "" {
		http.Error(w, "app is required", http.StatusBadRequest)
		return
	}
	if s.blackboxMgr == nil {
		http.Error(w, "no mobile sessions are connected", http.StatusServiceUnavailable)
		return
	}

	cmd := BlackBoxCommand{
		Command: "open_app",
		Data: map[string]interface{}{
			"app":      app,
			"reason":   "yaver insert",
			"sentAt":   time.Now().UnixMilli(),
		},
	}

	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		// Broadcast: send to every paired mobile.
		sessions := s.blackboxMgr.ListSessions()
		if len(sessions) == 0 {
			http.Error(w, "no mobile sessions are connected — open the Yaver mobile app and pair it to this agent first", http.StatusServiceUnavailable)
			return
		}
		s.blackboxMgr.BroadcastCommand(cmd)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"app":     app,
			"sentTo":  len(sessions),
			"target":  "broadcast",
		})
		return
	}

	if !s.blackboxMgr.SendCommandToDevice(deviceID, cmd) {
		http.Error(w, fmt.Sprintf("device %q has no active session — `yaver remote detect` lists currently-paired phones", deviceID), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"app":      app,
		"deviceId": deviceID,
		"target":   "device",
	})
}

