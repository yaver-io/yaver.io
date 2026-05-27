package main

// ops_glass_pc.go — verbs for the "PC UI in glasses" surface.
//
// Five verbs, all owner-only (the browser holds session cookies +
// keystrokes for the wearer — guest scope must not pick them up):
//
//   glass_pc_open     — open a remote browser window pointed at URL,
//                        register a remote-runtime session, return id
//   glass_pc_navigate — change URL in an existing window
//   glass_pc_focus    — hint to spatial viewer which window to center
//   glass_pc_close    — close window + tear down WebRTC session
//   glass_pc_list     — list active windows (id, url, title)
//
// One extra verb for HUD glasses (Even G1/G2, Vuzix Z100):
//
//   glass_hud         — push a typed HUD view (terminal_tail /
//                        email_subjects / notification / voice_overlay)
//
// glass_hud is the verb-side mirror of POST /glass/hud — both accept
// the same payload shape so external scripts can pick whichever
// surface fits their pipeline.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "glass_pc_open",
		Description: "Open a remote browser window streamed over WebRTC. Returns sessionId + deviceId. " +
			"Caller follows up with POST /remote-runtime/sessions/<id>/offer to start streaming.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":            map[string]interface{}{"type": "string", "description": "Initial URL (about:blank if omitted)"},
				"focusOnSpatial": map[string]interface{}{"type": "boolean", "description": "When true, sends a 'focus' HUD event so the spatial viewer centers this window"},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Handler:    opsGlassPCOpenHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "glass_pc_navigate",
		Description: "Navigate an existing glass_pc window to a new URL.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"sessionId": map[string]interface{}{"type": "string"},
				"url":       map[string]interface{}{"type": "string"},
			},
			"required":             []string{"sessionId", "url"},
			"additionalProperties": false,
		},
		Handler:    opsGlassPCNavigateHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "glass_pc_focus",
		Description: "Broadcast a 'focus this window' event to spatial viewers. The agent does not " +
			"reorder windows itself; viewers (Quest, Vision Pro) react to the event.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"sessionId": map[string]interface{}{"type": "string"},
			},
			"required":             []string{"sessionId"},
			"additionalProperties": false,
		},
		Handler:    opsGlassPCFocusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "glass_pc_close",
		Description: "Close a glass_pc remote browser window and tear down its WebRTC session.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"sessionId": map[string]interface{}{"type": "string"},
			},
			"required":             []string{"sessionId"},
			"additionalProperties": false,
		},
		Handler:    opsGlassPCCloseHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "glass_pc_list",
		Description: "List active glass_pc remote browser windows (id, url, title).",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsGlassPCListHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "glass_hud",
		Description: "Push a typed HUD view to MentraOS HUD clients. Same payload shape as POST /glass/hud.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"view":    map[string]interface{}{"type": "string", "enum": []string{"terminal_tail", "email_subjects", "notification", "voice_overlay"}},
				"payload": map[string]interface{}{"type": "object"},
			},
			"required":             []string{"view", "payload"},
			"additionalProperties": false,
		},
		Handler:    opsGlassHUDHandler,
		AllowGuest: false,
	})
}

type opsGlassPCOpenPayload struct {
	URL            string `json:"url"`
	FocusOnSpatial bool   `json:"focusOnSpatial"`
}

func opsGlassPCOpenHandler(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_missing", Error: "server context unavailable"}
	}
	var p opsGlassPCOpenPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	mgr := c.Server.ensureRemoteRuntimeManager()
	session, err := mgr.Create("", "browser", "browser-window", "direct-webrtc")
	if err != nil {
		return OpsResult{OK: false, Code: "create_failed", Error: err.Error()}
	}
	session, err = mgr.Attach(session.ID)
	if err != nil {
		mgr.Delete(session.ID)
		return OpsResult{OK: false, Code: "attach_failed", Error: err.Error()}
	}
	if u := strings.TrimSpace(p.URL); u != "" {
		if err := browserPool.navigate(session.DeviceID, u); err != nil {
			return OpsResult{OK: true, Initial: map[string]any{
				"sessionId":     session.ID,
				"deviceId":      session.DeviceID,
				"transport":     session.FrameTransport,
				"navigateError": err.Error(),
			}}
		}
	}
	if p.FocusOnSpatial {
		opsGlassBroadcastFocus(c.Server, session.ID)
	}
	return OpsResult{OK: true, Initial: map[string]any{
		"sessionId": session.ID,
		"deviceId":  session.DeviceID,
		"transport": session.FrameTransport,
		"url":       p.URL,
	}}
}

type opsGlassPCNavigatePayload struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
}

