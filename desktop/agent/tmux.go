package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	ID           string `json:"id,omitempty"` // tmux session_id, e.g. "$1"
	Windows      int    `json:"windows"`
	Created      string `json:"created"`
	Attached     bool   `json:"attached"`
	Relationship string `json:"relationship"`          // "adopted", "forked-by-yaver", "unrelated"
	AgentType    string `json:"agentType,omitempty"`   // "claude", "codex", "opencode"
	MainPID      int    `json:"mainPid,omitempty"`     // PID of the main process in the active pane
	WindowIndex  string `json:"windowIndex,omitempty"` // active window index
	WindowName   string `json:"windowName,omitempty"`  // active window name
	PaneIndex    string `json:"paneIndex,omitempty"`   // active pane index
	PaneID       string `json:"paneId,omitempty"`      // tmux pane_id, e.g. "%17"
	PanePreview  string `json:"panePreview,omitempty"` // last ~20 lines of pane output
	TaskID       string `json:"taskId,omitempty"`      // set if adopted as a Yaver task
	SessionKind  string `json:"sessionKind,omitempty"` // task | autorun | runner | other
	Origin       string `json:"origin,omitempty"`      // yaver-task | yaver-autorun | yaver-runner | manual
	StartedAt    string `json:"startedAt,omitempty"`   // start hint encoded in Yaver-owned names
	RunnerHint   string `json:"runnerHint,omitempty"`
	ProjectHint  string `json:"projectHint,omitempty"`
	TaskIDHint   string `json:"taskIdHint,omitempty"`
	InputMode    string `json:"inputMode,omitempty"` // interactive | task-followup

	// Panes is every pane in the session, each with its own agent and vibing
	// status. The flat fields above describe the ACTIVE pane only and are kept
	// for clients that predate this field — on a split window they describe one
	// arbitrary agent out of several, which is why new callers should read
	// Panes instead.
	Panes []VibePane `json:"panes,omitempty"`
}

type tmuxPaneIdentity struct {
	SessionID   string
	WindowIndex string
	WindowName  string
	PaneIndex   string
	PaneID      string
	PanePID     int
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

// tmuxWSLShimPath is where the agent keeps its WSL tmux shim on native
// Windows. Written to ~/.yaver/bin/ so it is on the same managed-bin shelf the
// agent already owns; the shim is a .cmd that forwards to the WSL distro's
// real tmux, which lets every existing exec.Command(tmuxCmdName(), args…)
// call site work UNCHANGED on Windows (Go routes .cmd through cmd.exe /c).
func tmuxWSLShimPath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bin", "tmux.cmd")
}

// tmuxWSLShimContent returns the .cmd body that bridges tmux to the WSL2
// default distro. Pure (no filesystem, no OS gates) so the shim contract is
// unit-testable on every platform.
func tmuxWSLShimContent() string {
	return "@echo off\r\n" +
		"rem Generated by the Yaver agent — bridges tmux to the WSL2 default distro.\r\n" +
		"rem Set YAVER_WSL_DISTRO below to a specific distro name to override.\r\n" +
		"set DISTRO=%YAVER_WSL_DISTRO%\r\n" +
		"if not defined DISTRO (\r\n" +
		"  for /f \"delims=\" %%d in ('wsl.exe --list --quiet') do if not defined DISTRO set DISTRO=%%d\r\n" +
		")\r\n" +
		"wsl.exe -d %DISTRO% -- tmux %*\r\n"
}

// ensureWSLTmuxShim writes the tmux.cmd shim that bridges to the WSL distro's
// tmux, returning the shim path when WSL tmux is reachable, "" otherwise.
//
// WHY THIS EXISTS — the tmux-on-Windows gap (audit 2026-08-12): the install
// hint said "tmux on Windows runs via WSL2 — `wsl --install` first", but the
// EXEC path could not reach it: discoverBinary("tmux") only looks for a native
// tmux.exe on PATH + install prefixes, which essentially never exists on
// Windows, so tmuxAvailable() was false and every runner seat (autorun, the
// keeper, the TUI) silently refused to drive through tmux. The hint named WSL2
// as the path; the code could not walk it. The shim closes that gap at ONE
// point: on Windows, tmuxBin() returns this shim, so all ~50 exec sites work
// and the "inventory" (tmuxAvailable) now matches the "operation" (exec).
//
// The shim uses the DEFAULT WSL distro (wsl.exe --list --quiet's first entry).
// A user with tmux in a non-default distro can point the shim there by editing
// the single YAVER_WSL_DISTRO env line — no recompilation, no second code path.
func ensureWSLTmuxShim() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return ""
	}
	shim := tmuxWSLShimPath()
	if shim == "" {
		return ""
	}
	// Verify tmux actually exists INSIDE the distro before advertising the
	// shim — a shim that points at a distro without tmux is the exact
	// "inventory says yes, operation says no" lie this bridge exists to kill.
	probe := exec.Command("wsl.exe", "bash", "-lc", "command -v tmux >/dev/null 2>&1")
	if err := probe.Run(); err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(shim, []byte(tmuxWSLShimContent()), 0o755); err != nil {
		return ""
	}
	return shim
}

