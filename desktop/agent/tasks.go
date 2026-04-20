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
	// Model is the LLM backing this runner when it supports a
	// selectable backend (e.g. aider with --model ollama/qwen…).
	// Empty = runner's default. Consumed by spawnAider and the
	// hybrid orchestrator.
	Model string `json:"model,omitempty"`
	// BaseURL points the runner at a non-default LLM endpoint.
	// For ollama-backed runs this is exported as OLLAMA_API_BASE
	// (default http://127.0.0.1:11434).
	BaseURL      string `json:"baseUrl,omitempty"`
	AutoDetected bool   `json:"-"` // true if user never explicitly chose a runner
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
	"claude":       "/exit",
	"codex":        "exit",
	"aider":        "/quit",
	"aider-ollama": "/quit",
	"goose":        "exit",
	"opencode":     "/quit",
}

// builtinRunners defines all known runner configurations.
var builtinRunners = map[string]RunnerConfig{
	"claude": {
		RunnerID: "claude",
		Name:     "Claude Code",
		Command:  "claude",
		// NOTE: --model is intentionally NOT in Args; runImplementer
		// (hybrid.go) and any future yaver-managed spawn prepends it
		// from RunnerConfig.Model / HybridSpec.Model so the user's
		// chosen model wins. Hardcoding "sonnet" here would shadow
		// --implementer claude:opus (sees --model twice, last one
		// wins, depends on CLI parsing — flaky).
		Args:        []string{"-p", "{prompt}", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--tools", "Bash", "--dangerously-skip-permissions"},
		Model:       "claude-sonnet-4-6", // cheap default; HybridSpec / --implementer claude:X overrides
		OutputMode:  "stream-json",
		ExitCommand: "/exit",
	},
	"codex": {
		RunnerID:   "codex",
		Name:       "OpenAI Codex",
		Command:    "codex",
		Args:       []string{"exec", "--full-auto", "{prompt}"},
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
		RunnerID: "ollama",
		Name:     "Ollama",
		Command:  "ollama",
		// {model} is substituted from task.Model via buildRunnerArgs.
		// Falls back to a reasonable small default when the caller
		// doesn't specify one so `runner=ollama` without a model
		// still works. The previous hardcoded "llama3" silently
		// broke CI because ubuntu-latest runners only pulled
		// qwen2.5-coder:1.5b and ollama does not auto-download.
		Args:       []string{"run", "{model}", "{prompt}"},
		Model:      "qwen2.5-coder:1.5b",
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
		RunnerID: "opencode",
		Name:     "OpenCode",
		Command:  "opencode",
		// Newer opencode (sst/opencode) uses `opencode run <message>` for
		// non-interactive mode. The old `--message` flag was removed.
		// --dangerously-skip-permissions is required so it doesn't block
		// on permission prompts when run from the agent.
		Args:       []string{"run", "--dangerously-skip-permissions", "{prompt}"},
		OutputMode: "raw",
	},
	// aider-ollama: Aider driven by a local Ollama model. File-editing
	// stays with aider; the LLM is whatever qwen2.5-coder variant the
	// user has pulled locally. Default model fits a 24 GB Apple
	// Silicon machine; override with RunnerConfig.Model (e.g.
	// "qwen2.5-coder:7b" on smaller boxes). spawnAider / hybrid.go
	// prepend --model + export OLLAMA_API_BASE at invocation time.
	"aider-ollama": {
		RunnerID:    "aider-ollama",
		Name:        "Aider + Qwen (local, free)",
		Command:     "aider",
		Args:        []string{"--yes-always", "--no-git", "--no-pretty", "--no-stream", "--message", "{prompt}"},
		OutputMode:  "raw",
		ExitCommand: "/quit",
		Model:       "ollama_chat/qwen2.5-coder:14b",
		BaseURL:     "http://127.0.0.1:11434",
	},
}

// GetRunnerConfig returns the RunnerConfig for a given runner ID.
// Falls back to defaultRunner if not found.
func GetRunnerConfig(runnerID string) RunnerConfig {
	runnerID = normalizeRunnerID(runnerID)
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
//
//	{"type":"system","subtype":"init",...}
//	{"type":"stream_event","event":{...}} — incremental streaming (text_delta, tool_use, etc.)
//	{"type":"assistant","message":{...}}  — complete assistant message (text or tool_use)
//	{"type":"user","message":{...},"tool_use_result":{...}} — tool execution results (stdout/stderr)
//	{"type":"result","result":"...", "total_cost_usd":0.01,...}
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
	Runner          RunnerStatusInfo `json:"runner"`
	RunningTasks    int              `json:"runningTasks"`
	TotalTasks      int              `json:"totalTasks"`
	RunnerProcesses []RunnerProcess  `json:"runnerProcesses"`
	System          SystemInfo       `json:"system"`
}

// RunnerStatusInfo describes the configured runner.
type RunnerStatusInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Command        string `json:"command"`
	Installed      bool   `json:"installed"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

// SystemInfo describes the host machine.
type SystemInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	MemoryMB int64  `json:"memoryMb,omitempty"`
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
		status := DetectRunnerRuntimeStatus(tm.runner, tm.workDir)
		runnerInfo.AuthConfigured = status.AuthConfigured
		runnerInfo.AuthSource = status.AuthSource
		runnerInfo.Warning = status.Warning
		if status.Error != "" {
			runnerInfo.Error = status.Error
		}
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
	STTProvider     string `json:"sttProvider"`     // "on-device", "openai", "deepgram", "assemblyai"
	TTSEnabled      bool   `json:"ttsEnabled"`      // user wants response read aloud
	TTSProvider     string `json:"ttsProvider"`     // "device" (OS TTS)
	Verbosity       *int   `json:"verbosity"`       // 0-10: response detail level (nil = default 10)
}

// ImageAttachment represents a base64-encoded image sent from mobile.
type ImageAttachment struct {
	Base64   string `json:"base64"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
}