func opsGlassPCNavigateHandler(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_missing", Error: "server context unavailable"}
	}
	var p opsGlassPCNavigatePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.SessionID) == "" || strings.TrimSpace(p.URL) == "" {
		return OpsResult{OK: false, Code: "missing_args", Error: "sessionId and url are required"}
	}
	mgr := c.Server.ensureRemoteRuntimeManager()
	session, ok := mgr.Get(p.SessionID)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "glass_pc session not found"}
	}
	if err := browserPool.navigate(session.DeviceID, p.URL); err != nil {
		return OpsResult{OK: false, Code: "navigate_failed", Error: err.Error()}
	}
	mgr.Update(session.ID, func(s *RemoteRuntimeSession) {
		s.Note = "navigated to " + p.URL
	})
	return OpsResult{OK: true, Initial: map[string]any{"sessionId": session.ID, "url": p.URL}}
}

type opsGlassPCFocusPayload struct {
	SessionID string `json:"sessionId"`
}

func opsGlassPCFocusHandler(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_missing", Error: "server context unavailable"}
	}
	var p opsGlassPCFocusPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return OpsResult{OK: false, Code: "missing_args", Error: "sessionId is required"}
	}
	opsGlassBroadcastFocus(c.Server, p.SessionID)
	return OpsResult{OK: true, Initial: map[string]any{"sessionId": p.SessionID}}
}

func opsGlassPCCloseHandler(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "server_missing", Error: "server context unavailable"}
	}
	var p opsGlassPCFocusPayload // same shape — sessionId only
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return OpsResult{OK: false, Code: "missing_args", Error: "sessionId is required"}
	}
	mgr := c.Server.ensureRemoteRuntimeManager()
	session, ok := mgr.Get(p.SessionID)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "glass_pc session not found"}
	}
	browserPool.close(session.DeviceID)
	mgr.CloseSession(session.ID)
	return OpsResult{OK: true, Initial: map[string]any{"sessionId": session.ID, "closed": true}}
}

func opsGlassPCListHandler(c OpsContext, _ json.RawMessage) OpsResult {
	out := browserPool.list()
	// Decorate with the page title so glass UIs can label windows
	// without round-tripping. Best-effort: a stuck CDP context just
	// returns the URL as the title.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = ctx // reserved for a future title batch
	for i := range out {
		if id, ok := out[i]["id"].(string); ok && id != "" {
			if t, err := browserGetTitle(id); err == nil && t != "" {
				out[i]["title"] = t
			}
		}
	}
	return OpsResult{OK: true, Initial: map[string]any{
		"windows": out,
		"asOf":    time.Now().UTC().Format(time.RFC3339),
	}}
}

type opsGlassHUDPayload struct {
	View    string          `json:"view"`
	Payload json.RawMessage `json:"payload"`
}

func opsGlassHUDHandler(c OpsContext, raw json.RawMessage) OpsResult {
	if c.Server == nil || c.Server.blackboxMgr == nil {
		return OpsResult{OK: false, Code: "blackbox_missing", Error: "blackbox manager not available"}
	}
	var p opsGlassHUDPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	switch strings.TrimSpace(p.View) {
	case "terminal_tail":
		var view HUDTerminalTailView
		if err := json.Unmarshal(p.Payload, &view); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		BroadcastHUDTerminalTail(c.Server.blackboxMgr, view.SessionLabel, view.Lines)
	case "email_subjects":
		var view HUDEmailSubjectsView
		if err := json.Unmarshal(p.Payload, &view); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		BroadcastHUDEmailSubjects(c.Server.blackboxMgr, view.Folder, view.Items)
	case "notification":
		var view HUDNotificationView
		if err := json.Unmarshal(p.Payload, &view); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		BroadcastHUDNotification(c.Server.blackboxMgr, view.Title, view.Body, view.Source)
	case "voice_overlay":
		var view HUDVoiceOverlayView
		if err := json.Unmarshal(p.Payload, &view); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		BroadcastHUDVoiceOverlay(c.Server.blackboxMgr, view.Partial, view.Final, view.Latency)
	default:
		return OpsResult{OK: false, Code: "bad_view", Error: fmt.Sprintf("unknown view %q", p.View)}
	}
	return OpsResult{OK: true, Initial: map[string]any{"view": p.View}}
}

// opsGlassBroadcastFocus emits a 'glass_pc_focus' blackbox command so
// spatial viewers (Quest, Vision Pro) and the web /spatial route can
// re-center the named window. HUD wearers ignore this view — focus
// is meaningless on a single text wall.
func opsGlassBroadcastFocus(s *HTTPServer, sessionID string) {
	if s == nil || s.blackboxMgr == nil {
		return
	}
	s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
		Command: "glass_pc_focus",
		Data: map[string]interface{}{
			"sessionId": sessionID,
			"ts":        time.Now().UnixMilli(),
		},
	})
}
