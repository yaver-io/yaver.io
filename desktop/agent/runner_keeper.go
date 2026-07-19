package main

// runner_keeper.go — P7 same-session runner continuation supervisor.
//
// COMPLIANCE (read once, carry through every change):
//   Runs on the USER's OWN machine via the USER's OWN subscription
//   interactive CLI, single-instance, sequential, never parallel.
//   No -p headless farming, no API-key impersonation, no shared
//   sessions. This mirrors the "subscription-CLI-only / no-api-keys"
//   law from the runner_auth story: the keeper is a scheduler for the
//   already-authorised interactive session, not a runner replicator.
//   It NEVER forks a new runner process — that's the whole point.
//
// Runner-agnostic: works with claude / codex / opencode / glm.
// Liveness detection is CONTENT-BASED, not `pane_current_command`-
// based (that returns the shell PID name inside tmux even when the
// runner has stopped emitting output).
//
// What the keeper does per session:
//   1. Poll `tmux capture-pane` every N seconds, hash the tail.
//   2. When the hash stops changing for `idleDebounce` AND the queue
//      for that session is non-empty, dequeue the next prompt and
//      `tmux send-keys` it into the same pane. Cap nudges per hour.
//   3. Log every nudge to disk so runner_status can attribute time
//      and count.
//
// Two persistence files (owner-only 0600 mode) — both under
// ~/.yaver/runner/:
//   queue.json     — list of {sessionName, prompt, addedAt} rows
//   keeper.state   — {mode: user-driven|auto, lastActivity, nudges}
//                    per session. Read by runner_status.
//
// The MCP verbs (runner_attach/detach/autorun/queue_*) live in
// runner_keeper_mcp.go.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// KeeperMode is the current stance for a supervised session.
type KeeperMode string

const (
	KeeperModeUserDriven KeeperMode = "user-driven" // human attached and vibing
	KeeperModeAuto       KeeperMode = "auto"        // keeper drains the queue
	KeeperModeOff        KeeperMode = "off"         // not supervised
)

// QueueEntry is one work item waiting to be nudged into a session.
type QueueEntry struct {
	ID          string `json:"id"`
	SessionName string `json:"sessionName"`
	Prompt      string `json:"prompt"`
	AddedAt     string `json:"addedAt"`
	Source      string `json:"source,omitempty"` // phone / mcp / cli
}

// SessionState is the keeper's per-session bookkeeping. Persisted
// so runner_status can render an accurate picture across restarts.
type SessionState struct {
	SessionName    string     `json:"sessionName"`
	Mode           KeeperMode `json:"mode"`
	LastActivity   string     `json:"lastActivity,omitempty"` // when the pane content last changed
	LastNudge      string     `json:"lastNudge,omitempty"`
	NudgesLastHour int        `json:"nudgesLastHour"`
	NudgesTotal    int        `json:"nudgesTotal"`
	QueuedCount    int        `json:"queuedCount"`
	Runner         string     `json:"runner,omitempty"` // claude / codex / opencode / glm
}

// RunnerKeeper is the supervisor. One instance per HTTPServer.
type RunnerKeeper struct {
	mu           sync.Mutex
	queue        []QueueEntry
	states       map[string]*SessionState
	baseDir      string
	pollInterval time.Duration
	idleDebounce time.Duration
	// paneHash[session] = last observed hash; used to detect stall.
	paneHash map[string]string
	// lastActivityAt[session] = in-memory high-resolution timestamp of
	// the last pane content change. RFC3339 on SessionState.LastActivity
	// loses sub-second precision, so the debounce check compares against
	// this instead — required for the 10ms-debounce test path.
	lastActivityAt map[string]time.Time
	stopSignals    map[string]chan struct{}
	// sendKeys is the seam tests use to intercept `tmux send-keys`
	// without touching a real tmux server.
	sendKeys func(sessionName, text string) error
	// capturePane is the corresponding read seam.
	capturePane func(sessionName string) (string, error)
	// clock is the time source (tests can freeze it).
	clock func() time.Time
	// nudgeCap is the per-hour cap; 0 disables the cap (tests).
	nudgeCap int
	// superviseOnce guards the single production drain goroutine so that
	// repeated ensureRunnerKeeper()/StartSupervisor() calls can't spawn
	// duplicate loops.
	superviseOnce sync.Once
}