// TaskSliceContract describes the repo/workdir isolation policy for one task slice.
// It is metadata only and must never contain raw secrets such as API keys.
type TaskSliceContract struct {
	RunID            string `json:"runId,omitempty"`
	NodeID           string `json:"nodeId,omitempty"`
	DeviceID         string `json:"deviceId,omitempty"`
	DeviceName       string `json:"deviceName,omitempty"`
	SourceWorkDir    string `json:"sourceWorkDir,omitempty"`
	EffectiveWorkDir string `json:"effectiveWorkDir,omitempty"`
	GitRemote        string `json:"gitRemote,omitempty"`
	GitBranch        string `json:"gitBranch,omitempty"`
	GitCommit        string `json:"gitCommit,omitempty"`
	IsolationMode    string `json:"isolationMode,omitempty"`
}

type TaskCreateOptions struct {
	WorkDir       string
	SliceContract *TaskSliceContract

	// Guest policy fields are applied before startProcess runs so per-task
	// guards (e.g. autoSwitchProject skip) can see GuestUserID atomically.
	// Setting these after CreateTaskWithOptions returns is a race.
	GuestUserID                 string
	GuestUseHostAPIKeys         bool
	GuestAllowGuestProvidedKeys bool
	GuestRequireIsolation       bool
	GuestCPULimitPercent        *int
	GuestRAMLimitMB             *int
	GuestSharedStorageMounts    []string
}

type Task struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Status      TaskStatus         `json:"status"`
	Source      string             `json:"source,omitempty"`      // "mobile", "mcp", "cli"
	GuestUserID string             `json:"guestUserId,omitempty"` // set when task created by a guest
	Model       string             `json:"model,omitempty"`
	RunnerID    string             `json:"runnerId,omitempty"` // which runner is executing this task
	SessionID   string             `json:"session_id,omitempty"`
	Output      string             `json:"output"`
	ResultText  string             // Extracted clean result text from Claude
	CostUSD     float64            // Total API cost
	Turns       []ConversationTurn // Full conversation history
	CreatedAt   time.Time          `json:"created_at"`
	StartedAt   *time.Time         `json:"started_at,omitempty"`
	FinishedAt  *time.Time         `json:"finished_at,omitempty"`

	WorkDir     string `json:"workDir,omitempty"`     // per-task workDir (auto-detected from prompt)
	TmuxSession string `json:"tmuxSession,omitempty"` // tmux session name (for adopted sessions)
	IsAdopted   bool   `json:"isAdopted,omitempty"`   // true if adopted from an existing tmux session

	// Chained tasks: execute in order, next starts when previous completes
	ChainID    string `json:"chainId,omitempty"`    // shared ID linking tasks in a chain
	ChainOrder int    `json:"chainOrder,omitempty"` // 0-based position in the chain

	// Auto-retry: retry failed tasks with error context
	AutoRetry      bool `json:"autoRetry,omitempty"`      // enable auto-retry on task failure
	AutoRetryCount int  `json:"autoRetryCount,omitempty"` // how many task-level retries so far
	AutoRetryMax   int  `json:"autoRetryMax,omitempty"`   // max task-level retries (default 3)

	// Speech context from mobile — passed through to the AI runner prompt
	SpeechContext *SpeechContext `json:"-"`

	// Image paths saved to disk for this task (not persisted in tasks.json)
	ImagePaths []string `json:"-"`

	SliceContract *TaskSliceContract `json:"sliceContract,omitempty"`

	// Guest execution policy snapshot resolved at task creation time.
	GuestUseHostAPIKeys         bool     `json:"-"`
	GuestAllowGuestProvidedKeys bool     `json:"-"`
	GuestRequireIsolation       bool     `json:"-"`
	GuestCPULimitPercent        *int     `json:"-"`
	GuestRAMLimitMB             *int     `json:"-"`
	GuestSharedStorageMounts    []string `json:"-"`

	runner     RunnerConfig // the runner config used for this task (not persisted)
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stdin      io.WriteCloser
	outputCh   chan string
	doneCh     chan struct{}
	retryCount int // Number of auto-restart attempts so far
}

