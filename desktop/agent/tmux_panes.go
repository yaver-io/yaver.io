package main

// tmux_panes.go — the PANE is the unit of a vibing agent, not the session.
//
// Why (2026-07-22 audit, docs/architecture/TMUX_VIBE_SESSIONS_AUDIT.md):
//
// tmux.go models a session by its ACTIVE pane alone — one AgentType, one
// MainPID, one PanePreview, all from getActivePaneIdentity(). That is correct
// for one-agent-per-session and blind for the layout people actually use: a
// single session split into panes with claude in one, codex in another,
// opencode in a third. Probed on the author's own box the day this was
// written — one session, two claude panes — the old code reported one agent.
//
// Blindness is the mild half. The dangerous half is that every session-targeted
// tmux call resolves to whatever pane happens to be ACTIVE:
//
//   - capture-pane -t <session>  reads the active pane, so a task's "output"
//     can be a different agent's screen;
//   - send-keys -t <session>     TYPES INTO the active pane, so a follow-up
//     aimed at the codex task can land in the claude one — and the caller is
//     still told "sent";
//   - tmuxPaneAwaitingChoice(<session>) inspects the active pane, so the menu
//     guard that exists to stop a prompt selecting `1. Update now` in codex
//     (which self-updated, exited, and took the session with it) GUARDS THE
//     WRONG PANE. A menu open in a non-active pane is invisible to it.
//
// So pane targeting is a safety fix wearing a feature's clothes. Everything
// here targets `%<paneID>`, which tmux accepts anywhere a session name is
// accepted — that is why the helpers in tmux.go needed no signature change,
// only honest callers.
//
// The status model follows RunnerPTYSession.Confirmed (runner_pty.go): classify
// by OBSERVING the process, never by trusting a name. A pane whose agent has
// exited is a plain shell, and a "prompt" typed into a shell is a COMMAND.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Vibe status values. These describe what the pane is DOING, which is the one
// thing a task list with three agents in it cannot compute for itself.
const (
	VibeStatusWorking   = "working"        // producing output right now
	VibeStatusAwaiting  = "awaiting-input" // showing a menu; it needs the user
	VibeStatusIdle      = "idle"           // agent alive, nothing changing
	VibeStatusNoAgent   = "no-agent"       // a bare shell — typing here RUNS things
	VibeStatusDead      = "dead"           // pane's process exited
	VibeStatusUnknown   = "unknown"        // probe deadline hit; we will not guess
	vibeIdleThreshold   = 6 * time.Second
	vibePreviewLines    = 40
	vibeSignatureLines  = 12
	vibeAgentWalkDepth  = 3
	vibeDefaultDeadline = 2500 * time.Millisecond
)

// VibePane is one agent seat: a single tmux pane and everything we can honestly
// say about it.
type VibePane struct {
	PaneID      string `json:"paneId"` // "%37" — the ONLY safe send-keys target
	SessionName string `json:"sessionName"`
	SessionID   string `json:"sessionId,omitempty"` // "$0"
	WindowIndex string `json:"windowIndex,omitempty"`
	WindowName  string `json:"windowName,omitempty"`
	PaneIndex   string `json:"paneIndex,omitempty"`
	Active      bool   `json:"active"`

	Agent string `json:"agent,omitempty"` // claude | codex | opencode | shell
	// AgentConfirmed is true only when a matching process was OBSERVED in the
	// pane's tree. False means we are guessing from a name, and a guess is not
	// good enough to type a prompt into — see runner_pty.go:367 for the
	// incident that established this.
	AgentConfirmed bool `json:"agentConfirmed"`
	PID            int  `json:"pid,omitempty"`

	Status       string   `json:"status"`
	StatusReason string   `json:"statusReason,omitempty"`
	Options      []string `json:"options,omitempty"` // when awaiting-input
	Title        string   `json:"title,omitempty"`   // pane_title
	IdleMs       int64    `json:"idleMs,omitempty"`

	// CurrentPath is the pane's working directory. It is an ABSOLUTE path and
	// therefore P2P-ONLY: it leaks the user's home-dir username, and
	// convex_privacy_test.go forbids it in every Convex payload. Never add it
	// to a mutation, a device row, or a userProjects record.
	CurrentPath string `json:"currentPath,omitempty"`

	Preview string `json:"preview,omitempty"`
	TaskID  string `json:"taskId,omitempty"` // the Yaver Task this pane is
}