// NewRunnerKeeper builds a keeper rooted at ~/.yaver/runner/.
func NewRunnerKeeper() (*RunnerKeeper, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(cfgDir, "runner")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	k := &RunnerKeeper{
		baseDir:      base,
		pollInterval: 15 * time.Second,
		idleDebounce: 90 * time.Second,
		nudgeCap:     20,
		states:         map[string]*SessionState{},
		paneHash:       map[string]string{},
		lastActivityAt: map[string]time.Time{},
		stopSignals:    map[string]chan struct{}{},
		sendKeys:     defaultTmuxSendKeys,
		capturePane:  defaultTmuxCapturePane,
		clock:        time.Now,
	}
	if err := k.loadFromDisk(); err != nil {
		return nil, err
	}
	return k, nil
}

// EnqueuePrompt adds a work item for a session. Empty session name
// or prompt is rejected. Returns the new entry id.
func (k *RunnerKeeper) EnqueuePrompt(sessionName, prompt, source string) (string, error) {
	sessionName = strings.TrimSpace(sessionName)
	prompt = strings.TrimSpace(prompt)
	if sessionName == "" {
		return "", fmt.Errorf("sessionName is required")
	}
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	id := fmt.Sprintf("q_%d_%s", k.clock().UTC().UnixNano(), keeperShortHash(prompt))
	k.queue = append(k.queue, QueueEntry{
		ID:          id,
		SessionName: sessionName,
		Prompt:      prompt,
		AddedAt:     k.clock().UTC().Format(time.RFC3339),
		Source:      source,
	})
	k.recomputeQueuedCountLocked(sessionName)
	return id, k.persistLocked()
}

// ListQueue returns entries; when sessionName is empty, all sessions.
func (k *RunnerKeeper) ListQueue(sessionName string) []QueueEntry {
	k.mu.Lock()
	defer k.mu.Unlock()
	if sessionName == "" {
		out := make([]QueueEntry, len(k.queue))
		copy(out, k.queue)
		return out
	}
	out := make([]QueueEntry, 0, len(k.queue))
	for _, q := range k.queue {
		if q.SessionName == sessionName {
			out = append(out, q)
		}
	}
	return out
}

// ClearQueue drops queued items (all or one session).
func (k *RunnerKeeper) ClearQueue(sessionName string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	removed := 0
	if sessionName == "" {
		removed = len(k.queue)
		k.queue = k.queue[:0]
	} else {
		kept := k.queue[:0]
		for _, q := range k.queue {
			if q.SessionName != sessionName {
				kept = append(kept, q)
			} else {
				removed++
			}
		}
		k.queue = kept
	}
	k.recomputeQueuedCountLocked(sessionName)
	_ = k.persistLocked()
	return removed
}

// SetMode flips a session between user-driven / auto / off.
func (k *RunnerKeeper) SetMode(sessionName string, mode KeeperMode) error {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return fmt.Errorf("sessionName is required")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	st, ok := k.states[sessionName]
	if !ok {
		st = &SessionState{SessionName: sessionName}
		k.states[sessionName] = st
	}
	st.Mode = mode
	return k.persistLocked()
}

// State returns the (possibly zero) SessionState for reporting.
func (k *RunnerKeeper) State(sessionName string) SessionState {
	k.mu.Lock()
	defer k.mu.Unlock()
	if st, ok := k.states[sessionName]; ok {
		return *st
	}
	return SessionState{SessionName: sessionName, Mode: KeeperModeOff}
}