func formatTaskSliceContract(contract *TaskSliceContract) string {
	if contract == nil {
		return ""
	}
	var lines []string
	lines = append(lines, "\n\n[Task Slice Contract]")
	if contract.RunID != "" || contract.NodeID != "" {
		lines = append(lines, fmt.Sprintf("Graph run: %s  Node: %s", firstNonEmpty(contract.RunID, "n/a"), firstNonEmpty(contract.NodeID, "n/a")))
	}
	if contract.DeviceID != "" || contract.DeviceName != "" {
		lines = append(lines, fmt.Sprintf("Assigned machine: %s (%s)", firstNonEmpty(contract.DeviceName, contract.DeviceID, "unknown"), firstNonEmpty(contract.DeviceID, "unknown")))
	}
	if contract.SourceWorkDir != "" {
		lines = append(lines, "Source work dir: "+contract.SourceWorkDir)
	}
	if contract.EffectiveWorkDir != "" {
		lines = append(lines, "Effective work dir: "+contract.EffectiveWorkDir)
	}
	if contract.GitBranch != "" || contract.GitCommit != "" {
		lines = append(lines, fmt.Sprintf("Git branch: %s  Commit: %s", firstNonEmpty(contract.GitBranch, "unknown"), firstNonEmpty(contract.GitCommit, "unknown")))
	}
	if contract.GitRemote != "" {
		lines = append(lines, "Git remote: "+contract.GitRemote)
	}
	if contract.IsolationMode != "" {
		lines = append(lines, "Isolation mode: "+contract.IsolationMode)
	}
	if contract.IsolationMode == "remote-repo-contract" {
		lines = append(lines,
			"You are already running on the assigned machine inside its local isolated checkout.",
			"Do not use SSH, relay hops, or any second remote-control step to reach that machine.",
			"Treat the current filesystem as the assigned machine's workspace and make the change directly here.")
	}
	lines = append(lines,
		"Operate only inside the effective work dir for this slice.",
		"Do not assume write access to sibling slices or the developer's main worktree.",
		"Prefer producing coherent commits or diffs within this slice so the orchestrator can merge safely.")
	return strings.Join(lines, "\n")
}

// TaskInfo is the JSON-safe subset returned in listings.
type TaskInfo struct {
	ID             string             `json:"id"`
	Title          string             `json:"title"`
	Description    string             `json:"description"`
	Status         TaskStatus         `json:"status"`
	RunnerID       string             `json:"runnerId,omitempty"`
	SessionID      string             `json:"sessionId,omitempty"`
	Output         string             `json:"output,omitempty"`
	ResultText     string             `json:"resultText,omitempty"`
	CostUSD        float64            `json:"costUsd,omitempty"`
	Turns          []ConversationTurn `json:"turns,omitempty"`
	Source         string             `json:"source,omitempty"`
	TmuxSession    string             `json:"tmuxSession,omitempty"`
	IsAdopted      bool               `json:"isAdopted,omitempty"`
	CreatedAt      time.Time          `json:"createdAt"`
	StartedAt      *time.Time         `json:"startedAt,omitempty"`
	FinishedAt     *time.Time         `json:"finishedAt,omitempty"`
	ChainID        string             `json:"chainId,omitempty"`
	ChainOrder     int                `json:"chainOrder,omitempty"`
	AutoRetry      bool               `json:"autoRetry,omitempty"`
	AutoRetryCount int                `json:"autoRetryCount,omitempty"`
	AutoRetryMax   int                `json:"autoRetryMax,omitempty"`
}

