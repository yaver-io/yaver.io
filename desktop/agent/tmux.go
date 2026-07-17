package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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
	AgentType    string `json:"agentType,omitempty"`   // "claude", "codex", "opencode"
	MainPID      int    `json:"mainPid,omitempty"`     // PID of the main process in the active pane
	PanePreview  string `json:"panePreview,omitempty"` // last ~20 lines of pane output
	TaskID       string `json:"taskId,omitempty"`      // set if adopted as a Yaver task
}

// TmuxManager manages tmux session adoption and I/O bridging.
// It keeps track of adopted sessions and their polling goroutines.
type TmuxManager struct {
	mu       sync.RWMutex
	adopted  map[string]string // tmux session name -> task ID
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

// tmuxBin returns the absolute path to tmux, or "" if it is not installed.
//
// The agent is launched by launchd/systemd with a minimal $PATH (observed on
// the Mac mini: PATH=/usr/bin:/bin:/usr/sbin:/sbin), which does not include
// /opt/homebrew/bin where tmux lives on Apple Silicon. augmentAgentPATH()
// (main.go) is the first thing main() does and normally repairs that, so a
// plain exec.LookPath usually works.
//
// This is belt-and-braces for the cases where it does not:
//   - augmentAgentPATH returns early when os.UserHomeDir() fails, leaving the
//     minimal $PATH intact;
//   - it probes a narrower set of prefixes than binary_discovery.go (no
//     cargo/snap/flatpak/pipx);
//   - it runs only via main(), so any path that reaches this code without
//     going through main() (tests, future embedding) never gets the repair.
//
// tmux is load-bearing — the runner TUI, the keeper, and every autorun seat are
// driven through it — so it is worth resolving from the same source of truth
// that /infra/summary reports from, rather than from whatever $PATH happens to
// hold.
//
// Resolving to an ABSOLUTE path matters as much as finding it: callers exec
// tmux, and a bare "tmux" argv would re-inherit whatever $PATH the lookup just
// worked around.
func tmuxBin() string {
	return DiscoverBinary("tmux")
}

// tmuxCmdName returns the tmux argv[0] for exec. It falls back to the bare name
// so a caller still produces the familiar "executable file not found" error
// rather than trying to exec "".
func tmuxCmdName() string {
	if p := tmuxBin(); p != "" {
		return p
	}
	return "tmux"
}

// tmuxAvailable reports whether tmux is installed anywhere this agent can reach
// it — not merely whether it is on $PATH.
func tmuxAvailable() bool {
	return tmuxBin() != ""
}

// EnsureTmuxInstalled installs tmux when it is missing, best-effort, at agent
// startup. Reports whether tmux is usable afterwards.
//
// Why the agent installs this itself rather than printing a hint: tmux is not a
// nice-to-have. autorun, the runner keeper, and every runner seat are driven
// through it, so a box without tmux accepts an autorun and then silently never
// runs it. That is not a thought experiment — a Mac mini here sat with a
// configured autorun loop that could not start, because nothing on the box had
// ever installed tmux and `yaver serve` only mentioned it in a log line about
// the Terminal tab. A fresh cloud machine has exactly the same hole.
//
// Constraints, because this runs unattended inside a daemon:
//   - NEVER prompt. brew needs no sudo; on Linux we install only as root or
//     when `sudo -n` already works. Otherwise we decline and say so, rather
//     than hanging serve on a password prompt forever.
//   - NEVER fatal. A box with no package manager is still a useful agent; it
//     just cannot host runner seats.
func EnsureTmuxInstalled(ctx context.Context, logf func(format string, v ...interface{})) bool {
	if tmuxBin() != "" {
		return true
	}
	install := func(name string, args ...string) bool {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(c, name, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "NONINTERACTIVE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			logf("Tmux: auto-install via %s failed (non-fatal): %v: %s", name, err, strings.TrimSpace(lastLine(string(out))))
			return false
		}
		clearDiscoveryCacheFor("tmux") // the 60s memo still says "missing"
		return tmuxBin() != ""
	}

	switch runtime.GOOS {
	case "darwin":
		brew := DiscoverBinary("brew")
		if brew == "" {
			logf("Tmux: not installed and Homebrew is absent — cannot auto-install. %s", TmuxInstallHint())
			return false
		}
		logf("Tmux: not installed — installing it now with brew (runner seats need it)")
		return install(brew, "install", "tmux")
	case "linux":
		type mgr struct {
			bin  string
			args []string
		}
		for _, m := range []mgr{
			{"apt-get", []string{"install", "-y", "tmux"}},
			{"dnf", []string{"install", "-y", "tmux"}},
			{"pacman", []string{"-S", "--noconfirm", "tmux"}},
			{"apk", []string{"add", "tmux"}},
			{"zypper", []string{"install", "-y", "tmux"}},
		} {
			bin := DiscoverBinary(m.bin)
			if bin == "" {
				continue
			}
			if os.Geteuid() == 0 {
				logf("Tmux: not installed — installing it now with %s (runner seats need it)", m.bin)
				return install(bin, m.args...)
			}
			// Only use sudo if it is already password-less; a prompt here would
			// hang the daemon forever.
			if sudo := DiscoverBinary("sudo"); sudo != "" {
				probe, cancel := context.WithTimeout(ctx, 5*time.Second)
				ok := exec.CommandContext(probe, sudo, "-n", "true").Run() == nil
				cancel()
				if ok {
					logf("Tmux: not installed — installing it now with sudo %s (runner seats need it)", m.bin)
					return install(sudo, append([]string{"-n", bin}, m.args...)...)
				}
			}
			logf("Tmux: not installed and installing it needs a password. Run: %s", TmuxInstallHint())
			return false
		}
		logf("Tmux: not installed and no known package manager found. %s", TmuxInstallHint())
		return false
	}
	logf("Tmux: not installed. %s", TmuxInstallHint())
	return false
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// TmuxInstallHint returns a platform-specific one-line install command
// to print when the user hits `yaver serve` without tmux installed.
// Mirrors the per-platform recipes in install_cmd.go (single source of
// truth would be ideal; this duplicate is small enough to be safe).
func TmuxInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install tmux"
	case "linux":
		// Detect package manager. apt-get is most common (Debian, Ubuntu);
		// dnf is Fedora / RHEL; pacman is Arch. Fall back to apt-get.
		for _, candidate := range []struct {
			bin, cmd string
		}{
			{"apt-get", "sudo apt-get install -y tmux"},
			{"dnf", "sudo dnf install -y tmux"},
			{"pacman", "sudo pacman -S --noconfirm tmux"},
			{"apk", "sudo apk add tmux"},
			{"zypper", "sudo zypper install -y tmux"},
		} {
			if _, err := exec.LookPath(candidate.bin); err == nil {
				return candidate.cmd
			}
		}
		return "sudo apt-get install -y tmux  # or your distro's equivalent"
	case "windows":
		return "tmux on Windows runs via WSL2 — `wsl --install` first, then `sudo apt install tmux` inside"
	}
	return "install tmux for your platform (https://github.com/tmux/tmux/wiki/Installing)"
}

