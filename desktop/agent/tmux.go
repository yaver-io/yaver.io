package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ansiRegex matches ANSI escape sequences (colors, cursor movement, etc.)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]|\x1b\[[\?]?[0-9;]*[hlm]`)

// TmuxSession represents a discovered tmux session with its relationship to Yaver.
type TmuxSession struct {
	Name         string `json:"name"`
	Windows      int    `json:"windows"`
	Created      string `json:"created"`
	Attached     bool   `json:"attached"`
	Relationship string `json:"relationship"`          // "adopted", "forked-by-yaver", "unrelated"
	AgentType    string `json:"agentType,omitempty"`    // "claude", "codex", "opencode"
	MainPID      int    `json:"mainPid,omitempty"`      // PID of the main process in the active pane
	PanePreview  string `json:"panePreview,omitempty"`  // last ~20 lines of pane output
	TaskID       string `json:"taskId,omitempty"`       // set if adopted as a Yaver task
}

// TmuxManager manages tmux session adoption and I/O bridging.
// It keeps track of adopted sessions and their polling goroutines.
type TmuxManager struct {
	mu       sync.RWMutex
	adopted  map[string]string             // tmux session name -> task ID
	taskMgr  *TaskManager
	pollStop map[string]context.CancelFunc // per-session poll cancellation
}

// knownAgentBinaries maps binary substrings to friendly agent type names.
// Only yaver's three first-class runners are recognised here.
var knownAgentBinaries = map[string]string{
	"claude":   "claude",
	"codex":    "codex",
	"opencode": "opencode",
}

// NewTmuxManager creates a TmuxManager. Returns nil if tmux is not available.
func NewTmuxManager(taskMgr *TaskManager) *TmuxManager {
	if !tmuxAvailable() {
		return nil
	}
	return &TmuxManager{
		adopted:  make(map[string]string),
		taskMgr:  taskMgr,
		pollStop: make(map[string]context.CancelFunc),
	}
}

// tmuxAvailable checks whether tmux is installed and in PATH.
func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// ListTmuxSessions returns all tmux sessions with metadata about their
// relationship to Yaver (adopted, forked-by-yaver, or unrelated).
func (m *TmuxManager) ListTmuxSessions() ([]TmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F",
		"#{session_name}|#{session_windows}|#{session_created}|#{session_attached}").CombinedOutput()
	if err != nil {
		// tmux returns error if no server is running (no sessions)
		if strings.Contains(string(out), "no server running") || strings.Contains(string(out), "no sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, string(out))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sessions := make([]TmuxSession, 0, len(lines))

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, line := range lines {
		if line == "" {
			continue
		}
		s := parseTmuxSessionLine(line)

		// Determine relationship
		if taskID, ok := m.adopted[s.Name]; ok {
			s.Relationship = "adopted"
			s.TaskID = taskID
		} else if m.isForkedByYaver(s.Name) {
			s.Relationship = "forked-by-yaver"
		} else {
			s.Relationship = "unrelated"
		}

		// Get pane PID and detect agent type
		s.MainPID = getPanePID(s.Name)
		if s.MainPID > 0 {
			s.AgentType = detectAgentType(s.MainPID)
		}

		// Get pane preview (last 20 lines)
		s.PanePreview = capturePanePreview(s.Name, 20)

		sessions = append(sessions, s)
	}
	return sessions, nil
}

// AdoptSession creates a Yaver task for an existing tmux session and starts
// polling its output. The tmux session continues running as-is.
func (m *TmuxManager) AdoptSession(sessionName string) (*Task, error) {
	// Verify the tmux session exists
	if !tmuxSessionExists(sessionName) {
		return nil, fmt.Errorf("tmux session %q not found", sessionName)
	}

	m.mu.Lock()
	if _, already := m.adopted[sessionName]; already {
		m.mu.Unlock()
		return nil, fmt.Errorf("tmux session %q is already adopted", sessionName)
	}
	m.mu.Unlock()

	// Detect what's running in the session
	pid := getPanePID(sessionName)
	agentType := ""
	if pid > 0 {
		agentType = detectAgentType(pid)
	}

	runnerID := agentType
	if runnerID == "" {
		runnerID = "unknown"
	}

	// Create a task in the task manager
	id := uuid.New().String()[:8]
	now := time.Now()
	task := &Task{
		ID:          id,
		Title:       fmt.Sprintf("tmux: %s", sessionName),
		Description: fmt.Sprintf("Adopted tmux session %q", sessionName),
		Status:      TaskStatusRunning,
		Source:      "tmux-adopted",
		RunnerID:    runnerID,
		TmuxSession: sessionName,
		IsAdopted:   true,
		CreatedAt:   now,
		StartedAt:   &now,
		outputCh:    make(chan string, 512),
		doneCh:      make(chan struct{}),
	}

	m.taskMgr.mu.Lock()
	m.taskMgr.tasks[id] = task
	m.taskMgr.persist()
	m.taskMgr.mu.Unlock()

	// Register adoption and start polling
	m.mu.Lock()
	m.adopted[sessionName] = id
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.pollStop[sessionName] = cancel
	m.mu.Unlock()

	go m.pollTmuxOutput(ctx, id, sessionName)

	log.Printf("[tmux] Adopted session %q as task %s (agent=%s, pid=%d)", sessionName, id, runnerID, pid)
	return task, nil
}

// DetachSession stops monitoring an adopted tmux session without killing it.
// The task is marked as stopped but the tmux session continues running.
func (m *TmuxManager) DetachSession(taskID string) error {
	m.mu.Lock()
	var sessionName string
	for name, tid := range m.adopted {
		if tid == taskID {
			sessionName = name
			break
		}
	}
	if sessionName == "" {
		m.mu.Unlock()
		return fmt.Errorf("task %s is not an adopted tmux session", taskID)
	}

	// Stop polling
	if cancel, ok := m.pollStop[sessionName]; ok {
		cancel()
		delete(m.pollStop, sessionName)
	}
	delete(m.adopted, sessionName)
	m.mu.Unlock()

	// Mark task as stopped
	m.taskMgr.mu.Lock()
	task, ok := m.taskMgr.tasks[taskID]
	if ok {
		task.Status = TaskStatusStopped
		now := time.Now()
		task.FinishedAt = &now
		// Close doneCh to unblock any SSE listeners
		if task.doneCh != nil {
			select {
			case <-task.doneCh:
			default:
				close(task.doneCh)
			}
		}
	}
	m.taskMgr.persist()
	m.taskMgr.mu.Unlock()

	log.Printf("[tmux] Detached session %q (task %s)", sessionName, taskID)
	return nil
}

// SendTmuxInput sends keyboard input to an adopted tmux session via send-keys.
func (m *TmuxManager) SendTmuxInput(taskID, input string) error {
	m.mu.RLock()
	var sessionName string
	for name, tid := range m.adopted {
		if tid == taskID {
			sessionName = name
			break
		}
	}
	m.mu.RUnlock()

	if sessionName == "" {
		return fmt.Errorf("task %s is not an adopted tmux session", taskID)
	}

	if !tmuxSessionExists(sessionName) {
		return fmt.Errorf("tmux session %q no longer exists", sessionName)
	}

	// Send the input followed by Enter
	out, err := exec.Command("tmux", "send-keys", "-t", sessionName, input, "Enter").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}

	// Record the input as a user turn
	m.taskMgr.mu.Lock()
	if task, ok := m.taskMgr.tasks[taskID]; ok {
		task.Turns = append(task.Turns, ConversationTurn{
			Role:      "user",
			Content:   input,
			Timestamp: time.Now(),
		})
		m.taskMgr.persist()
	}
	m.taskMgr.mu.Unlock()

	log.Printf("[tmux] Sent input to session (task %s): %s", taskID, truncate(input, 80))
	return nil
}

// pollTmuxOutput continuously captures the tmux pane and emits new content
// through the task's output channel. Runs until context is cancelled or the
// tmux session disappears.
func (m *TmuxManager) pollTmuxOutput(ctx context.Context, taskID, sessionName string) {
	var prevCapture string
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if tmux session still exists
			if !tmuxSessionExists(sessionName) {
				log.Printf("[tmux] Session %q disappeared — marking task %s as finished", sessionName, taskID)
				m.taskMgr.mu.Lock()
				if task, ok := m.taskMgr.tasks[taskID]; ok {
					task.Status = TaskStatusFinished
					now := time.Now()
					task.FinishedAt = &now
					if task.doneCh != nil {
						select {
						case <-task.doneCh:
						default:
							close(task.doneCh)
						}
					}
				}
				m.taskMgr.persist()
				m.taskMgr.mu.Unlock()

				// Clean up adoption state
				m.mu.Lock()
				delete(m.adopted, sessionName)
				delete(m.pollStop, sessionName)
				m.mu.Unlock()
				return
			}

			// Capture current pane content (last 200 lines for reasonable diff window)
			capture := capturePaneContent(sessionName, 200)
			if capture == "" || capture == prevCapture {
				continue
			}

			// Find new content by diffing
			newContent := diffCapture(prevCapture, capture)
			prevCapture = capture

			if newContent == "" {
				continue
			}

			// Emit new lines through the task's output channel
			m.taskMgr.mu.Lock()
			task, ok := m.taskMgr.tasks[taskID]
			if ok {
				task.Output += newContent
				// Truncate stored output to last 50000 chars
				if len(task.Output) > 50000 {
					task.Output = task.Output[len(task.Output)-50000:]
				}
				// Send to output channel (non-blocking)
				for _, line := range strings.Split(newContent, "\n") {
					if line == "" {
						continue
					}
					select {
					case task.outputCh <- line:
					default:
						// Channel full — drop oldest by draining one
						select {
						case <-task.outputCh:
						default:
						}
						task.outputCh <- line
					}
				}
			}
			m.taskMgr.mu.Unlock()
		}
	}
}

// ReAdoptOnStartup checks persisted adopted tasks and restarts polling for
// sessions that are still alive. Called during agent startup.
func (m *TmuxManager) ReAdoptOnStartup() {
	m.taskMgr.mu.Lock()
	defer m.taskMgr.mu.Unlock()

	for _, task := range m.taskMgr.tasks {
		if !task.IsAdopted || task.TmuxSession == "" {
			continue
		}
		if task.Status != TaskStatusRunning {
			continue
		}

		if tmuxSessionExists(task.TmuxSession) {
			// Re-create channels and restart polling
			task.outputCh = make(chan string, 512)
			task.doneCh = make(chan struct{})

			m.mu.Lock()
			m.adopted[task.TmuxSession] = task.ID
			ctx, cancel := context.WithCancel(context.Background())
			m.pollStop[task.TmuxSession] = cancel
			m.mu.Unlock()

			go m.pollTmuxOutput(ctx, task.ID, task.TmuxSession)
			log.Printf("[tmux] Re-adopted session %q for task %s on startup", task.TmuxSession, task.ID)
		} else {
			// Session gone — mark task as stopped
			task.Status = TaskStatusStopped
			now := time.Now()
			task.FinishedAt = &now
			log.Printf("[tmux] Session %q no longer exists — marking task %s as stopped", task.TmuxSession, task.ID)
		}
	}
	m.taskMgr.persist()
}

// Shutdown stops all polling goroutines. Called during agent shutdown.
func (m *TmuxManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, cancel := range m.pollStop {
		cancel()
		log.Printf("[tmux] Stopped polling for session %q", name)
	}
	m.pollStop = make(map[string]context.CancelFunc)
}

// isForkedByYaver checks if a tmux session was created by Yaver's process spawning.
// This checks if the session's pane PID is tracked in our forked-pids file.
func (m *TmuxManager) isForkedByYaver(sessionName string) bool {
	pid := getPanePID(sessionName)
	if pid <= 0 {
		return false
	}
	return isForkedPID(pid)
}

// --- Helper functions ---

// parseTmuxSessionLine parses a single line from `tmux list-sessions -F`.
// Format: "name|windows|created|attached"
func parseTmuxSessionLine(line string) TmuxSession {
	parts := strings.SplitN(line, "|", 4)
	s := TmuxSession{
		Name: parts[0],
	}
	if len(parts) > 1 {
		s.Windows, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		// Convert epoch to human-readable
		epoch, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil {
			s.Created = time.Unix(epoch, 0).Format("2006-01-02 15:04")
		} else {
			s.Created = parts[2]
		}
	}
	if len(parts) > 3 {
		s.Attached = parts[3] == "1"
	}
	return s
}

// tmuxSessionExists checks if a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

// getPanePID returns the PID of the active pane's process in a tmux session.
func getPanePID(sessionName string) int {
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName,
		"-F", "#{pane_pid}").CombinedOutput()
	if err != nil {
		return 0
	}
	// Take the first pane's PID
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0
	}
	return pid
}

// detectAgentType inspects the process tree starting from a PID to identify
// which AI agent is running. Returns the agent type or empty string.
func detectAgentType(pid int) string {
	// First check the process itself
	cmd := getProcessCommand(pid)
	if agent := matchAgentCommand(cmd); agent != "" {
		return agent
	}

	// Check child processes (the pane's shell spawns the agent)
	children := getChildPIDs(pid)
	for _, childPID := range children {
		cmd := getProcessCommand(childPID)
		if agent := matchAgentCommand(cmd); agent != "" {
			return agent
		}
	}
	return ""
}

// matchAgentCommand matches a process command string against known agent binaries.
func matchAgentCommand(cmd string) string {
	cmd = strings.ToLower(cmd)
	for binary, agentType := range knownAgentBinaries {
		// Match the binary name at a word boundary (avoid false positives)
		// Check if the binary name appears as a standalone command or path component
		if strings.Contains(cmd, "/"+binary) || strings.HasPrefix(cmd, binary+" ") || cmd == binary {
			return agentType
		}
	}
	return ""
}

// getProcessCommand returns the command line for a given PID.
func getProcessCommand(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getChildPIDs returns the PIDs of all direct child processes of a given PID.
func getChildPIDs(parentPID int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(parentPID)).CombinedOutput()
	if err != nil {
		return nil
	}
	var children []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil {
			children = append(children, pid)
		}
	}
	return children
}

// capturePanePreview captures the last N lines from a tmux pane.
func capturePanePreview(sessionName string, lines int) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", sessionName,
		"-p", "-S", fmt.Sprintf("-%d", lines)).CombinedOutput()
	if err != nil {
		return ""
	}
	return stripControlChars(strings.TrimRight(string(out), "\n "))
}

// capturePaneContent captures the last N lines from a tmux pane for diffing.
func capturePaneContent(sessionName string, lines int) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", sessionName,
		"-p", "-S", fmt.Sprintf("-%d", lines)).CombinedOutput()
	if err != nil {
		return ""
	}
	return stripControlChars(string(out))
}

// stripControlChars removes ANSI escape sequences and other control characters
// that would break JSON serialization.
func stripControlChars(s string) string {
	s = ansiRegex.ReplaceAllString(s, "")
	// Remove remaining non-printable control chars (except newline, tab, carriage return)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' || r >= 32 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// diffCapture finds new content by comparing previous and current pane captures.
// It looks for content in current that wasn't in previous by finding the longest
// matching suffix of prev in current, then returning everything after it.
func diffCapture(prev, current string) string {
	if prev == "" {
		return current
	}

	// Split into lines for comparison
	prevLines := strings.Split(prev, "\n")
	currLines := strings.Split(current, "\n")

	// Find where the previous content ends in the current content.
	// We look for the last few non-empty lines of prev in current.
	matchLines := lastNonEmptyLines(prevLines, 5)
	if len(matchLines) == 0 {
		return current
	}

	// Search for these lines in current
	matchTarget := strings.Join(matchLines, "\n")
	idx := strings.LastIndex(strings.Join(currLines, "\n"), matchTarget)
	if idx < 0 {
		// No overlap found — likely screen was cleared or scrolled significantly
		// Return the whole current capture
		return current
	}

	// Return everything after the match
	after := strings.Join(currLines, "\n")[idx+len(matchTarget):]
	after = strings.TrimLeft(after, "\n")
	if after == "" {
		return ""
	}
	return after
}

// lastNonEmptyLines returns the last N non-empty lines from a slice.
func lastNonEmptyLines(lines []string, n int) []string {
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			result = append([]string{lines[i]}, result...)
		}
	}
	return result
}

// isForkedPID checks if a PID was forked by the Yaver agent.
// Uses the existing getForkedPIDs() from tasks.go.
func isForkedPID(pid int) bool {
	for _, p := range getForkedPIDs() {
		if p == pid {
			return true
		}
	}
	return false
}
