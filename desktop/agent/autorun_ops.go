package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	WorkDir      string    `json:"workDir"`
	ProgressPath string    `json:"progressPath"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt,omitempty"`
	Error        string    `json:"error,omitempty"`
	Summary      autorunRunSummary
	cancel       context.CancelFunc
}

type autorunSessionView struct {
	ID string `json:"id"`
	// Slot is the agent's stable address (task:seat). A UI pins its fixed slots
	// to this, never to ID (new every run) or to list position (moves whenever
	// any sibling changes).
	Slot         string    `json:"slot"`
	Task         string    `json:"task"`
	Runner       string    `json:"runner"`
	WorkDir      string    `json:"workDir"`
	ProgressPath string    `json:"progressPath"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt,omitempty"`
	Error        string    `json:"error,omitempty"`
	ProgressTail string    `json:"progressTail,omitempty"`
	Iterations   int       `json:"iterations"`
	Commits      int       `json:"commits"`
	FinishReason string    `json:"finishReason,omitempty"`
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
	Master    string             `json:"master,omitempty"`
	Heals     []autorunHealEvent `json:"heals,omitempty"`
	Resources autorunResources   `json:"resources"`
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
	ctx, cancel := autorunSessionContext(parent)
	s := &autorunSession{
		ID: id, Slot: workspace.Slot, Task: taskPath, Runner: opts.Runner, WorkDir: workspace.WorkDir,
		ProgressPath: workspace.ProgressPath, Status: "running",
		StartedAt: time.Now().UTC(), cancel: cancel,
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go func() {
		summary, err := executeAutorun(ctx, opts)
		m.mu.Lock()
		defer m.mu.Unlock()
		s.FinishedAt = time.Now().UTC()
		s.cancel = nil
		s.Status = "completed"
		s.Summary = summary
		if err != nil {
			s.Error = err.Error()
			if ctx.Err() != nil {
				s.Status = "stopped"
			} else {
				s.Status = "failed"
			}
		}
	}()
	return m.view(s), nil
}

func (m *autorunSessionManager) view(s *autorunSession) autorunSessionView {
	v := autorunSessionView{ID: s.ID, Slot: s.Slot, Task: s.Task, Runner: s.Runner, WorkDir: s.WorkDir, ProgressPath: s.ProgressPath, Status: s.Status, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt, Error: s.Error,
		Iterations: s.Summary.Iterations, Commits: s.Summary.Commits, FinishReason: s.Summary.FinishReason,
		FinalCommit: s.Summary.FinalCommit, FinalCommitSubject: s.Summary.FinalSubject,
		ActiveRunner: s.Summary.Runner, Master: s.Summary.Master, Heals: s.Summary.Heals, Resources: s.Summary.Resources}
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