// tmuxCmdName returns the tmux argv[0] for exec. It falls back to the bare name
// so a caller still produces the familiar "executable file not found" error
// rather than trying to exec "".
//
// On native Windows the returned path is the WSL shim (tmux.cmd) when WSL
// tmux is reachable — see ensureWSLTmuxShim. This is the single point where
// the Windows tmux story is decided, so every caller (runner_pty.go,
// autorun_tmux.go, tmux_panes.go, …) inherits the bridge without edits.
func tmuxCmdName() string {
	if p := tmuxBin(); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		if shim := ensureWSLTmuxShim(); shim != "" {
			return shim
		}
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
		if shim := ensureWSLTmuxShim(); shim != "" {
			return "tmux bridged via WSL2 (shim at " + shim + "). To use a non-default distro, set YAVER_WSL_DISTRO=<name> before starting the agent."
		}
		return "tmux on Windows runs via WSL2 — `wsl --install` first, then `sudo apt install tmux` inside. The agent auto-bridges to WSL tmux once it is installed."
	}
	return "install tmux for your platform (https://github.com/tmux/tmux/wiki/Installing)"
}

// ListTmuxSessions returns all tmux sessions with metadata about their
// relationship to Yaver (adopted, forked-by-yaver, or unrelated).
func (m *TmuxManager) ListTmuxSessions() ([]TmuxSession, error) {
	out, err := exec.Command(tmuxCmdName(), "list-sessions", "-F",
		"#{session_name}|#{session_id}|#{session_windows}|#{session_created}|#{session_attached}|#{@yaver-task-id}|#{@yaver-runner}|#{@yaver-input-mode}|#{@yaver-origin}").CombinedOutput()
	if err != nil {
		// tmux returns error if no server is running (no sessions)
		if strings.Contains(string(out), "no server running") || strings.Contains(string(out), "no sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, string(out))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sessions := make([]TmuxSession, 0, len(lines))

	// One enumeration for the whole machine, then grouped per session — a fork
	// per session per pane would put an unbounded number of `ps` calls in the
	// task list's critical path. Failure here degrades to the legacy
	// active-pane-only view rather than failing the listing.
	panesBySession := map[string][]VibePane{}
	if all, perr := ListVibePanes(context.Background()); perr == nil {
		for _, p := range all {
			panesBySession[p.SessionName] = append(panesBySession[p.SessionName], p)
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, line := range lines {
		if line == "" {
			continue
		}
		s := parseTmuxSessionLine(line)

		// Determine relationship.
		//
		// Adoption is keyed by PANE, so a session counts as adopted when ANY of
		// its panes is — the session-name lookup alone reported "unrelated" for
		// every pane-adopted session. The flat TaskID keeps naming one task for
		// old clients; Panes below says which pane each task actually owns.
		if taskID, ok := m.adopted[s.Name]; ok {
			s.Relationship = "adopted"
			s.TaskID = taskID
		} else if taskID := m.anyAdoptedPaneTask(panesBySession[s.Name]); taskID != "" {
			s.Relationship = "adopted"
			s.TaskID = taskID
		} else if taskID := tmuxSessionTaskID(m.taskMgr, s.Name); taskID != "" {
			s.Relationship = "forked-by-yaver"
			s.TaskID = taskID
		} else if s.TaskID != "" {
			s.Relationship = "forked-by-yaver"
		} else if m.isForkedByYaver(s.Name) {
			s.Relationship = "forked-by-yaver"
		} else {
			s.Relationship = "unrelated"
		}

		// Get pane PID and detect agent type
		pane := getActivePaneIdentity(s.Name)
		applyTmuxPaneIdentity(&s, pane)
		s.MainPID = pane.PanePID
		if s.MainPID > 0 {
			if observed := detectAgentType(s.MainPID); observed != "" {
				s.AgentType = observed
			}
		}

		// Get pane preview (last 20 lines)
		s.PanePreview = capturePanePreview(s.Name, 20)

		s.Panes = panesBySession[s.Name]
		// Carry the adopted task id down onto the pane it actually belongs to,
		// so a client can tell which of three agents in a window is the task it
		// is looking at.
		for i := range s.Panes {
			s.Panes[i].TaskID = paneTaskID(m.taskMgr, s.Panes[i].PaneID)
		}

		sessions = append(sessions, s)
	}
	return sessions, nil
}

// AdoptSession creates a Yaver task for an existing tmux session and starts
// polling its output. The tmux session continues running as-is.
func (m *TmuxManager) AdoptSession(sessionName string) (*Task, error) {
	return m.AdoptTarget(sessionName, "")
}

// ReconcileUntrackedRunnerPanes makes the Tasks ledger the complete inventory
// of live AI runner seats. It never starts a runner and never types into a
// pane; it only adopts a process that ListVibePanes positively observed.
// Running this outside request handlers keeps process-tree inspection out of
// GET /tasks' critical path.
func (m *TmuxManager) ReconcileUntrackedRunnerPanes(ctx context.Context) int {
	panes, err := ListVibePanes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[tmux] runner-seat reconciliation failed: %v", err)
		}
		return 0
	}
	adopted := 0
	for _, pane := range untrackedRunnerPanes(m.taskMgr, panes) {
		if ctx.Err() != nil {
			break
		}
		if _, err := m.AdoptTarget(pane.SessionName, pane.PaneID); err != nil {
			log.Printf("[tmux] reconcile pane %s in %q failed: %v", pane.PaneID, pane.SessionName, err)
			continue
		}
		adopted++
	}
	if adopted > 0 {
		log.Printf("[tmux] reconciled %d previously untracked runner seat(s) into Tasks", adopted)
	}
	return adopted
}

func untrackedRunnerPanes(taskMgr *TaskManager, panes []VibePane) []VibePane {
	result := make([]VibePane, 0)
	for _, pane := range panes {
		if !pane.AgentConfirmed || normalizeRunnerID(pane.Agent) == "" || normalizeRunnerID(pane.Agent) == "unknown" {
			continue
		}
		if paneTaskID(taskMgr, pane.PaneID) != "" {
			continue
		}
		result = append(result, pane)
	}
	return result
}

// AdoptTarget adopts ONE PANE as a Yaver task. With an empty paneID it adopts
// the session's active pane, which is what AdoptSession has always done.
//
// The pane is the unit because a task must map to one agent. A session split
// into a claude pane and a codex pane is two tasks, and adopting "the session"
// would silently pick whichever pane happened to be active — then poll its
// output and type follow-ups into it, both under a task title naming the other
// agent.
//
// Adoption is keyed on the PANE id for the same reason: keyed on the session,
// the second pane's adoption would collide with the first and be refused as
// "already adopted".
func (m *TmuxManager) AdoptTarget(sessionName, paneID string) (*Task, error) {
	// Verify the tmux session exists
	if !tmuxSessionExists(sessionName) {
		return nil, fmt.Errorf("tmux session %q not found", sessionName)
	}

	// Resolve the pane BEFORE registering anything: an adoption keyed on a pane
	// that does not exist is a task that can never be driven.
	var pane tmuxPaneIdentity
	if strings.TrimSpace(paneID) == "" {
		pane = getActivePaneIdentity(sessionName)
	} else {
		var ok bool
		pane, ok = paneIdentityByID(sessionName, paneID)
		if !ok {
			return nil, fmt.Errorf("pane %s is not part of tmux session %q (it may have closed since the list was fetched)", paneID, sessionName)
		}
	}

	key := adoptionKey(sessionName, pane.PaneID)

	m.mu.Lock()
	if existing, already := m.adopted[key]; already {
		m.mu.Unlock()
		// Adoption is an idempotent open operation. A mobile retry can arrive
		// after the first POST succeeded but its response was lost; returning a
		// duplicate error leaves the user with an "Adopt Failed" alert even
		// though the pane is already healthy and managed.
		m.taskMgr.mu.RLock()
		existingTask := m.taskMgr.tasks[existing]
		m.taskMgr.mu.RUnlock()
		if existingTask != nil {
			return existingTask, nil
		}

		// A registration without its persisted task cannot be opened or driven.
		// Heal that stale in-memory entry and let this call create the task the
		// map claimed existed.
		m.mu.Lock()
		if m.adopted[key] == existing {
			delete(m.adopted, key)
			if cancel := m.pollStop[key]; cancel != nil {
				cancel()
				delete(m.pollStop, key)
			}
		}
	}
	m.mu.Unlock()

	pid := pane.PanePID
	agentType := ""
	if pid > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), vibeDefaultDeadline)
		agentType, _ = detectPaneAgent(ctx, pid)
		cancel()
	}

	runnerID := agentType
	if runnerID == "" {
		runnerID = "unknown"
	}

	title := fmt.Sprintf("tmux: %s", sessionName)
	if agentType != "" {
		// Name the agent, not the session: with three panes in one session the
		// session name is the one thing that does NOT distinguish the tasks.
		title = fmt.Sprintf("%s · %s", agentType, sessionName)
	}

	// Create a task in the task manager
	id := uuid.New().String()[:8]
	now := time.Now()
	task := &Task{
		ID:                 id,
		Title:              title,
		Description:        fmt.Sprintf("Adopted tmux session %q pane %s", sessionName, pane.PaneID),
		Status:             TaskStatusRunning,
		Source:             "tmux-adopted",
		RunnerID:           runnerID,
		YaverSessionID:     newYaverSessionID(),
		RemoteBoxID:        strings.TrimSpace(m.taskMgr.DeviceID),
		RunnerName:         firstNonEmpty(GetRunnerConfig(runnerID).Name, runnerID),
		SessionStartedFrom: "tmux-adopt",
		StartedFromSurface: "unknown",
		InitialSurface:     "unknown",
		SessionStartedAt:   now,
		LastSurface:        "unknown",
		LastActiveAt:       now,
		TmuxSession:        sessionName,
		TmuxSessionID:      pane.SessionID,
		TmuxWindowIndex:    pane.WindowIndex,
		TmuxWindowName:     pane.WindowName,
		TmuxPaneIndex:      pane.PaneIndex,
		TmuxPaneID:         pane.PaneID,
		IsAdopted:          true,
		CreatedAt:          now,
		StartedAt:          &now,
		outputCh:           make(chan string, 512),
		eventCh:            make(chan map[string]interface{}, 32),
		doneCh:             make(chan struct{}),
	}

	m.taskMgr.mu.Lock()
	m.taskMgr.tasks[id] = task
	m.taskMgr.presentLocked(task, taskPresentationInput{
		ID: task.ID + "-activity", Kind: "status",
		Text:  "Following a live " + firstNonEmpty(task.RunnerName, "runner") + " session. Terminal output stays under Details until the runner sends a semantic reply through Yaver.",
		Phase: "adopted", State: "running",
	})
	m.taskMgr.persist()
	m.taskMgr.mu.Unlock()

	// Register adoption and start polling
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.adopted[key] = id
	m.pollStop[key] = cancel
	m.mu.Unlock()

	// Poll the PANE, not the session: capture-pane -t <session> reads whichever
	// pane is active, so a session-targeted poll on a split window streams a
	// neighbouring agent's screen into this task's output.
	go m.pollTmuxOutput(ctx, id, key, adoptionPollTarget(key, sessionName))

	log.Printf("[tmux] Adopted %s (session %q) as task %s (agent=%s, pid=%d)", key, sessionName, id, runnerID, pid)
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