// BootstrapDefaultSession creates a detached `yaver` tmux session if no
// sessions currently exist. Lets a fresh `yaver serve` produce a working
// /spatial layout immediately — the trio user shouldn't have to ssh in
// and type `tmux new -s yaver` before their first vibe attempt.
//
// No-ops if any session already exists. cwd is the user's home dir so
// the session starts in a sensible place.
func (m *TmuxManager) BootstrapDefaultSession() error {
	sessions, err := m.ListTmuxSessions()
	if err != nil {
		return fmt.Errorf("list sessions to check bootstrap: %w", err)
	}
	if len(sessions) > 0 {
		// User already has tmux sessions running; don't auto-create.
		return nil
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	// tmux new-session -d (detached) -s yaver -c $HOME
	cmd := exec.Command(tmuxCmdName(), "new-session", "-d", "-s", "yaver", "-c", home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListTmuxSessions returns all tmux sessions with metadata about their
// relationship to Yaver (adopted, forked-by-yaver, or unrelated).
func (m *TmuxManager) ListTmuxSessions() ([]TmuxSession, error) {
	out, err := exec.Command(tmuxCmdName(), "list-sessions", "-F",
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

// tmuxSubmitDelay is the pause between typing a line and pressing Enter.
//
// `send-keys <text> Enter` in one call delivers both before a TUI has finished
// ingesting the text, and coding-agent composers (verified against codex
// 0.142.5) swallow the Enter — the prompt sits in the box, unsent, and the
// caller is told "sent". Splitting the calls with a beat in between makes the
// submit land. Small enough that a voice turn still feels immediate.
var tmuxSubmitDelay = 250 * time.Millisecond

// sendTmuxKey types input literally with NO Enter. Used for menu answers,
// where the keypress itself is the confirmation.
func sendTmuxKey(sessionName, input string) error {
	key := strings.TrimSpace(input)
	if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", sessionName, "-l", "--", key).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys (choice): %w: %s", err, string(out))
	}
	return nil
}

// sendTmuxLine types input literally, waits, then presses Enter.
//
// `-l` matters: without it tmux parses the argument as key names, so a prompt
// containing words like "Enter", "Space" or "C-c" would be delivered as those
// keystrokes instead of as text. `--` guards inputs that begin with a dash.
func sendTmuxLine(sessionName, input string) error {
	if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", sessionName, "-l", "--", input).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys (text): %w: %s", err, string(out))
	}
	time.Sleep(tmuxSubmitDelay)
	if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", sessionName, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys (submit): %w: %s", err, string(out))
	}
	return nil
}

