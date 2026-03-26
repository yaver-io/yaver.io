package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// saveImages decodes base64 images and writes them to ~/.yaver/images/{taskID}/.
// Returns the absolute file paths of saved images.
func saveImages(taskID string, images []ImageAttachment) []string {
	if len(images) == 0 {
		return nil
	}
	dir, err := ConfigDir()
	if err != nil {
		log.Printf("[images] config dir error: %v", err)
		return nil
	}
	imgDir := filepath.Join(dir, "images", taskID)
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		log.Printf("[images] mkdir error: %v", err)
		return nil
	}

	var paths []string
	for i, img := range images {
		data, err := base64.StdEncoding.DecodeString(img.Base64)
		if err != nil {
			log.Printf("[images] base64 decode error for image %d: %v", i+1, err)
			continue
		}
		ext := ".jpg"
		if img.MimeType == "image/png" {
			ext = ".png"
		}
		fname := fmt.Sprintf("img_%03d%s", i+1, ext)
		fpath := filepath.Join(imgDir, fname)
		if err := os.WriteFile(fpath, data, 0644); err != nil {
			log.Printf("[images] write error for %s: %v", fname, err)
			continue
		}
		paths = append(paths, fpath)
		log.Printf("[images] Saved %s (%d bytes)", fpath, len(data))
	}
	return paths
}

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusQueued   TaskStatus = "queued"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusStopped  TaskStatus = "stopped"
	TaskStatusFinished TaskStatus = "completed"
	TaskStatusFailed   TaskStatus = "failed"
)

// RunnerConfig describes how to invoke an AI runner (Claude or a custom tool).
type RunnerConfig struct {
	RunnerID        string   `json:"runnerId"`
	Name            string   `json:"name"`
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	OutputMode      string   `json:"outputMode"` // "stream-json" or "raw"
	ResumeSupported bool     `json:"resumeSupported"`
	ResumeArgs      []string `json:"resumeArgs,omitempty"`
	ExitCommand     string   `json:"exitCommand,omitempty"` // e.g. "/exit" for Claude, "/quit" for Aider
	AutoDetected    bool     `json:"-"`                     // true if user never explicitly chose a runner
}

var defaultRunner = RunnerConfig{
	RunnerID: "claude",
	Name:     "Claude Code",
	Command:  "claude",
	Args: []string{
		"-p", "{prompt}",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--model", "sonnet",
		"--tools", "Bash",
		"--dangerously-skip-permissions",
	},
	OutputMode:      "stream-json",
	ResumeSupported: false,
	ResumeArgs:      []string{"--resume", "{sessionId}"},
	ExitCommand:     "/exit",
}

// exitCommands maps runner IDs to their graceful exit commands.
var exitCommands = map[string]string{
	"claude":   "/exit",
	"codex":    "exit",
	"aider":    "/quit",
	"goose":    "exit",
	"opencode": "/quit",
}

// builtinRunners defines all known runner configurations.
var builtinRunners = map[string]RunnerConfig{
	"claude": {
		RunnerID:    "claude",
		Name:        "Claude Code",
		Command:     "claude",
		Args:        []string{"-p", "{prompt}", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--model", "sonnet", "--tools", "Bash", "--dangerously-skip-permissions"},
		OutputMode:  "stream-json",
		ExitCommand: "/exit",
	},
	"codex": {
		RunnerID:   "codex",
		Name:       "OpenAI Codex",
		Command:    "codex",
		Args:       []string{"--quiet", "--full-auto", "{prompt}"},
		OutputMode: "raw",
	},
	"aider": {
		RunnerID:    "aider",
		Name:        "Aider",
		Command:     "aider",
		Args:        []string{"--yes-always", "--no-git", "--message", "{prompt}"},
		OutputMode:  "raw",
		ExitCommand: "/quit",
	},
	"goose": {
		RunnerID:    "goose",
		Name:        "Goose",
		Command:     "goose",
		Args:        []string{"run", "--text", "{prompt}"},
		OutputMode:  "raw",
		ExitCommand: "exit",
	},
	"ollama": {
		RunnerID:   "ollama",
		Name:       "Ollama",
		Command:    "ollama",
		Args:       []string{"run", "llama3", "{prompt}"},
		OutputMode: "raw",
	},
	"amp": {
		RunnerID:   "amp",
		Name:       "Amp",
		Command:    "amp",
		Args:       []string{"run", "{prompt}"},
		OutputMode: "raw",
	},
	"opencode": {
		RunnerID:    "opencode",
		Name:        "OpenCode",
		Command:     "opencode",
		Args:        []string{"--message", "{prompt}"},
		OutputMode:  "raw",
		ExitCommand: "/quit",
	},
}

// GetRunnerConfig returns the RunnerConfig for a given runner ID.
// Falls back to defaultRunner if not found.
func GetRunnerConfig(runnerID string) RunnerConfig {
	if r, ok := builtinRunners[runnerID]; ok {
		return r
	}
	return defaultRunner
}

// cachedModels stores models fetched from Convex for the /agent/runners endpoint.
var cachedModels []BackendModel

// LoadRunnersFromBackend populates builtinRunners from Convex backend data.
func LoadRunnersFromBackend(runners []backendRunnerFull) {
	for _, r := range runners {
		if r.Command == "" || r.RunnerID == "custom" {
			continue // skip custom runner template
		}
		rc := RunnerConfig{
			RunnerID:        r.RunnerID,
			Name:            r.Name,
			Command:         r.Command,
			OutputMode:      r.OutputMode,
			ResumeSupported: r.ResumeSupported,
			ExitCommand:     r.ExitCommand,
		}
		if r.Args != "" {
			_ = json.Unmarshal([]byte(r.Args), &rc.Args)
		}
		if r.ResumeArgs != "" {
			_ = json.Unmarshal([]byte(r.ResumeArgs), &rc.ResumeArgs)
		}
		builtinRunners[r.RunnerID] = rc
		log.Printf("  Runner loaded: %s (%s)", rc.Name, rc.RunnerID)
	}
}

// LoadModelsFromBackend caches models fetched from Convex.
func LoadModelsFromBackend(models []BackendModel) {
	cachedModels = models
}

// GetCachedModels returns models loaded from Convex.
func GetCachedModels() []BackendModel {
	return cachedModels
}

// ClaudeEvent represents a top-level line of stream-json output from Claude CLI.
// With --include-partial-messages, events include:
//   {"type":"system","subtype":"init",...}
//   {"type":"stream_event","event":{...}} — incremental streaming (text_delta, tool_use, etc.)
//   {"type":"assistant","message":{...}}  — complete assistant message (text or tool_use)
//   {"type":"user","message":{...},"tool_use_result":{...}} — tool execution results (stdout/stderr)
//   {"type":"result","result":"...", "total_cost_usd":0.01,...}
type ClaudeEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"` // For stream_event wrapper
	RawResult json.RawMessage `json:"result,omitempty"`
	TotalCost float64         `json:"total_cost_usd,omitempty"`
	Errors    []string        `json:"errors,omitempty"` // e.g. ["No conversation found with session ID: ..."]
	// Tool result (for "user" type events with tool output)
	ToolUseResult *ToolUseResult `json:"tool_use_result,omitempty"`
}

// ToolUseResult contains stdout/stderr from a tool execution.
type ToolUseResult struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`
}

// streamEventInner is the inner event payload inside {"type":"stream_event","event":{...}}.
type streamEventInner struct {
	Type         string          `json:"type"` // message_start, content_block_start, content_block_delta, etc.
	Index        int             `json:"index,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`
}

// contentBlockInfo describes a content_block_start payload.
type contentBlockInfo struct {
	Type string `json:"type"` // "text" or "tool_use"
	Name string `json:"name,omitempty"`
}

// deltaInfo describes a content_block_delta payload.
type deltaInfo struct {
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// claudeMessage is the parsed "message" field from assistant events.
type claudeMessage struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
}

