package main

// TaskSupervisor — the agent's in-process "what should be running and is it
// actually running?" registry. Addresses Gap 1 from the self-healing-timers
// research: the 7+ ticker loops that run inside `yaver serve` (heartbeat,
// scheduler, state sync, peer watcher, token refresh, auto-update, …) all
// used to be bare `time.NewTicker + go f()` calls scattered across files.
// A panic in one, or a silent stall caused by a deadlocked mutex / blocking
// I/O, would go unnoticed until a human noticed a second-order effect
// (peer offline, auth expired, …).
//
// What the supervisor adds, in priority order:
//
//   1. **Panic recovery.** Every registered task runs inside a recover()
//      wrapper. A panic kills the tick, not the goroutine; the next tick
//      runs as scheduled. Stack trace lands in the supervisor's ErrorCount
//      and the agent log — not in an uncaught-panic-crash-the-daemon path.
//
//   2. **LastTick / LastError / ErrorCount observability.** The agent
//      (and any client — smoke check, CLI, /self-check viewer) can see
//      "when did the heartbeat last fire?" without grepping logs.
//
//   3. **Stall watchdog.** A separate goroutine checks every 30s whether
//      any registered task has gone silent (no tick or error reported
//      in > 10 × its configured interval — research note: 3× produces
//      false positives from GC pauses and overrunning tick bodies). On
//      first transition to stalled it logs loudly; desktop notifications
//      are opt-in (env YAVER_SUPERVISOR_DESKTOP_NOTIFY=1), because
//      Syncthing / Tailscale / Home Assistant all surface internal
//      stalls via a status endpoint rather than OS popups — popups
//      train users to mute them.
//
//   4. **Unified /self-check HTTP endpoint.** One place for humans +
//      scripts to see the full ticker state without having to know which
//      file each ticker lives in.
//
//   5. **Tick-duration tracking.** `time.Ticker` silently coalesces:
//      if the tick body takes longer than the interval, the next tick
//      vanishes and the only symptom is missed work. We record
//      LastTickDuration and log a warning when a tick exceeds interval/2,
//      before stall-detection would kick in.
//
//   6. **Panic backoff.** If a task panics N times in a short window,
//      we skip its next M ticks to avoid a CPU-burning restart storm.
//      OTP's `max_restarts` lesson in miniature.
//
// Non-goals (deliberately):
//   - Not a scheduler. User-facing cron jobs still live in scheduler.go.
//   - Not cross-process. A stalled ticker here does not restart another
//     process; it logs. The external systemd smoke watchdog remains
//     the "agent is completely dead" tripwire.
//   - Not durable. Supervisor state is in-memory only; restarting yaver
//     wipes the history.
//   - Cannot rescue a genuinely deadlocked goroutine. If a tick blocks
//     on `mu.Lock()` forever, ctx cancellation does NOT unblock it
//     (Go mutex acquisition is not preemptible by context). The stall
//     will be visible in /self-check but only a process restart can
//     actually recover. We log it loudly so the fact is not silent.

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Tuning knobs — extracted so tests can override without env vars.
const (
	// stallMultiplier: mark a task stalled after lastTick is older than
	// interval × this. 10× is per research note — 3× produces false
	// positives on GC pauses and tick-bodies-longer-than-interval.
	stallMultiplier = 10

	// slowTickWarnRatio: log a warning if a single tick takes longer
	// than interval × this. Catches "silent coalesced tick" loss before
	// stall-detection would.
	slowTickWarnRatio = 0.5

	// panicBackoffWindow + panicBackoffMax: after N panics within the
	// window, skip the task for this-many intervals. Applied per task.
	panicBackoffWindow = 60 * time.Second
	panicBackoffMax    = 3 // skip this many ticks after burst
)

// TaskFunc is the payload of one tick. Returning an error records it as
// the LastError for this task; returning nil clears the error state.
// A panic is caught and converted to an error via recover().
type TaskFunc func(ctx context.Context) error