// vibeSampler remembers what each pane looked like last time so "is it
// working?" can be answered by OUTPUT DELTA — the one signal that works for
// every agent, including ones Yaver has never heard of.
type vibeSampler struct {
	mu   sync.Mutex
	seen map[string]vibeSample
}

type vibeSample struct {
	sig        [32]byte
	lastChange time.Time
}

var paneSampler = &vibeSampler{seen: map[string]vibeSample{}}

// observe records a pane's current signature and reports how long it has been
// unchanged. A pane seen for the first time counts as just-changed: we have no
// evidence of idleness yet, and claiming some would be a guess.
func (v *vibeSampler) observe(paneID string, content string) time.Duration {
	sig := sha256.Sum256([]byte(content))
	now := time.Now()

	v.mu.Lock()
	defer v.mu.Unlock()
	prev, ok := v.seen[paneID]
	if !ok || prev.sig != sig {
		v.seen[paneID] = vibeSample{sig: sig, lastChange: now}
		return 0
	}
	return now.Sub(prev.lastChange)
}

// forget drops panes that no longer exist, so a long-lived agent does not
// accumulate a sample per pane the user has ever opened.
func (v *vibeSampler) forget(live map[string]bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for id := range v.seen {
		if !live[id] {
			delete(v.seen, id)
		}
	}
}

// ListVibePanes enumerates every pane on this machine with its agent and status.
//
// Shape matters here. The spine is ONE `tmux list-panes -a` — a single fork for
// the whole machine. Everything after it (ps, pgrep, capture-pane) is
// per-pane enrichment, which is ADVISORY: CLAUDE.md requires advisory work to
// carry a wall-clock deadline and degrade to empty rather than block, because
// bounding the proxy (pane count, tree depth) never bounds the thing that
// actually hurts — wall-clock. Past the deadline, remaining panes come back
// as VibeStatusUnknown, and the caller still gets an answer.
func ListVibePanes(ctx context.Context) ([]VibePane, error) {
	if !tmuxAvailable() {
		return nil, nil
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, vibeDefaultDeadline)
		defer cancel()
	}

	out, err := exec.CommandContext(ctx, tmuxCmdName(), "list-panes", "-a", "-F",
		// TAB separates, and pane_title is LAST on purpose.
		//
		// The obvious choice — an ASCII unit separator (0x1f) — does not
		// survive: tmux ESCAPES control bytes in -F output, so the field
		// separator arrives as the four literal characters `\037` and every
		// line parses as one field. Probed against tmux 3.5a. Tab passes
		// through untouched and cannot appear in a session name, pane id or
		// pid; a title may contain anything, which is why it goes last and the
		// split is bounded.
		strings.Join([]string{
			"#{session_name}", "#{session_id}", "#{window_index}", "#{window_name}",
			"#{pane_index}", "#{pane_id}", "#{pane_pid}", "#{pane_active}",
			"#{pane_dead}", "#{pane_current_path}", "#{pane_title}",
		}, "\t")).CombinedOutput()
	if err != nil {
		// No server running is the empty case, not a failure. A task list that
		// hard-errors because the user has not opened tmux yet is a bug.
		if isTmuxNoServer(string(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var panes []VibePane
	live := map[string]bool{}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 11)
		if len(f) < 11 {
			continue
		}
		pid, _ := strconv.Atoi(f[6])
		p := VibePane{
			SessionName: f[0],
			SessionID:   f[1],
			WindowIndex: f[2],
			WindowName:  f[3],
			PaneIndex:   f[4],
			PaneID:      f[5],
			PID:         pid,
			Active:      f[7] == "1",
			CurrentPath: f[9],
			Title:       f[10],
			Status:      VibeStatusUnknown,
		}
		if f[8] == "1" {
			p.Status = VibeStatusDead
			p.StatusReason = "the pane's process has exited; nothing is listening here"
		}
		live[p.PaneID] = true
		panes = append(panes, p)
	}

	for i := range panes {
		if panes[i].Status == VibeStatusDead {
			continue
		}
		if ctx.Err() != nil {
			// Deadline hit. Leave the rest as unknown and SAY so — a guessed
			// status on a pane we never probed is exactly the false green this
			// whole feature exists to remove.
			panes[i].StatusReason = "status probe timed out; this pane was not inspected"
			continue
		}
		enrichVibePane(ctx, &panes[i])
	}

	paneSampler.forget(live)
	return panes, nil
}

