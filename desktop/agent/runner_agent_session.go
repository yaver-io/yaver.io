package main

// runner_agent_session.go — Devin-shape coding-agent sessions
// (RUNNER_DEV.md Phase 2). A session is a long-lived conversation
// with a coding agent — initial prompt + zero or more follow-up
// messages — that the user can poll or extend asynchronously.
//
// Implementation: each session message spawns an independent
// TaskManager task. Sequencing is enforced at this layer — Message
// refuses while the previous task is still running so two
// follow-ups can't race. The runner re-establishes context on each
// turn from the prompt body (which embeds the session's history),
// so the runner-side state can be ephemeral. We talk to TaskManager
// only through its public CreateTask / GetTask / StopTask methods.
//
// Persistence: ~/.yaver/runner/agent-sessions.json. Captures session
// metadata + message text + chained task IDs. Deliberately *not*
// persisting per-message output — that's already in the TaskManager
// and lives on disk under tasks.json. Output gets re-fetched via
// the public TaskManager.GetTask whenever a client reads a session.
//
// Privacy contract: per-message text + workdir stay on the host.
// The Convex sync path never sees agent_session_text /
// agent_session_workdir / agent_session_message — those keys are
// added to convex_privacy_test.go's forbidden list.

import (
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

// AgentSessionStatus is the lifecycle marker used by the UI.
type AgentSessionStatus string

const (
	AgentSessionPending   AgentSessionStatus = "pending"
	AgentSessionRunning   AgentSessionStatus = "running"
	AgentSessionAwaiting  AgentSessionStatus = "awaiting_input"
	AgentSessionDone      AgentSessionStatus = "done"
	AgentSessionFailed    AgentSessionStatus = "failed"
	AgentSessionCancelled AgentSessionStatus = "cancelled"
)

// AgentSessionMessage is one turn in a session conversation.
// Direction is "user" for caller-supplied text and "agent" for
// runner output. The agent direction's TaskID points back to the
// underlying TaskManager task.
type AgentSessionMessage struct {
	Direction string  `json:"direction"`        // "user" | "agent"
	Text      string  `json:"text"`             // user prompt text or agent result text
	TaskID    string  `json:"taskId,omitempty"` // TaskManager task ID for agent turns
	CreatedAt int64   `json:"createdAt"`        // unix millis
	CostUSD   float64 `json:"costUsd,omitempty"`
}

// AgentSession is one Devin-shape session. Stored verbatim in
// agent-sessions.json. ChainID is the linkage TaskManager uses to
// run tasks sequentially (defined in tasks.go::Task.ChainID).
type AgentSession struct {
	ID          string                `json:"id"`
	Title       string                `json:"title,omitempty"`
	WorkDir     string                `json:"workDir,omitempty"`
	Runner      string                `json:"runner"`           // claude-code | codex | aider | aider-ollama | hybrid
	Engine      string                `json:"engine,omitempty"` // claude | hybrid | runner alias
	Model       string                `json:"model,omitempty"`
	Project     string                `json:"project,omitempty"`
	OwnerUserID string                `json:"ownerUserID,omitempty"`
	ChainID     string                `json:"chainId,omitempty"`
	Status      AgentSessionStatus    `json:"status"`
	CurrentTask string                `json:"currentTaskId,omitempty"`
	Messages    []AgentSessionMessage `json:"messages,omitempty"`
	CreatedAt   int64                 `json:"createdAt"`
	UpdatedAt   int64                 `json:"updatedAt"`
}

// AgentSessionStartOpts feeds Create.
type AgentSessionStartOpts struct {
	Title   string `json:"title,omitempty"`
	WorkDir string `json:"workDir,omitempty"`
	Runner  string `json:"runner,omitempty"`
	Engine  string `json:"engine,omitempty"`
	Model   string `json:"model,omitempty"`
	Project string `json:"project,omitempty"`
	Prompt  string `json:"prompt"`
}

// AgentSessionManager owns the session map and persistence file.
// Wraps a TaskManager pointer for the actual runner spawns.
type AgentSessionManager struct {
	mu        sync.Mutex
	sessions  map[string]*AgentSession
	taskMgr   *TaskManager
	storePath string
}

// NewAgentSessionManager opens the on-disk store and rehydrates any
// sessions left behind by a previous agent run. Statuses for
// in-flight sessions are downgraded to "awaiting_input" — the
// underlying TaskManager has its own crash recovery; we just need
// to surface a sane state to the UI.
func NewAgentSessionManager(tm *TaskManager) *AgentSessionManager {
	m := &AgentSessionManager{
		sessions: map[string]*AgentSession{},
		taskMgr:  tm,
	}
	if dir, err := ConfigDir(); err == nil {
		runnerDir := filepath.Join(dir, "runner")
		if err := os.MkdirAll(runnerDir, 0700); err != nil {
			log.Printf("[agent-session] mkdir %s failed: %v — disk persistence disabled", runnerDir, err)
		} else {
			m.storePath = filepath.Join(runnerDir, "agent-sessions.json")
		}
	}
	m.load()
	// Crash recovery: any session that was running is now suspended
	// — the TaskManager's crash-recovery path will mark its task
	// failed independently.
	m.mu.Lock()
	for _, s := range m.sessions {
		if s.Status == AgentSessionRunning || s.Status == AgentSessionPending {
			s.Status = AgentSessionAwaiting
		}
	}
	m.mu.Unlock()
	return m
}

func (m *AgentSessionManager) load() {
	if m.storePath == "" {
		return
	}
	data, err := os.ReadFile(m.storePath)
	if err != nil {
		return
	}
	var ss map[string]*AgentSession
	if err := json.Unmarshal(data, &ss); err != nil {
		log.Printf("[agent-session] failed to parse %s: %v — starting empty", m.storePath, err)
		return
	}
	m.sessions = ss
}

func (m *AgentSessionManager) saveLocked() {
	if m.storePath == "" {
		return
	}
	data, err := json.MarshalIndent(m.sessions, "", "  ")
	if err != nil {
		return
	}
	tmp := m.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.storePath)
}

