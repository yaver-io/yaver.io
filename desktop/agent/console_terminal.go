package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// sudoPromptPattern matches the prompts that BSD/Linux sudo writes to
// the PTY right before it reads the password. We keep this narrow on
// purpose — firing a fake "password needed" sheet on every `echo "pw"`
// would be worse than letting the user type the password themselves.
//
// Matches:
//   [sudo] password for alice:
//   [sudo] Password:
//   Password:                      (generic, only after a sudo-ish command word on the same buffer)
var sudoPromptPattern = regexp.MustCompile(`(?m)(\[sudo\]\s*[Pp]assword(?:\s+for\s+\S+)?\s*:|^[Ss]udo\s+password\s*:)`)

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

	// wsMu serialises all writes on the websocket — the ptmx→ws
	// goroutine and the sudo-prompt detector both write, and gorilla
	// WS connections are NOT safe for concurrent writes.
	var wsMu sync.Mutex
	writeWS := func(mt int, payload []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		return conn.WriteMessage(mt, payload)
	}

	// lastPromptAt rate-limits duplicate sudo-prompt emissions so a
	// handful of echoed prompt characters don't spawn a cascade of
	// modal sheets on the mobile side.
	var lastPromptAt time.Time

	// pty → ws
	go func() {
		buf := make([]byte, 4096)
		// tail keeps a 256-byte sliding window of recent output so we
		// can still match a prompt that straddles a read boundary.
		tail := make([]byte, 0, 512)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := writeWS(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
				// Detect sudo-style password prompts and tell the client
				// to pop a secure input sheet. The prompt itself still
				// goes to the terminal (see writeWS above) so xterm-style
				// clients render it as usual — this is purely additive.
				tail = append(tail, buf[:n]...)
				if len(tail) > 512 {
					tail = tail[len(tail)-512:]
				}
				if loc := sudoPromptPattern.FindIndex(tail); loc != nil {
					if time.Since(lastPromptAt) > 500*time.Millisecond {
						lastPromptAt = time.Now()
						prompt := string(tail[loc[0]:loc[1]])
						frame, _ := json.Marshal(map[string]any{
							"type":   "sudo_prompt",
							"prompt": prompt,
							"hint":   "The shell is waiting for a sudo password. The password is sent once, as stdin, and is never logged or persisted.",
						})
						_ = writeWS(websocket.TextMessage, frame)
					}
					// Drop the matched range so the next prompt can
					// re-trigger without needing 500ms of silence.
					tail = tail[loc[1]:]
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = writeWS(websocket.TextMessage, []byte("pty read err: "+err.Error()))
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
			// Try resize control first.
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
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: ctl.Resize.Cols, Rows: ctl.Resize.Rows})
					continue
				}
				if ctl.Type == "sudo_response" {
					// Write the password + newline to the PTY so sudo
					// receives it as if typed. The OS already has echo
					// disabled during sudo's read, so the password is
					// never echoed back to the output stream.
					if _, err := ptmx.Write([]byte(ctl.Password + "\n")); err != nil {
						_ = writeWS(websocket.TextMessage, []byte("sudo stdin write failed: "+err.Error()))
					}
					// Zero the password string we just sent. Go strings
					// are immutable, but nilling the local reference
					// lets the GC reclaim it sooner.
					ctl.Password = ""
					continue
				}
				if ctl.Type == "cancel_sudo" {
					// Send ^C so sudo aborts the prompt cleanly.
					_, _ = ptmx.Write([]byte{3})
					continue
				}
			}
			// Fallback: treat as raw stdin.
			_, _ = ptmx.Write(data)
			continue
		}
		// Binary = raw stdin
		_, _ = ptmx.Write(data)
	}
}