// CloseAdoptedTask stops the runner in an adopted tmux session, then closes the
// WHOLE tmux session and detaches every adopted Yaver task bound to it.
//
// Why whole-session close is correct here: an adopted task is "open this tmux
// session from the phone". When the user removes that session from mobile, the
// session must stop existing on the remote box too; leaving a sibling pane or
// shell behind makes it immediately reappear as attachable inventory. Task-
// owned Yaver sessions already have a distinct lifecycle path
// (TaskManager.closeTaskOwnedTmuxSeat). This function is only for user-adopted
// tmux sessions.
func (m *TmuxManager) CloseAdoptedTask(taskID string) error {
	m.mu.RLock()
	var key string
	for k, tid := range m.adopted {
		if tid == taskID {
			key = k
			break
		}
	}
	m.mu.RUnlock()
	if key == "" {
		return fmt.Errorf("task %s is not an adopted tmux session", taskID)
	}

	m.taskMgr.mu.RLock()
	task, ok := m.taskMgr.tasks[taskID]
	if !ok || task == nil {
		m.taskMgr.mu.RUnlock()
		return fmt.Errorf("task %s not found", taskID)
	}
	sessionName := task.TmuxSession
	paneID := strings.TrimSpace(task.TmuxPaneID)
	runnerID := normalizeRunnerID(task.RunnerID)
	m.taskMgr.mu.RUnlock()

	target := sessionName
	if paneID != "" {
		target = paneID
	}
	if target == "" {
		return fmt.Errorf("task %s has no tmux target", taskID)
	}

	if tmuxTargetExists(target) {
		if exitCmd := tmuxRunnerExitCommand(target, runnerID); exitCmd != "" {
			if err := sendTmuxLine(target, exitCmd); err != nil {
				log.Printf("[tmux] graceful runner exit for task %s target %s failed: %v", taskID, target, err)
			} else {
				waitForTmuxRunnerExit(target, 4*time.Second)
			}
		}
	}
	if strings.TrimSpace(sessionName) != "" {
		if out, err := exec.Command(tmuxCmdName(), "kill-session", "-t", sessionName).CombinedOutput(); err != nil && tmuxSessionExists(sessionName) {
			return fmt.Errorf("close tmux session %s: %w: %s", sessionName, err, strings.TrimSpace(string(out)))
		}
	}
	closed := m.detachAdoptedSessionTasks(sessionName)
	if len(closed) == 0 {
		return fmt.Errorf("task %s is not an adopted tmux session", taskID)
	}
	log.Printf("[tmux] Closed adopted tmux session %q via task %s (%d task(s) detached)", sessionName, taskID, len(closed))
	return nil
}

