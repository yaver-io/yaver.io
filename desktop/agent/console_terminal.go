package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// handleTerminalWS: WS /ws/terminal — starts a PTY and pipes bytes both ways.
// Protocol:
//   - binary frames: stdin bytes → pty
//   - text frames:   {"resize":{"cols":N,"rows":M}} → resize signal
//   - server emits binary frames of stdout/stderr bytes
func (s *HTTPServer) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	shell := r.URL.Query().Get("shell")
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/bash"
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cwd := r.URL.Query().Get("cwd")
	if cwd != "" {
		cmd.Dir = cwd
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("pty start failed: "+err.Error()))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	// pty → ws
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					conn.WriteMessage(websocket.TextMessage, []byte("pty read err: "+err.Error()))
				}
				return
			}
		}
	}()

	// ws → pty
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			// Attempt to parse resize messages.
			var ctl struct {
				Resize struct {
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
				} `json:"resize"`
			}
			if json.Unmarshal(data, &ctl) == nil && (ctl.Resize.Cols > 0 || ctl.Resize.Rows > 0) {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: ctl.Resize.Cols, Rows: ctl.Resize.Rows})
				continue
			}
			// Otherwise treat as stdin.
			_, _ = ptmx.Write(data)
			continue
		}
		// Binary = raw stdin
		_, _ = ptmx.Write(data)
	}
}
