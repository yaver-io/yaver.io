package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type autorunSession struct {
	ID string `json:"id"`
	// Slot is the run's stable address (task:seat) — see autorunSlotKey. ID
	// identifies THIS run; Slot identifies the agent across runs.
	Slot         string    `json:"slot"`
	Task         string    `json:"task"`
	Runner       string    `json:"runner"`
	MaxIters     int       `json:"maxIters"`
	WorkDir      string    `json:"workDir"`
	ProgressPath string    `json:"progressPath"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt,omitempty"`
	Error        string    `json:"error,omitempty"`
	// LandingError is set when the WORK succeeded and only the bookkeeping —
	// the final commit, its push, the merge onto main — failed. Such a run is
	// `completed`: its iterations ran and its commits exist. Kept separate from
	// Error so no surface has to guess which half of a run went wrong.
	LandingError string `json:"landingError,omitempty"`
	// Scopes is the run's declared allowlist, retained so a STARTING run can ask
	// whether it would collide with this one before spending a turn. Without it
	// admission has nothing to compare and every run looks independent — which
	// is how six runs died on scope violation in one night, each having done
	// real work that was then stashed. See autorun_coordination.go.
	Scopes  []string `json:"scopes,omitempty"`
	Summary autorunRunSummary
	cancel  context.CancelFunc
}

type autorunSessionView struct {
	ID string `json:"id"`
	// Slot is the agent's stable address (task:seat). A UI pins its fixed slots
	// to this, never to ID (new every run) or to list position (moves whenever
	// any sibling changes).
	Slot         string    `json:"slot"`
	Task         string    `json:"task"`
	Runner       string    `json:"runner"`
	MaxIters     int       `json:"maxIters"`
	WorkDir      string    `json:"workDir"`
	ProgressPath string    `json:"progressPath"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt,omitempty"`
	Error        string    `json:"error,omitempty"`
	// LandingError: the work succeeded, only the bookkeeping failed to land. A
	// surface must not paint such a run as a failure — see autorunSession.
	LandingError string `json:"landingError,omitempty"`
	ProgressTail string `json:"progressTail,omitempty"`
	Iterations   int    `json:"iterations"`
	Commits      int    `json:"commits"`
	FinishReason string `json:"finishReason,omitempty"`
	// FinalCommit is the SHA of the run's explicitly-marked final commit.
	// While it is empty the run has not ended, however quiet it looks.
	FinalCommit        string `json:"finalCommit,omitempty"`
	FinalCommitSubject string `json:"finalCommitSubject,omitempty"`
	// ActiveRunner is the runner currently driving the loop — it changes when a
	// failover heals a dead runner, so it is not always the one that was asked for.
	ActiveRunner string `json:"activeRunner,omitempty"`
	// Master is the planning seat, empty on a single-runner loop. Present so a
	// reader can tell a two-seat run from a one-seat run without reading the
	// progress file — the seats fail for different reasons.
	Master string `json:"master,omitempty"`
	// TmuxSession is the tmux session driving this loop (Epic 7 observability):
	// lets a surface label the run and lets the user `tmux attach -t <name>` from
	// a terminal. Deterministic from task+runner (autorunTmuxSessionName).
	TmuxSession string             `json:"tmuxSession,omitempty"`
	Heals       []autorunHealEvent `json:"heals,omitempty"`
	Resources   autorunResources   `json:"resources"`
	// Parked is true while the loop is held at the freeze gate for a deploy.
	// It is deliberately NOT a Status value: the run is still `running` and
	// still counts as live, so a client filtering on status keeps seeing it.
	// Parked answers a different question — "has it stopped touching the repo
	// yet?" A running loop that is not parked during a freeze is still
	// mid-iteration and may yet commit. See autorunGate.
	Parked bool `json:"parked,omitempty"`
	// Landing answers "did the work actually get out?" — commits/finalCommit
	// only prove the loop wrote something locally. Populated by the status
	// handler from git, not from the loop's own bookkeeping. See
	// autorunLandingSnapshot (ops_git_land.go).
	Landing *autorunLandingState `json:"landing,omitempty"`
}

type autorunSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*autorunSession
}

var autorunSessions = &autorunSessionManager{sessions: make(map[string]*autorunSession)}

func autorunSessionContext(requestContext context.Context) (context.Context, context.CancelFunc) {
	// An autorun session is daemon-owned and must survive the MCP/ops request
	// that created it. Preserve request-scoped values for tracing while making
	// explicit autorun_stop cancellation the session's lifetime boundary.
	return context.WithCancel(context.WithoutCancel(requestContext))
}

func (m *autorunSessionManager) start(parent context.Context, opts autorunOptions) (autorunSessionView, error) {
	taskPath, err := filepath.Abs(opts.TaskPath)
	if err != nil {
		return autorunSessionView{}, err
	}
	if opts.WorkDir, err = filepath.Abs(opts.WorkDir); err != nil {
		return autorunSessionView{}, err
	}
	if _, err = os.Stat(taskPath); err != nil {
		return autorunSessionView{}, fmt.Errorf("task: %w", err)
	}
	seat := autorunRequestedDoer(taskPath, opts.Runner)
	workspace, err := autorunWorkspaceFor(taskPath, opts.WorkDir, seat)
	if err != nil {
		return autorunSessionView{}, err
	}
	id := fmt.Sprintf("autorun-%d", time.Now().UTC().UnixNano())
	// The loop parks under the session's own ID, so a caller can cross-reference
	// the freeze gate's parked list against autorun_status without a second map.
	opts.SessionID = id
	ctx, cancel := autorunSessionContext(parent)
	s := &autorunSession{
		ID: id, Slot: workspace.Slot, Task: taskPath, Runner: opts.Runner, WorkDir: workspace.WorkDir,
		MaxIters: opts.MaxIters, ProgressPath: workspace.ProgressPath, Status: "running", Scopes: opts.Scopes,
		StartedAt: time.Now().UTC(), cancel: cancel,
	}
	// The bus channel publishes on the slot topic, so the loop needs its slot.
	// RunID is deliberately not carried: it held the same `id` as SessionID (set
	// just above), and one identity under two names is how two subsystems begin
	// to disagree about which run they mean.
	opts.Slot = workspace.Slot
	// Admission BEFORE the goroutine. Checked and inserted under one lock so two
	// simultaneous starts cannot both observe a free slot and both proceed —
	// the check is worthless if it is not atomic with the claim.
	//
	// Refusing here is the whole point: the alternative is not "no conflict", it
	// is a conflict discovered after a runner turn has been spent, when the
	// loser's iteration gets stashed and thrown away.
	areas := autorunOwnedAreas(opts.Scopes)
	m.mu.Lock()
	if adm := m.admitLocked(workspace.Slot, areas); !adm.Allowed {
		m.mu.Unlock()
		cancel()
		return autorunSessionView{}, &autorunAdmissionError{admission: adm}
	}
	m.sessions[id] = s
	m.mu.Unlock()

	// Take the run's typed claims now that admission has cleared it. Source
	// areas plus the runner seat — the `edit` shape — because that is what a run
	// holds between kicks. The build target is taken later, by the phase that
	// actually compiles, and the seat is handed back there so a sibling can
	// think while this one builds (autorun_leases.go).
	//
	// Best-effort: admission already proved there is no conflict, so a refusal
	// here would mean the two models disagree — worth a log line, never worth
	// refusing a run admission just approved.
	//
	// Through the FLEET coordinator, not the local singleton. Taking these
	// locally only was the gap between cross-machine exclusion existing and
	// being in effect: two boxes could each be locally certain they held
	// build/ios, because their local managers cannot see each other.
	fleet := autorunFleetLeases(ctx, workspace.WorkDir)
	for _, k := range autorunPhaseLeases("edit", opts.Runner, areas, nil, "main") {
		if r := fleet.Acquire(ctx, id, workspace.Slot, "edit", k); !r.OK {
			log.Printf("[autorun] %s could not claim %s (%s tier): %v", id, k, r.Tier, r.Conflict)
		} else if r.Degraded {
			// Local-only: real on this box, unverified across the fleet. Worth
			// a line so a cross-machine collision later is explicable.
			log.Printf("[autorun] %s holds %s locally; fleet tier unavailable", id, k)
		}
	}
	go func() {
		summary, err := executeAutorun(ctx, opts)
		m.mu.Lock()
		s.FinishedAt = time.Now().UTC()
		s.cancel = nil
		s.Status = "completed"
		s.Summary = summary
		if err != nil {
			s.Error = err.Error()
			var landing *autorunLandingError
			switch {
			case ctx.Err() != nil:
				s.Status = "stopped"
			case errors.As(err, &landing) && autorunWorkSucceeded(summary.FinishReason):
				// The loop did its job and only the bookkeeping failed to land.
				// Calling that `failed` is how a converged 3-iteration run got
				// recorded as a failure for losing a push race — the work is real,
				// the commits exist, and the run must say so. The landing failure
				// is still reported, just not as the run's verdict.
				s.Status = "completed"
				s.LandingError = err.Error()
			default:
				s.Status = "failed"
			}
		}
		// Snapshot under the lock, notify outside it: this is the only place
		// that knows a run ended, and onAutorunFinished must not run while we
		// hold the manager's write lock.
		finished := *s
		m.mu.Unlock()
		// Drop every claim the moment the run ends, however it ended. TTL is the
		// backstop for an agent that dies without reaching this line; it is not
		// the mechanism. A run that finishes and keeps its leases blocks every
		// sibling for 45 minutes for no reason.
		autorunFleetLeases(context.WithoutCancel(ctx), finished.WorkDir).ReleaseAll(
			context.WithoutCancel(ctx), finished.ID)
		onAutorunFinished(&finished)
	}()
	return m.view(s), nil
}

