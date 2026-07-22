package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	terminalSessionReplayLimit = 128 * 1024
	terminalSessionIdleTTL     = 90 * time.Second
)

type terminalSession struct {
	id      string
	cmd     *exec.Cmd
	ptmx    *os.File
	srv     *HTTPServer
	onTouch func(force bool)
	// onClose runs once, after the PTY and process are torn down, for cleanup
	// that lives OUTSIDE the process tree — killing the PTY cannot reach it.
	// The tmux-attach path (runner_pty_attach.go) uses it to reap the grouped
	// mirror session, which belongs to the tmux server, not to this pty.
	onClose func()

	mu             sync.Mutex
	conn           *websocket.Conn
	wsMu           sync.Mutex
	replay         []byte
	promptTail     []byte
	lastPromptAt   time.Time
	closed         bool
	detachTimer    *time.Timer
	hostShareID    string
	guestUserID    string
	workspaceDir   string
	runnerID       string
	createdAt      time.Time
	lastAttachedAt time.Time
}

func (s *HTTPServer) newTerminalSession(cmd *exec.Cmd, onTouch func(bool), hostShareID, guestUserID, workspaceDir string) (*terminalSession, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	ts := &terminalSession{
		id:           uuid.New().String(),
		cmd:          cmd,
		ptmx:         ptmx,
		srv:          s,
		onTouch:      onTouch,
		hostShareID:  hostShareID,
		guestUserID:  guestUserID,
		workspaceDir: workspaceDir,
		createdAt:    time.Now(),
	}
	s.terminalSessions.Store(ts.id, ts)
	go ts.readLoop()
	go ts.waitLoop()
	return ts, nil
}

// newTerminalSessionFromPTY adopts a PTY master that was opened elsewhere — in
// particular, one handed back by the privilege-separated helper for a tenant
// shell (helper_client_fd_unix.go). There is no local *exec.Cmd to Wait on; the
// session closes when the master hits EOF (the remote shell exited), handled in
// readLoop.
func (s *HTTPServer) newTerminalSessionFromPTY(ptmx *os.File, onTouch func(bool), hostShareID, guestUserID, workspaceDir string) (*terminalSession, error) {
	if ptmx == nil {
		return nil, fmt.Errorf("nil pty master")
	}
	ts := &terminalSession{
		id:           uuid.New().String(),
		cmd:          nil, // helper-owned process; no local Wait
		ptmx:         ptmx,
		srv:          s,
		onTouch:      onTouch,
		hostShareID:  hostShareID,
		guestUserID:  guestUserID,
		workspaceDir: workspaceDir,
		createdAt:    time.Now(),
	}
	s.terminalSessions.Store(ts.id, ts)
	go ts.readLoop()
	// No waitLoop: readLoop's EOF path closes the session for cmd-less sessions.
	return ts, nil
}

func (s *HTTPServer) terminalSessionByID(id string) (*terminalSession, bool) {
	v, ok := s.terminalSessions.Load(id)
	if !ok {
		return nil, false
	}
	ts, ok := v.(*terminalSession)
	return ts, ok
}

func (ts *terminalSession) waitLoop() {
	if ts.cmd == nil {
		return // helper-brokered session; readLoop's EOF path closes it
	}
	_ = ts.cmd.Wait()
	ts.close(true)
}

func (ts *terminalSession) appendReplay(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	ts.replay = append(ts.replay, chunk...)
	if len(ts.replay) > terminalSessionReplayLimit {
		ts.replay = ts.replay[len(ts.replay)-terminalSessionReplayLimit:]
	}
}

func (ts *terminalSession) writeWS(mt int, payload []byte) error {
	ts.wsMu.Lock()
	defer ts.wsMu.Unlock()
	if ts.conn == nil {
		return io.EOF
	}
	return ts.conn.WriteMessage(mt, payload)
}

func (ts *terminalSession) emitSessionMeta(resumed bool) {
	frame, _ := json.Marshal(map[string]any{
		"type":         "terminal_session",
		"sessionId":    ts.id,
		"resumed":      resumed,
		"createdAt":    ts.createdAt.UTC().Format(time.RFC3339),
		"hostShareId":  ts.hostShareID,
		"guestUserId":  ts.guestUserID,
		"workspaceDir": ts.workspaceDir,
	})
	_ = ts.writeWS(websocket.TextMessage, frame)
}