// TaskManager manages the lifecycle of tasks.
type TaskManager struct {
	mu          sync.RWMutex
	tasks       map[string]*Task
	workDir     string
	store       *TaskStore
	runner      RunnerConfig
	TmuxMgr     *TmuxManager  // manages tmux session adoption (nil if tmux unavailable)
	Sandbox     SandboxConfig // Command sandbox configuration
	WaitForSlot bool          // If true, wait for other Claude Code sessions to finish before starting
	DummyMode   bool          // If true, use fake responses instead of launching a real runner

	// Container isolation (optional — set by httpserver when enabled)
	ContainerRunner    *ContainerRunner
	ContainerizeGuests bool
	ContainerizeHost   bool
	ContainerCPU       string
	ContainerMemory    string
	ContainerImage     string
	ContainerNetwork   string   // "host" (default), "bridge", "none"
	ContainerReadOnly  bool     // read-only root filesystem
	ContainerMounts    []string // extra volume mounts from config

	// Callbacks (set after construction)
	OnTaskDone func(task *Task) // called when a task finishes (completed/failed/stopped)

	// Convex reporting (set after construction)
	ConvexURL  string
	AuthToken  string
	DeviceID   string
	OwnerEmail string // for dev logging

	// Warm session: forked at startup, reused for all tasks
	warmSessionID string    // Claude session ID from warmup
	warmCreatedAt time.Time // when the warm session was established
	warmPID       int       // PID of the warmup process (0 if not running)
	warmReady     bool      // true once warmup completed successfully
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
	return tm.CreateTaskWithOptions(title, description, model, source, runnerID, customCommand, images, TaskCreateOptions{}, speechCtx...)
}