func (m *TmuxManager) detachAdoptedSessionTasks(sessionName string) []string {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return nil
	}

	m.mu.Lock()
	keys := make([]string, 0)
	taskIDs := make([]string, 0)
	for k, tid := range m.adopted {
		m.taskMgr.mu.RLock()
		task := m.taskMgr.tasks[tid]
		m.taskMgr.mu.RUnlock()
		if task == nil || strings.TrimSpace(task.TmuxSession) != sessionName {
			continue
		}
		keys = append(keys, k)
		taskIDs = append(taskIDs, tid)
	}
	for _, k := range keys {
		if cancel := m.pollStop[k]; cancel != nil {
			cancel()
			delete(m.pollStop, k)
		}
		delete(m.adopted, k)
	}
	m.mu.Unlock()

	if len(taskIDs) == 0 {
		return nil
	}

	now := time.Now()
	m.taskMgr.mu.Lock()
	for _, taskID := range taskIDs {
		task := m.taskMgr.tasks[taskID]
		if task == nil {
			continue
		}
		task.Status = TaskStatusStopped
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

	return taskIDs
}

func tmuxRunnerExitCommand(target, runnerID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), vibeDefaultDeadline)
	defer cancel()

	agent, confirmed := tmuxTargetAgent(ctx, target)
	if !confirmed {
		return ""
	}
	if runnerID == "" || runnerID == "unknown" {
		runnerID = agent
	}
	switch normalizeRunnerID(runnerID) {
	case "claude":
		return "/exit"
	case "codex":
		return "/exit"
	case "opencode":
		return "/quit"
	default:
		return ""
	}
}