// AllStates lists every known session state (sorted, deterministic).
func (k *RunnerKeeper) AllStates() []SessionState {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]SessionState, 0, len(k.states))
	for _, st := range k.states {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionName < out[j].SessionName })
	return out
}

// Tick is one supervision cycle. Exported so tests can drive
// deterministically. Real runtime calls this from a per-session
// goroutine at pollInterval.
func (k *RunnerKeeper) Tick(sessionName string) (nudged bool, err error) {
	pane, err := k.capturePane(sessionName)
	if err != nil {
		return false, err
	}
	hash := sha256Hex(tailOf(pane, 40))

	k.mu.Lock()
	prev, hadPrev := k.paneHash[sessionName]
	k.paneHash[sessionName] = hash
	st, ok := k.states[sessionName]
	if !ok {
		st = &SessionState{SessionName: sessionName, Mode: KeeperModeAuto}
		k.states[sessionName] = st
	}
	now := k.clock()
	if !hadPrev || prev != hash {
		st.LastActivity = now.UTC().Format(time.RFC3339)
		k.lastActivityAt[sessionName] = now
		k.mu.Unlock()
		_ = k.persistWithLock()
		return false, nil
	}
	// Pane content is unchanged since last tick.
	if st.Mode != KeeperModeAuto {
		k.mu.Unlock()
		return false, nil
	}
	// Cap: at most nudgeCap nudges per hour.
	if k.nudgeCap > 0 && st.NudgesLastHour >= k.nudgeCap {
		k.mu.Unlock()
		return false, nil
	}
	// Idle-long-enough gate — compare against the high-res in-memory
	// timestamp so 10ms-debounce tests work; the RFC3339 string only
	// exists for status display.
	if last, seen := k.lastActivityAt[sessionName]; seen && now.Sub(last) < k.idleDebounce {
		k.mu.Unlock()
		return false, nil
	}
	// Find next queued prompt for this session.
	var next *QueueEntry
	var nextIdx int
	for i := range k.queue {
		if k.queue[i].SessionName == sessionName {
			next = &k.queue[i]
			nextIdx = i
			break
		}
	}
	if next == nil {
		k.mu.Unlock()
		return false, nil
	}
	prompt := next.Prompt
	k.queue = append(k.queue[:nextIdx], k.queue[nextIdx+1:]...)
	st.NudgesTotal++
	st.NudgesLastHour++
	st.LastNudge = now.UTC().Format(time.RFC3339)
	k.recomputeQueuedCountLocked(sessionName)
	k.mu.Unlock()

	if err := k.sendKeys(sessionName, prompt); err != nil {
		return false, err
	}
	_ = k.persistWithLock()
	return true, nil
}

// DecrementHourlyNudges is called from a per-hour tick to reset the
// nudgeCap window. Exposed so tests can advance the accounting.
func (k *RunnerKeeper) DecrementHourlyNudges() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, st := range k.states {
		st.NudgesLastHour = 0
	}
}

// sessionsNeedingTick returns every session the drain loop must visit this
// cycle: the union of sessions we already track state for AND sessions that
// only exist as queued work (EnqueuePrompt can add a prompt for a session
// before the keeper has ever seen its pane). Without the queue half, a freshly
// enqueued prompt for a brand-new session would never be drained.
func (k *RunnerKeeper) sessionsNeedingTick() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	seen := make(map[string]bool, len(k.states)+len(k.queue))
	out := make([]string, 0, len(k.states)+len(k.queue))
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for name := range k.states {
		add(name)
	}
	for i := range k.queue {
		add(k.queue[i].SessionName)
	}
	sort.Strings(out)
	return out
}

