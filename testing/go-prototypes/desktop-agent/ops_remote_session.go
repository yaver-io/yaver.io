package main

// ops_remote_session.go — managed remote browser/session surface for the web
// dashboard. This is the first productized slice of "run the app on a Yaver
// device/cloud worker, stream/control it from a viewer": Teams/Meet/Zoom/web
// apps run in a headful Chrome session on the selected machine; screen frames
// are exposed through the existing Remote Desktop/WebRTC "screen" source.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const remoteSessionBrowserID = "yaver-remote-session"

type remoteSessionState struct {
	mu        sync.Mutex
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
	Title     string `json:"title,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

var remoteSession = &remoteSessionState{}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "remote_session_start",
		Description: "Start or retarget a managed headful browser session on this device and make its screen streamable over Yaver WebRTC. Payload {url, fps?}.",
		Schema: atvSchema(map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "http(s) meeting or web-app URL to open"},
			"fps": map[string]interface{}{"type": "integer", "description": "screen capture fps for the backing frame buffer"},
		}),
		Handler: remoteSessionStartHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "remote_session_status",
		Description: "Status for the managed remote browser/session surface.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     remoteSessionStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "remote_session_stop",
		Description: "Close the managed remote browser/session surface. Payload {stopStream?}.",
		Schema: atvSchema(map[string]interface{}{
			"stopStream": map[string]interface{}{"type": "boolean", "description": "also stop the shared screen frame buffer"},
		}),
		Handler: remoteSessionStopHandler,
	})
}

func remoteSessionProfileDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, "remote-session", "chrome-profile")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func normalizeRemoteSessionURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url must be http(s)")
	}
	if u.Host == "" {
		return "", fmt.Errorf("url host required")
	}
	return u.String(), nil
}

func remoteSessionStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		URL string `json:"url"`
		FPS int    `json:"fps"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	u, err := normalizeRemoteSessionURL(p.URL)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if c.Server == nil || c.Server.browserMgr == nil {
		return OpsResult{OK: false, Code: "not_available", Error: "browser manager not available"}
	}

	if p.FPS <= 0 {
		p.FPS = 8
	}
	eng, err := c.Server.ensureGhost()
	if err != nil {
		return OpsResult{OK: false, Code: "screen_unavailable", Error: "screen capture unavailable: " + err.Error()}
	}
	if err := ghostStream.start(eng, p.FPS); err != nil {
		return OpsResult{OK: false, Code: "screen_unavailable", Error: err.Error()}
	}

	profile, err := remoteSessionProfileDir()
	if err != nil {
		return OpsResult{OK: false, Code: "profile_failed", Error: err.Error()}
	}
	if err := c.Server.browserMgr.OpenSessionWithProfileOptions(remoteSessionBrowserID, true, "", profile, BrowserSessionOptions{MuteAudio: false}); err != nil {
		// Reusing an existing session is intentional: update its URL instead of
		// forcing the user to lose an authenticated Teams/Meet browser.
		if !strings.Contains(err.Error(), "already exists") {
			remoteSession.setError(err.Error())
			return OpsResult{OK: false, Code: "browser_failed", Error: err.Error()}
		}
	}
	res, err := c.Server.browserMgr.Navigate(remoteSessionBrowserID, u)
	if err != nil {
		remoteSession.setError(err.Error())
		return OpsResult{OK: false, Code: "navigate_failed", Error: err.Error()}
	}
	remoteSession.mu.Lock()
	now := time.Now().UnixMilli()
	if !remoteSession.Running || remoteSession.StartedAt == 0 {
		remoteSession.StartedAt = now
	}
	remoteSession.Running = true
	remoteSession.URL = res.URL
	remoteSession.Title = res.Title
	remoteSession.UpdatedAt = now
	remoteSession.LastError = ""
	out := remoteSession.snapshotLocked()
	remoteSession.mu.Unlock()
	return OpsResult{OK: true, Initial: out}
}

func remoteSessionStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	remoteSession.mu.Lock()
	out := remoteSession.snapshotLocked()
	remoteSession.mu.Unlock()
	out["screen"] = ghostStream.status()
	if c.Server != nil && c.Server.browserMgr != nil {
		for _, s := range c.Server.browserMgr.ListSessions() {
			if s.ID == remoteSessionBrowserID {
				out["browser"] = s
				if s.CurrentURL != "" {
					out["url"] = s.CurrentURL
				}
				if s.CurrentTitle != "" {
					out["title"] = s.CurrentTitle
				}
			}
		}
	}
	return OpsResult{OK: true, Initial: out}
}

func remoteSessionStopHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		StopStream bool `json:"stopStream"`
	}
	_ = json.Unmarshal(payload, &p)
	if c.Server != nil && c.Server.browserMgr != nil {
		if err := c.Server.browserMgr.CloseSession(remoteSessionBrowserID); err != nil && !strings.Contains(err.Error(), "not found") {
			remoteSession.setError(err.Error())
			return OpsResult{OK: false, Code: "browser_failed", Error: err.Error()}
		}
	}
	if p.StopStream {
		ghostStream.stop()
	}
	remoteSession.mu.Lock()
	remoteSession.Running = false
	remoteSession.UpdatedAt = time.Now().UnixMilli()
	remoteSession.LastError = ""
	out := remoteSession.snapshotLocked()
	remoteSession.mu.Unlock()
	out["screen"] = ghostStream.status()
	return OpsResult{OK: true, Initial: out}
}

func (s *remoteSessionState) setError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastError = msg
	s.UpdatedAt = time.Now().UnixMilli()
}

func (s *remoteSessionState) snapshotLocked() map[string]interface{} {
	return map[string]interface{}{
		"running":   s.Running,
		"url":       s.URL,
		"title":     s.Title,
		"startedAt": s.StartedAt,
		"updatedAt": s.UpdatedAt,
		"lastError": s.LastError,
		"source":    "screen",
		"webrtc":    "/stream/webrtc/offer",
		"rdStream":  "/rd/stream",
		"rdInput":   "/rd/input",
		"browserId": remoteSessionBrowserID,
	}
}