// enrichVibePane fills in agent identity and status for one pane.
func enrichVibePane(ctx context.Context, p *VibePane) {
	agent, confirmed := detectPaneAgent(ctx, p.PID)
	p.Agent, p.AgentConfirmed = agent, confirmed

	content := capturePaneTarget(ctx, p.PaneID, vibePreviewLines)
	p.Preview = strings.TrimRight(content, "\n ")

	// Signature deliberately covers only the visible tail: a long transcript
	// scrolling by would otherwise hash differently forever and every pane
	// would read as "working".
	idle := paneSampler.observe(p.PaneID, tailLines(content, vibeSignatureLines))
	p.IdleMs = idle.Milliseconds()

	// Order is the trust ladder, most trustworthy first. A menu beats a
	// spinner: an agent can render both, and only one of them needs the user.
	switch {
	case !confirmed:
		p.Status = VibeStatusNoAgent
		p.StatusReason = "no coding agent is running in this pane — text sent here would be executed as a SHELL COMMAND, not read as a prompt. Start an agent in it first."

	case paneAwaitingChoiceAt(ctx, p.PaneID, p):
		p.Status = VibeStatusAwaiting
		p.StatusReason = "waiting on a menu choice — reply with just the option number; it confirms immediately, so re-read the pane afterwards because menus chain"

	case idle == 0 || idle < vibeIdleThreshold || hasSpinnerGlyph(p.Title):
		p.Status = VibeStatusWorking
		p.StatusReason = "producing output"

	default:
		p.Status = VibeStatusIdle
		p.StatusReason = "agent is running but has been quiet — it is waiting for a prompt"
	}
}

// paneAwaitingChoiceAt is tmuxPaneAwaitingChoice's pane-targeted twin, and
// records the options on the pane so a screenless surface (watch, car) can read
// them out and answer with a digit.
func paneAwaitingChoiceAt(ctx context.Context, paneID string, p *VibePane) bool {
	out, err := exec.CommandContext(ctx, tmuxCmdName(), "capture-pane", "-p", "-t", paneID).Output()
	if err != nil {
		return false // cannot see the pane; do not claim it is blocked
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
		return false
	}
	p.Options = options
	return true
}

// detectPaneAgent walks the pane's process tree to bounded depth looking for a
// known agent binary.
//
// Depth is the fix for a real miss: detectAgentType() checks the pane pid and
// its DIRECT children only. Probed live, `zsh → claude` resolves at depth 1 and
// works — but `zsh → npm exec → claude`, a direnv/mise shim, or any wrapper
// puts the agent at depth 2 and the pane reports NO AGENT, which under the
// status model above means "typing here runs a shell command". A false
// no-agent is a silently unusable pane.
//
// The second return is the confidence bit: true only when a process was
// actually observed. Nothing here infers an agent from a session NAME.
func detectPaneAgent(ctx context.Context, pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	frontier := []int{pid}
	for depth := 0; depth < vibeAgentWalkDepth && len(frontier) > 0; depth++ {
		var next []int
		for _, p := range frontier {
			if ctx.Err() != nil {
				return "", false
			}
			// ps preserves argv[0], which is the only reason this works: a live
			// claude renames its own process, so `pane_current_command` reports
			// its VERSION ("2.1.216") and matching on that finds nothing.
			if agent := matchAgentCommand(getProcessCommand(p)); agent != "" {
				return agent, true
			}
			next = append(next, getChildPIDs(p)...)
		}
		frontier = next
	}
	return "", false
}