// Supervise is the production drain loop the Tick doc comment references
// ("Real runtime calls this from a per-session goroutine at pollInterval").
// It was never wired up, so every prompt EnqueuePrompt wrote to queue.json was
// silently swallowed — runner_queue_add looked like it worked but nothing
// delivered the work. This loop Ticks each known/queued session every
// pollInterval (draining idle KeeperModeAuto sessions) and resets the
// per-hour nudge accounting each hour. Runs until ctx is cancelled.
func (k *RunnerKeeper) Supervise(ctx context.Context) {
	poll := k.pollInterval
	if poll <= 0 {
		poll = 15 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			k.DecrementHourlyNudges()
		case <-ticker.C:
			for _, name := range k.sessionsNeedingTick() {
				// A dead/renamed session makes capturePane fail; that is
				// expected churn, not fatal. Skip it and keep draining the
				// rest so one gone session can't stall the whole queue.
				_, _ = k.Tick(name)
			}
		}
	}
}

// StartSupervisor launches the drain loop exactly once for this keeper.
// Safe to call from every ensureRunnerKeeper() path.
func (k *RunnerKeeper) StartSupervisor(ctx context.Context) {
	k.superviseOnce.Do(func() {
		go k.Supervise(ctx)
	})
}

// -- persistence helpers -------------------------------------------

func (k *RunnerKeeper) persistWithLock() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.persistLocked()
}

func (k *RunnerKeeper) persistLocked() error {
	queuePath := filepath.Join(k.baseDir, "queue.json")
	buf, err := json.MarshalIndent(k.queue, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(queuePath, buf, 0o600); err != nil {
		return err
	}
	statePath := filepath.Join(k.baseDir, "keeper.state")
	states := make([]*SessionState, 0, len(k.states))
	for _, st := range k.states {
		states = append(states, st)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].SessionName < states[j].SessionName })
	sbuf, _ := json.MarshalIndent(states, "", "  ")
	return os.WriteFile(statePath, sbuf, 0o600)
}

func (k *RunnerKeeper) loadFromDisk() error {
	queuePath := filepath.Join(k.baseDir, "queue.json")
	if data, err := os.ReadFile(queuePath); err == nil {
		var q []QueueEntry
		if json.Unmarshal(data, &q) == nil {
			k.queue = q
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	statePath := filepath.Join(k.baseDir, "keeper.state")
	if data, err := os.ReadFile(statePath); err == nil {
		var states []*SessionState
		if json.Unmarshal(data, &states) == nil {
			for _, st := range states {
				k.states[st.SessionName] = st
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (k *RunnerKeeper) recomputeQueuedCountLocked(session string) {
	counts := map[string]int{}
	for _, q := range k.queue {
		counts[q.SessionName]++
	}
	for name, st := range k.states {
		st.QueuedCount = counts[name]
	}
	if _, ok := k.states[session]; !ok && session != "" {
		k.states[session] = &SessionState{SessionName: session, QueuedCount: counts[session], Mode: KeeperModeOff}
	}
}

// -- helpers -------------------------------------------------------

func defaultTmuxSendKeys(sessionName, text string) error {
	// tmux send-keys with literal text + Enter. Explicit -l so ~/./
	// aren't shell-interpreted; -R resets state for a fresh prompt.
	tmux := tmuxBin()
	if tmux == "" {
		return fmt.Errorf("tmux is not installed: %s", TmuxInstallHint())
	}
	if err := exec.Command(tmux, "send-keys", "-t", sessionName, "-l", "-R", text).Run(); err != nil {
		return fmt.Errorf("tmux send-keys text: %w", err)
	}
	return exec.Command(tmux, "send-keys", "-t", sessionName, "Enter").Run()
}

func defaultTmuxCapturePane(sessionName string) (string, error) {
	tmux := tmuxBin()
	if tmux == "" {
		return "", fmt.Errorf("tmux is not installed: %s", TmuxInstallHint())
	}
	out, err := exec.Command(tmux, "capture-pane", "-p", "-t", sessionName).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func tailOf(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func sha256Hex(s string) string {
	sum := sha256.New()
	_, _ = io.WriteString(sum, s)
	return hex.EncodeToString(sum.Sum(nil))
}

func keeperShortHash(s string) string {
	h := sha256Hex(s)
	if len(h) < 8 {
		return h
	}
	return h[:8]
}