func (m *autorunSessionManager) view(s *autorunSession) autorunSessionView {
	v := autorunSessionView{ID: s.ID, Slot: s.Slot, Task: s.Task, Runner: s.Runner, MaxIters: s.MaxIters, WorkDir: s.WorkDir, ProgressPath: s.ProgressPath, Status: s.Status, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt, Error: s.Error, LandingError: s.LandingError,
		Iterations: s.Summary.Iterations, Commits: s.Summary.Commits, FinishReason: s.Summary.FinishReason,
		FinalCommit: s.Summary.FinalCommit, FinalCommitSubject: s.Summary.FinalSubject,
		ActiveRunner: s.Summary.Runner, Master: s.Summary.Master, Heals: s.Summary.Heals, Resources: s.Summary.Resources,
		TmuxSession: autorunTmuxSessionName(s.Task, s.Runner),
		Parked:      autorunFreeze.isParked(s.ID)}
	if b, err := os.ReadFile(s.ProgressPath); err == nil {
		const maxTail = 16 * 1024
		if len(b) > maxTail {
			b = b[len(b)-maxTail:]
		}
		v.ProgressTail = string(b)
	}
	return v
}

func (m *autorunSessionManager) status(id string) ([]autorunSessionView, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if id != "" {
		s, ok := m.sessions[id]
		if !ok {
			return nil, fmt.Errorf("autorun session %q not found", id)
		}
		return []autorunSessionView{m.view(s)}, nil
	}
	views := make([]autorunSessionView, 0, len(m.sessions))
	for _, s := range m.sessions {
		views = append(views, m.view(s))
	}
	sortAutorunViewsBySlot(views)
	return views, nil
}