func (ts *terminalSession) attach(conn *websocket.Conn, resumed bool) error {
	ts.mu.Lock()
	if ts.closed {
		ts.mu.Unlock()
		return io.EOF
	}
	if ts.detachTimer != nil {
		ts.detachTimer.Stop()
		ts.detachTimer = nil
	}
	prevConn := ts.conn
	ts.conn = conn
	replay := append([]byte(nil), ts.replay...)
	ts.lastAttachedAt = time.Now()
	ts.mu.Unlock()

	if prevConn != nil && prevConn != conn {
		_ = prevConn.Close()
	}

	ts.emitSessionMeta(resumed)
	if len(replay) > 0 {
		if err := ts.writeWS(websocket.BinaryMessage, replay); err != nil {
			return err
		}
	}
	if ts.onTouch != nil {
		ts.onTouch(true)
	}
	return nil
}

func (ts *terminalSession) scheduleDetach() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.closed {
		return
	}
	if ts.detachTimer != nil {
		ts.detachTimer.Stop()
	}
	ts.detachTimer = time.AfterFunc(terminalSessionIdleTTL, func() {
		ts.close(true)
	})
}

func (ts *terminalSession) detach(conn *websocket.Conn) {
	ts.mu.Lock()
	if ts.conn == conn {
		ts.conn = nil
	}
	ts.mu.Unlock()
	ts.scheduleDetach()
}

func (ts *terminalSession) close(remove bool) {
	ts.mu.Lock()
	if ts.closed {
		ts.mu.Unlock()
		return
	}
	ts.closed = true
	conn := ts.conn
	ts.conn = nil
	if ts.detachTimer != nil {
		ts.detachTimer.Stop()
		ts.detachTimer = nil
	}
	ptmx := ts.ptmx
	var process *os.Process
	if ts.cmd != nil { // helper-brokered sessions have no local cmd
		process = ts.cmd.Process
	}
	onClose := ts.onClose
	ts.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if ptmx != nil {
		// Closing the master sends SIGHUP to the tenant shell's session (the
		// helper reaps it); for local cmds we also Kill below.
		_ = ptmx.Close()
	}
	if process != nil {
		_ = process.Kill()
	}
	if remove && ts.srv != nil {
		ts.srv.terminalSessions.Delete(ts.id)
	}
	if onClose != nil {
		onClose()
	}
}

func (ts *terminalSession) resize(cols, rows uint16) error {
	return pty.Setsize(ts.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (ts *terminalSession) writeInput(data []byte) error {
	_, err := ts.ptmx.Write(data)
	return err
}

func (ts *terminalSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := ts.ptmx.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			ts.mu.Lock()
			ts.appendReplay(chunk)
			ts.promptTail = append(ts.promptTail, chunk...)
			if len(ts.promptTail) > 512 {
				ts.promptTail = ts.promptTail[len(ts.promptTail)-512:]
			}
			promptTail := append([]byte(nil), ts.promptTail...)
			shouldPrompt := false
			if loc := sudoPromptPattern.FindIndex(promptTail); loc != nil && time.Since(ts.lastPromptAt) > 500*time.Millisecond {
				ts.lastPromptAt = time.Now()
				promptTail = promptTail[loc[0]:loc[1]]
				ts.promptTail = ts.promptTail[loc[1]:]
				shouldPrompt = true
			}
			ts.mu.Unlock()

			if ts.onTouch != nil {
				ts.onTouch(false)
			}
			if ts.runnerID != "" && IsRunnerAuthFailureOutput(string(chunk)) == ts.runnerID {
				MarkRunnerAuthInvalid(ts.runnerID)
			}
			_ = ts.writeWS(websocket.BinaryMessage, chunk)
			if shouldPrompt {
				frame, _ := json.Marshal(map[string]any{
					"type":   "sudo_prompt",
					"prompt": string(promptTail),
					"hint":   "The shell is waiting for a sudo password. The password is sent once, as stdin, and is never logged or persisted.",
				})
				_ = ts.writeWS(websocket.TextMessage, frame)
			}
		}
		if err != nil {
			if err != io.EOF {
				_ = ts.writeWS(websocket.TextMessage, []byte("pty read err: "+err.Error()))
			}
			// Helper-brokered sessions have no local cmd/waitLoop, so the PTY
			// EOF (remote shell exited) is the only close signal — act on it.
			if ts.cmd == nil {
				ts.close(true)
			}
			return
		}
	}
}