// spinner glyphs agents animate while they work. Braille is what Claude Code
// uses (probed: "⠂ Discuss arbitrage…"); the asterisk forms cover its other
// states. This is a TIEBREAK only — whether codex and opencode set pane_title
// at all is unverified, so no status may rest on it alone.
func hasSpinnerGlyph(title string) bool {
	for _, r := range title {
		if r >= 0x2800 && r <= 0x28FF { // braille patterns
			return true
		}
		switch r {
		case '✳', '✶', '✻', '✽', '·', '∗':
			return true
		}
	}
	return false
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// capturePaneTarget captures a bounded tail from a pane by PANE ID.
//
// Mirrors capturePane()'s alternate-screen handling: a TUI that has switched to
// the alternate screen renders nothing useful on the normal one, so try both
// and keep whichever carries more signal.
func capturePaneTarget(ctx context.Context, paneID string, lines int) string {
	normal := capturePaneTargetMode(ctx, paneID, lines, false)
	alternate := capturePaneTargetMode(ctx, paneID, lines, true)
	if strings.TrimSpace(alternate) != "" && paneCaptureSignal(alternate) > paneCaptureSignal(normal) {
		return alternate
	}
	return normal
}

func capturePaneTargetMode(ctx context.Context, paneID string, lines int, alternate bool) string {
	args := []string{"capture-pane", "-t", paneID, "-p", "-S", "-" + strconv.Itoa(lines)}
	if alternate {
		args = append([]string{"capture-pane", "-a"}, args[1:]...)
	}
	out, err := exec.CommandContext(ctx, tmuxCmdName(), args...).Output()
	if err != nil {
		return ""
	}
	return stripControlChars(string(out))
}

// taskTmuxTarget resolves the tmux `-t` target for an adopted task.
//
// Prefer the recorded pane id. Sessions adopted before pane targeting existed
// have no PaneID, and for those the session name is the only handle we have —
// but fall back only when the pane is genuinely unknown, never as a shortcut.
// A stale pane id (the pane closed, the id got reused) is caught here rather
// than by typing into whatever inherited it.
func (m *TmuxManager) taskTmuxTarget(taskID, sessionName string) string {
	if m.taskMgr == nil {
		return sessionName
	}
	m.taskMgr.mu.RLock()
	task, ok := m.taskMgr.tasks[taskID]
	var paneID string
	if ok && task != nil {
		paneID = strings.TrimSpace(task.TmuxPaneID)
	}
	m.taskMgr.mu.RUnlock()

	if paneID == "" || !tmuxPaneExists(paneID) {
		return sessionName
	}
	return paneID
}

// adoptionKey is the map key under which a pane's adoption is registered.
//
// The pane id when we have one, the session name otherwise. Keying on the
// session was the old behaviour and it caps a session at ONE task — adopting
// the codex pane of a session whose claude pane was already adopted came back
// "already adopted". Pane ids ("%37") and tmux session names do not collide in
// practice, and the map is internal.
func adoptionKey(sessionName, paneID string) string {
	if p := strings.TrimSpace(paneID); p != "" {
		return p
	}
	return sessionName
}

// adoptionPollTarget picks the tmux `-t` target for a task's output poll: the
// pane when the key is one, else the session name for legacy adoptions.
func adoptionPollTarget(key, sessionName string) string {
	if strings.HasPrefix(key, "%") {
		return key
	}
	return sessionName
}

// tmuxTargetExists reports whether a pane id or session name still resolves.
// `has-session` does not accept a pane id, so route each to the right probe.
func tmuxTargetExists(target string) bool {
	if strings.HasPrefix(target, "%") {
		return tmuxPaneExists(target)
	}
	return tmuxSessionExists(target)
}

// paneIdentityByID resolves one pane of a session by its pane id.
func paneIdentityByID(sessionName, paneID string) (tmuxPaneIdentity, bool) {
	paneID = strings.TrimSpace(paneID)
	out, err := exec.Command(tmuxCmdName(), "list-panes", "-t", sessionName,
		"-F", "#{session_id}\t#{window_index}\t#{window_name}\t#{pane_index}\t#{pane_id}\t#{pane_pid}").Output()
	if err != nil {
		return tmuxPaneIdentity{}, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "\t", 6)
		if len(f) < 6 || f[4] != paneID {
			continue
		}
		pid, _ := strconv.Atoi(f[5])
		return tmuxPaneIdentity{
			SessionID: f[0], WindowIndex: f[1], WindowName: f[2],
			PaneIndex: f[3], PaneID: f[4], PanePID: pid,
		}, true
	}
	return tmuxPaneIdentity{}, false
}