func (m *autorunSessionManager) refreshViews() []autorunRefreshView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	views := make([]autorunRefreshView, 0, len(m.sessions))
	for _, s := range m.sessions {
		views = append(views, autorunRefreshView{
			ID:           s.ID,
			Slot:         s.Slot,
			Task:         s.Task,
			Runner:       s.Runner,
			MaxIters:     s.MaxIters,
			Status:       s.Status,
			Iterations:   s.Summary.Iterations,
			Commits:      s.Summary.Commits,
			FinishReason: s.Summary.FinishReason,
			ActiveRunner: s.Summary.Runner,
			Master:       s.Summary.Master,
			Heals:        make([]struct{}, len(s.Summary.Heals)),
		})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Slot != views[j].Slot {
			return views[i].Slot < views[j].Slot
		}
		return views[i].ID < views[j].ID
	})
	return views
}

// sortAutorunViewsBySlot orders sessions by their stable address, NOT by recency.
//
// Recency order (the previous behavior) means an agent's position is a function
// of time: any session starting or finishing renumbers every row, so a client
// cannot give an agent a fixed home and a human cannot build muscle memory. Slot
// order changes only when the SET of agents changes — a status flip never moves
// anything. The tiebreak on ID keeps two runs of the same slot deterministic
// rather than dependent on Go's random map iteration, which would otherwise let
// the list shuffle between two polls that saw identical state.
func sortAutorunViewsBySlot(views []autorunSessionView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].Slot != views[j].Slot {
			return views[i].Slot < views[j].Slot
		}
		return views[i].ID < views[j].ID
	})
}

func (m *autorunSessionManager) stop(id string) (autorunSessionView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return autorunSessionView{}, fmt.Errorf("autorun session %q not found", id)
	}
	if s.cancel != nil {
		s.cancel()
		s.Status = "stopping"
	}
	return m.view(s), nil
}

// stopAll cancels every session still running. Sessions that already finished
// are skipped rather than reported as stopped, so the count is the number of
// loops this call actually halted.
func (m *autorunSessionManager) stopAll() []autorunSessionView {
	m.mu.Lock()
	defer m.mu.Unlock()
	views := make([]autorunSessionView, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.cancel == nil {
			continue
		}
		s.cancel()
		s.Status = "stopping"
		views = append(views, m.view(s))
	}
	sortAutorunViewsBySlot(views)
	return views
}

type autorunStartPayload struct {
	Task     string   `json:"task"`
	Runner   string   `json:"runner"`
	Master   string   `json:"master"`
	Interval string   `json:"interval"`
	MaxIters int      `json:"maxIters"`
	Gate     string   `json:"gate"`
	Push     bool     `json:"push"`
	Scopes   []string `json:"scopes"`
	WorkDir  string   `json:"workDir"`
}

