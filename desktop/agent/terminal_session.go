package main

import (
	"encoding/json"
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

func (s *HTTPServer) terminalSessionByID(id string) (*terminalSession, bool) {
	v, ok := s.terminalSessions.Load(id)
	if !ok {
		return nil, false
	}
	ts, ok := v.(*terminalSession)
	return ts, ok
}

func (ts *terminalSession) waitLoop() {
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
	process := ts.cmd.Process
	ts.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if ptmx != nil {
		_ = ptmx.Close()
	}
	if process != nil {
		_ = process.Kill()
	}
	if remove && ts.srv != nil {
		ts.srv.terminalSessions.Delete(ts.id)
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
			return
		}
	}
}
