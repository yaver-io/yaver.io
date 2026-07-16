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
	ID           string    `json:"id"`
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
	ID           string    `json:"id"`
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
	id := fmt.Sprintf("autorun-%d", time.Now().UTC().UnixNano())
	ctx, cancel := autorunSessionContext(parent)
	s := &autorunSession{
		ID: id, Task: taskPath, Runner: opts.Runner, WorkDir: opts.WorkDir,
		ProgressPath: autorunProgressPath(taskPath, opts.WorkDir), Status: "running",
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
	v := autorunSessionView{ID: s.ID, Task: s.Task, Runner: s.Runner, WorkDir: s.WorkDir, ProgressPath: s.ProgressPath, Status: s.Status, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt, Error: s.Error,
		Iterations: s.Summary.Iterations, Commits: s.Summary.Commits, FinishReason: s.Summary.FinishReason,
		FinalCommit: s.Summary.FinalCommit, FinalCommitSubject: s.Summary.FinalSubject}
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
	sort.Slice(views, func(i, j int) bool { return views[i].StartedAt.After(views[j].StartedAt) })
	return views, nil
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
	sort.Slice(views, func(i, j int) bool { return views[i].StartedAt.After(views[j].StartedAt) })
	return views
}

type autorunStartPayload struct {
	Task     string   `json:"task"`
	Runner   string   `json:"runner"`
	Interval string   `json:"interval"`
	MaxIters int      `json:"maxIters"`
	Gate     string   `json:"gate"`
	Push     bool     `json:"push"`
	Scopes   []string `json:"scopes"`
	WorkDir  string   `json:"workDir"`
}

func init() {
	registerOpsVerb(opsVerbSpec{Name: "autorun_start", Description: "Start a gate-verified autorun loop and return its session ID immediately.", Schema: autorunStartSchema(), Handler: opsAutorunStartHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_status", Description: "List autorun sessions, or inspect one session: progress tail, iterations, finish reason, and the final autorun commit. An empty finalCommit means the run has not finished. Pass machine to inspect a remote device's autoruns.", Schema: autorunIDSchema(false), Handler: opsAutorunStatusHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_stop", Description: "Cancel one running autorun session. It still records its final autorun commit.", Schema: autorunIDSchema(true), Handler: opsAutorunStopHandler})
	registerOpsVerb(opsVerbSpec{Name: "autorun_stop_all", Description: "Cancel every running autorun session on a machine. Pass machine:<deviceId|alias|primary> to stop a remote device's autoruns; each stopped loop still records its final autorun commit.", Schema: autorunStopAllSchema(), Handler: opsAutorunStopAllHandler})
}

func autorunStopAllSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}
}

func autorunStartSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "required": []string{"task", "gate", "scopes"}, "properties": map[string]interface{}{
		"task": map[string]interface{}{"type": "string"}, "runner": map[string]interface{}{"type": "string", "default": "auto"},
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
	view, err := autorunSessions.start(c.Ctx, autorunOptions{TaskPath: p.Task, Runner: runner, Interval: interval, MaxIters: p.MaxIters, Gate: p.Gate, Push: p.Push, Scopes: p.Scopes, WorkDir: workDir})
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