// resolveRunner picks the runner ID to spawn given Engine + Runner
// hints. Same precedence as autodev: explicit runner wins, else
// engine maps to a runner ID, else default to claude-code.
func resolveAgentSessionRunner(engine, runner string) string {
	r := strings.TrimSpace(runner)
	if r != "" {
		return r
	}
	e := strings.ToLower(strings.TrimSpace(engine))
	switch e {
	case "claude", "claude-code", "":
		return "claude-code"
	case "codex":
		return "codex"
	case "aider":
		return "aider"
	case "hybrid":
		return "hybrid"
	default:
		return e
	}
}

// Create starts a new session and spawns the first TaskManager task
// for the initial prompt. The session ID is independent from the
// task ID — clients should hold the session ID and use Get to
// retrieve the current task's status.
func (m *AgentSessionManager) Create(opts AgentSessionStartOpts, ownerUserID string) (*AgentSession, error) {
	if m == nil || m.taskMgr == nil {
		return nil, errors.New("agent session manager unavailable — TaskManager not wired")
	}
	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	runner := resolveAgentSessionRunner(opts.Engine, opts.Runner)
	now := time.Now().UnixMilli()
	id := "agentsess-" + NewRunnerRunID()
	chainID := "chain-" + NewRunnerRunID()

	title := opts.Title
	if title == "" {
		title = firstLine(prompt)
	}

	sess := &AgentSession{
		ID:          id,
		Title:       title,
		WorkDir:     opts.WorkDir,
		Runner:      runner,
		Engine:      opts.Engine,
		Model:       opts.Model,
		Project:     opts.Project,
		OwnerUserID: ownerUserID,
		ChainID:     chainID,
		Status:      AgentSessionPending,
		CreatedAt:   now,
		UpdatedAt:   now,
		Messages: []AgentSessionMessage{{
			Direction: "user",
			Text:      prompt,
			CreatedAt: now,
		}},
	}

	taskTitle := title
	taskDescription := composeAgentSessionPrompt(sess, prompt)
	task, err := m.taskMgr.CreateTaskWithOptions(taskTitle, taskDescription, opts.Model, "agent-session", runner, "", nil, TaskCreateOptions{
		WorkDir: opts.WorkDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	sess.CurrentTask = task.ID
	sess.Status = AgentSessionRunning
	sess.Messages = append(sess.Messages, AgentSessionMessage{
		Direction: "agent",
		Text:      "",
		TaskID:    task.ID,
		CreatedAt: now,
	})

	m.mu.Lock()
	m.sessions[id] = sess
	m.saveLocked()
	m.mu.Unlock()
	cp := *sess
	return &cp, nil
}

// Get returns one session, refreshing the latest agent-turn entry
// from TaskManager so a poll-based client sees the live cost / output.
// The bool is false when the id is unknown (or the caller's
// requireOwner doesn't match — same hide-from-other-users trick).
func (m *AgentSessionManager) Get(id, requireOwner string) (*AgentSession, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if requireOwner != "" && s.OwnerUserID != requireOwner {
		return nil, false
	}
	m.refreshLocked(s)
	cp := *s
	return &cp, true
}

// List returns every session sorted by CreatedAt desc. Owner filter
// matches Get.
func (m *AgentSessionManager) List(requireOwner string) []AgentSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		if requireOwner != "" && s.OwnerUserID != requireOwner {
			continue
		}
		m.refreshLocked(s)
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Message appends a follow-up user prompt and spawns the next chain
// task. Refuses if the previous task is still running — the caller
// can either Cancel first or wait. This mirrors the Devin REST shape
// where appending a message during a running step queues it.
//
// In Phase 2 we keep it simple: refuse + return "busy" so the caller
// has to ack the in-flight task. The queueing mode lands when the
// scheduler in Phase 3 takes over chained-task scheduling.
func (m *AgentSessionManager) Message(id, text, requireOwner string) (*AgentSession, error) {
	if m == nil || m.taskMgr == nil {
		return nil, errors.New("agent session manager unavailable")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("message text is required")
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || (requireOwner != "" && s.OwnerUserID != requireOwner) {
		m.mu.Unlock()
		return nil, errors.New("session not found")
	}
	m.refreshLocked(s)
	if s.Status == AgentSessionRunning {
		m.mu.Unlock()
		return nil, errors.New("session is still running — wait for the current step or cancel it first")
	}
	now := time.Now().UnixMilli()
	s.Messages = append(s.Messages, AgentSessionMessage{
		Direction: "user",
		Text:      text,
		CreatedAt: now,
	})
	prompt := composeAgentSessionPrompt(s, text)
	taskMgr := m.taskMgr
	workDir := s.WorkDir
	model := s.Model
	runner := s.Runner
	m.mu.Unlock()

	task, err := taskMgr.CreateTaskWithOptions(firstLine(text), prompt, model, "agent-session", runner, "", nil, TaskCreateOptions{
		WorkDir: workDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s = m.sessions[id]
	if s == nil {
		return nil, errors.New("session vanished mid-message")
	}
	s.CurrentTask = task.ID
	s.Status = AgentSessionRunning
	s.UpdatedAt = time.Now().UnixMilli()
	s.Messages = append(s.Messages, AgentSessionMessage{
		Direction: "agent",
		Text:      "",
		TaskID:    task.ID,
		CreatedAt: s.UpdatedAt,
	})
	m.saveLocked()
	cp := *s
	return &cp, nil
}

// Cancel stops the in-flight task and marks the session cancelled.
// Idempotent — already-cancelled sessions return nil.
func (m *AgentSessionManager) Cancel(id, requireOwner string) error {
	if m == nil {
		return errors.New("agent session manager unavailable")
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || (requireOwner != "" && s.OwnerUserID != requireOwner) {
		m.mu.Unlock()
		return errors.New("session not found")
	}
	currentTaskID := s.CurrentTask
	s.Status = AgentSessionCancelled
	s.UpdatedAt = time.Now().UnixMilli()
	m.saveLocked()
	m.mu.Unlock()
	if currentTaskID != "" && m.taskMgr != nil {
		_ = m.taskMgr.StopTask(currentTaskID)
	}
	return nil
}

// Delete removes a session. The underlying chained tasks are NOT
// stopped — the user may want to keep the task history alongside.
func (m *AgentSessionManager) Delete(id, requireOwner string) error {
	if m == nil {
		return errors.New("agent session manager unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || (requireOwner != "" && s.OwnerUserID != requireOwner) {
		return errors.New("session not found")
	}
	delete(m.sessions, id)
	_ = s
	m.saveLocked()
	return nil
}

// refreshLocked pulls the latest TaskManager state for the session's
// most recent agent turn. Caller holds m.mu. Updates ResultText into
// the trailing agent message and downgrades status if the task is
// finished/failed.
func (m *AgentSessionManager) refreshLocked(s *AgentSession) {
	if m.taskMgr == nil || s == nil {
		return
	}
	if s.CurrentTask == "" {
		return
	}
	task, ok := m.taskMgr.GetTask(s.CurrentTask)
	if !ok {
		return
	}
	// Find the trailing agent message and update it.
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Direction == "agent" && s.Messages[i].TaskID == s.CurrentTask {
			if task.ResultText != "" {
				s.Messages[i].Text = task.ResultText
			} else if task.Output != "" {
				s.Messages[i].Text = task.Output
			}
			s.Messages[i].CostUSD = task.CostUSD
			break
		}
	}
	switch task.Status {
	case TaskStatusFinished:
		if s.Status != AgentSessionCancelled {
			s.Status = AgentSessionAwaiting // ready for the next user message
		}
	case TaskStatusFailed:
		if s.Status != AgentSessionCancelled {
			s.Status = AgentSessionFailed
		}
	case TaskStatusStopped:
		if s.Status != AgentSessionCancelled {
			s.Status = AgentSessionAwaiting
		}
	}
}

// composeAgentSessionPrompt builds the prompt sent to the runner for
// a given user message. Includes the session title + workdir hint +
// running message history (capped) so the runner can re-establish
// context in chains where the previous task already ended.
//
// The cap (last 10 turns) keeps the prompt size bounded; full
// history is still in TaskManager for forensic / replay.
func composeAgentSessionPrompt(s *AgentSession, latestText string) string {
	var b strings.Builder
	b.WriteString(latestText)
	b.WriteString("\n\n")
	b.WriteString("--- agent session context ---\n")
	if s.Title != "" {
		b.WriteString("Title: ")
		b.WriteString(s.Title)
		b.WriteString("\n")
	}
	if s.WorkDir != "" {
		b.WriteString("Workdir: ")
		b.WriteString(s.WorkDir)
		b.WriteString("\n")
	}
	if s.Project != "" {
		b.WriteString("Project: ")
		b.WriteString(s.Project)
		b.WriteString("\n")
	}
	// Last 10 messages (excluding the latest user message which is
	// already at the top of the prompt).
	start := 0
	if len(s.Messages) > 10 {
		start = len(s.Messages) - 10
	}
	if start < len(s.Messages) {
		b.WriteString("Recent turns:\n")
		for _, m := range s.Messages[start:] {
			if m.Direction == "user" {
				b.WriteString("[user] ")
			} else {
				b.WriteString("[agent] ")
			}
			b.WriteString(strings.TrimSpace(m.Text))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// firstLine returns the first non-empty line of text, capped at 80
// chars, suitable as a session title or task title.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 80 {
			return line[:77] + "..."
		}
		return line
	}
	return "agent session"
}