// TickerHealth describes the lifecycle state of a single registered task.
type TickerHealth struct {
	Name            string        `json:"name"`
	Interval        time.Duration `json:"-"`
	IntervalSeconds int64         `json:"intervalSeconds"`

	Registered bool `json:"registered"`
	// Stopped is true when Stop() has been called on the supervisor or
	// on this individual task. Stopped tasks are not watched.
	Stopped bool `json:"stopped"`

	// Counters (atomics) — no lock needed to read in /self-check.
	Runs     uint64 `json:"runs"`
	Errors   uint64 `json:"errors"`
	Panics   uint64 `json:"panics"`
	Stalls   uint64 `json:"stalls"`
	Restarts uint64 `json:"restarts"`

	LastTickAt       *time.Time `json:"lastTickAt,omitempty"`
	LastOKAt         *time.Time `json:"lastOkAt,omitempty"`
	LastErrorAt      *time.Time `json:"lastErrorAt,omitempty"`
	LastErrorText    string     `json:"lastErrorText,omitempty"`
	LastPanicAt      *time.Time `json:"lastPanicAt,omitempty"`
	LastPanicStack   string     `json:"lastPanicStack,omitempty"`
	LastTickDuration string     `json:"lastTickDuration,omitempty"`
	MaxTickDuration  string     `json:"maxTickDuration,omitempty"`
	SkippedTicks     uint64     `json:"skippedTicks,omitempty"`

	// Computed in the snapshot, not stored.
	StalledFor       *time.Duration `json:"-"`
	StalledForSec    int64          `json:"stalledForSeconds,omitempty"`
	HealthState      string         `json:"health"` // "ok" | "error" | "stalled" | "idle"
	NextExpectedTick *time.Time     `json:"nextExpectedTickAt,omitempty"`
}

type supervisedTask struct {
	name     string
	interval time.Duration
	fn       TaskFunc
	runNow   bool // if true, run once before waiting for the first tick

	// Counters (atomic).
	runs         atomic.Uint64
	errors       atomic.Uint64
	panics       atomic.Uint64
	stalls       atomic.Uint64
	restarts     atomic.Uint64
	skippedTicks atomic.Uint64

	mu                sync.RWMutex
	registeredAt      time.Time // set on Register; used for stall detection before first tick
	lastTickAt        time.Time
	lastOKAt          time.Time
	lastErrorAt       time.Time
	lastErrorText     string
	lastPanicAt       time.Time
	lastPanicStack    string
	lastTickDuration  time.Duration
	maxTickDuration   time.Duration
	stopped           bool
	stalled           bool // edge state — true while in stalled window
	panicWindowStart  time.Time
	panicWindowCount  int
	skipTicksRemain   int

	cancel context.CancelFunc
}

// TaskSupervisor registers, runs, and observes recurring tasks.
type TaskSupervisor struct {
	mu             sync.RWMutex
	tasks          map[string]*supervisedTask
	ctx            context.Context
	cancel         context.CancelFunc
	watchdogPeriod time.Duration

	// Beacon — optional sidecar file path. When set, the watchdog
	// loop writes a short JSON payload here every tick *iff* no
	// supervised task is in "stalled" state. Idle/ok/error states
	// do not block the beacon — only stalls (goroutine is stuck or
	// blocked beyond the 10× threshold). An external watchdog unit
	// reads this file to decide "is the agent alive".
	beaconPath string

	// Test / injection hooks. Unset in prod.
	nowFn     func() time.Time
	notifyFn  func(title, body string)
	onStalled func(name string)
	onRecover func(name string)
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewTaskSupervisor wires a supervisor to a parent context; cancelling it
// stops every registered task cleanly.
func NewTaskSupervisor(parent context.Context) *TaskSupervisor {
	ctx, cancel := context.WithCancel(parent)
	s := &TaskSupervisor{
		tasks:          make(map[string]*supervisedTask),
		ctx:            ctx,
		cancel:         cancel,
		watchdogPeriod: 30 * time.Second,
		nowFn:          time.Now,
	}
	// Default: log-only on stalls. Desktop notifications are opt-in
	// (YAVER_SUPERVISOR_DESKTOP_NOTIFY=1) because mature self-hosted
	// agents (Syncthing, Tailscale, Home Assistant) surface internal
	// stalls via status endpoints rather than OS popups — popups
	// train users to mute them. The /self-check endpoint + loud logs
	// are the primary signal.
	if v := os.Getenv("YAVER_SUPERVISOR_DESKTOP_NOTIFY"); v == "1" || v == "true" {
		s.notifyFn = defaultDesktopNotify
	}
	return s
}

// Register adds a task. Safe to call before or after Start(); tasks added
// after Start() begin running immediately on their own goroutine.
//
//   name     — unique label. Re-registering with the same name replaces
//              the previous task (useful during `yaver reload` flows).
//   interval — how often the task's fn runs. Minimum 10ms; lower
//              values are clamped silently. The real floor is set by
//              scheduler latency anyway.
//   fn       — the task body. Should respect ctx for cancellation.
//   runNow   — if true, fire fn immediately before waiting for the first
//              tick. Matches what several existing tickers already do
//              (heartbeat, state sync).
func (s *TaskSupervisor) Register(name string, interval time.Duration, runNow bool, fn TaskFunc) {
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	s.mu.Lock()
	if existing, ok := s.tasks[name]; ok && existing.cancel != nil {
		existing.cancel()
		existing.mu.Lock()
		existing.stopped = true
		existing.mu.Unlock()
	}
	t := &supervisedTask{
		name:         name,
		interval:     interval,
		fn:           fn,
		runNow:       runNow,
		registeredAt: s.nowFn(),
	}
	s.tasks[name] = t
	s.mu.Unlock()

	if s.ctx.Err() == nil {
		// Only spawn if the supervisor hasn't been shut down.
		s.launch(t)
	}
}

// Start the watchdog. Idempotent. Registered tasks are launched eagerly
// by Register, so Start only spins up the stall-detection loop.
func (s *TaskSupervisor) Start() {
	s.startOnce.Do(func() {
		go s.watchdogLoop()
	})
}

// Stop cancels the supervisor context. All tasks observe ctx.Done() and
// exit on their next tick boundary. Safe to call multiple times.
func (s *TaskSupervisor) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
	})
}