// paneTaskID finds the Yaver Task adopted for a given pane, if any.
//
// Keyed on PANE id, not session name: on a split window several tasks share one
// session, and a session-keyed lookup would hand every pane the same task.
// Takes the TaskManager explicitly so callers already holding TmuxManager's
// lock cannot deadlock re-entering it.
func paneTaskID(taskMgr *TaskManager, paneID string) string {
	if taskMgr == nil || strings.TrimSpace(paneID) == "" {
		return ""
	}
	taskMgr.mu.RLock()
	defer taskMgr.mu.RUnlock()
	for id, t := range taskMgr.tasks {
		if t != nil && t.TmuxPaneID == paneID && t.IsAdopted {
			return id
		}
	}
	return ""
}

// tmuxPaneExists reports whether a pane id is still live. Pane ids are recycled
// after a pane dies, so existence is necessary but not sufficient — callers
// that are about to TYPE also check tmuxTargetAcceptsPrompt.
func tmuxPaneExists(paneID string) bool {
	out, err := exec.Command(tmuxCmdName(), "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == paneID {
			return true
		}
	}
	return false
}

// tmuxTargetAcceptsPrompt reports whether a coding agent is actually running in
// the target, and if not, why typing there would be wrong.
//
// This is the "probe the operation, not the inventory" check for the send path:
// the target existing proves nothing about what is listening on it.
func tmuxTargetAcceptsPrompt(target string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), vibeDefaultDeadline)
	defer cancel()

	out, err := exec.CommandContext(ctx, tmuxCmdName(), "list-panes", "-t", target,
		"-F", "#{pane_active}\t#{pane_pid}\t#{pane_dead}").Output()
	if err != nil {
		// Cannot see it. Do not block the caller on our own blindness — the
		// send will fail loudly on its own if the target is truly gone.
		return true, ""
	}
	var pid int
	var dead bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		p, _ := strconv.Atoi(f[1])
		if pid == 0 || f[0] == "1" { // first pane, or the active one
			pid, dead = p, f[2] == "1"
		}
	}
	if dead {
		return false, "the pane's process has exited — there is nothing left to read the input"
	}
	if _, confirmed := detectPaneAgent(ctx, pid); !confirmed {
		return false, "no coding agent is running there, so this text would be EXECUTED as a shell command rather than read as a prompt. Start an agent in that pane first"
	}
	return true, ""
}

// trimTrailingBlankLines drops the empty rows capture-pane pads a pane with.
//
// The menu scan looks at the last N lines so a numbered list scrolled up in a
// transcript is not mistaken for a live menu. But capture-pane always returns
// the FULL pane height, so a menu that does not reach the bottom is followed by
// blank rows — and with enough of them the real menu falls outside the window
// and the guard reports "no menu" for a pane that is plainly waiting. Padding
// is not content; drop it before measuring.
func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

func isTmuxNoServer(out string) bool {
	return strings.Contains(out, "no server running") || strings.Contains(out, "no sessions")
}

// anyAdoptedPaneTask returns the task id of the first adopted pane in the set.
// Caller must already hold TmuxManager's read lock.
func (m *TmuxManager) anyAdoptedPaneTask(panes []VibePane) string {
	for _, p := range panes {
		if id, ok := m.adopted[p.PaneID]; ok {
			return id
		}
	}
	return ""
}