// tmuxChoiceAnswerPattern matches a bare option number ("2", " 3 ") — the only
// input allowed through to a pane that is showing a menu.
var tmuxChoiceAnswerPattern = regexp.MustCompile(`^\s*\d{1,2}\s*$`)

func isTmuxChoiceAnswer(input string) bool {
	return tmuxChoiceAnswerPattern.MatchString(input)
}

// tmuxMenuOptionPattern matches a rendered menu row: an optional selection
// caret, then "1." / "2)" etc. Covers claude ("❯ 1. Yes, I trust this folder")
// and codex ("› 1. Update now").
var tmuxMenuOptionPattern = regexp.MustCompile(`^\s*[›❯>*]?\s*(\d{1,2})[.)]\s+\S`)

// tmuxPaneAwaitingChoice reports whether the pane's visible tail is a menu —
// two or more numbered options — and returns them. Two is the threshold on
// purpose: a single "1." can appear in ordinary agent output (a numbered list
// in a reply), while a real menu always offers an alternative.
func tmuxPaneAwaitingChoice(sessionName string) (bool, []string) {
	out, err := exec.Command(tmuxCmdName(), "capture-pane", "-p", "-t", sessionName).Output()
	if err != nil {
		return false, nil // cannot see the pane; do not block the caller
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > tmuxChoiceScanLines {
		lines = lines[len(lines)-tmuxChoiceScanLines:]
	}
	var options []string
	for _, line := range lines {
		if tmuxMenuOptionPattern.MatchString(line) {
			options = append(options, strings.TrimSpace(line))
		}
	}
	if len(options) < 2 {
		return false, nil
	}
	return true, options
}

// tmuxChoiceScanLines bounds the menu scan to the visible prompt region, so a
// numbered list scrolled up in a transcript is not mistaken for a live menu.
const tmuxChoiceScanLines = 12

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

	// Refuse to type into a pane that is waiting on a menu choice. The Enter we
	// append would pick whatever option happens to be highlighted: a prompt sent
	// while codex showed "› 1. Update now" selected it, codex ran
	// `npm install -g @openai/codex`, exited, and took the tmux session with it.
	// A screenless surface (watch, car) cannot see that dialog, so the agent has
	// to refuse on its behalf. A bare number is how a caller answers the menu.
	if isTmuxChoiceAnswer(input) {
		// A menu digit selects AND confirms on its own. Appending Enter here is
		// actively dangerous: answering claude's "1. Yes, I trust this folder"
		// pops a second modal whose option 1 is "No, exit", and the trailing
		// Enter confirms it — claude quits and the session dies. Send the key,
		// nothing more, and let the caller read the pane again.
		if err := sendTmuxKey(sessionName, input); err != nil {
			return err
		}
	} else {
		if awaiting, options := tmuxPaneAwaitingChoice(sessionName); awaiting {
			return fmt.Errorf("session %q is waiting on a choice, not a prompt — send just the option number (it confirms immediately; re-read the pane afterwards, menus can chain). Options: %s",
				sessionName, strings.Join(options, " | "))
		}
		if err := sendTmuxLine(sessionName, input); err != nil {
			return err
		}
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
	err := exec.Command(tmuxCmdName(), "has-session", "-t", name).Run()
	return err == nil
}

// getPanePID returns the PID of the active pane's process in a tmux session.
func getPanePID(sessionName string) int {
	out, err := exec.Command(tmuxCmdName(), "list-panes", "-t", sessionName,
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
	return strings.TrimRight(capturePane(sessionName, lines), "\n ")
}

// capturePaneContent captures the last N lines from a tmux pane for diffing.
func capturePaneContent(sessionName string, lines int) string {
	return capturePane(sessionName, lines)
}

func capturePane(sessionName string, lines int) string {
	normal := capturePaneMode(sessionName, lines, false)
	alternate := capturePaneMode(sessionName, lines, true)
	if strings.TrimSpace(alternate) != "" && paneCaptureSignal(alternate) > paneCaptureSignal(normal) {
		return alternate
	}
	return normal
}

func capturePaneMode(sessionName string, lines int, alternate bool) string {
	args := []string{"capture-pane", "-t", sessionName, "-p", "-S", fmt.Sprintf("-%d", lines)}
	if alternate {
		args = []string{"capture-pane", "-a", "-t", sessionName, "-p", "-S", fmt.Sprintf("-%d", lines)}
	}
	out, err := exec.Command(tmuxCmdName(), args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return stripControlChars(string(out))
}

func paneCaptureSignal(s string) int {
	score := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			score++
		}
	}
	return score
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