// bashInput is the parsed input from a Bash tool_use.
type bashInput struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// ConversationTurn represents one user or assistant message in the task conversation.
type ConversationTurn struct {
	Role      string    `json:"role"` // "user" or "assistant"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

const maxProcessRetries = 4 // Max auto-restart attempts when Claude crashes (2s, 4s, 8s, 16s)

// RunnerProcess describes a running process found via ps/tasklist.
type RunnerProcess struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// sessionProcess describes a running agent process with parent info for doctor.
type sessionProcess struct {
	PID        int
	PPID       int
	Command    string
	BinaryName string
}

// AgentStatus is returned by the /agent/status endpoint.
type AgentStatus struct {
	Runner          RunnerStatusInfo  `json:"runner"`
	RunningTasks    int               `json:"runningTasks"`
	TotalTasks      int               `json:"totalTasks"`
	RunnerProcesses []RunnerProcess   `json:"runnerProcesses"`
	System          SystemInfo        `json:"system"`
}

// RunnerStatusInfo describes the configured runner.
type RunnerStatusInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Installed bool   `json:"installed"`
	Error     string `json:"error,omitempty"`
}

// SystemInfo describes the host machine.
type SystemInfo struct {
	Hostname string  `json:"hostname"`
	OS       string  `json:"os"`
	Arch     string  `json:"arch"`
	MemoryMB int64   `json:"memoryMb,omitempty"`
}

// GetRunnerInfos returns info about active runner processes for heartbeat reporting.
func (tm *TaskManager) GetRunnerInfos() []RunnerInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	infos := make([]RunnerInfo, 0) // never nil — Convex expects [] not null
	for _, t := range tm.tasks {
		if t.Status == TaskStatusRunning || t.Status == TaskStatusQueued {
			pid := 0
			if t.cmd != nil && t.cmd.Process != nil {
				pid = t.cmd.Process.Pid
			}
			status := "running"
			if t.Status == TaskStatusQueued {
				status = "idle"
			}
			infos = append(infos, RunnerInfo{
				TaskID:   t.ID,
				RunnerID: t.RunnerID,
				Model:    t.Model,
				PID:      pid,
				Status:   status,
				Title:    t.Title,
			})
		}
	}
	return infos
}

// GetOwnRunnerProcesses returns PIDs of runner processes spawned by this agent.
func (tm *TaskManager) GetOwnRunnerProcesses() []RunnerProcess {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	var procs []RunnerProcess

	// Warm session process
	if tm.warmPID > 0 {
		procs = append(procs, RunnerProcess{
			PID:     tm.warmPID,
			Command: fmt.Sprintf("warm session (id=%s)", tm.warmSessionID),
		})
	}

	// Task processes
	for _, t := range tm.tasks {
		if t.cmd != nil && t.cmd.Process != nil && (t.Status == TaskStatusRunning || t.Status == TaskStatusQueued) {
			procs = append(procs, RunnerProcess{
				PID:     t.cmd.Process.Pid,
				Command: fmt.Sprintf("task %s: %s", t.ID, t.Title),
			})
		}
	}
	return procs
}

// GetAgentStatus returns the current agent and runner health.
// GetRunningTaskCount returns the number of currently running tasks.
func (tm *TaskManager) GetRunningTaskCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	count := 0
	for _, t := range tm.tasks {
		if t.Status == TaskStatusRunning {
			count++
		}
	}
	return count
}

func (tm *TaskManager) GetAgentStatus() AgentStatus {
	// Check runner binary
	runnerInfo := RunnerStatusInfo{
		ID:      tm.runner.RunnerID,
		Name:    tm.runner.Name,
		Command: tm.runner.Command,
	}
	if err := tm.CheckRunner(); err != nil {
		runnerInfo.Installed = false
		runnerInfo.Error = err.Error()
	} else {
		runnerInfo.Installed = true
	}

	// Count running tasks
	tm.mu.RLock()
	running := 0
	for _, t := range tm.tasks {
		if t.Status == TaskStatusRunning {
			running++
		}
	}
	total := len(tm.tasks)
	tm.mu.RUnlock()

	// Only show runner processes that this agent forked
	procs := tm.GetOwnRunnerProcesses()

	// System info
	hostname, _ := os.Hostname()
	var memMB int64
	if m, err := getSystemMemoryMB(); err == nil {
		memMB = m
	}

	return AgentStatus{
		Runner:          runnerInfo,
		RunningTasks:    running,
		TotalTasks:      total,
		RunnerProcesses: procs,
		System: SystemInfo{
			Hostname: hostname,
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			MemoryMB: memMB,
		},
	}
}

// Task represents a single Claude CLI task running as a subprocess.
// SpeechContext carries speech-to-text/text-to-speech preferences from the mobile app.
type SpeechContext struct {
	InputFromSpeech bool   `json:"inputFromSpeech"` // true if user dictated this task
	STTProvider     string `json:"sttProvider"`      // "on-device", "openai", "deepgram", "assemblyai"
	TTSEnabled      bool   `json:"ttsEnabled"`       // user wants response read aloud
	TTSProvider     string `json:"ttsProvider"`      // "device" (OS TTS)
	Verbosity       *int   `json:"verbosity"`        // 0-10: response detail level (nil = default 10)
}

// ImageAttachment represents a base64-encoded image sent from mobile.
type ImageAttachment struct {
	Base64   string `json:"base64"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
}

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Source      string     `json:"source,omitempty"` // "mobile", "mcp", "cli"
	Model       string     `json:"model,omitempty"`
	RunnerID    string     `json:"runnerId,omitempty"` // which runner is executing this task
	SessionID   string     `json:"session_id,omitempty"`
	Output      string     `json:"output"`
	ResultText   string  // Extracted clean result text from Claude
	CostUSD      float64 // Total API cost
	Turns       []ConversationTurn // Full conversation history
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`

	TmuxSession  string `json:"tmuxSession,omitempty"` // tmux session name (for adopted sessions)
	IsAdopted    bool   `json:"isAdopted,omitempty"`   // true if adopted from an existing tmux session

	// Speech context from mobile — passed through to the AI runner prompt
	SpeechContext *SpeechContext `json:"-"`

	// Image paths saved to disk for this task (not persisted in tasks.json)
	ImagePaths []string `json:"-"`

	runner       RunnerConfig // the runner config used for this task (not persisted)
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	stdin        io.WriteCloser
	outputCh     chan string
	doneCh       chan struct{}
	retryCount   int  // Number of auto-restart attempts so far
}