// Snapshot returns the current status of every registered task. Safe to
// call concurrently with Register / task execution.
func (s *TaskSupervisor) Snapshot() []TickerHealth {
	s.mu.RLock()
	names := make([]string, 0, len(s.tasks))
	for n := range s.tasks {
		names = append(names, n)
	}
	s.mu.RUnlock()
	sort.Strings(names)

	now := s.nowFn()
	out := make([]TickerHealth, 0, len(names))
	for _, n := range names {
		s.mu.RLock()
		t, ok := s.tasks[n]
		s.mu.RUnlock()
		if !ok {
			continue
		}
		out = append(out, t.statusAt(now))
	}
	return out
}

// Unhealthy returns only tasks in "stalled" or "error" state. Useful
// shortcut for CLI / MCP surfaces that only want to warn when something
// is wrong.
func (s *TaskSupervisor) Unhealthy() []TickerHealth {
	full := s.Snapshot()
	out := make([]TickerHealth, 0, len(full))
	for _, t := range full {
		if t.HealthState == "stalled" || t.HealthState == "error" {
			out = append(out, t)
		}
	}
	return out
}

// ── internals ────────────────────────────────────────────────────────

func (s *TaskSupervisor) launch(t *supervisedTask) {
	taskCtx, cancel := context.WithCancel(s.ctx)
	t.mu.Lock()
	t.cancel = cancel
	t.stopped = false
	t.mu.Unlock()

	go s.runLoop(taskCtx, t)
}

func (s *TaskSupervisor) runLoop(ctx context.Context, t *supervisedTask) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	if t.runNow {
		s.runOnce(ctx, t)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx, t)
		}
	}
}

func (s *TaskSupervisor) runOnce(ctx context.Context, t *supervisedTask) {
	// Panic backoff: if this task has been repeatedly panicking, skip
	// a few ticks before trying again. Avoids CPU-burning restart
	// storms on a deterministic panic (OTP `max_restarts` lesson).
	t.mu.Lock()
	if t.skipTicksRemain > 0 {
		t.skipTicksRemain--
		t.skippedTicks.Add(1)
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()

	start := s.nowFn()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				t.panics.Add(1)
				now := s.nowFn()
				t.mu.Lock()
				t.lastPanicAt = now
				t.lastPanicStack = stack
				// Rolling window: N panics within panicBackoffWindow
				// triggers a short skip. Reset window on each escape.
				if t.panicWindowStart.IsZero() || now.Sub(t.panicWindowStart) > panicBackoffWindow {
					t.panicWindowStart = now
					t.panicWindowCount = 1
				} else {
					t.panicWindowCount++
				}
				if t.panicWindowCount >= panicBackoffMax {
					t.skipTicksRemain = panicBackoffMax
					log.Printf("[supervisor] task %q: %d panics in %s — skipping next %d ticks",
						t.name, t.panicWindowCount, panicBackoffWindow, panicBackoffMax)
				}
				t.mu.Unlock()
				log.Printf("[supervisor] task %q panicked: %v\n%s", t.name, r, stack)
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		err = t.fn(ctx)
	}()

	now := s.nowFn()
	dur := now.Sub(start)
	t.runs.Add(1)
	t.mu.Lock()
	t.lastTickAt = now
	t.lastTickDuration = dur
	if dur > t.maxTickDuration {
		t.maxTickDuration = dur
	}
	if err == nil {
		t.lastOKAt = now
		t.lastErrorText = ""
	} else {
		t.errors.Add(1)
		t.lastErrorAt = now
		t.lastErrorText = err.Error()
	}
	t.mu.Unlock()

	// time.Ticker's channel buffer is 1: a tick body exceeding the
	// interval silently coalesces the next fire. Warn at half-interval
	// so this surfaces before the loss actually starts.
	if t.interval > 0 && float64(dur)/float64(t.interval) > slowTickWarnRatio {
		log.Printf("[supervisor] slow tick: task %q took %s (interval %s) — coalesce risk",
			t.name, dur.Round(time.Millisecond), t.interval)
	}
}

