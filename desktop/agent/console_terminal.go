package main

import (
	"encoding/json"
	"log"
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

		isHostShare := r.Header.Get("X-Yaver-HostShare") == "true"
		cwd := r.URL.Query().Get("cwd")

		// STEP 5: on a confined operator node (NoNewPrivileges=true, no sudo)
		// the tenant's shell is spawned by the root helper, which drops to the
		// tenant uid and hands us the PTY master fd. Try this first; on any
		// failure fall through to the sudo path below (non-confined nodes).
		if s.operatorMode && isHostShare && tenantOSUsersEnabled() && guestUserID != "" && helperAvailable() {
			if name, home, terr := ensureTenantOSUser(guestUserID); terr == nil {
				tshell := shell
				if validShell(tshell) != nil {
					tshell = "/bin/bash"
				}
				tcwd := strings.TrimSpace(cwd)
				if tcwd == "" {
					tcwd = home + "/Workspace"
				}
				env := append(s.gatewayInjectEnv(guestUserID), "TERM=xterm-256color")
				if hostShareSessionID != "" {
					env = append(env,
						"YAVER_HOST_SHARE=1",
						"YAVER_HOST_SHARE_SESSION_ID="+hostShareSessionID,
						"YAVER_HOST_SHARE_GUEST_USER_ID="+guestUserID,
					)
				}
				if ptmx, ferr := helperTenantShellFD(name, tshell, env, tcwd); ferr == nil {
					if sess, serr := s.newTerminalSessionFromPTY(ptmx, touchSession, hostShareSessionID, guestUserID, ""); serr == nil {
						ts = sess
						touchSession(true)
					} else {
						_ = ptmx.Close()
						log.Printf("[OPERATOR] helper tenant PTY session for %s failed (%v); falling back to sudo path", guestUserID, serr)
					}
				} else {
					log.Printf("[OPERATOR] helper tenant shell for %s unavailable (%v); falling back to sudo path", guestUserID, ferr)
				}
			}
		}

		var cmd *exec.Cmd
		tenantOSUser := ""
		switch {
		case ts != nil:
			// Already started via the privilege-separated helper above.
		case s.operatorMode && isHostShare && tenantOSUsersEnabled() && guestUserID != "":
			// OPERATOR FLEET (primary isolation, docs §4b): run the tenant's
			// shell AS their own unprivileged OS user (yv-<id>), in their
			// $HOME/Workspace, with ONLY the gateway inference env overlaid.
			// A tenant can't read the operator/yaver files or another
			// tenant's home; the upstream key never appears (scoped token).
			if name, home, err := ensureTenantOSUser(guestUserID); err == nil {
				inject := append(s.gatewayInjectEnv(guestUserID), "TERM=xterm-256color")
				argv := tenantShellArgv(name, shell, inject)
				cmd = exec.Command(argv[0], argv[1:]...)
				// Don't leak the agent's env into the sudo invocation; sudo
				// resets anyway, and `env …` overlays what the tenant needs.
				cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
				tenantOSUser = name
				if strings.TrimSpace(cwd) == "" {
					cwd = home + "/Workspace"
				}
			} else {
				log.Printf("[OPERATOR] tenant OS user for %s unavailable (%v); falling back to scoped yaver shell", guestUserID, err)
			}
		case s.operatorMode && isHostShare:
			// Fallback when OS users aren't available (non-Linux / no sudo):
			// run as the yaver agent user but with a secret-stripped env +
			// the gateway provider. Still never the host key.
			cmd = exec.Command(shell)
			cmd.Env = append(s.tenantRunnerBaseEnv(guestUserID), "TERM=xterm-256color")
		}
		if ts == nil {
			if cmd == nil {
				cmd = exec.Command(shell)
				cmd.Env = append(os.Environ(), "TERM=xterm-256color")
			}
			workspaceDir := ""
			if isHostShare {
				cmd.Env = append(cmd.Env,
					"YAVER_HOST_SHARE=1",
					"YAVER_HOST_SHARE_SESSION_ID="+hostShareSessionID,
					"YAVER_HOST_SHARE_GUEST_USER_ID="+guestUserID,
				)
				// When the tenant runs as their own OS user, keep cwd at their
				// $HOME/Workspace (set above) — don't redirect to the shared
				// host-share workspace dir.
				if tenantOSUser == "" && s.hostShareWorkspaceMgr != nil && hostShareSessionID != "" {
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
