package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var sudoPromptPattern = regexp.MustCompile(`(?i)(?:\[(?:sudo|SUDO)\]\s*)?password(?:\s+for\s+[^:\r\n]+)?\s*:`)

// handleTerminalWS: WS /ws/terminal — starts or resumes a PTY-backed shell.
// Protocol:
//   - binary frames: stdin bytes → pty
//   - text frames:   {"resize":{"cols":N,"rows":M}} → resize signal
//   - server emits binary frames of stdout/stderr bytes
//   - server emits text frame {"type":"terminal_session","sessionId":"..."} on attach
func (s *HTTPServer) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	var touchMu sync.Mutex
	lastTouch := time.Time{}
	touchSession := func(force bool) {
		if r.Header.Get("X-Yaver-HostShare") != "true" {
			return
		}
		sessionID := strings.TrimSpace(r.Header.Get("X-Yaver-HostShareSessionID"))
		if sessionID == "" {
			return
		}
		touchMu.Lock()
		if !force && !lastTouch.IsZero() && time.Since(lastTouch) < 15*time.Second {
			touchMu.Unlock()
			return
		}
		lastTouch = time.Now()
		touchMu.Unlock()
		go func() {
			_ = TouchHostShareSession(s.convexURL, s.token, sessionID)
		}()
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	hostShareSessionID := strings.TrimSpace(r.Header.Get("X-Yaver-HostShareSessionID"))
	guestUserID := strings.TrimSpace(r.Header.Get("X-Yaver-HostShareGuestUserID"))
	terminalSessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	var ts *terminalSession
	resumed := false

	if terminalSessionID != "" {
		if existing, ok := s.terminalSessionByID(terminalSessionID); ok {
			if hostShareSessionID != "" && existing.hostShareID != "" && existing.hostShareID != hostShareSessionID {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("terminal session mismatch"))
				_ = conn.Close()
				return
			}
			ts = existing
			resumed = true
		}
	}

	if ts == nil {
		shell := r.URL.Query().Get("shell")
		if shell == "" {
			shell = os.Getenv("SHELL")
		}
		if shell == "" {
			shell = "/bin/bash"
		}

		cmd := exec.Command(shell)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		cwd := r.URL.Query().Get("cwd")
		workspaceDir := ""
		if r.Header.Get("X-Yaver-HostShare") == "true" {
			cmd.Env = append(cmd.Env,
				"YAVER_HOST_SHARE=1",
				"YAVER_HOST_SHARE_SESSION_ID="+hostShareSessionID,
				"YAVER_HOST_SHARE_GUEST_USER_ID="+guestUserID,
			)
			if s.hostShareWorkspaceMgr != nil && hostShareSessionID != "" {
				if ws, err := s.hostShareWorkspaceMgr.EnsureWorkspace(hostShareSessionID); err == nil && ws != nil && strings.TrimSpace(ws.RepoDir) != "" {
					cwd = ws.RepoDir
					workspaceDir = ws.RepoDir
					cmd.Env = append(cmd.Env, "YAVER_HOST_SHARE_WORKSPACE_DIR="+ws.RepoDir)
				}
			}
		}
		if cwd != "" {
			cmd.Dir = cwd
		}
		// On Android the agent runs native but the shell must execute inside
		// the proot Alpine rootfs so claude/codex/node resolve. No-op on every
		// other platform (gated on YAVER_ANDROID_* env). See sandbox_proot.go.
		cmd = sandboxWrapCmd(cmd)
		ts, err = s.newTerminalSession(cmd, touchSession, hostShareSessionID, guestUserID, workspaceDir)
		if err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("pty start failed: "+err.Error()))
			_ = conn.Close()
			return
		}
		touchSession(true)
	}

	if err := ts.attach(conn, resumed); err != nil {
		_ = conn.Close()
		return
	}
	defer ts.detach(conn)

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			touchSession(false)
			var ctl struct {
				Resize *struct {
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
				} `json:"resize"`
				Type     string `json:"type"`
				Password string `json:"password"`
			}
			if json.Unmarshal(data, &ctl) == nil {
				if ctl.Resize != nil && (ctl.Resize.Cols > 0 || ctl.Resize.Rows > 0) {
					_ = ts.resize(ctl.Resize.Cols, ctl.Resize.Rows)
					continue
				}
				if ctl.Type == "sudo_response" {
					if err := ts.writeInput([]byte(ctl.Password + "\n")); err != nil {
						_ = ts.writeWS(websocket.TextMessage, []byte("sudo stdin write failed: "+err.Error()))
					}
					ctl.Password = ""
					continue
				}
				if ctl.Type == "cancel_sudo" {
					_ = ts.writeInput([]byte{3})
					continue
				}
				if ctl.Type == "terminate_session" {
					ts.close(true)
					return
				}
			}
			_ = ts.writeInput(data)
			continue
		}
		touchSession(false)
		_ = ts.writeInput(data)
	}
}