func (s *TaskSupervisor) watchdogLoop() {
	ticker := time.NewTicker(s.watchdogPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.watchdogCheck()
		}
	}
}

func (s *TaskSupervisor) watchdogCheck() {
	now := s.nowFn()
	s.mu.RLock()
	tasks := make([]*supervisedTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()

	for _, t := range tasks {
		t.mu.RLock()
		last := t.lastTickAt
		registered := t.registeredAt
		stopped := t.stopped
		wasStalled := t.stalled
		t.mu.RUnlock()
		if stopped {
			continue
		}
		// Fall back to registeredAt when the task has never ticked — a
		// task that blocks on its first run must still be flagged as
		// stalled, otherwise a hung task would hide forever behind
		// "lastTickAt is zero → skip".
		reference := last
		if reference.IsZero() {
			reference = registered
		}
		if reference.IsZero() {
			continue
		}
		stalledFor := now.Sub(reference)
		threshold := time.Duration(stallMultiplier) * t.interval
		stalledNow := stalledFor > threshold

		if stalledNow && !wasStalled {
			t.stalls.Add(1)
			t.mu.Lock()
			t.stalled = true
			t.mu.Unlock()
			title := "Yaver: task stalled"
			body := fmt.Sprintf("%s has not ticked in %s (interval %s). Check `yaver self-check`.",
				t.name, stalledFor.Round(time.Second), t.interval)
			log.Printf("[supervisor] STALL %s: no tick in %s (interval %s)", t.name, stalledFor, t.interval)
			if s.onStalled != nil {
				s.onStalled(t.name)
			}
			if s.notifyFn != nil {
				s.notifyFn(title, body)
			}
		} else if !stalledNow && wasStalled {
			t.mu.Lock()
			t.stalled = false
			t.mu.Unlock()
			log.Printf("[supervisor] RECOVERED %s", t.name)
			if s.onRecover != nil {
				s.onRecover(t.name)
			}
		}
	}

	// After classifying every task, refresh the beacon iff nothing is
	// currently stalled. The external watchdog polls this file to
	// decide "is the agent alive" without having to hit the HTTP port.
	// Intentionally tolerant: "idle" (never ticked yet) and "error"
	// (transient network failures in convex_state_sync etc.) do NOT
	// block the beacon — only stalls do.
	s.maybeWriteBeacon(now)
}

// SetBeaconPath wires a sidecar "last-healthy" file the supervisor
// refreshes on each watchdog tick when no task is stalled. Called
// once at serve startup; passing "" disables the feature.
func (s *TaskSupervisor) SetBeaconPath(path string) {
	s.mu.Lock()
	s.beaconPath = path
	s.mu.Unlock()
}

func (s *TaskSupervisor) maybeWriteBeacon(now time.Time) {
	s.mu.RLock()
	path := s.beaconPath
	tasks := make([]*supervisedTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()
	if path == "" {
		return
	}

	// Count stalls + gather minimal health summary for the file body.
	stalled := 0
	errored := 0
	for _, t := range tasks {
		t.mu.RLock()
		if t.stalled {
			stalled++
		}
		if t.lastErrorText != "" && t.lastOKAt.Before(t.lastErrorAt) {
			errored++
		}
		t.mu.RUnlock()
	}
	if stalled > 0 {
		// Block the beacon refresh. External watchdog will see a stale
		// timestamp and escalate.
		return
	}

	body := fmt.Sprintf(`{"ok":true,"ts":%q,"tasks":%d,"errored":%d}`+"\n",
		now.UTC().Format(time.RFC3339), len(tasks), errored)

	// Write atomically — temp + rename — so a watchdog racing the
	// write never sees a half-written file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		log.Printf("[supervisor] beacon write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[supervisor] beacon rename failed: %v", err)
		_ = os.Remove(tmp)
	}
}

func (t *supervisedTask) statusAt(now time.Time) TickerHealth {
	t.mu.RLock()
	defer t.mu.RUnlock()

	st := TickerHealth{
		Name:            t.name,
		Interval:        t.interval,
		IntervalSeconds: int64(t.interval.Seconds()),
		Registered:      true,
		Stopped:         t.stopped,
		Runs:            t.runs.Load(),
		Errors:          t.errors.Load(),
		Panics:          t.panics.Load(),
		Stalls:          t.stalls.Load(),
		Restarts:        t.restarts.Load(),
	}
	if !t.lastTickAt.IsZero() {
		v := t.lastTickAt
		st.LastTickAt = &v
		next := v.Add(t.interval)
		st.NextExpectedTick = &next
	}
	if !t.lastOKAt.IsZero() {
		v := t.lastOKAt
		st.LastOKAt = &v
	}
	if !t.lastErrorAt.IsZero() {
		v := t.lastErrorAt
		st.LastErrorAt = &v
		st.LastErrorText = t.lastErrorText
	}
	if !t.lastPanicAt.IsZero() {
		v := t.lastPanicAt
		st.LastPanicAt = &v
		st.LastPanicStack = t.lastPanicStack
	}

	if t.lastTickDuration > 0 {
		st.LastTickDuration = t.lastTickDuration.Round(time.Millisecond).String()
	}
	if t.maxTickDuration > 0 {
		st.MaxTickDuration = t.maxTickDuration.Round(time.Millisecond).String()
	}
	st.SkippedTicks = t.skippedTicks.Load()

	if t.stopped {
		st.HealthState = "stopped"
	} else {
		// Stall check: use lastTickAt if it exists, else registeredAt
		// (a task that blocks on its first run still needs to be
		// surfaced as stalled). Only fully unscheduled tasks are idle.
		reference := t.lastTickAt
		if reference.IsZero() {
			reference = t.registeredAt
		}
		if reference.IsZero() {
			st.HealthState = "idle"
		} else {
			stalledFor := now.Sub(reference)
			threshold := time.Duration(stallMultiplier) * t.interval
			switch {
			case stalledFor > threshold:
				st.HealthState = "stalled"
				st.StalledFor = &stalledFor
				st.StalledForSec = int64(stalledFor.Seconds())
			case t.lastTickAt.IsZero():
				st.HealthState = "idle"
			case t.lastErrorText != "" && t.lastOKAt.Before(t.lastErrorAt):
				st.HealthState = "error"
			default:
				st.HealthState = "ok"
			}
		}
	}
	return st
}

// ── global singleton ─────────────────────────────────────────────────
//
// Most callers want one supervisor per `yaver serve`, started once at
// boot. We wire that up here so code paths that register tasks (heartbeat,
// state sync, scheduler, …) don't each need to thread a supervisor
// reference through every constructor.

var (
	globalSupervisor   *TaskSupervisor
	globalSupervisorMu sync.Mutex
)

// supervisor returns the process-global supervisor, lazily initialising
// it against context.Background() if the caller hasn't set one up. In
// practice main.go calls initSupervisor(ctx) during startup so the real
// context gets wired in.
func supervisor() *TaskSupervisor {
	globalSupervisorMu.Lock()
	defer globalSupervisorMu.Unlock()
	if globalSupervisor == nil {
		globalSupervisor = NewTaskSupervisor(context.Background())
		globalSupervisor.Start()
	}
	return globalSupervisor
}

// initSupervisor replaces the global supervisor with one bound to ctx.
// Called once from main() during serve startup. Idempotent.
func initSupervisor(ctx context.Context) *TaskSupervisor {
	globalSupervisorMu.Lock()
	defer globalSupervisorMu.Unlock()
	if globalSupervisor != nil {
		globalSupervisor.Stop()
	}
	globalSupervisor = NewTaskSupervisor(ctx)
	globalSupervisor.Start()
	return globalSupervisor
}

// SupervisedGo is the thin replacement for the bare
// `go func(){ ticker := time.NewTicker(d); for { ... } }()` pattern.
// Kept package-private by choice: every call site is in desktop/agent/.
//
// Example (convex_state_sync.go):
//     SupervisedGo("convex-state-sync", 60*time.Second, true,
//         func(ctx context.Context) error {
//             globalConvexSync.syncAll(ctx)
//             return nil
//         })
func SupervisedGo(name string, interval time.Duration, runNow bool, fn TaskFunc) {
	supervisor().Register(name, interval, runNow, fn)
}

// ── Desktop notification ─────────────────────────────────────────────

// defaultDesktopNotify fires a best-effort desktop notification on
// macOS (osascript) or Linux (notify-send). Windows: no-op — the agent
// already logs stalls and surfaces them via /self-check, which is the
// channel we care about there. Never blocks > 2s.
func defaultDesktopNotify(title, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		_ = exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		_ = exec.CommandContext(ctx, "notify-send", title, body).Run()
	}
}