// TaskInfo is the JSON-safe subset returned in listings.
type TaskInfo struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Status      TaskStatus         `json:"status"`
	RunnerID    string             `json:"runnerId,omitempty"`
	SessionID   string             `json:"sessionId,omitempty"`
	Output      string             `json:"output,omitempty"`
	ResultText  string             `json:"resultText,omitempty"`
	CostUSD     float64            `json:"costUsd,omitempty"`
	Turns       []ConversationTurn `json:"turns,omitempty"`
	Source      string             `json:"source,omitempty"`
	TmuxSession string            `json:"tmuxSession,omitempty"`
	IsAdopted   bool               `json:"isAdopted,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	StartedAt   *time.Time         `json:"startedAt,omitempty"`
	FinishedAt  *time.Time         `json:"finishedAt,omitempty"`
}

// TaskManager manages the lifecycle of tasks.
type TaskManager struct {
	mu           sync.RWMutex
	tasks        map[string]*Task
	workDir      string
	store        *TaskStore
	runner       RunnerConfig
	TmuxMgr      *TmuxManager // manages tmux session adoption (nil if tmux unavailable)
	Sandbox      SandboxConfig // Command sandbox configuration
	WaitForSlot  bool // If true, wait for other Claude Code sessions to finish before starting
	DummyMode    bool // If true, use fake responses instead of launching a real runner

	// Callbacks (set after construction)
	OnTaskDone func(task *Task) // called when a task finishes (completed/failed/stopped)

	// Convex reporting (set after construction)
	ConvexURL  string
	AuthToken  string
	DeviceID   string
	OwnerEmail string // for dev logging

	// Warm session: forked at startup, reused for all tasks
	warmSessionID  string     // Claude session ID from warmup
	warmCreatedAt  time.Time  // when the warm session was established
	warmPID        int        // PID of the warmup process (0 if not running)
	warmReady      bool       // true once warmup completed successfully
}

// NewTaskManager creates a new TaskManager. If store is non-nil, previously
// persisted tasks are loaded from disk (running/queued ones become stopped).
func NewTaskManager(workDir string, store *TaskStore, runner RunnerConfig) *TaskManager {
	tasks := make(map[string]*Task)
	if store != nil {
		tasks = store.Load()
	}
	// Mark orphaned "running" tasks as failed — they have no live process after restart.
	now := time.Now()
	for _, t := range tasks {
		if t.Status == TaskStatusRunning {
			log.Printf("[task %s] Marking orphaned task as failed (was running before restart)", t.ID)
			t.Status = TaskStatusFailed
			t.FinishedAt = &now
		}
	}
	tm := &TaskManager{
		tasks:   tasks,
		workDir: workDir,
		store:   store,
		runner:  runner,
	}
	tm.persist()
	return tm
}

// fireTaskDone calls the OnTaskDone callback if set (non-blocking).
func (tm *TaskManager) fireTaskDone(task *Task) {
	if tm.OnTaskDone != nil {
		// Copy fields under lock to avoid races
		t := *task
		go tm.OnTaskDone(&t)
	}
}

// WarmUp forks the runner at startup to establish a session.
// This avoids cold-start delays and keeps us in one session for rate limiting.
func (tm *TaskManager) WarmUp() {
	if !tm.runner.ResumeSupported {
		log.Printf("[warmup] Skipping — resume not supported for %s", tm.runner.Name)
		return
	}
	if err := tm.CheckRunner(); err != nil {
		log.Printf("[warmup] Runner not available: %v — skipping warmup", err)
		return
	}

	log.Printf("[warmup] Forking %s to establish warm session...", tm.runner.Name)

	warmPrompt := "You are a warm session. Reply with just: ready"
	args := tm.buildArgs(warmPrompt)

	cmd := exec.CommandContext(context.Background(), tm.runner.Command, args...)
	cmd.Dir = tm.workDir

	// Set PATH
	home, _ := os.UserHomeDir()
	if home != "" {
		existingPath := os.Getenv("PATH")
		extraPaths := filepath.Join(home, ".local", "bin") + ":" +
			"/opt/homebrew/bin" + ":" +
			"/usr/local/bin"
		cmd.Env = append(os.Environ(), "PATH="+extraPaths+":"+existingPath)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[warmup] Failed to create stdout pipe: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[warmup] Failed to start: %v", err)
		return
	}

	tm.mu.Lock()
	tm.warmPID = cmd.Process.Pid
	tm.mu.Unlock()

	log.Printf("[warmup] %s started (PID %d)", tm.runner.Name, cmd.Process.Pid)

	// Read output to get session ID
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var event ClaudeEvent
			if json.Unmarshal(line, &event) == nil && event.SessionID != "" {
				tm.mu.Lock()
				tm.warmSessionID = event.SessionID
				tm.warmCreatedAt = time.Now()
				tm.mu.Unlock()
				log.Printf("[warmup] Got session ID: %s", event.SessionID)
			}
		}
	}()

	// Wait for process to finish
	go func() {
		err := cmd.Wait()
		tm.mu.Lock()
		if tm.warmSessionID != "" {
			tm.warmReady = true
			log.Printf("[warmup] Session ready (id=%s)", tm.warmSessionID)
		} else {
			log.Printf("[warmup] Process exited without session ID: %v", err)
		}
		tm.warmPID = 0
		tm.mu.Unlock()
	}()
}

// forkedPidsFile returns the path to the file tracking PIDs forked by the agent.
func forkedPidsFile() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "forked-pids.txt")
}

// trackForkedPID adds a PID to the forked-pids file.
func trackForkedPID(pid int) {
	path := forkedPidsFile()
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%d\n", pid)
}

// untrackForkedPID removes a PID from the forked-pids file.
func untrackForkedPID(pid int) {
	path := forkedPidsFile()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var remaining []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != fmt.Sprintf("%d", pid) && line != "" {
			remaining = append(remaining, line)
		}
	}
	os.WriteFile(path, []byte(strings.Join(remaining, "\n")+"\n"), 0600)
}

// getForkedPIDs returns all tracked forked PIDs.
func getForkedPIDs() []int {
	path := forkedPidsFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// clearForkedPIDs removes the forked-pids file.
func clearForkedPIDs() {
	path := forkedPidsFile()
	if path != "" {
		os.Remove(path)
	}
}

// Shutdown stops all running tasks and kills the warm session process.
func (tm *TaskManager) Shutdown() {
	stopped := tm.StopAllTasks()
	if stopped > 0 {
		log.Printf("[shutdown] Stopped %d running task(s)", stopped)
	}

	tm.mu.Lock()
	pid := tm.warmPID
	tm.mu.Unlock()

	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			log.Printf("[shutdown] Killing warm session (PID %d)", pid)
			_ = proc.Kill()
		}
	}

	clearForkedPIDs()
}

// GetWarmSessionID returns the warm session ID if available.
func (tm *TaskManager) GetWarmSessionID() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.warmSessionID
}

// persist saves the current task map to disk if a store is configured.
// Must be called while tm.mu is held (read or write).
func (tm *TaskManager) persist() {
	if tm.store != nil {
		tm.store.Save(tm.tasks)
	}
}

// CheckRunner verifies that the configured runner binary exists and is callable.
// Returns nil if the runner is healthy, or an error with a user-friendly message.
func (tm *TaskManager) CheckRunner() error {
	// 1. Check if the binary exists in PATH
	path, err := exec.LookPath(tm.runner.Command)
	if err != nil {
		return fmt.Errorf("%s not found in PATH — install it first (https://docs.anthropic.com/en/docs/claude-code)", tm.runner.Command)
	}
	log.Printf("[runner-check] Found %s at %s", tm.runner.Command, path)

	// 2. Quick version check to verify it's callable
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, tm.runner.Command, "--version")
	// Use same env setup as startProcess
	home, _ := os.UserHomeDir()
	if home != "" {
		existingPath := os.Getenv("PATH")
		extraPaths := filepath.Join(home, ".local", "bin") + ":" +
			"/opt/homebrew/bin" + ":" +
			"/usr/local/bin"
		cmd.Env = append(os.Environ(), "PATH="+extraPaths+":"+existingPath)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s found but not working: %v (output: %s)", tm.runner.Command, err, strings.TrimSpace(string(out)))
	}
	log.Printf("[runner-check] %s version: %s", tm.runner.Command, strings.TrimSpace(string(out)))
	return nil
}

// CreateTask creates a new task and runs the specified (or default) runner.
// runnerID selects which runner to use — empty uses the agent's default.
// model overrides the default model (e.g. "opus", "sonnet", "haiku") — empty uses runner default.
// source indicates where the task originated: "mobile", "mcp", or "cli" — defaults to "mobile".
// customCommand, if non-empty, runs an arbitrary command via sh -c (ignores runnerID).
func (tm *TaskManager) CreateTask(title, description, model, source, runnerID, customCommand string, images []ImageAttachment, speechCtx ...*SpeechContext) (*Task, error) {
	var taskRunner RunnerConfig

	if customCommand != "" {
		// Sandbox: validate custom commands before execution
		if err := ValidateCommand(customCommand, tm.Sandbox); err != nil {
			return nil, fmt.Errorf("command blocked: %w", err)
		}
		// Ad-hoc custom command from mobile — run via sh -c
		taskRunner = RunnerConfig{
			RunnerID:   "custom",
			Name:       "Custom",
			Command:    "sh",
			Args:       []string{"-c", customCommand},
			OutputMode: "raw",
		}
	} else {
		// Resolve which runner to use for this task
		taskRunner = tm.runner // default (could be custom)
		if runnerID != "" && runnerID != tm.runner.RunnerID {
			if r, ok := builtinRunners[runnerID]; ok {
				taskRunner = r
			} else if runnerID == "custom" || runnerID == tm.runner.RunnerID {
				taskRunner = tm.runner
			} else {
				return nil, fmt.Errorf("unknown runner: %s", runnerID)
			}
		}
	}

	// Pre-flight: verify the runner binary is available (skip in dummy mode).
	if !tm.DummyMode {
		if err := CheckRunnerBinary(taskRunner.Command); err != nil {
			return nil, fmt.Errorf("runner not ready: %w", err)
		}
	}

	if source == "" {
		source = "mobile"
	}
	id := uuid.New().String()[:8]

	now := time.Now()
	task := &Task{
		ID:          id,
		Title:       title,
		Description: description,
		Status:      TaskStatusQueued,
		Source:      source,
		Model:       model,
		RunnerID:    taskRunner.RunnerID,
		runner:      taskRunner,
		CreatedAt:   now,
		outputCh:    make(chan string, 512),
		doneCh:      make(chan struct{}),
		Turns: []ConversationTurn{
			{Role: "user", Content: title, Timestamp: now},
		},
	}
	if len(speechCtx) > 0 && speechCtx[0] != nil {
		task.SpeechContext = speechCtx[0]
	}
	if len(images) > 0 {
		task.ImagePaths = saveImages(id, images)
	}

	tm.mu.Lock()
	tm.tasks[id] = task
	tm.persist()
	tm.mu.Unlock()

	// Dummy mode: stream fake response without launching a real process.
	if tm.DummyMode {
		log.Printf("[task %s] DUMMY MODE — streaming fake response for: %s", id, title)
		go tm.runDummyTask(task)
		return task, nil
	}

	log.Printf("[task %s] Starting %s process for: %s", id, taskRunner.Name, title)
	if err := tm.startProcess(task); err != nil {
		log.Printf("[task %s] Failed to start %s: %v", id, taskRunner.Name, err)
		task.Status = TaskStatusFailed
		tm.mu.Lock()
		tm.persist()
		tm.mu.Unlock()
		return task, fmt.Errorf("start process: %w", err)
	}
	log.Printf("[task %s] %s process started (PID %d)", id, taskRunner.Name, task.cmd.Process.Pid)

	return task, nil
}

// commonExtraPaths returns platform-appropriate extra binary search paths.
func commonExtraPaths() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	paths := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/local/go/bin",
		"/snap/bin",
		filepath.Join(home, ".nix-profile", "bin"),
		"/nix/var/nix/profiles/default/bin",
	}
	if runtime.GOOS == "windows" {
		paths = append(paths,
			filepath.Join(home, "AppData", "Local", "Microsoft", "WinGet", "Packages"),
			filepath.Join(home, "scoop", "shims"),
			filepath.Join(home, "AppData", "Roaming", "npm"),
			filepath.Join(home, "AppData", "Local", "Programs", "Python", "Python311", "Scripts"),
		)
	}
	if runtime.GOOS == "linux" {
		paths = append(paths,
			"/home/linuxbrew/.linuxbrew/bin",
			filepath.Join(home, ".linuxbrew", "bin"),
		)
	}
	return strings.Join(paths, ":")
}

// expandedPath returns PATH with common extra binary locations prepended.
func expandedPath() string {
	return commonExtraPaths() + ":" + os.Getenv("PATH")
}

// CheckRunnerBinary checks if a runner binary is available in PATH or common locations.
// If found outside PATH, logs a hint about adding it to PATH.
func CheckRunnerBinary(command string) error {
	// First try standard PATH
	path, err := exec.LookPath(command)
	if err != nil {
		// Try expanded PATH with common locations
		path = findInExpandedPath(command)
		if path == "" {
			return fmt.Errorf("%s not found in PATH or common locations", command)
		}
		log.Printf("[runner-check] %s found at %s (not in default PATH — using expanded search)", command, path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "PATH="+expandedPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s found but not working: %v (output: %s)", command, err, strings.TrimSpace(string(out)))
	}
	log.Printf("[runner-check] %s at %s — %s", command, path, strings.TrimSpace(string(out)))
	return nil
}

// findInExpandedPath searches for a command in common binary locations beyond PATH.
func findInExpandedPath(command string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	searchDirs := strings.Split(commonExtraPaths(), ":")
	for _, dir := range searchDirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, command)
		if runtime.GOOS == "windows" {
			// Try .exe and .cmd extensions on Windows
			for _, ext := range []string{".exe", ".cmd", ".bat", ""} {
				p := candidate + ext
				if info, err := os.Stat(p); err == nil && !info.IsDir() {
					return p
				}
			}
		} else {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

// runDummyTask streams a fake response for network testing (no real runner).
func (tm *TaskManager) runDummyTask(task *Task) {
	now := time.Now()
	task.StartedAt = &now
	task.Status = TaskStatusRunning

	var output strings.Builder

	chunks := []string{
		"## Dummy Response\n\n",
		"This is a **dummy response** from the Yaver agent.\n\n",
		fmt.Sprintf("Your prompt was: *%s*\n\n", task.Title),
		"Network connection is working correctly.\n\n",
		fmt.Sprintf("- Device: `%s`\n", tm.DeviceID),
		fmt.Sprintf("- Work dir: `%s`\n", tm.workDir),
		fmt.Sprintf("- Time: `%s`\n", now.Format(time.RFC3339)),
		"\nDummy mode active — no real AI runner was invoked.\n",
	}

	for _, chunk := range chunks {
		time.Sleep(300 * time.Millisecond)
		tm.emit(task, &output, chunk)
	}

	finishNow := time.Now()
	tm.mu.Lock()
	task.Status = TaskStatusFinished
	task.FinishedAt = &finishNow
	task.ResultText = output.String()
	task.Turns = append(task.Turns, ConversationTurn{
		Role:      "assistant",
		Content:   task.ResultText,
		Timestamp: finishNow,
	})
	tm.persist()
	tm.fireTaskDone(task)
	tm.mu.Unlock()
	close(task.outputCh)
	close(task.doneCh)
	log.Printf("[task %s] DUMMY task completed", task.ID)
}

// buildArgs replaces placeholders in the runner's arg template with actual values.
func buildRunnerArgs(runner RunnerConfig, prompt string) []string {
	args := make([]string, len(runner.Args))
	for i, a := range runner.Args {
		args[i] = strings.ReplaceAll(a, "{prompt}", prompt)
	}
	return args
}

// buildArgs is a convenience wrapper using the task manager's default runner.
func (tm *TaskManager) buildArgs(prompt string) []string {
	return buildRunnerArgs(tm.runner, prompt)
}

// countOtherClaudeProcesses counts how many `claude` processes are running
// that are NOT spawned by this yaver agent (i.e. other interactive sessions).
func countOtherClaudeProcesses(ownPids map[int]bool) int {
	out, err := exec.Command("pgrep", "-f", "claude.*-p\\b|claude.*--resume").CombinedOutput()
	if err != nil {
		return 0 // pgrep returns 1 if no match
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			if !ownPids[pid] {
				count++
			}
		}
	}
	return count
}

// waitForSessionSlot waits until no other Claude Code sessions are active.
// Emits progress messages to the task output so the mobile user sees what's happening.
func (tm *TaskManager) waitForSessionSlot(task *Task) {
	if !tm.WaitForSlot {
		return
	}

	// Collect PIDs of tasks we own
	ownPids := make(map[int]bool)
	tm.mu.RLock()
	for _, t := range tm.tasks {
		if t.cmd != nil && t.cmd.Process != nil {
			ownPids[t.cmd.Process.Pid] = true
		}
	}
	tm.mu.RUnlock()

	others := countOtherClaudeProcesses(ownPids)
	if others == 0 {
		return
	}

	log.Printf("[task %s] Waiting for %d other Claude Code session(s) to finish...", task.ID, others)
	var output strings.Builder
	tm.mu.RLock()
	output.WriteString(task.Output)
	tm.mu.RUnlock()
	tm.emit(task, &output, fmt.Sprintf("⏳ Waiting for %d other Claude Code session(s) to finish...\n", others))

	for {
		time.Sleep(5 * time.Second)
		tm.mu.RLock()
		status := task.Status
		tm.mu.RUnlock()
		if status != TaskStatusQueued && status != TaskStatusRunning {
			return // Task was cancelled
		}
		others = countOtherClaudeProcesses(ownPids)
		if others == 0 {
			log.Printf("[task %s] Session slot available, proceeding", task.ID)
			tm.emit(task, &output, "✅ Session available, starting task...\n")
			return
		}
	}
}

// startProcess spawns the configured runner with the task's prompt.
func (tm *TaskManager) startProcess(task *Task) error {
	// Wait for other Claude Code sessions to finish (if --wait-for-session is set)
	tm.waitForSessionSlot(task)

	prompt := task.Title
	if task.Description != "" && task.Description != task.Title {
		prompt = task.Title + "\n\n" + task.Description
	}

	// Auto-detect project from task text and switch workDir if needed.
	// This enables "start AcmeStore" from Yaver mobile when serving from ~.
	tm.autoSwitchProject(task, prompt)

	// System prompt: behave as a remote terminal agent, tailored to the task source.
	switch task.Source {
	case "mcp":
		prompt += "\n\nYou are running tasks via MCP from an AI agent. Show what you are doing step by step. Use only terminal commands. Be concise. Format output in markdown."
	case "cli":
		prompt += "\n\nYou are running tasks from a remote CLI terminal. Show what you are doing step by step. Use only terminal commands. Be concise. Format output in markdown."
	default:
		prompt += "\n\nYou are running tasks from a remote mobile device. Show what you are doing step by step. Use only terminal commands. Be concise. Format output in markdown."
	}

	// Inject Yaver dev server proxy context so the runner knows to use /dev/start.
	// This is critical: the runner must NEVER output exp:// URLs or tell the user
	// to install Expo Go. Everything flows through the Yaver P2P channel.
	prompt += yaverDevServerContext(tm.workDir)

	// Append speech context if the user sent this task via voice
	if sc := task.SpeechContext; sc != nil {
		speechInfo := "\n\n[Speech context] "
		if sc.InputFromSpeech {
			sttQuality := "high"
			switch sc.STTProvider {
			case "on-device":
				speechInfo += "User dictated this task using on-device speech recognition (Whisper tiny model — good accuracy for commands, may have minor transcription errors in technical terms). "
				sttQuality = "good"
			case "openai":
				speechInfo += "User dictated this task using OpenAI GPT-4o transcription (excellent accuracy, very low error rate). "
			case "deepgram":
				speechInfo += "User dictated this task using Deepgram Nova-2 (excellent real-time accuracy, strong with technical vocabulary). "
			case "assemblyai":
				speechInfo += "User dictated this task using AssemblyAI (good accuracy, may have minor errors). "
				sttQuality = "good"
			default:
				speechInfo += fmt.Sprintf("User dictated this task using %s speech-to-text. ", sc.STTProvider)
			}
			if sttQuality == "good" {
				speechInfo += "If the task text seems slightly off, interpret the likely intent — minor transcription errors are possible. "
			}
		}
		if sc.TTSEnabled {
			speechInfo += "The user will hear your response read aloud via device text-to-speech (basic quality, no code rendering). "
			speechInfo += "Structure your response for listening: use short sentences, spell out abbreviations, avoid complex markdown tables or code blocks in explanations. "
			speechInfo += "Put code changes in files rather than inline when possible, and summarize what you did in plain language."
		}
		// Verbosity level (0-10)
		if sc.Verbosity != nil {
			v := *sc.Verbosity
			if v <= 2 {
				speechInfo += fmt.Sprintf("\n[Verbosity: %d/10] The user prefers very brief responses. Just confirm what was done, report any errors, skip all implementation details. Example: 'Done. Created the file and added the function. No issues.'", v)
			} else if v <= 4 {
				speechInfo += fmt.Sprintf("\n[Verbosity: %d/10] The user prefers concise responses. Summarize what you did in 2-3 sentences. Only show code snippets if there's something the user needs to review or decide on.", v)
			} else if v <= 6 {
				speechInfo += fmt.Sprintf("\n[Verbosity: %d/10] The user prefers moderate detail. Show key changes, explain your reasoning briefly, include important code snippets.", v)
			} else if v <= 8 {
				speechInfo += fmt.Sprintf("\n[Verbosity: %d/10] The user wants detailed responses. Show what you're doing, include code changes, explain your approach.", v)
			} else {
				speechInfo += fmt.Sprintf("\n[Verbosity: %d/10] The user wants full detail. Stream everything: all code changes, file diffs, reasoning, alternatives considered, potential issues.", v)
			}
		}
		prompt += speechInfo
	}

	// Append image file paths so the AI agent can read them
	if len(task.ImagePaths) > 0 {
		prompt += "\n\n[Attached images — use the Read tool to examine these files]\n"
		for i, p := range task.ImagePaths {
			prompt += fmt.Sprintf("Image %d: %s\n", i+1, p)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel

	runner := task.runner
	args := buildRunnerArgs(runner, prompt)

	// Use warm session if available (resume = same rate-limit bucket).
	// Expire warm sessions after 1 hour — Claude Code purges them and resume
	// will fail with "No conversation found with session ID".
	const warmSessionMaxAge = 1 * time.Hour
	tm.mu.RLock()
	warmSID := tm.warmSessionID
	warmAge := time.Since(tm.warmCreatedAt)
	tm.mu.RUnlock()
	if warmSID != "" && warmAge > warmSessionMaxAge {
		log.Printf("[task %s] Warm session %s expired (age=%v) — skipping resume", task.ID, warmSID, warmAge.Round(time.Second))
		tm.mu.Lock()
		tm.warmSessionID = ""
		tm.mu.Unlock()
		warmSID = ""
	}
	if warmSID != "" && runner.ResumeSupported && len(runner.ResumeArgs) > 0 {
		for _, ra := range runner.ResumeArgs {
			args = append(args, strings.ReplaceAll(ra, "{sessionId}", warmSID))
		}
		// Claude Code 2.1.80+ requires --fork-session with --session-id when resuming
		if runner.RunnerID == "claude" {
			args = append(args, "--fork-session", "--session-id", uuid.New().String())
		}
		log.Printf("[task %s] Resuming warm session %s (age=%v)", task.ID, warmSID, warmAge.Round(time.Second))
	}

	// Override model if specified on the task (e.g. "opus", "sonnet", "haiku").
	if task.Model != "" {
		modelOverride := false
		for i, a := range args {
			if a == "--model" && i+1 < len(args) {
				args[i+1] = task.Model
				modelOverride = true
				break
			}
		}
		if !modelOverride {
			args = append(args, "--model", task.Model)
		}
	}

	cmd := exec.CommandContext(ctx, runner.Command, args...)
	cmd.Dir = tm.workDir

	// Ensure common tool paths are in PATH for background processes.
	cmd.Env = append(os.Environ(), "PATH="+expandedPath())

	log.Printf("[task %s] Launching: %s %v (dir=%s)", task.ID, runner.Command, args[:2], tm.workDir)

	// Dev log: task launch
	go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-launch",
		fmt.Sprintf("Launching task %s: %s", task.ID, task.Title),
		map[string]interface{}{"runner": runner.RunnerID, "model": task.Model, "argCount": len(args)})

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Point stdin to /dev/null — Claude CLI blocks when stdin is a pipe.
	// Graceful exit is handled via process signals instead.
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	task.cmd = cmd

	if err := cmd.Start(); err != nil {
		cancel()
		go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-start-fail",
			fmt.Sprintf("Failed to start process for task %s: %v", task.ID, err), nil)
		return fmt.Errorf("start process: %w", err)
	}

	now := time.Now()
	task.StartedAt = &now
	task.Status = TaskStatusRunning

	trackForkedPID(cmd.Process.Pid)

	go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-started",
		fmt.Sprintf("Claude PID %d started for task %s", cmd.Process.Pid, task.ID), nil)

	// Monitor stdout based on output mode.
	if runner.OutputMode == "raw" {
		go tm.readRawOutput(task, stdout)
	} else {
		go tm.readStreamJSON(task, stdout)
	}

	// Drain stderr.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[task %s stderr] %s", task.ID, scanner.Text())
		}
	}()

	// Watchdog: if no output after 30s, emit a warning to the mobile user.
	go func() {
		time.Sleep(30 * time.Second)
		tm.mu.RLock()
		hasOutput := len(task.Output) > 0
		status := task.Status
		tm.mu.RUnlock()
		if !hasOutput && status == TaskStatusRunning {
			log.Printf("[task %s] WARNING: no output after 30s — runner may be rate-limited or stuck", task.ID)
			var output strings.Builder
			tm.mu.RLock()
			output.WriteString(task.Output)
			tm.mu.RUnlock()
			tm.emit(task, &output, "⏳ Waiting for response from AI agent... this may take longer if another session is active.\n")
		}
	}()

	// Wait for process to exit; auto-restart on unexpected crash.
	go func() {
		err := cmd.Wait()
		if cmd.Process != nil {
			untrackForkedPID(cmd.Process.Pid)
		}
		tm.mu.Lock()
		if task.Status == TaskStatusRunning {
			if err != nil {
				outputLen := len(task.Output)
				retries := task.retryCount

				// Auto-restart if the process crashed with little/no output
				// and we haven't exhausted retries. This covers cases where
				// Claude gets OOM-killed, segfaults, or is terminated externally.
				if retries < maxProcessRetries && outputLen < 100 {
					task.retryCount++
					backoff := time.Duration(2<<uint(retries)) * time.Second // 2s, 4s, 8s, 16s
					log.Printf("[task %s] %s crashed (exit: %v, output_len=%d) — auto-restarting in %v (attempt %d/%d)",
						task.ID, task.runner.Name, err, outputLen, backoff, retries+1, maxProcessRetries)

					// Report crash event to Convex
					go func() {
						if tm.ConvexURL != "" {
							detail := fmt.Sprintf("exit: %v, output_len=%d, attempt %d/%d, backoff %v", err, outputLen, retries+1, maxProcessRetries, backoff)
							_ = ReportDeviceEvent(tm.ConvexURL, tm.AuthToken, tm.DeviceID, "crash", detail)
						}
					}()

					// Emit status to mobile user (channel may be closed)
					restartMsg := fmt.Sprintf("\n⚠️ Agent process crashed — restarting (attempt %d/%d)...\n", retries+1, maxProcessRetries)
					task.Output += restartMsg
					func() {
						defer func() { recover() }() // guard against send on closed channel
						select {
						case task.outputCh <- restartMsg:
						default:
						}
					}()

					tm.persist()
					tm.mu.Unlock()

					time.Sleep(backoff)

					// Re-create channels for the new process
					task.outputCh = make(chan string, 512)
					task.doneCh = make(chan struct{})

					if restartErr := tm.startProcess(task); restartErr != nil {
						log.Printf("[task %s] Auto-restart failed: %v", task.ID, restartErr)
						tm.mu.Lock()
						task.Status = TaskStatusFailed
						finishNow := time.Now()
						task.FinishedAt = &finishNow
						tm.persist()
						tm.mu.Unlock()
						close(task.doneCh)
					} else {
						// Report successful restart
						go func() {
							if tm.ConvexURL != "" {
								_ = ReportDeviceEvent(tm.ConvexURL, tm.AuthToken, tm.DeviceID, "restart", fmt.Sprintf("attempt %d/%d succeeded", retries+1, maxProcessRetries))
								_ = SetRunnerDown(tm.ConvexURL, tm.AuthToken, tm.DeviceID, false)
							}
						}()
					}
					return
				}

				// All retries exhausted — mark runner as down in Convex
				go func() {
					if tm.ConvexURL != "" {
						detail := fmt.Sprintf("all %d retries exhausted, exit: %v", maxProcessRetries, err)
						_ = ReportDeviceEvent(tm.ConvexURL, tm.AuthToken, tm.DeviceID, "crash", detail)
						_ = SetRunnerDown(tm.ConvexURL, tm.AuthToken, tm.DeviceID, true)
					}
				}()

				task.Status = TaskStatusFailed
				log.Printf("[task %s] %s process failed: %v", task.ID, task.runner.Name, err)
			} else {
				task.Status = TaskStatusFinished
				log.Printf("[task %s] %s process finished successfully (output_len=%d)", task.ID, task.runner.Name, len(task.Output))
			}
			finishNow := time.Now()
			task.FinishedAt = &finishNow
			// Save assistant response as conversation turn
			if task.ResultText != "" {
				task.Turns = append(task.Turns, ConversationTurn{
					Role:      "assistant",
					Content:   task.ResultText,
					Timestamp: finishNow,
				})
			}
		}
		// Report runner usage to Convex (non-blocking)
		if tm.ConvexURL != "" && task.StartedAt != nil && task.FinishedAt != nil {
			duration := task.FinishedAt.Sub(*task.StartedAt).Seconds()
			startMs := task.StartedAt.UnixMilli()
			finishMs := task.FinishedAt.UnixMilli()
			runner := task.runner.Name
			model := task.Model
			source := task.Source
			taskID := task.ID
			go func() {
				if err := ReportRunnerUsage(tm.ConvexURL, tm.AuthToken, tm.DeviceID, taskID, runner, model, source, duration, startMs, finishMs); err != nil {
					log.Printf("[usage] failed to report: %v", err)
				} else {
					log.Printf("[usage] recorded %.0fs of %s for task %s", duration, runner, taskID[:8])
				}
			}()
		}
		tm.persist()
		tm.fireTaskDone(task)
		// Save session file for recent history (non-blocking)
		go saveSessionFile(task, task.runner.Name, tm.workDir)
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	return nil
}

// readRawOutput reads plain text lines from stdout (for non-JSON runners).
func (tm *TaskManager) readRawOutput(task *Task, r io.Reader) {
	defer close(task.outputCh)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var output strings.Builder
	tm.mu.RLock()
	output.WriteString(task.Output)
	tm.mu.RUnlock()

	for scanner.Scan() {
		line := scanner.Text()
		tm.emit(task, &output, line+"\n")
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[task %s] scanner error: %v", task.ID, err)
	}

	// Store final output as result text for raw runners.
	tm.mu.Lock()
	task.ResultText = task.Output
	tm.mu.Unlock()

	log.Printf("[task %s] Raw output reader finished (output_len=%d)", task.ID, output.Len())
}

// emit pushes text to both the output buffer and the streaming channel.
func (tm *TaskManager) emit(task *Task, output *strings.Builder, text string) {
	output.WriteString(text)
	tm.mu.Lock()
	task.Output = output.String()
	tm.mu.Unlock()
	select {
	case task.outputCh <- text:
	default:
	}
}

// readStreamJSON reads NDJSON from Claude CLI stdout with --include-partial-messages.
// It produces a live markdown stream showing:
//   - Commands Claude is running (from tool_use events)
//   - Terminal output (from tool_result/user events)
//   - Claude's text commentary (from text_delta streaming events)
func (tm *TaskManager) readStreamJSON(task *Task, r io.Reader) {
	defer close(task.outputCh)

	log.Printf("[task %s] Stream JSON reader started", task.ID)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	// Start from existing output (important for resumed tasks).
	var output strings.Builder
	tm.mu.RLock()
	output.WriteString(task.Output)
	tm.mu.RUnlock()

	// Track state for accumulating tool input JSON across deltas.
	var toolInputAccum strings.Builder
	inToolUse := false
	lastEmittedCmd := "" // Prevent duplicate command emissions
	lineCount := 0
	firstOutputLogged := false

	for scanner.Scan() {
		lineCount++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Dev log: first output from Claude
		if !firstOutputLogged {
			firstOutputLogged = true
			go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-first-output",
				fmt.Sprintf("First stdout line for task %s (len=%d)", task.ID, len(line)), nil)
		}

		// Log raw stdout for debugging (truncate long lines)
		rawLine := string(line)
		if len(rawLine) > 300 {
			log.Printf("[task %s] stdout[%d]: %s...(truncated, total %d)", task.ID, lineCount, rawLine[:300], len(rawLine))
		} else {
			log.Printf("[task %s] stdout[%d]: %s", task.ID, lineCount, rawLine)
		}

		var event ClaudeEvent
		if err := json.Unmarshal(line, &event); err != nil {
			text := string(line)
			tm.emit(task, &output, text+"\n")
			continue
		}

		// Extract session ID if present.
		if event.SessionID != "" {
			tm.mu.Lock()
			task.SessionID = event.SessionID
			tm.mu.Unlock()
		}

		switch event.Type {
		case "stream_event":
			// Parse the inner streaming event.
			if len(event.Event) == 0 {
				continue
			}
			var inner streamEventInner
			if err := json.Unmarshal(event.Event, &inner); err != nil {
				continue
			}

			switch inner.Type {
			case "content_block_start":
				// Check if this is a tool_use or text block.
				if len(inner.ContentBlock) > 0 {
					var cb contentBlockInfo
					if json.Unmarshal(inner.ContentBlock, &cb) == nil {
						if cb.Type == "tool_use" {
							inToolUse = true
							toolInputAccum.Reset()
						}
					}
				}

			case "content_block_delta":
				if len(inner.Delta) == 0 {
					continue
				}
				var d deltaInfo
				if json.Unmarshal(inner.Delta, &d) != nil {
					continue
				}

				if d.Type == "text_delta" && d.Text != "" {
					// Stream Claude's text commentary token-by-token.
					tm.emit(task, &output, d.Text)
					log.Printf("[task %s delta] %s", task.ID, d.Text)
				} else if d.Type == "input_json_delta" && d.PartialJSON != "" {
					// Accumulate tool input JSON fragments.
					toolInputAccum.WriteString(d.PartialJSON)
				}

			case "content_block_stop":
				// If we were accumulating tool input, emit the command (if not already emitted).
				if inToolUse && toolInputAccum.Len() > 0 {
					var bi bashInput
					if json.Unmarshal([]byte(toolInputAccum.String()), &bi) == nil && bi.Command != "" && bi.Command != lastEmittedCmd {
						cmdText := fmt.Sprintf("\n**$ %s**\n", bi.Command)
						tm.emit(task, &output, cmdText)
						lastEmittedCmd = bi.Command
						log.Printf("[task %s cmd] %s", task.ID, bi.Command)
					}
					inToolUse = false
					toolInputAccum.Reset()
				}
			}

		case "assistant":
			// Complete assistant message. We already stream text via text_delta
			// and commands via content_block_stop, so only emit tool_use as fallback
			// if it wasn't already emitted.
			if len(event.Message) > 0 {
				var msg claudeMessage
				if json.Unmarshal(event.Message, &msg) == nil {
					for _, block := range msg.Content {
						if block.Type == "tool_use" && len(block.Input) > 0 {
							var bi bashInput
							if json.Unmarshal(block.Input, &bi) == nil && bi.Command != "" && bi.Command != lastEmittedCmd {
								cmdText := fmt.Sprintf("\n**$ %s**\n", bi.Command)
								tm.emit(task, &output, cmdText)
								lastEmittedCmd = bi.Command
								log.Printf("[task %s cmd-fallback] %s", task.ID, bi.Command)
							}
						}
					}
				}
			}

		case "user":
			// Tool result — contains stdout/stderr from bash execution.
			// We only log these (don't emit to output) because Claude's text_delta
			// already streams a formatted version of the same content.
			if event.ToolUseResult != nil {
				if event.ToolUseResult.Stdout != "" {
					log.Printf("[task %s stdout] %s", task.ID, truncate(strings.TrimRight(event.ToolUseResult.Stdout, "\n"), 200))
				}
				if event.ToolUseResult.Stderr != "" {
					log.Printf("[task %s stderr-out] %s", task.ID, truncate(strings.TrimRight(event.ToolUseResult.Stderr, "\n"), 200))
				}
			}

		case "result":
			// If Claude reports a session-related error, invalidate the warm session
			// so retries don't hit the same stale session.
			for _, e := range event.Errors {
				if strings.Contains(e, "No conversation found with session ID") {
					log.Printf("[task %s] Warm session invalid: %s — clearing for retries", task.ID, e)
					tm.mu.Lock()
					tm.warmSessionID = ""
					tm.mu.Unlock()
					break
				}
			}
			// Final result — extract clean text and cost.
			if len(event.RawResult) > 0 {
				var resultStr string
				if err := json.Unmarshal(event.RawResult, &resultStr); err == nil {
					tm.mu.Lock()
					task.ResultText = resultStr
					task.CostUSD = event.TotalCost
					tm.mu.Unlock()
					log.Printf("[task %s result] cost=$%.4f len=%d", task.ID, event.TotalCost, len(resultStr))
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[task %s] scanner error: %v", task.ID, err)
	}
	if lineCount == 0 {
		log.Printf("[task %s] WARNING: Stream reader got zero lines from %s — process may have hung or crashed before producing output", task.ID, tm.runner.Name)
	}
	log.Printf("[task %s] Stream reader finished (output_len=%d, lines=%d)", task.ID, output.Len(), lineCount)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// isAskingToContinue checks if Claude's result text is asking for permission
// to continue rather than genuinely being done. Used by autopilot mode.
func isAskingToContinue(resultText string) bool {
	lower := strings.ToLower(resultText)
	// Check the last 500 chars — the question is always at the end
	if len(lower) > 500 {
		lower = lower[len(lower)-500:]
	}
	patterns := []string{
		"should i continue",
		"shall i continue",
		"would you like me to continue",
		"would you like me to proceed",
		"should i proceed",
		"shall i proceed",
		"want me to continue",
		"want me to proceed",
		"continue with the remaining",
		"move on to the next",
		"should i move on",
		"ready to proceed",
		"let me know if you'd like",
		"let me know if you want",
		"do you want me to",
		"shall i go ahead",
		"should i go ahead",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// StopTask stops a running task by cancelling the context (kills the process).
func (tm *TaskManager) StopTask(id string) error {
	tm.mu.Lock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.Unlock()
		return fmt.Errorf("task %s not found", id)
	}
	tm.mu.Unlock()

	if task.cancel != nil {
		task.cancel()
	}

	// Wait for process to exit.
	select {
	case <-task.doneCh:
	case <-time.After(10 * time.Second):
		// Force kill if still alive.
		if task.cmd != nil && task.cmd.Process != nil {
			_ = task.cmd.Process.Kill()
		}
	}

	tm.mu.Lock()
	task.Status = TaskStatusStopped
	now := time.Now()
	task.FinishedAt = &now
	tm.persist()
	tm.fireTaskDone(task)
	tm.mu.Unlock()

	return nil
}

// GracefulStopTask sends the runner's exit command via stdin, waits for graceful exit,
// then falls back to kill if the process doesn't exit in time.
func (tm *TaskManager) GracefulStopTask(id string) error {
	tm.mu.RLock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.RUnlock()
		return fmt.Errorf("task %s not found", id)
	}
	tm.mu.RUnlock()

	if task.Status != TaskStatusRunning && task.Status != TaskStatusQueued {
		return fmt.Errorf("task %s is not running", id)
	}

	// Determine exit command: runner config > known defaults > fallback to kill
	// Use the task's runner for exit commands, fall back to global
	exitCmd := task.runner.ExitCommand
	if exitCmd == "" {
		if cmd, ok := exitCommands[task.runner.RunnerID]; ok {
			exitCmd = cmd
		}
	}
	// Final fallback to global runner
	if exitCmd == "" {
		exitCmd = tm.runner.ExitCommand
	}

	// Try graceful exit via stdin
	if exitCmd != "" && task.stdin != nil {
		log.Printf("[task %s] Sending exit command: %s", id, exitCmd)
		_, err := fmt.Fprintf(task.stdin, "%s\n", exitCmd)
		if err != nil {
			log.Printf("[task %s] Failed to write exit command: %v, falling back to kill", id, err)
		} else {
			// Wait up to 10s for graceful exit
			select {
			case <-task.doneCh:
				log.Printf("[task %s] Gracefully exited", id)
				tm.mu.Lock()
				if task.Status == TaskStatusRunning {
					task.Status = TaskStatusStopped
					now := time.Now()
					task.FinishedAt = &now
				}
				tm.persist()
				tm.mu.Unlock()
				return nil
			case <-time.After(10 * time.Second):
				log.Printf("[task %s] Graceful exit timed out, killing process", id)
			}
		}
	}

	// Fall back to regular stop (kill)
	return tm.StopTask(id)
}

// DeleteTask removes a task from history. If running/queued, stops it first.
func (tm *TaskManager) DeleteTask(id string) error {
	tm.mu.RLock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.RUnlock()
		return fmt.Errorf("task %s not found", id)
	}
	isRunning := task.Status == TaskStatusRunning || task.Status == TaskStatusQueued
	tm.mu.RUnlock()

	// Auto-stop running tasks before deleting
	if isRunning {
		log.Printf("[task %s] Stopping running task before delete", id)
		if err := tm.StopTask(id); err != nil {
			log.Printf("[task %s] Stop failed during delete: %v", id, err)
		}
		// Wait briefly for process cleanup
		select {
		case <-task.doneCh:
		case <-time.After(3 * time.Second):
			log.Printf("[task %s] Timed out waiting for process exit during delete", id)
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.tasks, id)
	tm.persist()
	return nil
}

// StopAllTasks stops all running/queued tasks.
func (tm *TaskManager) StopAllTasks() int {
	tm.mu.RLock()
	var ids []string
	for id, t := range tm.tasks {
		if t.Status == TaskStatusRunning || t.Status == TaskStatusQueued {
			ids = append(ids, id)
		}
	}
	tm.mu.RUnlock()

	stopped := 0
	for _, id := range ids {
		if err := tm.StopTask(id); err == nil {
			stopped++
		}
	}
	return stopped
}

// DeleteAllTasks removes all finished tasks from history.
func (tm *TaskManager) DeleteAllTasks() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	deleted := 0
	for id, t := range tm.tasks {
		if t.Status != TaskStatusRunning && t.Status != TaskStatusQueued {
			delete(tm.tasks, id)
			deleted++
		}
	}
	tm.persist()
	return deleted
}

// ResumeTask resumes an existing task in-place with a follow-up prompt.
// Output is concatenated, same task ID is kept, and Claude session is resumed.
func (tm *TaskManager) ResumeTask(id, input string, images []ImageAttachment) (*Task, error) {
	tm.mu.Lock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.Unlock()
		return nil, fmt.Errorf("task %s not found", id)
	}
	if task.Status == TaskStatusRunning || task.Status == TaskStatusQueued {
		tm.mu.Unlock()
		return nil, fmt.Errorf("task %s is already running", id)
	}

	// Append follow-up to conversation history
	turn := ConversationTurn{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now(),
	}
	task.Turns = append(task.Turns, turn)

	// Save new images if any
	if len(images) > 0 {
		newPaths := saveImages(id, images)
		task.ImagePaths = append(task.ImagePaths, newPaths...)
	}

	// Clear output for the new run — turns track conversation history
	task.Output = ""
	task.ResultText = "" // Clear previous result — new one will come
	task.FinishedAt = nil
	task.Status = TaskStatusQueued

	// Re-create channels for the new run
	task.outputCh = make(chan string, 512)
	task.doneCh = make(chan struct{})

	tm.persist()
	tm.mu.Unlock()

	log.Printf("[task %s] Resuming with follow-up (session=%s): %s", id, task.SessionID, input)

	if err := tm.startResume(task, input); err != nil {
		tm.mu.Lock()
		task.Status = TaskStatusFailed
		tm.persist()
		tm.mu.Unlock()
		return task, fmt.Errorf("resume task: %w", err)
	}

	return task, nil
}

// startResume spawns the runner resuming the task's existing session (if supported).
func (tm *TaskManager) startResume(task *Task, prompt string) error {
	// Append image file paths so the AI agent can read them
	if len(task.ImagePaths) > 0 {
		prompt += "\n\n[Attached images — use the Read tool to examine these files]\n"
		for i, p := range task.ImagePaths {
			prompt += fmt.Sprintf("Image %d: %s\n", i+1, p)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel

	// Use task's runner if set, otherwise fall back to global
	runner := task.runner
	if runner.Command == "" {
		runner = tm.runner
	}

	args := buildRunnerArgs(runner, prompt)

	// Resume with session ID if available (follow-up conversation)
	if task.SessionID != "" && runner.RunnerID == "claude" {
		args = append(args, "--resume", task.SessionID)
		// Remove --no-session-persistence — can't resume a non-persisted session
		filtered := args[:0]
		for _, a := range args {
			if a != "--no-session-persistence" {
				filtered = append(filtered, a)
			}
		}
		args = filtered
		log.Printf("[task %s] Resuming session %s", task.ID, task.SessionID)
	} else if task.SessionID != "" && len(runner.ResumeArgs) > 0 {
		// Non-Claude runner with resume support
		for _, ra := range runner.ResumeArgs {
			args = append(args, strings.ReplaceAll(ra, "{sessionId}", task.SessionID))
		}
	} else if runner.RunnerID == "claude" {
		// New task — give it a unique session ID
		args = append(args, "--session-id", uuid.New().String())
	}

	cmd := exec.CommandContext(ctx, runner.Command, args...)
	cmd.Dir = tm.workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Point stdin to /dev/null — Claude CLI blocks when stdin is a pipe.
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	task.cmd = cmd

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start process: %w", err)
	}

	now := time.Now()
	task.StartedAt = &now
	task.Status = TaskStatusRunning

	if tm.runner.OutputMode == "raw" {
		go tm.readRawOutput(task, stdout)
	} else {
		go tm.readStreamJSON(task, stdout)
	}

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[task %s stderr] %s", task.ID, scanner.Text())
		}
	}()

	go func() {
		err := cmd.Wait()
		tm.mu.Lock()
		if task.Status == TaskStatusRunning {
			if err != nil {
				task.Status = TaskStatusFailed
			} else {
				task.Status = TaskStatusFinished
			}
			now := time.Now()
			task.FinishedAt = &now
			// Save the latest result as a conversation turn
			if task.ResultText != "" {
				task.Turns = append(task.Turns, ConversationTurn{
					Role:      "assistant",
					Content:   task.ResultText,
					Timestamp: now,
				})
			}
		}
		tm.persist()
		tm.fireTaskDone(task)
		go saveSessionFile(task, task.runner.Name, tm.workDir)
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	return nil
}

// ListTasks returns info about all tasks.
func (tm *TaskManager) ListTasks() []TaskInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]TaskInfo, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		// Only include last 2000 chars of output in listings.
		output := t.Output
		if len(output) > 2000 {
			output = output[len(output)-2000:]
		}
		result = append(result, TaskInfo{
			ID:          t.ID,
			Title:       t.Title,
			Description: t.Description,
			Status:      t.Status,
			RunnerID:    t.RunnerID,
			SessionID:   t.SessionID,
			Output:      output,
			ResultText:  t.ResultText,
			CostUSD:     t.CostUSD,
			Turns:       t.Turns,
			Source:      t.Source,
			TmuxSession: t.TmuxSession,
			IsAdopted:   t.IsAdopted,
			CreatedAt:   t.CreatedAt,
			StartedAt:   t.StartedAt,
			FinishedAt:  t.FinishedAt,
		})
	}
	return result
}

// GetTask returns a single task by ID.
func (tm *TaskManager) GetTask(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.tasks[id]
	return t, ok
}

// BroadcastControlSignal injects a control signal JSON line into all running tasks' output.
// The mobile app parses these to trigger auto-navigation (e.g. dev_server_ready → Apps tab).
func (tm *TaskManager) BroadcastControlSignal(signal string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, t := range tm.tasks {
		if t.Status == TaskStatusRunning {
			t.Output += "\n" + signal + "\n"
			// Also emit via streaming channel so mobile gets it in real-time
			if t.outputCh != nil {
				select {
				case t.outputCh <- signal:
				default:
				}
			}
			log.Printf("[control] Sent to task %s: %s", t.ID, signal)
		}
	}
}