func (tm *TaskManager) CreateTaskWithOptions(title, description, model, source, runnerID, customCommand string, images []ImageAttachment, opts TaskCreateOptions, speechCtx ...*SpeechContext) (*Task, error) {
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
		normalizedRunnerID := normalizeRunnerID(runnerID)
		currentRunnerID := normalizeRunnerID(tm.runner.RunnerID)
		if normalizedRunnerID != "" && normalizedRunnerID != currentRunnerID {
			if r, ok := builtinRunners[normalizedRunnerID]; ok {
				taskRunner = r
			} else if normalizedRunnerID == "custom" || normalizedRunnerID == currentRunnerID {
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
		ID:                          id,
		Title:                       title,
		Description:                 description,
		Status:                      TaskStatusQueued,
		Source:                      source,
		Model:                       model,
		RunnerID:                    taskRunner.RunnerID,
		runner:                      taskRunner,
		CreatedAt:                   now,
		outputCh:                    make(chan string, 512),
		doneCh:                      make(chan struct{}),
		WorkDir:                     strings.TrimSpace(opts.WorkDir),
		SliceContract:               opts.SliceContract,
		GuestUserID:                 opts.GuestUserID,
		GuestUseHostAPIKeys:         opts.GuestUseHostAPIKeys,
		GuestAllowGuestProvidedKeys: opts.GuestAllowGuestProvidedKeys,
		GuestRequireIsolation:       opts.GuestRequireIsolation,
		GuestCPULimitPercent:        opts.GuestCPULimitPercent,
		GuestRAMLimitMB:             opts.GuestRAMLimitMB,
		GuestSharedStorageMounts:    append([]string{}, opts.GuestSharedStorageMounts...),
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

func taskEnv(task *Task) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+expandedPath())
	if task != nil && task.GuestUserID != "" && !task.GuestUseHostAPIKeys {
		filtered := make([]string, 0, len(env))
		for _, entry := range env {
			name := entry
			if i := strings.IndexByte(entry, '='); i >= 0 {
				name = entry[:i]
			}
			blocked := false
			for _, secret := range sharedSecretEnvVars {
				if name == secret {
					blocked = true
					break
				}
			}
			if !blocked {
				filtered = append(filtered, entry)
			}
		}
		return filtered
	}
	existing := make(map[string]int, len(env))
	for idx, entry := range env {
		name := entry
		value := ""
		if eq := strings.IndexByte(entry, '='); eq >= 0 {
			name = entry[:eq]
			value = entry[eq+1:]
		}
		if value != "" {
			existing[name] = idx + 1
		}
	}
	for name, value := range collectHostSecretEnv(sharedSecretEnvVars) {
		if pos, ok := existing[name]; ok && pos > 0 {
			continue
		}
		replaced := false
		for i, entry := range env {
			if strings.HasPrefix(entry, name+"=") {
				env[i] = name + "=" + value
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func guestContainerCPULimit(task *Task) string {
	if task == nil || task.GuestCPULimitPercent == nil || *task.GuestCPULimitPercent <= 0 {
		return ""
	}
	cpus := float64(runtime.NumCPU()) * float64(*task.GuestCPULimitPercent) / 100.0
	if cpus < 0.1 {
		cpus = 0.1
	}
	return fmt.Sprintf("%.2f", cpus)
}

func guestContainerMemoryLimit(task *Task) string {
	if task == nil || task.GuestRAMLimitMB == nil || *task.GuestRAMLimitMB <= 0 {
		return ""
	}
	return fmt.Sprintf("%dm", *task.GuestRAMLimitMB)
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
	args := []string{"--version"}
	switch filepath.Base(path) {
	case "sh", "bash", "zsh", "dash":
		args = []string{"-c", "exit 0"}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "PATH="+expandedPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s found but not working: %v (output: %s)", command, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		log.Printf("[runner-check] %s at %s — ok", command, path)
	} else {
		log.Printf("[runner-check] %s at %s — %s", command, path, strings.TrimSpace(string(out)))
	}
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
// Supported placeholders:
//
//	{prompt} — always required
//	{model}  — substituted from the runner config's Model field (used
//	           by the ollama runner so `yaver tasks --runner ollama
//	           --model qwen2.5-coder:7b` lands on the right model
//	           instead of the previous hardcoded "llama3").
func buildRunnerArgs(runner RunnerConfig, prompt string) []string {
	args := make([]string, len(runner.Args))
	for i, a := range runner.Args {
		a = strings.ReplaceAll(a, "{prompt}", prompt)
		if runner.Model != "" {
			a = strings.ReplaceAll(a, "{model}", runner.Model)
		}
		args[i] = a
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
	// This enables "start BentoApp" from Yaver mobile when serving from ~.
	//
	// Security: never auto-switch for guest tasks — the guest prompt prefix
	// pins the task to the host's workdir, and allowing prompt keywords to
	// redirect the task cwd into a neighboring project would let a guest
	// traverse host projects they were not granted.
	if task.GuestUserID == "" {
		tm.autoSwitchProject(task, prompt)
	}

	// System prompt: behave as a remote terminal agent, tailored to the task source.
	prompt += taskSourcePromptSuffix(task.Source)

	// Inject Yaver dev server proxy context so the runner knows to use /dev/start.
	// This is critical: the runner must NEVER output exp:// URLs or tell the user
	// to install Expo Go. Everything flows through the Yaver P2P channel.
	// Use per-task workDir if detected, otherwise global
	contextDir := tm.workDir
	if task.WorkDir != "" {
		contextDir = task.WorkDir
	}
	prompt += formatTaskSliceContract(task.SliceContract)
	prompt += yaverDevServerContext(contextDir)

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
	// Per-task model override flows into {model} substitution for
	// runners that template it (currently the ollama runner). Must
	// happen before buildRunnerArgs or the placeholder lands in the
	// argv literally.
	if task.Model != "" {
		runner.Model = task.Model
	}
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

	// Determine working directory
	taskDir := tm.workDir
	if task.WorkDir != "" && task.GuestUserID == "" {
		taskDir = task.WorkDir
	}
	if err := CheckRunnerReady(runner, taskDir); err != nil {
		cancel()
		return fmt.Errorf("runner not ready: %w", err)
	}

	// ── Container execution (optional) ──────────────────────────────
	// If containerization is enabled for this task type, run inside Docker.
	useContainer := false
	if tm.ContainerRunner != nil && tm.ContainerRunner.IsAvailable() {
		if task.GuestUserID != "" && (tm.ContainerizeGuests || task.GuestRequireIsolation) {
			useContainer = true
		} else if task.GuestUserID == "" && tm.ContainerizeHost {
			useContainer = true
		}
		// Auto-build image on first use if not ready
		if useContainer && !tm.ContainerRunner.IsImageReady() {
			buildCtx, buildCancel := context.WithTimeout(ctx, 15*time.Minute)
			if !tm.ContainerRunner.AutoBuild(buildCtx) {
				useContainer = false // fall back to direct execution
			}
			buildCancel()
		}
	}

	if useContainer {
		log.Printf("[task %s] Launching in container: %s (dir=%s)", task.ID, runner.Command, taskDir)
		containerCmd := append([]string{runner.Command}, args...)
		opts := ContainerTaskOpts{
			TaskID:      task.ID,
			ProjectDir:  taskDir,
			Command:     containerCmd,
			Env:         CollectAPIKeysForTask(task),
			NetworkMode: tm.ContainerNetwork,
			ReadOnly:    tm.ContainerReadOnly,
		}
		if tm.ContainerCPU != "" {
			opts.CPULimit = tm.ContainerCPU
		}
		if tm.ContainerMemory != "" {
			opts.MemoryLimit = tm.ContainerMemory
		}
		if task.GuestUserID != "" {
			if cpuLimit := guestContainerCPULimit(task); cpuLimit != "" {
				opts.CPULimit = cpuLimit
			}
			if memoryLimit := guestContainerMemoryLimit(task); memoryLimit != "" {
				opts.MemoryLimit = memoryLimit
			}
			if task.GuestRequireIsolation {
				if opts.NetworkMode == "" || opts.NetworkMode == "host" {
					opts.NetworkMode = "bridge"
				}
				opts.ReadOnly = true
			}
		}
		// Check for project-specific Dockerfile.yaver first, then config override
		if projectImage := tm.ContainerRunner.DetectProjectImage(ctx, taskDir); projectImage != "" {
			opts.CustomImage = projectImage
		} else if tm.ContainerImage != "" {
			opts.CustomImage = tm.ContainerImage
		}
		opts.ExtraMounts = append([]string{}, tm.ContainerMounts...)
		if len(task.GuestSharedStorageMounts) > 0 {
			opts.ExtraMounts = append(opts.ExtraMounts, task.GuestSharedStorageMounts...)
		}

		cmd, stdout, stderr, err := tm.ContainerRunner.RunTask(ctx, opts)
		if err != nil {
			cancel()
			return fmt.Errorf("container start: %w", err)
		}

		task.cmd = cmd
		now := time.Now()
		task.StartedAt = &now
		task.Status = TaskStatusRunning
		trackForkedPID(cmd.Process.Pid)

		if runner.OutputMode == "raw" {
			go tm.readRawOutput(task, stdout)
		} else {
			go tm.readStreamJSON(task, stdout)
		}
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				log.Printf("[task %s] [container stderr] %s", task.ID, scanner.Text())
			}
		}()
	} else {
		// ── Direct execution (default) ──────────────────────────────────

		cmd := exec.CommandContext(ctx, runner.Command, args...)
		cmd.Dir = taskDir

		// Ensure common tool paths are in PATH for background processes.
		cmd.Env = taskEnv(task)

		log.Printf("[task %s] Launching: %s %v (dir=%s)", task.ID, runner.Command, args[:2], taskDir)

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
	} // end else (direct execution)

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
		err := task.cmd.Wait()
		if task.cmd.Process != nil {
			untrackForkedPID(task.cmd.Process.Pid)
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
	prompt += taskSourcePromptSuffix(task.Source)

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

func taskSourcePromptSuffix(source string) string {
	switch source {
	case "mcp":
		return "\n\nYou are running tasks via MCP from an AI agent. Show what you are doing step by step. Use only terminal commands. Be concise. Format output in markdown."
	case "cli":
		return "\n\nYou are running tasks from a remote CLI terminal. Show what you are doing step by step. Use only terminal commands. Be concise. Format output in markdown."
	default:
		return "\n\nYou are running tasks from a remote mobile device. Show what you are doing step by step. Use only terminal commands. Be concise." + mobileTaskResponseContext()
	}
}

func mobileTaskResponseContext() string {
	return `

[Mobile response contract]
The human is reading this on a phone. Optimize for fast scanning, not rich markdown.
- Keep progress updates short and concrete. Prefer one short sentence over long narration.
- Start the final answer with a plain-language outcome sentence.
- After that, use at most three short bullets chosen from: changed, checked, blocked, next.
- Do NOT use tables.
- Do NOT dump long command outputs unless they are essential to understand a failure.
- If a command succeeds, summarize the result in plain language instead of pasting the whole output.
- Keep markdown light: short bullets and inline code are fine; avoid heavy heading stacks and long fenced blocks unless truly necessary.
- Stay agent-agnostic in wording. Do not mention a specific coding assistant brand unless the user asked about it.
- Never hide important failures, commands, or file changes. Be concise without dropping critical information.`
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
			ID:             t.ID,
			Title:          t.Title,
			Description:    t.Description,
			Status:         t.Status,
			RunnerID:       t.RunnerID,
			SessionID:      t.SessionID,
			Output:         output,
			ResultText:     t.ResultText,
			CostUSD:        t.CostUSD,
			Turns:          t.Turns,
			Source:         t.Source,
			TmuxSession:    t.TmuxSession,
			IsAdopted:      t.IsAdopted,
			CreatedAt:      t.CreatedAt,
			StartedAt:      t.StartedAt,
			FinishedAt:     t.FinishedAt,
			ChainID:        t.ChainID,
			ChainOrder:     t.ChainOrder,
			AutoRetry:      t.AutoRetry,
			AutoRetryCount: t.AutoRetryCount,
			AutoRetryMax:   t.AutoRetryMax,
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

// ── Chained Tasks ──────────────────────────────────────────────────

// CreateChainedTasks creates multiple tasks linked by a chain ID.
// Tasks execute sequentially: the next starts when the previous completes successfully.
// Only the first task starts immediately; the rest stay queued.
func (tm *TaskManager) CreateChainedTasks(tasks []ChainedTaskInput, model, source, runnerID string, autoRetry bool) ([]*Task, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks provided")
	}

	chainID := uuid.New().String()[:8]
	var created []*Task

	for i, input := range tasks {
		var taskRunner RunnerConfig
		if runnerID != "" {
			if r, ok := builtinRunners[runnerID]; ok {
				taskRunner = r
			} else {
				taskRunner = tm.runner
			}
		} else {
			taskRunner = tm.runner
		}

		if !tm.DummyMode {
			if err := CheckRunnerBinary(taskRunner.Command); err != nil {
				return created, fmt.Errorf("runner not ready: %w", err)
			}
		}

		if source == "" {
			source = "mobile"
		}

		id := uuid.New().String()[:8]
		now := time.Now()
		retryMax := 0
		if autoRetry {
			retryMax = 3
		}
		task := &Task{
			ID:           id,
			Title:        input.Title,
			Description:  input.Description,
			Status:       TaskStatusQueued,
			Source:       source,
			Model:        model,
			RunnerID:     taskRunner.RunnerID,
			runner:       taskRunner,
			CreatedAt:    now,
			outputCh:     make(chan string, 512),
			doneCh:       make(chan struct{}),
			ChainID:      chainID,
			ChainOrder:   i,
			AutoRetry:    autoRetry,
			AutoRetryMax: retryMax,
			Turns: []ConversationTurn{
				{Role: "user", Content: input.Title, Timestamp: now},
			},
		}

		tm.mu.Lock()
		tm.tasks[id] = task
		tm.persist()
		tm.mu.Unlock()

		created = append(created, task)

		// Only start the first task; the rest wait for chain progression
		if i == 0 {
			if !tm.DummyMode {
				log.Printf("[chain %s] Starting first task %s: %s", chainID, id, input.Title)
				if err := tm.startProcess(task); err != nil {
					log.Printf("[chain %s] Failed to start first task %s: %v", chainID, id, err)
					task.Status = TaskStatusFailed
					tm.mu.Lock()
					tm.persist()
					tm.mu.Unlock()
				}
			} else {
				go tm.runDummyTask(task)
			}
		} else {
			log.Printf("[chain %s] Task %s queued at position %d: %s", chainID, id, i, input.Title)
		}
	}

	return created, nil
}

// ChainedTaskInput represents a single task in a chain creation request.
type ChainedTaskInput struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// advanceChain starts the next queued task in a chain after one completes.
// Called from OnTaskDone callback.
func (tm *TaskManager) advanceChain(completedTask *Task) {
	if completedTask.ChainID == "" {
		return
	}

	// Only advance if the task completed successfully
	if completedTask.Status != TaskStatusFinished {
		log.Printf("[chain %s] Task %s finished with status %s — chain stopped", completedTask.ChainID, completedTask.ID, completedTask.Status)
		return
	}

	nextOrder := completedTask.ChainOrder + 1

	tm.mu.RLock()
	var nextTask *Task
	for _, t := range tm.tasks {
		if t.ChainID == completedTask.ChainID && t.ChainOrder == nextOrder && t.Status == TaskStatusQueued {
			nextTask = t
			break
		}
	}
	tm.mu.RUnlock()

	if nextTask == nil {
		log.Printf("[chain %s] Chain complete — no more tasks after position %d", completedTask.ChainID, completedTask.ChainOrder)
		return
	}

	log.Printf("[chain %s] Advancing to task %s (position %d): %s", completedTask.ChainID, nextTask.ID, nextOrder, nextTask.Title)

	if tm.DummyMode {
		go tm.runDummyTask(nextTask)
		return
	}

	if err := tm.startProcess(nextTask); err != nil {
		log.Printf("[chain %s] Failed to start next task %s: %v", completedTask.ChainID, nextTask.ID, err)
		nextTask.Status = TaskStatusFailed
		tm.mu.Lock()
		tm.persist()
		tm.mu.Unlock()
	}
}

// GetChainStatus returns the status of all tasks in a chain.
func (tm *TaskManager) GetChainStatus(chainID string) []TaskInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var chain []TaskInfo
	for _, t := range tm.tasks {
		if t.ChainID == chainID {
			output := t.Output
			if len(output) > 2000 {
				output = output[len(output)-2000:]
			}
			chain = append(chain, TaskInfo{
				ID:         t.ID,
				Title:      t.Title,
				Status:     t.Status,
				ChainID:    t.ChainID,
				ChainOrder: t.ChainOrder,
				CreatedAt:  t.CreatedAt,
				StartedAt:  t.StartedAt,
				FinishedAt: t.FinishedAt,
				ResultText: t.ResultText,
				CostUSD:    t.CostUSD,
			})
		}
	}

	// Sort by chain order
	for i := 0; i < len(chain); i++ {
		for j := i + 1; j < len(chain); j++ {
			if chain[j].ChainOrder < chain[i].ChainOrder {
				chain[i], chain[j] = chain[j], chain[i]
			}
		}
	}
	return chain
}

// ── Auto-Retry (task-level) ────────────────────────────────────────

// autoRetryTask retries a failed task by creating a new run with error context.
// Returns true if retry was initiated, false if retries exhausted.
func (tm *TaskManager) autoRetryTask(task *Task) bool {
	if !task.AutoRetry || task.AutoRetryMax <= 0 {
		return false
	}
	if task.AutoRetryCount >= task.AutoRetryMax {
		log.Printf("[retry] Task %s exhausted all %d retries", task.ID, task.AutoRetryMax)
		return false
	}

	task.AutoRetryCount++
	log.Printf("[retry] Task %s failed — auto-retrying (attempt %d/%d)", task.ID, task.AutoRetryCount, task.AutoRetryMax)

	// Build retry prompt with error context
	lastOutput := task.Output
	if len(lastOutput) > 2000 {
		lastOutput = lastOutput[len(lastOutput)-2000:]
	}
	retryPrompt := fmt.Sprintf(
		"The previous attempt failed. Here is the error output:\n\n```\n%s\n```\n\nPlease fix the issues and try again. Original task: %s",
		lastOutput, task.Title,
	)

	// Reset task state for retry
	task.Output = fmt.Sprintf("⟳ Auto-retry attempt %d/%d...\n\n", task.AutoRetryCount, task.AutoRetryMax)
	task.ResultText = ""
	task.Status = TaskStatusQueued
	task.FinishedAt = nil
	task.outputCh = make(chan string, 512)
	task.doneCh = make(chan struct{})

	// Update the prompt for this retry
	task.Turns = append(task.Turns, ConversationTurn{
		Role:      "user",
		Content:   retryPrompt,
		Timestamp: time.Now(),
	})

	tm.mu.Lock()
	tm.persist()
	tm.mu.Unlock()

	if err := tm.startProcess(task); err != nil {
		log.Printf("[retry] Task %s auto-retry failed to start: %v", task.ID, err)
		tm.mu.Lock()
		task.Status = TaskStatusFailed
		now := time.Now()
		task.FinishedAt = &now
		tm.persist()
		tm.mu.Unlock()
		return false
	}

	return true
}

// ── Task Summary ───────────────────────────────────────────────────

// TaskSummary provides a digest of task activity for a time period.
type TaskSummary struct {
	Period    string            `json:"period"` // e.g. "last 24 hours"
	Total     int               `json:"total"`
	Completed int               `json:"completed"`
	Failed    int               `json:"failed"`
	Running   int               `json:"running"`
	Queued    int               `json:"queued"`
	TotalCost float64           `json:"totalCost"`
	Items     []TaskSummaryItem `json:"items"`
}

// TaskSummaryItem is a brief description of a completed task.
type TaskSummaryItem struct {
	Title    string     `json:"title"`
	Status   TaskStatus `json:"status"`
	CostUSD  float64    `json:"costUsd,omitempty"`
	Duration int        `json:"durationSec,omitempty"` // seconds
}

// GetSummary returns a summary of tasks completed in the given time window.
func (tm *TaskManager) GetSummary(since time.Time) TaskSummary {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	summary := TaskSummary{Period: fmt.Sprintf("since %s", since.Format("2006-01-02 15:04"))}

	for _, t := range tm.tasks {
		if t.CreatedAt.Before(since) {
			continue
		}
		summary.Total++
		switch t.Status {
		case TaskStatusFinished:
			summary.Completed++
		case TaskStatusFailed:
			summary.Failed++
		case TaskStatusRunning:
			summary.Running++
		case TaskStatusQueued:
			summary.Queued++
		}
		summary.TotalCost += t.CostUSD

		if t.Status == TaskStatusFinished || t.Status == TaskStatusFailed {
			dur := 0
			if t.StartedAt != nil && t.FinishedAt != nil {
				dur = int(t.FinishedAt.Sub(*t.StartedAt).Seconds())
			}
			titlePreview := t.Title
			if len(titlePreview) > 80 {
				titlePreview = titlePreview[:80] + "..."
			}
			summary.Items = append(summary.Items, TaskSummaryItem{
				Title:    titlePreview,
				Status:   t.Status,
				CostUSD:  t.CostUSD,
				Duration: dur,
			})
		}
	}

	return summary
}

// GenerateSummaryText creates a human-readable summary for notifications.
func (tm *TaskManager) GenerateSummaryText(since time.Time) string {
	s := tm.GetSummary(since)
	if s.Total == 0 {
		return "No tasks in the last 24 hours."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 %d tasks: %d completed, %d failed", s.Total, s.Completed, s.Failed)
	if s.Running > 0 {
		fmt.Fprintf(&b, ", %d running", s.Running)
	}
	if s.Queued > 0 {
		fmt.Fprintf(&b, ", %d queued", s.Queued)
	}
	if s.TotalCost > 0 {
		fmt.Fprintf(&b, " ($%.2f)", s.TotalCost)
	}
	b.WriteString("\n\n")

	for _, item := range s.Items {
		icon := "✅"
		if item.Status == TaskStatusFailed {
			icon = "❌"
		}
		fmt.Fprintf(&b, "%s %s", icon, item.Title)
		if item.Duration > 0 {
			fmt.Fprintf(&b, " (%ds)", item.Duration)
		}
		b.WriteString("\n")
	}

	return b.String()
}