func waitForTmuxRunnerExit(target string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !tmuxTargetExists(target) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, confirmed := tmuxTargetAgent(ctx, target)
		cancel()
		if !confirmed {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func tmuxTargetAgent(ctx context.Context, target string) (string, bool) {
	out, err := exec.CommandContext(ctx, tmuxCmdName(), "list-panes", "-t", target, "-F", "#{pane_active}\t#{pane_pid}\t#{pane_dead}").Output()
	if err != nil {
		return "", false
	}
	var pid int
	var dead bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		p, _ := strconv.Atoi(f[1])
		if pid == 0 || f[0] == "1" {
			pid, dead = p, f[2] == "1"
		}
	}
	if dead {
		return "", false
	}
	return detectPaneAgent(ctx, pid)
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
//
// Queued per target like sendTmuxLine: a menu answer is a single character
// whose meaning depends entirely on WHICH prompt is on screen, so letting it
// slip between another sender's text and Enter would answer the wrong question.
func sendTmuxKey(target, input string) error {
	key := strings.TrimSpace(input)
	return submitTmuxInput(target, func() error {
		if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", target, "-l", "--", key).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux send-keys (choice): %w: %s", err, string(out))
		}
		return nil
	})
}

// sendTmuxLine types input literally, waits, then presses Enter.
//
// `-l` matters: without it tmux parses the argument as key names, so a prompt
// containing words like "Enter", "Space" or "C-c" would be delivered as those
// keystrokes instead of as text. `--` guards inputs that begin with a dash.
//
// The whole text→beat→Enter sequence runs as ONE queued unit per target
// (tmux_input_queue.go). It is three separate tmux calls with a 250ms gap in
// the middle, and without serialization a second sender's text lands inside
// that gap — fusing two people's words into one prompt and submitting it,
// while telling both senders it was sent. Never call the exec steps directly.
func sendTmuxLine(target, input string) error {
	return submitTmuxInput(target, func() error {
		if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", target, "-l", "--", input).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux send-keys (text): %w: %s", err, string(out))
		}
		time.Sleep(tmuxSubmitDelay)
		if out, err := exec.Command(tmuxCmdName(), "send-keys", "-t", target, "Enter").CombinedOutput(); err != nil {
			return fmt.Errorf("tmux send-keys (submit): %w: %s", err, string(out))
		}
		return nil
	})
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
func tmuxPaneAwaitingChoice(target string) (bool, []string) {
	out, err := exec.Command(tmuxCmdName(), "capture-pane", "-p", "-t", target).Output()
	if err != nil {
		return false, nil // cannot see the pane; do not block the caller
	}
	lines := trimTrailingBlankLines(strings.Split(string(out), "\n"))
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
//
// It targets the task's PANE when one is recorded, falling back to the session
// name only for tasks adopted before pane targeting existed. That distinction
// is a safety property, not a nicety: `send-keys -t <session>` resolves to
// whichever pane is ACTIVE, so on a split window a follow-up meant for the
// codex task lands in the claude one while the caller is told "sent". The menu
// guard below inherits the same target for the same reason — guarding the
// active pane while typing into another is worse than not guarding at all.
func (m *TmuxManager) SendTmuxInput(taskID, input string) error {
	return m.SendTmuxInputWithIntent(taskID, input, false)
}

// SendTmuxInputWithIntent is SendTmuxInput with the caller's intent made
// explicit.
//
// allowShell=false (the default, and what every prompt-shaped caller wants)
// refuses a pane with no agent in it, because the text would be EXECUTED
// rather than read. allowShell=true is the deliberate "run this command in my
// adopted shell session" path — a real, shipped capability that predates agent
// panes and must keep working; it simply has to be asked for, so that dictated
// text can never fall into it by default.
func (m *TmuxManager) SendTmuxInputWithIntent(taskID, input string, allowShell bool) error {
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

	target := m.taskTmuxTarget(taskID, sessionName)
	if !tmuxTargetExists(target) {
		return fmt.Errorf("tmux target %q no longer exists", target)
	}

	if isTmuxChoiceAnswer(input) {
		// A menu digit selects AND confirms on its own. Appending Enter here is
		// actively dangerous: answering claude's "1. Yes, I trust this folder"
		// pops a second modal whose option 1 is "No, exit", and the trailing
		// Enter confirms it — claude quits and the session dies. Send the key,
		// nothing more, and let the caller read the pane again.
		if err := sendTmuxKey(target, input); err != nil {
			return err
		}
	} else {
		// A pane whose agent has exited is a plain SHELL, and text typed into a
		// shell is a COMMAND, submitted by the Enter we append below. Verified
		// on a live box: a turn aimed at a runner-less session ran the prompt
		// and came back `zsh: command not found`. Sessions routinely outlive
		// their runner, so this is the normal end state, not a corner case —
		// same lesson as RunnerPTYSession.Confirmed (runner_pty.go:367).
		// Digits are exempt above: answering a menu is safe by construction.
		if ok, reason := tmuxTargetAcceptsPrompt(target); !ok && !allowShell {
			return fmt.Errorf("refusing to type into %s: %s. If you meant to run it as a shell command, resend with allowShell", target, reason)
		}

		// Refuse to type into a pane that is waiting on a menu choice. The Enter
		// we append would pick whatever option happens to be highlighted: a
		// prompt sent while codex showed "› 1. Update now" selected it, codex ran
		// `npm install -g @openai/codex`, exited, and took the tmux session with
		// it. A screenless surface (watch, car) cannot see that dialog, so the
		// agent has to refuse on its behalf. A bare number answers the menu.
		if awaiting, options := tmuxPaneAwaitingChoice(target); awaiting {
			return fmt.Errorf("%s is waiting on a choice, not a prompt — send just the option number (it confirms immediately; re-read the pane afterwards, menus can chain). Options: %s",
				target, strings.Join(options, " | "))
		}
		if err := sendTmuxLine(target, input); err != nil {
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
		task.Status = TaskStatusRunning
		task.FinishedAt = nil
		m.taskMgr.persist()
	}
	m.taskMgr.mu.Unlock()

	log.Printf("[tmux] Sent input to session (task %s): %s", taskID, truncate(input, 80))
	return nil
}

// pollTmuxOutput continuously captures the tmux pane and emits new content
// through the task's output channel. Runs until context is cancelled or the
// tmux session disappears.
// The target is a PANE id whenever the task was adopted at pane granularity;
// only pre-pane tasks fall back to a session name. Liveness is therefore
// checked on the target, not on the session: a pane can close while its session
// keeps running, and a session-scoped check would leave that task "running"
// forever against a pane nobody can reach.
func (m *TmuxManager) pollTmuxOutput(ctx context.Context, taskID, key, target string) {
	var prevCapture string
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check whether the pane (or legacy session) still exists
			if !tmuxTargetExists(target) {
				log.Printf("[tmux] Target %q disappeared — reconciling task %s", target, taskID)
				m.taskMgr.mu.Lock()
				if task, ok := m.taskMgr.tasks[taskID]; ok {
					now := time.Now()
					if taskOwnsRecoverableTmuxSeat(task) {
						task.Status = TaskStatusFailed
						task.Failure = &TaskFailureDiagnosis{
							Kind:       "runner_session",
							Code:       ReasonTaskRunnerSeatLost,
							Title:      "Runner session disappeared",
							Reason:     "The task-owned tmux pane disappeared without an explicit Complete, Stop, or Delete action.",
							Remedy:     "Review any partial project edits, then retry the task to open a new runner seat.",
							Probe:      "task_owned_tmux_poll",
							DetectedAt: now,
						}
					} else {
						// Losing an externally-owned/adopted pane is not proof that
						// its work completed. Only the user's Complete gesture may
						// produce Finished; an observed disappearance is Stopped and
						// remains in task history while the durable roster records the
						// closed seat.
						task.Status = TaskStatusStopped
					}
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
				delete(m.adopted, key)
				delete(m.pollStop, key)
				m.mu.Unlock()
				return
			}

			// Capture current pane content (last 200 lines for reasonable diff window)
			capture := capturePaneContent(target, 200)
			if capture == "" || capture == prevCapture {
				continue
			}

			// Find new content by diffing
			newContent := diffCapture(prevCapture, capture)
			prevCapture = capture

			if newContent == "" {
				continue
			}

			// Preserve the adopted terminal bytes in the same P2P raw/typed lane
			// as Yaver-owned runner processes. An arbitrary TUI cannot be turned
			// into a truthful semantic chat stream, but stdout must not disappear
			// merely because Yaver attached after the process started.
			m.taskMgr.mu.RLock()
			task, ok := m.taskMgr.tasks[taskID]
			m.taskMgr.mu.RUnlock()
			if !ok || task == nil {
				continue
			}
			m.taskMgr.emitRunnerProcessChunk(task, "pty", []byte(newContent))

			// Emit new lines through the groomed task output channel.
			m.taskMgr.mu.Lock()
			if current, stillPresent := m.taskMgr.tasks[taskID]; stillPresent && current == task {
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
			if reason := runtimeRenderReasonFromTaskOutput(newContent); reason != "" {
				emitRuntimeRenderRequested(task, reason, newContent)
			}
			m.taskMgr.present(task, taskPresentationInput{
				ID: task.ID + "-activity", Kind: "status",
				Text:  "The adopted runner produced new terminal output. Open Details to inspect it.",
				Phase: "adopted", State: "running",
			})
		}
	}
}

// ReAdoptOnStartup reconciles persisted tmux tasks against their real pane and
// restarts polling for live runners. This includes Yaver-owned task seats: the
// tmux server and runner can survive an agent restart, so declaring them failed
// from the persisted status alone is a false negative.
func (m *TmuxManager) ReAdoptOnStartup() {
	m.taskMgr.mu.Lock()
	defer m.taskMgr.mu.Unlock()

	for _, task := range m.taskMgr.tasks {
		owned := taskOwnsRecoverableTmuxSeat(task)
		if (!task.IsAdopted && !owned) || task.TmuxSession == "" {
			continue
		}
		if task.Status != TaskStatusRunning && task.Status != TaskStatusQueued {
			continue
		}

		if tmuxSessionExists(task.TmuxSession) {
			// Re-resolve the task's OWN pane. Re-reading the active pane here
			// would silently re-point the task at whatever the user happened to
			// be looking at when the agent restarted — on a split window that
			// is a different agent, and the task would then poll and type into
			// it under the old title.
			pane, ok := paneIdentityByID(task.TmuxSession, task.TmuxPaneID)
			if !ok && owned {
				// A task-owned session has exactly one pane. The agent may have
				// restarted in the short interval before its pane id was persisted,
				// so resolving that sole pane is safe; adopted multi-pane sessions
				// must never take this fallback.
				pane = getActivePaneIdentity(task.TmuxSession)
				ok = pane.PaneID != ""
			}
			if !ok {
				// The recorded pane is gone. Its session lives, but this task's
				// seat does not, so do not adopt a neighbour in its place.
				task.Status = TaskStatusStopped
				now := time.Now()
				task.FinishedAt = &now
				log.Printf("[tmux] Pane %s of session %q is gone — marking task %s as stopped",
					task.TmuxPaneID, task.TmuxSession, task.ID)
				continue
			}
			task.TmuxSessionID = pane.SessionID
			task.TmuxWindowIndex = pane.WindowIndex
			task.TmuxWindowName = pane.WindowName
			task.TmuxPaneIndex = pane.PaneIndex
			task.TmuxPaneID = pane.PaneID

			if owned {
				probeCtx, cancelProbe := context.WithTimeout(context.Background(), vibeDefaultDeadline)
				_, runnerAlive := detectPaneAgent(probeCtx, pane.PanePID)
				cancelProbe()
				if !runnerAlive {
					// The turn ended while the agent was away, but the runner-native
					// session id, scrollback and task identity remain. This is the
					// user's turn, not an invented review decision.
					task.Status = TaskStatusReady
					now := time.Now()
					task.FinishedAt = &now
					log.Printf("[tmux] Recovered idle task-owned seat %s in %q as ready", task.TmuxPaneID, task.TmuxSession)
					continue
				}
				task.Status = TaskStatusRunning
			}
			// Re-create channels and restart polling
			task.outputCh = make(chan string, 512)
			task.eventCh = make(chan map[string]interface{}, 32)
			task.doneCh = make(chan struct{})
			m.taskMgr.presentLocked(task, taskPresentationInput{
				ID: task.ID + "-activity", Kind: "status",
				Text:  "Following the restored runner session. Terminal output stays under Details until a semantic reply is available.",
				Phase: "adopted", State: string(task.Status),
			})

			key := adoptionKey(task.TmuxSession, task.TmuxPaneID)
			m.mu.Lock()
			if task.IsAdopted {
				m.adopted[key] = task.ID
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.pollStop[key] = cancel
			m.mu.Unlock()

			go m.pollTmuxOutput(ctx, task.ID, key, adoptionPollTarget(key, task.TmuxSession))
			log.Printf("[tmux] Recovered live pane %s of session %q for task %s on startup", task.TmuxPaneID, task.TmuxSession, task.ID)
		} else {
			// An absent adopted seat was user-owned and is now stopped. An absent
			// task-owned seat is an interruption, never an implicit completion.
			if owned {
				task.Status = TaskStatusFailed
			} else {
				task.Status = TaskStatusStopped
			}
			now := time.Now()
			task.FinishedAt = &now
			if owned {
				task.Failure = &TaskFailureDiagnosis{
					Kind:       "runner_session",
					Code:       ReasonTaskRunnerSeatLost,
					Title:      "Runner session disappeared",
					Reason:     "The Yaver agent restarted and the task-owned tmux seat was no longer present.",
					Remedy:     "Review any partial project edits, then retry the task to open a new runner seat.",
					Probe:      "task_owned_tmux_on_startup",
					DetectedAt: now,
				}
			}
			log.Printf("[tmux] Session %q no longer exists — reconciled task %s as %s", task.TmuxSession, task.ID, task.Status)
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
// Format: "name|id|windows|created|attached|task-option|runner-option|input-option|origin-option"
func parseTmuxSessionLine(line string) TmuxSession {
	parts := strings.SplitN(line, "|", 9)
	hints := parseYaverTmuxSessionName(parts[0])
	s := TmuxSession{
		Name: parts[0], SessionKind: hints.Kind, Origin: hints.Origin, RunnerHint: hints.Runner,
		ProjectHint: hints.ProjectHint, TaskIDHint: hints.TaskIDHint, InputMode: hints.InputMode,
	}
	if !hints.StartedAt.IsZero() {
		s.StartedAt = hints.StartedAt.Format(time.RFC3339)
	}
	if len(parts) > 1 {
		s.ID = parts[1]
	}
	if len(parts) > 2 {
		s.Windows, _ = strconv.Atoi(parts[2])
	}
	if len(parts) > 3 {
		// Convert epoch to human-readable
		epoch, err := strconv.ParseInt(parts[3], 10, 64)
		if err == nil {
			s.Created = time.Unix(epoch, 0).Format("2006-01-02 15:04")
		} else {
			s.Created = parts[3]
		}
	}
	if len(parts) > 4 {
		s.Attached = parts[4] == "1"
	}
	if len(parts) > 5 {
		s.TaskID = strings.TrimSpace(parts[5])
	}
	if len(parts) > 6 {
		if runner := normalizeRunnerID(parts[6]); runner != "" {
			s.RunnerHint = runner
		}
	}
	if len(parts) > 7 && strings.TrimSpace(parts[7]) != "" {
		s.InputMode = strings.TrimSpace(parts[7])
	}
	if len(parts) > 8 {
		if origin := normalizeTmuxOrigin(parts[8]); origin != "" {
			s.Origin = origin
		}
	}
	s.AgentType = s.RunnerHint
	return s
}

// tmuxSessionExists checks if a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	err := exec.Command(tmuxCmdName(), "has-session", "-t", name).Run()
	return err == nil
}

// getPanePID returns the PID of the active pane's process in a tmux session.
func getPanePID(sessionName string) int {
	return getActivePaneIdentity(sessionName).PanePID
}

func applyTmuxPaneIdentity(s *TmuxSession, pane tmuxPaneIdentity) {
	if pane.SessionID != "" {
		s.ID = pane.SessionID
	}
	s.WindowIndex = pane.WindowIndex
	s.WindowName = pane.WindowName
	s.PaneIndex = pane.PaneIndex
	s.PaneID = pane.PaneID
}

func getActivePaneIdentity(sessionName string) tmuxPaneIdentity {
	out, err := exec.Command(tmuxCmdName(), "list-panes", "-t", sessionName,
		"-F", "#{pane_active}|#{session_id}|#{window_index}|#{window_name}|#{pane_index}|#{pane_id}|#{pane_pid}").CombinedOutput()
	if err != nil {
		return tmuxPaneIdentity{}
	}
	var fallback tmuxPaneIdentity
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 7)
		if len(parts) < 7 {
			continue
		}
		pid, _ := strconv.Atoi(parts[6])
		pane := tmuxPaneIdentity{
			SessionID:   parts[1],
			WindowIndex: parts[2],
			WindowName:  parts[3],
			PaneIndex:   parts[4],
			PaneID:      parts[5],
			PanePID:     pid,
		}
		if fallback == (tmuxPaneIdentity{}) {
			fallback = pane
		}
		if parts[0] == "1" {
			return pane
		}
	}
	return fallback
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
	fields := strings.Fields(cmd)
	if len(fields) > 0 {
		bin := fields[0]
		if slash := strings.LastIndex(bin, "/"); slash >= 0 {
			bin = bin[slash+1:]
		}
		for binary, agentType := range knownAgentBinaries {
			if bin == binary || strings.HasPrefix(bin, binary+"-") {
				return agentType
			}
		}
	}
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