func init() {
	registerOpsVerb(opsVerbSpec{Name: "autorun_start", Description: "Start a gate-verified autorun loop and return its session ID immediately. Pass master:<runner> to split the loop across two seats: master plans each iteration and never edits, runner implements the plan. Any runner can hold either seat — the roles carry the behavior, not the runner. Omit master for a single-runner loop. The task file's front matter may name the seats itself (master:/doer:); these arguments win over it.", Schema: autorunStartSchema(), Handler: opsAutorunStartHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_status", Description: "List autorun sessions, or inspect one: progress tail, iterations, finish reason, activeRunner, self-heal events, and the final autorun commit. An empty finalCommit means the run has not finished. activeRunner differs from the requested runner after a failover. Pass machine to inspect a remote device's autoruns.", Schema: autorunIDSchema(false), Handler: opsAutorunStatusHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_stop", Description: "Cancel one running autorun session. It still records its final autorun commit.", Schema: autorunIDSchema(true), Handler: opsAutorunStopHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_stop_all", Description: "Cancel every running autorun session on a machine. Pass machine:<deviceId|alias|primary> to stop a remote device's autoruns; each stopped loop still records its final autorun commit.", Schema: autorunStopAllSchema(), Handler: opsAutorunStopAllHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_pause_all", Description: "Freeze every autorun on a machine at its iteration boundary WITHOUT ending any run — the loops park with nothing uncommitted and resume on autorun_resume_all. Use before a deploy so N loops cannot cause N deploys. Loops that start while frozen park too. Freezing is instant but draining is not: a loop already inside a runner kick takes up to 30m to reach the gate and may still commit until it does. Pass waitFor (e.g. 30m) to block until every loop parks; read drained to know whether it did. Pass machine:<deviceId|alias|primary> for a remote device.", Schema: autorunPauseAllSchema(), Handler: opsAutorunPauseAllHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_resume_all", Description: "Lift the freeze on a machine and wake every parked autorun. Loops continue from their next iteration; nothing was lost while they were held. Pass machine:<deviceId|alias|primary> for a remote device.", Schema: autorunStopAllSchema(), Handler: opsAutorunResumeAllHandler})
	registerOpsVerb(opsVerbSpec{Name: "recap_speak", Description: "Speak a TTS recap of the current autoruns via the voice_speak pipeline — the 'what happened while I was away' beach recap. Pass device:<id> to render on a specific paired surface (mobile/car/glass), machine:<deviceId|alias|primary> to recap a remote device's autoruns. Mirrors feedback_speak.", Handler: opsRecapSpeakHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_runs", Description: "Return the retained current-state view of autoruns from the local bus cache. Reads bus().Retained(\"autorun/\") first and returns immediately with per-row ageMs; refresh:true triggers best-effort live autorun_status refreshes in the background to top up a cold or stale cache without blocking the cached answer. Payload machine defaults to all and may be a specific deviceId or local.", Schema: autorunRunsSchema(), Handler: opsAutorunRunsHandler})
}

func autorunPauseAllSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"reason":  map[string]interface{}{"type": "string", "description": "Why the fleet is frozen. Written to each parked loop's progress file so the gap is explained in the run's own log."},
		"waitFor": map[string]interface{}{"type": "string", "description": "Block up to this long (e.g. 30m) for every running loop to reach the gate. Omit to return immediately with whatever has parked so far. A timeout is not an error — read drained."},
		"leaseMs": map[string]interface{}{"type": "integer", "minimum": 0, "description": "Dead-man lease in ms. The freeze thaws itself after this unless renewed, so a coordinator that dies cannot leave this machine frozen forever. 0 = no lease (only an explicit resume lifts it) — correct only for a human at a terminal on THIS machine."},
		"renew":   map[string]interface{}{"type": "boolean", "description": "Heartbeat an existing freeze's lease instead of taking a new one. Re-freezes if the lease already lapsed."},
	}, "additionalProperties": false}
}

func autorunStopAllSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}
}

func autorunStartSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "required": []string{"task", "gate", "scopes"}, "properties": map[string]interface{}{
		"task":     map[string]interface{}{"type": "string"},
		"runner":   map[string]interface{}{"type": "string", "default": "auto", "description": "The doer: the runner that edits files. Any supported runner, or auto."},
		"master":   map[string]interface{}{"type": "string", "description": "Optional planning runner. Reads the repo and writes each iteration's instruction for the doer; never edits, and must differ from runner. Any supported runner."},
		"interval": map[string]interface{}{"type": "string", "default": "5m"}, "maxIters": map[string]interface{}{"type": "integer", "minimum": 0},
		"gate": map[string]interface{}{"type": "string"}, "push": map[string]interface{}{"type": "boolean"},
		"scopes":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "minItems": 1},
		"workDir": map[string]interface{}{"type": "string"},
	}, "additionalProperties": false}
}

func autorunIDSchema(required bool) map[string]interface{} {
	s := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "additionalProperties": false}
	if required {
		s["required"] = []string{"id"}
	}
	return s
}

func autorunRunsSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"machine": map[string]interface{}{"type": "string", "description": `Device filter. "all" or empty = every cached device; "local" = this device; otherwise a specific deviceId.`},
		"refresh": map[string]interface{}{"type": "boolean", "description": "Start a best-effort live autorun_status refresh for the matching devices without blocking the cached response."},
	}, "additionalProperties": false}
}

func opsAutorunStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p autorunStartPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Task) == "" || strings.TrimSpace(p.Gate) == "" || len(p.Scopes) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "task, gate, and at least one scope are required"}
	}
	interval := 5 * time.Minute
	var err error
	if strings.TrimSpace(p.Interval) != "" {
		interval, err = time.ParseDuration(p.Interval)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid interval: " + err.Error()}
		}
	}
	workDir := strings.TrimSpace(p.WorkDir)
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return OpsResult{OK: false, Code: "autorun_failed", Error: err.Error()}
		}
	}
	runner := strings.TrimSpace(p.Runner)
	if runner == "" {
		runner = "auto"
	}
	view, err := autorunSessions.start(c.Ctx, autorunOptions{TaskPath: p.Task, Runner: runner, Master: p.Master, Interval: interval, MaxIters: p.MaxIters, Gate: p.Gate, Push: p.Push, Scopes: p.Scopes, WorkDir: workDir})
	if err != nil {
		// "Something live already owns this" is not a failure — nothing is
		// broken and the right move is to wait or pick another area. Reporting
		// it as autorun_failed would tell a caller to give up on work that is
		// merely early, and would tell a human to go debug a healthy fleet.
		var adm *autorunAdmissionError
		if errors.As(err, &adm) {
			a := adm.Admission()
			// Initial carries the holder so a caller can poll or stop the run
			// in the way without a second round trip.
			return OpsResult{OK: false, Code: a.Reason, Error: a.Detail, Initial: a}
		}
		return OpsResult{OK: false, Code: "autorun_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: view}
}

func opsAutorunStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	views, err := autorunSessions.status(strings.TrimSpace(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	// Enrich with commit/push awareness. A finished run whose commits never
	// reached the remote looks identical to a landed one on every field above,
	// so read it off git per session rather than trusting the loop's own count.
	for i := range views {
		views[i].Landing = autorunLandingSnapshot(views[i].WorkDir, "")
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sessions": views}}
}

func opsAutorunStopHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || strings.TrimSpace(p.ID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id is required"}
	}
	view, err := autorunSessions.stop(strings.TrimSpace(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: view}
}

func opsAutorunStopAllHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	stopped := autorunSessions.stopAll()
	return OpsResult{OK: true, Initial: map[string]interface{}{"stopped": stopped, "count": len(stopped)}}
}

func opsAutorunPauseAllHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Reason  string `json:"reason"`
		WaitFor string `json:"waitFor"`
		LeaseMs int64  `json:"leaseMs"`
		Renew   bool   `json:"renew"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		reason = "deploy"
	}
	lease := time.Duration(p.LeaseMs) * time.Millisecond
	// A renew from a live coordinator, not a fresh freeze. Kept on the same verb
	// so the heartbeat needs no second round-trip shape.
	if p.Renew {
		if autorunFreeze.renew(lease) {
			return OpsResult{OK: true, Initial: map[string]interface{}{"renewed": true, "drain": autorunDrain()}}
		}
		// Not frozen: the lease already expired or someone thawed. Re-freeze
		// rather than silently doing nothing — the caller believes it holds a
		// freeze and is about to deploy.
	}
	owned := autorunFreeze.pause(reason, lease)
	drain := autorunDrain()
	if w := strings.TrimSpace(p.WaitFor); w != "" {
		timeout, err := time.ParseDuration(w)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid waitFor: " + err.Error()}
		}
		drain = autorunAwaitDrain(c.Ctx, timeout)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"paused": true,
		// alreadyFrozen tells a second caller it did not create this freeze, so
		// it must not lift one it does not own — two overlapping ships would
		// otherwise thaw each other's fleet mid-deploy.
		"alreadyFrozen": !owned,
		"reason":        reason,
		"drain":         drain,
		"gate":          autorunFreeze.state(),
	}}
}

func opsAutorunResumeAllHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	lifted := autorunFreeze.resume()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"resumed": lifted,
		// wasFrozen false means there was nothing to lift. Reported rather than
		// swallowed: a resume that finds no freeze usually means someone else
		// already thawed the fleet, which the caller should know.
		"wasFrozen": lifted,
		"drain":     autorunDrain(),
	}}
}
