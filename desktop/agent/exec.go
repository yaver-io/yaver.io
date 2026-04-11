package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ExecStatus represents the state of an exec session.
type ExecStatus string

const (
	ExecStatusRunning   ExecStatus = "running"
	ExecStatusCompleted ExecStatus = "completed"
	ExecStatusFailed    ExecStatus = "failed"
	ExecStatusKilled    ExecStatus = "killed"
)

const (
	maxExecOutputSize  = 1 << 20 // 1MB per stream
	maxExecConcurrent  = 10
	defaultExecTimeout = 5 * time.Minute
	maxExecTimeout     = 1 * time.Hour
	execCleanupAge     = 1 * time.Hour
)

// ExecSession represents a running or completed command execution.
type ExecSession struct {
	ID         string     `json:"id"`
	Command    string     `json:"command"`
	Status     ExecStatus `json:"status"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Stdout     string     `json:"stdout"`
	Stderr     string     `json:"stderr"`
	PID        int        `json:"pid,omitempty"`
	StartedAt  string     `json:"startedAt"`
	FinishedAt string     `json:"finishedAt,omitempty"`

	// Internal — not serialized
	cmd    *exec.Cmd      `json:"-"`
	cancel context.CancelFunc `json:"-"`
	stdin  interface{ Write([]byte) (int, error); Close() error } `json:"-"`
	stdout *cappedBuffer  `json:"-"`
	stderr *cappedBuffer  `json:"-"`
	mu     sync.RWMutex   `json:"-"`
	doneCh chan struct{}   `json:"-"`

	// SSE listeners
	listeners   []chan ExecOutputEvent `json:"-"`
	listenersMu sync.Mutex            `json:"-"`
}

// ExecOutputEvent is sent to SSE listeners.
type ExecOutputEvent struct {
	Type string `json:"type"` // "stdout", "stderr", "exit"
	Text string `json:"text,omitempty"`
	Code *int   `json:"code,omitempty"`
}

// ExecManager manages concurrent command execution sessions.
type ExecManager struct {
	mu         sync.RWMutex
	sessions   map[string]*ExecSession
	workDir    string
	sandbox    SandboxConfig
	OnExecDone func(command string, exitCode int) // called when exec finishes
}

// NewExecManager creates a new exec manager.
func NewExecManager(workDir string, sandbox *SandboxConfig) *ExecManager {
	sbx := DefaultSandboxConfig()
	if sandbox != nil {
		sbx = *sandbox
	}
	em := &ExecManager{
		sessions: make(map[string]*ExecSession),
		workDir:  workDir,
		sandbox:  sbx,
	}
	// Start cleanup goroutine
	go em.cleanupLoop()
	return em
}

// StartExec starts a new command execution session.
func (em *ExecManager) StartExec(command, workDir, shell string, env map[string]string, timeoutSec int) (*ExecSession, error) {
	// Check concurrent limit
	em.mu.RLock()
	running := 0
	for _, s := range em.sessions {
		s.mu.RLock()
		if s.Status == ExecStatusRunning {
			running++
		}
		s.mu.RUnlock()
	}
	em.mu.RUnlock()
	if running >= maxExecConcurrent {
		return nil, fmt.Errorf("too many concurrent exec sessions (%d/%d)", running, maxExecConcurrent)
	}

	// Validate command
	if err := ValidateCommand(command, em.sandbox); err != nil {
		return nil, fmt.Errorf("command blocked: %w", err)
	}

	// Resolve work directory
	if workDir == "" {
		workDir = em.workDir
	}
	if err := ValidateWorkDir(workDir, em.sandbox); err != nil {
		return nil, fmt.Errorf("work directory blocked: %w", err)
	}

	// Resolve shell
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "cmd"
		} else {
			shell = "sh"
		}
	}

	// Resolve timeout
	timeout := defaultExecTimeout
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
		if timeout > maxExecTimeout {
			timeout = maxExecTimeout
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Build command
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, shell, "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, shell, "-c", command)
	}
	cmd.Dir = workDir

	// Set process group for signal delivery (platform-specific)
	setProcGroup(cmd)

	// Merge environment
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Set up pipes
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	session := &ExecSession{
		ID:        uuid.New().String(),
		Command:   command,
		Status:    ExecStatusRunning,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		cmd:       cmd,
		cancel:    cancel,
		stdin:     stdinPipe,
		stdout:    newCappedBuffer(maxExecOutputSize),
		stderr:    newCappedBuffer(maxExecOutputSize),
		doneCh:    make(chan struct{}),
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start command: %w", err)
	}
	session.PID = cmd.Process.Pid
	log.Printf("[exec] Started session %s: %s (pid=%d, timeout=%s)", session.ID, command, session.PID, timeout)

	// Read stdout in background
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				text := string(buf[:n])
				session.stdout.Write(buf[:n])
				session.broadcast(ExecOutputEvent{Type: "stdout", Text: text})
			}
			if err != nil {
				break
			}
		}
	}()

	// Read stderr in background
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				text := string(buf[:n])
				session.stderr.Write(buf[:n])
				session.broadcast(ExecOutputEvent{Type: "stderr", Text: text})
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for completion in background
	go func() {
		defer close(session.doneCh)
		defer cancel()

		err := cmd.Wait()
		session.mu.Lock()
		session.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		session.Stdout = session.stdout.String()
		session.Stderr = session.stderr.String()

		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				session.Status = ExecStatusKilled
				code := -1
				session.ExitCode = &code
				log.Printf("[exec] Session %s timed out", session.ID)
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				session.Status = ExecStatusFailed
				code := exitErr.ExitCode()
				session.ExitCode = &code
				log.Printf("[exec] Session %s exited with code %d", session.ID, code)
			} else {
				session.Status = ExecStatusFailed
				code := -1
				session.ExitCode = &code
				log.Printf("[exec] Session %s failed: %v", session.ID, err)
			}
		} else {
			session.Status = ExecStatusCompleted
			code := 0
			session.ExitCode = &code
			log.Printf("[exec] Session %s completed (exit 0)", session.ID)
		}
		session.mu.Unlock()

		session.broadcast(ExecOutputEvent{Type: "exit", Code: session.ExitCode})
		session.closeListeners()

		if em.OnExecDone != nil {
			code := 0
			if session.ExitCode != nil {
				code = *session.ExitCode
			}
			go em.OnExecDone(session.Command, code)
		}
	}()

	em.mu.Lock()
	em.sessions[session.ID] = session
	em.mu.Unlock()

	return session, nil
}

// GetExec returns an exec session by ID.
func (em *ExecManager) GetExec(id string) (*ExecSession, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	s, ok := em.sessions[id]
	return s, ok
}

// ListExecs returns all exec sessions.
func (em *ExecManager) ListExecs() []*ExecSession {
	em.mu.RLock()
	defer em.mu.RUnlock()
	result := make([]*ExecSession, 0, len(em.sessions))
	for _, s := range em.sessions {
		result = append(result, s)
	}
	return result
}

// SendInput writes to the stdin of a running exec session.
func (em *ExecManager) SendInput(id, input string) error {
	em.mu.RLock()
	s, ok := em.sessions[id]
	em.mu.RUnlock()
	if !ok {
		return fmt.Errorf("exec session not found: %s", id)
	}
	s.mu.RLock()
	if s.Status != ExecStatusRunning {
		s.mu.RUnlock()
		return fmt.Errorf("exec session %s is not running (status: %s)", id, s.Status)
	}
	s.mu.RUnlock()
	if s.stdin == nil {
		return fmt.Errorf("stdin not available")
	}
	_, err := s.stdin.Write([]byte(input))
	return err
}

// SignalExec sends a signal to a running exec session.
func (em *ExecManager) SignalExec(id, sig string) error {
	em.mu.RLock()
	s, ok := em.sessions[id]
	em.mu.RUnlock()
	if !ok {
		return fmt.Errorf("exec session not found: %s", id)
	}
	s.mu.RLock()
	if s.Status != ExecStatusRunning {
		s.mu.RUnlock()
		return fmt.Errorf("exec session %s is not running", id)
	}
	s.mu.RUnlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return fmt.Errorf("process not available")
	}

	sig = strings.ToUpper(sig)
	switch sig {
	case "SIGINT", "INT":
		return killProcessGroup(s.cmd.Process.Pid, "INT")
	case "SIGTERM", "TERM":
		return killProcessGroup(s.cmd.Process.Pid, "TERM")
	case "SIGKILL", "KILL":
		return killProcessGroup(s.cmd.Process.Pid, "KILL")
	default:
		return fmt.Errorf("unsupported signal: %s (use SIGINT, SIGTERM, or SIGKILL)", sig)
	}
}

// KillExec kills an exec session and removes it.
func (em *ExecManager) KillExec(id string) error {
	em.mu.RLock()
	s, ok := em.sessions[id]
	em.mu.RUnlock()
	if !ok {
		return fmt.Errorf("exec session not found: %s", id)
	}
	s.mu.RLock()
	running := s.Status == ExecStatusRunning
	s.mu.RUnlock()
	if running {
		if s.cmd != nil && s.cmd.Process != nil {
			killProcessGroup(s.cmd.Process.Pid, "KILL")
		}
		s.cancel()
		// Wait for process to finish
		select {
		case <-s.doneCh:
		case <-time.After(5 * time.Second):
		}
	}
	em.mu.Lock()
	delete(em.sessions, id)
	em.mu.Unlock()
	return nil
}

// Subscribe returns a channel that receives output events for an exec
// session. If the session is still running, the channel is registered
// as a live listener and gets every event broadcast() emits. If the
// session has *already* finished by the time the caller subscribes
// (the common race on a fast CI runner where a one-shot `echo`
// command exits before the SSE consumer connects), we replay the
// buffered stdout/stderr + the exit event into a fresh buffered
// channel and close it. This guarantees late subscribers always see
// the full stream — fixing a flake in TestExecStreamSSE that only
// reproduced under CPU pressure on GHA.
func (em *ExecManager) Subscribe(id string) (<-chan ExecOutputEvent, error) {
	em.mu.RLock()
	s, ok := em.sessions[id]
	em.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("exec session not found: %s", id)
	}

	s.mu.RLock()
	finished := s.Status != ExecStatusRunning
	stdout := s.Stdout
	if stdout == "" {
		stdout = s.stdout.String()
	}
	stderr := s.Stderr
	if stderr == "" {
		stderr = s.stderr.String()
	}
	exitCode := s.ExitCode
	s.mu.RUnlock()

	if finished {
		// Replay buffered output then close. Buffer big enough to
		// hold every line + the exit event without blocking.
		stdoutLines := splitNonEmptyLines(stdout)
		stderrLines := splitNonEmptyLines(stderr)
		ch := make(chan ExecOutputEvent, len(stdoutLines)+len(stderrLines)+2)
		for _, line := range stdoutLines {
			ch <- ExecOutputEvent{Type: "stdout", Text: line + "\n"}
		}
		for _, line := range stderrLines {
			ch <- ExecOutputEvent{Type: "stderr", Text: line + "\n"}
		}
		exit := ExecOutputEvent{Type: "exit"}
		if exitCode != nil {
			code := *exitCode
			exit.Code = &code
		}
		ch <- exit
		close(ch)
		return ch, nil
	}

	ch := make(chan ExecOutputEvent, 64)
	s.listenersMu.Lock()
	s.listeners = append(s.listeners, ch)
	s.listenersMu.Unlock()
	return ch, nil
}

// splitNonEmptyLines splits text on newlines and drops empty trailers.
func splitNonEmptyLines(text string) []string {
	if text == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			if i > start {
				out = append(out, text[start:i])
			}
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

// Snapshot returns a copy of the session data safe for JSON serialization.
func (s *ExecSession) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stdout := s.Stdout
	stderr := s.Stderr
	// For running sessions, read from the buffer
	if s.Status == ExecStatusRunning {
		stdout = s.stdout.String()
		stderr = s.stderr.String()
	}

	m := map[string]interface{}{
		"id":        s.ID,
		"command":   s.Command,
		"status":    s.Status,
		"stdout":    stdout,
		"stderr":    stderr,
		"startedAt": s.StartedAt,
	}
	if s.ExitCode != nil {
		m["exitCode"] = *s.ExitCode
	}
	if s.PID > 0 {
		m["pid"] = s.PID
	}
	if s.FinishedAt != "" {
		m["finishedAt"] = s.FinishedAt
	}
	return m
}

func (s *ExecSession) broadcast(evt ExecOutputEvent) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	for _, ch := range s.listeners {
		select {
		case ch <- evt:
		default:
			// drop if full
		}
	}
}

func (s *ExecSession) closeListeners() {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	for _, ch := range s.listeners {
		close(ch)
	}
	s.listeners = nil
}

func (em *ExecManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		em.mu.Lock()
		for id, s := range em.sessions {
			s.mu.RLock()
			if s.Status != ExecStatusRunning && s.FinishedAt != "" {
				if t, err := time.Parse(time.RFC3339, s.FinishedAt); err == nil {
					if time.Since(t) > execCleanupAge {
						delete(em.sessions, id)
					}
				}
			}
			s.mu.RUnlock()
		}
		em.mu.Unlock()
	}
}

// cappedBuffer is a thread-safe buffer that caps at maxSize bytes, keeping the latest.
type cappedBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	maxSize int
}

func newCappedBuffer(maxSize int) *cappedBuffer {
	return &cappedBuffer{maxSize: maxSize}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	// If buffer exceeds max, keep only the last maxSize bytes
	if b.buf.Len() > b.maxSize {
		data := b.buf.Bytes()
		b.buf.Reset()
		b.buf.Write(data[len(data)-b.maxSize:])
	}
	return n, err
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
