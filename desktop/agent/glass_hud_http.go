package main

// glass_hud_http.go — POST /glass/hud
//
// Lets external tools (the agent itself for terminal_tail, the
// mobile app for notification mirroring, scheduled jobs for
// email_subjects) push HUD views without re-deriving the blackbox
// command wire format. Body:
//
//   { "view": "terminal_tail",
//     "payload": { "sessionLabel": "yaver:dev", "lines": ["..."] } }
//
// Views: terminal_tail, email_subjects, notification, voice_overlay.
// Same auth posture as /blackbox/events — session token required.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type glassHUDRequest struct {
	View    string          `json:"view"`
	Payload json.RawMessage `json:"payload"`
}

func (s *HTTPServer) handleGlassHUDPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if s.blackboxMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "blackbox not available")
		return
	}
	var req glassHUDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}
	view := strings.TrimSpace(req.View)
	if view == "" {
		jsonError(w, http.StatusBadRequest, "view is required")
		return
	}
	switch view {
	case "terminal_tail":
		var p HUDTerminalTailView
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid terminal_tail payload: %v", err))
			return
		}
		BroadcastHUDTerminalTail(s.blackboxMgr, p.SessionLabel, p.Lines)
	case "email_subjects":
		var p HUDEmailSubjectsView
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid email_subjects payload: %v", err))
			return
		}
		BroadcastHUDEmailSubjects(s.blackboxMgr, p.Folder, p.Items)
	case "notification":
		var p HUDNotificationView
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid notification payload: %v", err))
			return
		}
		BroadcastHUDNotification(s.blackboxMgr, p.Title, p.Body, p.Source)
	case "voice_overlay":
		var p HUDVoiceOverlayView
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid voice_overlay payload: %v", err))
			return
		}
		BroadcastHUDVoiceOverlay(s.blackboxMgr, p.Partial, p.Final, p.Latency)
	default:
		jsonError(w, http.StatusBadRequest, "unknown view: "+view)
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "view": view})
}
