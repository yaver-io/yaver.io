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
	TaskStatusReview   TaskStatus = "review"
	TaskStatusStopped  TaskStatus = "stopped"
	TaskStatusFinished TaskStatus = "completed"
	TaskStatusFailed   TaskStatus = "failed"
)

// RunnerConfig describes how to invoke one of yaver's three first-class
// runners: claude-code, codex, or opencode.
type RunnerConfig struct {
	RunnerID        string   `json:"runnerId"`
	Name            string   `json:"name"`
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	OutputMode      string   `json:"outputMode"` // "stream-json" or "raw"
	ResumeSupported bool     `json:"resumeSupported"`
	ResumeArgs      []string `json:"resumeArgs,omitempty"`
	ExitCommand     string   `json:"exitCommand,omitempty"` // e.g. "/exit" for Claude Code, "/quit" for opencode
	// Model overrides the runner's default LLM. For claude/codex this
	// is forwarded as `--model`; for opencode it's an opencode model
	// id. Empty = runner's default.
	Model string `json:"model,omitempty"`
	// Mode is a runner-specific subcommand selector. Currently only
	// honored by opencode where it maps to `--agent <mode>` (build /
	// plan / any custom agent the user has defined in their
	// opencode.json config). Empty = runner default. Other runners
	// ignore it.
	Mode         string `json:"mode,omitempty"`
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
		// Plain mobile tasks can legitimately start from the agent's
		// global work-dir (often /root on ephemeral boxes) rather than a
		// git repo. Without this, Claude aborts before running even a
		// trivial command like `ls` with:
		//   "Not inside a trusted directory and --skip-git-repo-check was not specified."
		// Permission bypass is unrelated; it only controls edit
		// approvals. We still want Claude to start in non-repo dirs for
		// shell-like mobile flows.
		"--skip-git-repo-check",
		"--permission-mode", "bypassPermissions",
	},
	OutputMode:      "stream-json",
	ResumeSupported: false,
	ResumeArgs:      []string{"--resume", "{sessionId}"},
	ExitCommand:     "/exit",
}

// exitCommands maps runner IDs to their graceful exit commands.
// Keys are the agent-internal canonical ids (post-normalizeRunnerID).
var exitCommands = map[string]string{
	"claude":   "/exit",
	"codex":    "exit",
	"opencode": "/quit",
	"glm":      "/exit", // claude binary
}

var activeTaskManager *TaskManager

func ActiveTaskManager() *TaskManager {
	return activeTaskManager
}

// builtinRunners defines yaver's three first-class runner configurations.
// claude-code, codex, and opencode are the only runners we ship support
// for; everything else (Ollama, OpenRouter, GLM, ZAI, …) reaches the
// system through opencode's BYOK provider config rather than yaver
// shipping a dedicated wrapper for each CLI.
var builtinRunners = map[string]RunnerConfig{
	"claude": {
		RunnerID: "claude",
		Name:     "Claude Code",
		Command:  "claude",
		// NOTE: --model is intentionally NOT in Args; yaver-managed
		// spawn paths prepend it from RunnerConfig.Model so the user's
		// chosen model wins. Hardcoding "sonnet" here would shadow
		// per-task model overrides (sees --model twice, last one wins,
		// depends on CLI parsing — flaky).
		// claude-cli 2.1.138 (verified on the user's Mac mini)
		// REJECTS --skip-git-repo-check with "error: unknown
		// option" and exits non-zero. The agent's claude task
		// retries 4 times then surfaces "Agent process crashed"
		// with no useful stderr. Removing the flag is safe:
		// claude-cli 2.x runs in non-git dirs without it (verified
		// in /Users/pokayoke, a non-git home dir). --dangerously-
		// skip-permissions is the yolo flag per
		// feedback_runners_always_dangerous; it's been stable in
		// claude-cli since the 1.x line.
		Args: []string{"-p", "{prompt}", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--tools", "Bash", "--dangerously-skip-permissions"},
		// claude default = opus. Mirrors web/DevicesView.DEFAULT_MODEL_BY_RUNNER
		// and mobile/DeviceContext.DEFAULT_MODEL_BY_RUNNER — surfaces stay in
		// lockstep so a feedback task arriving with task.Model="" lands on
		// opus regardless of which client picked it. Per-task --model still
		// wins because callers prepend it and CLI last-flag-wins applies.
		Model:       "claude-opus-4-7",
		OutputMode:  "stream-json",
		ExitCommand: "/exit",
	},
	"codex": {
		RunnerID: "codex",
		Name:     "OpenAI Codex",
		Command:  "codex",
		// `--skip-git-repo-check` was suppressing codex's workspace
		// detection, leaving its workspace-write sandbox at
		// /root/.codex/.tmp/plugins and rejecting every write to the
		// real project as "outside writable root / Read-only file
		// system" (user verified, mobile feedback flow). Dropped: the
		// agent already sets cmd.Dir = task.WorkDir, so codex walks up
		// from there to the git root and sets workspace-write
		// correctly. Verified: this same prompt that previously failed
		// patched app/index.tsx (#0f172a → #22c55e) on yaver-test-
		// ephemeral once the flag was removed.
		Args: []string{"exec", "--full-auto", "{prompt}"},
		// Keep this aligned with the backend model catalogue used by
		// /agent/runners. Older "gpt-5.3-codex" ChatGPT-account runs now
		// fail with "model is not supported"; the current default exposed
		// by Codex is gpt-5.4.
		Model:      "gpt-5.4",
		OutputMode: "raw",
	},
	"opencode": {
		RunnerID: "opencode",
		Name:     "opencode",
		Command:  "opencode",
		// Newer opencode (sst/opencode) uses `opencode run <message>` for
		// non-interactive mode. The old `--message` flag was removed.
		// --dangerously-skip-permissions is required so it doesn't block
		// on permission prompts when run from the agent.
		Args:        []string{"run", "--dangerously-skip-permissions", "{prompt}"},
		OutputMode:  "raw",
		ExitCommand: "/quit",
	},
	// GLM (z.ai) is NOT a separate CLI — it is the `claude` binary pointed
	// at z.ai's Anthropic-compatible endpoint (https://api.z.ai/api/anthropic)
	// via ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN, which provider_keys.go
	// injects per-runner from the runtime vault (BASE_URL__glm / API_KEY__glm,
	// or a bare ZAI_API_KEY with the default z.ai base URL). Because it reuses
	// the claude binary, stream-json parsing, --model, --resume / warm
	// sessions, and --session-id all work unchanged — full parity for free.
	// It is a DISTINCT runner id (not aliased to "claude") so a user can run
	// real-Anthropic Claude and GLM side by side and pin either to a routine.
	"glm": {
		RunnerID: "glm",
		Name:     "GLM (z.ai)",
		Command:  "claude",
		// Same Args as the claude builtin. --model is intentionally NOT here;
		// yaver-managed spawn prepends RunnerConfig.Model so per-task overrides
		// win (CLI last-flag-wins). The model id is forwarded verbatim to
		// z.ai's Anthropic endpoint.
		Args:        []string{"-p", "{prompt}", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--tools", "Bash", "--dangerously-skip-permissions"},
		Model:       "glm-4.6",
		OutputMode:  "stream-json",
		ExitCommand: "/exit",
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

// firstInstalledBuiltinRunner returns the first builtin runner whose command
// resolves via PATH (or the expanded common-locations search). Scan order is
// stable so callers get a predictable pick. Returns (_, false) when nothing
// is installed, letting the caller surface a clean error instead of crashing
// in a retry loop.
func firstInstalledBuiltinRunner() (RunnerConfig, bool) {
	for _, id := range supportedRunnerIDs {
		r, ok := builtinRunners[id]
		if !ok {
			continue
		}
		if err := CheckRunnerBinary(r.Command); err == nil {
			return r, true
		}
	}
	return RunnerConfig{}, false
}

// supportedRunnerIDs is the canonical list of runner IDs yaver
// advertises in user-facing UX (slash menu, capability inventory,
// /autodev/options, hybrid implementer pick). These are the only
// runners yaver ships first-class support for. Order is the preference
// order for "default installed runner" fallbacks.
var supportedRunnerIDs = []string{"claude", "codex", "opencode", "glm"}

// IsSupportedRunner reports whether a runner ID is in the canonical
// user-facing set. Use this anywhere you'd otherwise enumerate the
// IDs by hand to keep the surface trim consistent.
func IsSupportedRunner(id string) bool {
	id = normalizeRunnerID(id)
	for _, s := range supportedRunnerIDs {
		if id == s {
			return true
		}
	}
	return false
}

// runnerModelCompatible reports whether the model name is plausibly a
// match for the runner. Catches the cross-runner stale-model footgun:
// a feedback task arrives with runner=codex but model=sonnet (left
// over from a previous claude pick), spawning codex with --model
// sonnet which the ChatGPT API rejects with HTTP 400. Heuristic
// (substring/prefix) keeps us forward-compatible with new model
// names without a hardcoded enum.
func runnerModelCompatible(runnerID, model string) bool {
	r := normalizeRunnerID(runnerID)
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return true // empty model means "use runner default", always fine
	}
	switch r {
	case "claude":
		return strings.HasPrefix(m, "claude") || m == "opus" || m == "sonnet" || m == "haiku" || m == "claude-opus-4-7"
	case "glm":
		// GLM runs on the claude binary against z.ai. Accept glm-* ids and,
		// since z.ai also maps Anthropic model names, the claude aliases too.
		return strings.HasPrefix(m, "glm") || strings.HasPrefix(m, "claude") || m == "opus" || m == "sonnet" || m == "haiku"
	case "codex":
		return strings.HasPrefix(m, "gpt") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
	case "opencode":
		// opencode accepts an enormous variety of provider-prefixed
		// model strings; trust whatever the user picked.
		return true
	}
	// Unknown runner → don't second-guess.
	return true
}

// cachedModels stores models fetched from Convex for the /agent/runners endpoint.
var cachedModels []BackendModel

// LoadRunnersFromBackend populates builtinRunners from Convex backend data.
func LoadRunnersFromBackend(runners []backendRunnerFull) {
	for _, r := range runners {
		if r.Command == "" || r.RunnerID == "custom" {
			continue // skip custom runner template
		}
		// Shipped runners (claude / codex / opencode) keep the local
		// builtin definition. The Convex aiRunners table stores rows
		// from older CLI releases — e.g. opencode is in there with
		// args=["{prompt}"] from before sst's CLI rename, which makes
		// startProcess spawn `opencode <prompt>` instead of
		// `opencode run --dangerously-skip-permissions <prompt>`.
		// The wrong argv used to crash the agent at args[:2]; even
		// after the slice guard, opencode interprets the prompt as a
		// filename and exits with ENAMETOOLONG. Argv for shipped
		// runners must come from the binary that ships them.
		if IsSupportedRunner(r.RunnerID) {
			if existing, ok := builtinRunners[r.RunnerID]; ok {
				log.Printf("  Runner loaded: %s (%s) — using local builtin (ignoring backend args)", existing.Name, existing.RunnerID)
				continue
			}
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
//	{"type":"result","result":"...", "total_cost_usd":0.01, "usage":{...}}
type ClaudeEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"` // For stream_event wrapper
	RawResult json.RawMessage `json:"result,omitempty"`
	TotalCost float64         `json:"total_cost_usd,omitempty"`
	Usage     *claudeUsage    `json:"usage,omitempty"`  // present on result events
	Errors    []string        `json:"errors,omitempty"` // e.g. ["No conversation found with session ID: ..."]
	// Tool result (for "user" type events with tool output)
	ToolUseResult *ToolUseResult `json:"tool_use_result,omitempty"`
}

// claudeUsage is the token-usage block emitted on the final result event.
// claude-code uses snake_case (input_tokens, output_tokens, cache_*); newer
// codex CLIs (>=0.124) emit the same shape, so this struct works for both.
type claudeUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
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

// isSoftRunnerFailure decides whether a non-zero exit from a coding-agent
// runner should be classified as completed-with-warning rather than a
// hard FAILED. The signal we trust most: the runner printed its own
// startup banner — that means the binary launched cleanly, it spawned
// inside our env, it could read its prompt, it streamed at least
// something back. A non-zero exit afterwards is almost always a "soft"
// stop in codex CLI 0.123.0 (research preview) — stdin EOF after the
// response is already complete, mid-stream rate-limit, etc.
//
// We require BOTH the banner AND a non-trivial output length so a
// run that crashed halfway through printing the banner is still flagged
// FAILED. We also explicitly exclude signal-kills (segfault, OOM,
// kill -9) — those are real crashes, never soft.
func isSoftRunnerFailure(runnerID, output string, runErr error) bool {
	if runErr == nil {
		return false
	}
	if containsHardRunnerFailure(output) {
		return false
	}
	// exec.ExitError exposes the wait status; signal-killed runs (OOM,
	// crash, kill -9) have ExitCode() == -1 on Unix, which is never a
	// valid soft outcome.
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		if exitErr.ExitCode() < 0 {
			return false
		}
	}
	if len(output) < 200 {
		return false
	}
	switch normalizeRunnerID(runnerID) {
	case "codex":
		return strings.Contains(output, "OpenAI Codex")
	case "claude", "glm":
		// Claude Code's `--print` mode is well-behaved on success but
		// occasionally exits 1 after a successful response. Banner
		// looks like "Claude Code" or "Anthropic Claude" depending on
		// version; cover both. GLM runs on the same binary.
		return strings.Contains(output, "Claude Code") || strings.Contains(output, "Anthropic Claude")
	case "opencode":
		return strings.Contains(output, "opencode")
	}
	return false
}

func containsHardRunnerFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, needle := range []string{
		"invalid_request_error",
		"unsupported model",
		"model is not supported",
		"provided authentication token is expired",
		"token_expired",
		"refresh_token_reused",
		"unauthorized",
		"failedtoopensocket",
		"ai_apicallerror",
		"stream error",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

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

// GetRunnerInfos returns info about active runner processes for heartbeat
// reporting, plus a synthetic entry per installed known runner so Convex
// (and therefore the web/mobile "coding agents" pills) can distinguish
// "codex is installed and authenticated" from "codex isn't here". Without
// the synthetic entries, the only runners ever showing up were whichever
// happened to have a live task — so right after a remote `codex login`,
// the pill had no way to flip from "needs auth" to "ready" until the next
// codex task ran. Status strings are chosen to match what the web's
// deriveRunnerChipStates already classifies: "ready", "needs-auth",
// "down".
func (tm *TaskManager) GetRunnerInfos() []RunnerInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	infos := make([]RunnerInfo, 0) // never nil — Convex expects [] not null
	seenRunner := map[string]bool{}
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
			seenRunner[normalizeRunnerID(t.RunnerID)] = true
		}
	}

	// Append one synthetic entry per installed known runner. We only report
	// runners whose binary is actually on PATH — pill stays "not-installed"
	// grey for everything else, which is correct. Status="ready" means the
	// runner's auth check passed; "needs-auth" means the binary is there but
	// the user still has to sign in; "down" means DetectRunnerRuntimeStatus
	// returned a hard error. Duplicates of running-task entries are skipped.
	knownRunnerIDs := supportedRunnerIDs
	for _, id := range knownRunnerIDs {
		if seenRunner[normalizeRunnerID(id)] {
			continue
		}
		cfg, ok := builtinRunners[id]
		if !ok {
			continue
		}
		if _, err := exec.LookPath(cfg.Command); err != nil {
			continue
		}
		healthStatus := "ready"
		rs := DetectRunnerRuntimeStatus(cfg, tm.workDir)
		switch {
		case strings.TrimSpace(rs.Error) != "":
			// "Codex is installed but not authenticated…" falls through here
			// (detectCodexStatus sets Ready=false + Error when auth is
			// missing). Map that to "needs-auth" so the pill shows amber and
			// the remote-sign-in flow opens on click.
			if strings.Contains(strings.ToLower(rs.Error), "authenticate") ||
				strings.Contains(strings.ToLower(rs.Error), "auth") ||
				strings.Contains(strings.ToLower(rs.Error), "login") {
				healthStatus = "needs-auth"
			} else {
				healthStatus = "down"
			}
		case !rs.AuthConfigured && (id == "codex" || id == "claude" || id == "opencode"):
			// Claude's keychain-backed auth can't be probed on macOS so
			// AuthConfigured stays false even when the user is signed in —
			// don't force "needs-auth" there; codex/opencode probes are
			// filesystem/provider-config based and should be downgraded after
			// a recent API rejection.
			if id == "codex" || id == "opencode" {
				healthStatus = "needs-auth"
			}
		}
		infos = append(infos, RunnerInfo{
			TaskID:   "",
			RunnerID: id,
			Status:   healthStatus,
			Title:    "",
		})
		seenRunner[normalizeRunnerID(id)] = true
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
// TaskVerbosity carries response-detail-level preference from the mobile app.
type TaskVerbosity struct {
	Verbosity *int `json:"verbosity"` // 0-10: response detail level (nil = default 10)
}

// TaskViewport describes the display surface the user is consuming this
// task's output on. The agent's prompt wrapper injects a hint based on
// this so Claude tunes the response length / format to the screen —
// terse headline for a glasses HUD, full markdown for a desktop window,
// columns-aware output for a tmux-style split.
//
// Surfaces in use 2026-05:
//
//	"mobile-phone" | "mobile-tablet" | "web-desktop"
//	"web-spatial-hud" | "web-spatial-vr"
//	"glasses-mentra-live" | "glasses-mentra-display" | "glasses-ray-ban"
//	"wearable-watch" | "wearable-wear" (Apple Watch / Wear OS)
//	"cli" | "" (no hint)
//
// All fields optional. nil = no viewport hint, default behavior.
type TaskViewport struct {
	Surface   string `json:"surface,omitempty"`
	PaneCount int    `json:"paneCount,omitempty"` // parallel Claude sessions visible
	PaneCols  int    `json:"paneCols,omitempty"`  // approx pane width in mono chars
	PaneRows  int    `json:"paneRows,omitempty"`  // approx pane height in rows
	Voice     bool   `json:"voice,omitempty"`     // task originated from voice (STT)
	TTSBudget int    `json:"ttsBudget,omitempty"` // max chars in TTS readback (0 = 280 default)

	// STT/TTS capability of the client that will consume this task's
	// stream. Set from the request's speechContext body or the
	// X-Yaver-Voice header (see mergeClientVoiceHints). These let the
	// prompt wrapper tune output: spoken-friendly + budgeted when TTS is
	// on, an explicit closing question when the user can reply by voice.
	// CLI default is both-false → plain text, no voice shaping.
	STTEnabled  bool   `json:"sttEnabled,omitempty"`
	TTSEnabled  bool   `json:"ttsEnabled,omitempty"`
	STTProvider string `json:"sttProvider,omitempty"` // e.g. "on-device" | "local" | "deepgram" (hint only; keys live in vault)
	TTSProvider string `json:"ttsProvider,omitempty"` // e.g. "device" | "local" | "cartesia"

	// TTSMode is the user-level "run tasks in TTS mode" setting (distinct
	// from TTSEnabled's voice-readback budget). When set, the agent asks
	// the runner to LEAD its reply with a `TTS:`-prefixed spoken-friendly
	// summary line, then continue with the normal formatted body for the
	// screen. No audio is synthesized — this only shapes text.
	TTSMode bool `json:"ttsMode,omitempty"`
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
	WorkDir           string
	InitialUserPrompt string
	SliceContract     *TaskSliceContract

	// Viewport (surface + STT/TTS shaping) is applied before startProcess
	// runs so the prompt wrapper sees it during prompt assembly. Setting
	// task.TaskViewport after CreateTaskWithOptions returns is a race —
	// startProcess builds the prompt synchronously inside this call.
	Viewport *TaskViewport
	// Runner-specific mode selector. Currently only honored by
	// opencode where it maps to `--agent <mode>` (build / plan /
	// any custom agent the user defines in opencode.json). Empty =
	// runner default. Other runners ignore it.
	Mode string

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

	// Video summary toggle — when true, OnTaskDone triggers the
	// vibe-preview clip recorder against this task's project once
	// the runner returns. Source is auto-detected from WorkDir
	// when empty; explicit values are "browser", "sim-ios",
	// "sim-android", "phone". See Task.VideoSource for semantics.
	VideoEnabled bool
	VideoSource  string

	// AskFreely opts the new task OUT of yaver's no-questions preamble
	// AND the soft-question fallback detector. Default false: yaver
	// instructs the runner to pick sensible defaults and only stop via
	// the yaver_ask_user MCP tool. Set true for audits, risky-change
	// reviews, or any task where the user wants the runner to confirm
	// decisions in prose. Guests can never set this — it's stripped in
	// the createTask handler.
	AskFreely bool

	// ResumeLast + ResumeSessionID wire native session resume into the FIRST
	// spawn (used by the scheduler for recurring schedules with resume on).
	// ResumeSessionID seeds task.SessionID so claude/glm/codex can resume by
	// id; opencode resumes by --continue regardless. Default zero = fresh.
	ResumeLast      bool
	ResumeSessionID string

	// AskMode reframes the task as a deep question-answer run instead of a
	// work run: the runner deeply analyzes THIS repo, grounds the answer in
	// file:line cites, escalates from a shallow scan to a wider read for
	// broad questions, and explains first — only acting (working-tree /
	// deploy / git changes) after confirming via yaver_ask_user. Set by
	// `yaver ask`, the yaver_ask MCP tool, and the Ask toggle on the
	// web/mobile console. Mutually exclusive with AskFreely's framing:
	// ask mode swaps in askModePreamble() in place of noQuestionsPreamble().
	// Guests can never set this.
	AskMode bool

	// RedactPII enforces the company dataPolicy.redactPII control on this
	// runtime: the assembled prompt is scrubbed of high-confidence PII/secrets
	// (RedactPII()) before any runner sees it. Set ONLY from the server-stamped
	// X-Yaver-RedactPII header (which is derived from the validated SDK token's
	// `policy:redactPII` scope) — never from a client body, so a caller cannot
	// turn the privacy control off.
	RedactPII bool
}

type TaskResumeOptions struct {
	RunnerID string `json:"runnerId,omitempty"`
	Model    string `json:"model,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

type PendingFollowUp struct {
	Input   string            `json:"input"`
	Images  []ImageAttachment `json:"images,omitempty"`
	Options TaskResumeOptions `json:"options,omitempty"`
}

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Source      string     `json:"source,omitempty"`      // "mobile", "mcp", "cli"
	GuestUserID string     `json:"guestUserId,omitempty"` // set when task created by a guest
	Model       string     `json:"model,omitempty"`
	RunnerID    string     `json:"runnerId,omitempty"` // which runner is executing this task
	SessionID   string     `json:"session_id,omitempty"`
	// ResumeLast asks startProcess to resume the prior session on the FIRST
	// spawn (not just on follow-ups). Set by the scheduler when a recurring
	// schedule with resume enabled re-fires, so the run picks up where the
	// previous fire left off (claude/glm via SessionID, opencode via
	// --continue, codex via exec resume). Default false = fresh spawn.
	ResumeLast   bool               `json:"-"`
	Output       string             `json:"output"`
	ResultText   string             // Extracted clean result text from Claude
	CostUSD      float64            // Total API cost
	InputTokens  int                // Tokens consumed (prompt + cache reads + cache creation)
	OutputTokens int                // Tokens produced by the model
	Turns        []ConversationTurn // Full conversation history
	CreatedAt    time.Time          `json:"created_at"`
	StartedAt    *time.Time         `json:"started_at,omitempty"`
	FinishedAt   *time.Time         `json:"finished_at,omitempty"`

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
	TaskVerbosity *TaskVerbosity `json:"-"`

	// Viewport — surface + pane geometry hints. Prompt wrapper uses
	// this to add a one-line display-context note for Claude so
	// response shape matches the screen.
	TaskViewport *TaskViewport `json:"viewport,omitempty"`

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

	// Video summary — when VideoEnabled, after the task finishes the
	// vibe-preview manager records a short MP4 demonstration of the
	// running result (sim/emulator MP4 for mobile, headless-Chrome
	// frame burst for web). VideoClipID is populated when the
	// recording is queued; the mobile + web task views render a
	// "▶ Watch demo" button when set. VideoSource:
	//   ""           — auto-detect from task workdir
	//   "browser"    — chromedp against the dev server URL
	//   "sim-ios"    — `xcrun simctl io booted recordVideo`
	//   "sim-android"— `adb shell screenrecord`
	//   "phone"      — drive the developer's phone (Phase 5)
	VideoEnabled bool   `json:"videoEnabled,omitempty"`
	VideoSource  string `json:"videoSource,omitempty"`
	VideoClipID  string `json:"videoClipId,omitempty"`
	VideoStatus  string `json:"videoStatus,omitempty"` // queued|recording|ready|failed

	// AskFreely opts out of the no-questions preamble (and the
	// soft-question fallback detector). See TaskCreateOptions.AskFreely
	// for the full rule.
	AskFreely bool `json:"askFreely,omitempty"`

	// AskMode runs the task as a grounded question-answer (deep repo
	// analysis, file:line cites, explain-first with a confirm gate before
	// acting). See TaskCreateOptions.AskMode and askModePreamble().
	AskMode bool `json:"askMode,omitempty"`

	// RedactPII — company dataPolicy.redactPII enforcement for this task.
	// When true, the assembled prompt is scrubbed of PII/secrets before the
	// runner sees it. Set only from the server-stamped header (token scope),
	// never persisted as a client-settable field.
	RedactPII bool `json:"-"`

	PendingFollowUps []PendingFollowUp `json:"pendingFollowUps,omitempty"`

	runner   RunnerConfig // the runner config used for this task (not persisted)
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stdin    io.WriteCloser
	outputCh chan string
	// eventCh carries structured (non-text) events for this task —
	// agent_question, agent_answered, agent_question_cancelled, …
	// The SSE writer in handleTaskByID/streamOutput selects on this
	// alongside outputCh and forwards each event verbatim. Old
	// clients that only know `{type:"output"}` and `{type:"done"}`
	// silently ignore unknown types, so adding new event kinds is
	// backwards-compatible. Buffered so a transient SSE backpressure
	// on a phone doesn't block the agent_question registration; the
	// emitter (emitTaskEvent) drops on full rather than stalling.
	eventCh    chan map[string]interface{}
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

func taskAwaitsManualCompletion(task *Task) bool {
	if task == nil {
		return false
	}
	switch strings.TrimSpace(task.Source) {
	case "mobile", "mobile-code":
		return true
	default:
		return false
	}
}

func taskSuccessStatus(task *Task) TaskStatus {
	if taskAwaitsManualCompletion(task) {
		return TaskStatusReview
	}
	return TaskStatusFinished
}

// TaskInfo is the JSON-safe subset returned in listings.
type TaskInfo struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	RunnerID    string     `json:"runnerId,omitempty"`
	// Model is the model id the task launched with (claude-opus-4-7,
	// gpt-5.4, "opus", etc.). Without this on the public Task API
	// the mobile UI couldn't tell whether a task that's been around
	// for a while ran with the user's expected model — it had to
	// guess from whatever picker state was current, which produced
	// "Claude Code · GPT-5.4" mislabels on cross-device tasks.
	Model string `json:"model,omitempty"`
	// DeviceName is the agent's hostname at the time the task was
	// created. Mobile clients render this on the per-task header and
	// in the task list card; without it, the focused-device name
	// leaked into every label and a task that ran on a sibling box
	// looked like it ran on whichever device the phone was focused
	// on at view time.
	DeviceName     string             `json:"deviceName,omitempty"`
	SessionID      string             `json:"sessionId,omitempty"`
	Output         string             `json:"output,omitempty"`
	ResultText     string             `json:"resultText,omitempty"`
	CostUSD        float64            `json:"costUsd,omitempty"`
	InputTokens    int                `json:"inputTokens,omitempty"`
	OutputTokens   int                `json:"outputTokens,omitempty"`
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
	VideoEnabled   bool               `json:"videoEnabled,omitempty"`
	VideoSource    string             `json:"videoSource,omitempty"`
	VideoClipID    string             `json:"videoClipId,omitempty"`
	VideoStatus    string             `json:"videoStatus,omitempty"`
	VideoClipURL   string             `json:"videoClipUrl,omitempty"`
	VideoPosterURL string             `json:"videoPosterUrl,omitempty"`
	AskFreely      bool               `json:"askFreely,omitempty"`
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
	activeTaskManager = tm
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

	// On Android, run the runner inside the proot rootfs (no-op elsewhere).
	cmd = sandboxWrapCmd(cmd)

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
	// 1. Check if the binary exists in PATH.
	// Under the Android proot sandbox the runner lives INSIDE the rootfs, not on
	// the host PATH, so a host LookPath would always miss. Skip it and let the
	// (sandbox-wrapped) version check below be the authority.
	if _, active := sandboxConfigFromEnv(); !active {
		path, err := exec.LookPath(tm.runner.Command)
		if err != nil {
			return fmt.Errorf("%s not found in PATH — install it first (https://docs.anthropic.com/en/docs/claude-code)", tm.runner.Command)
		}
		log.Printf("[runner-check] Found %s at %s", tm.runner.Command, path)
	}

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

	// On Android, probe the runner inside the proot rootfs (no-op elsewhere) so
	// CheckRunner sees the rootfs `claude`/`codex`/`opencode`, not a host PATH miss.
	cmd = sandboxWrapCmd(cmd)

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
func (tm *TaskManager) CreateTask(title, description, model, source, runnerID, customCommand string, images []ImageAttachment, verbosityCtx ...*TaskVerbosity) (*Task, error) {
	return tm.CreateTaskWithOptions(title, description, model, source, runnerID, customCommand, images, TaskCreateOptions{}, verbosityCtx...)
}

func (tm *TaskManager) CreateTaskWithOptions(title, description, model, source, runnerID, customCommand string, images []ImageAttachment, opts TaskCreateOptions, verbosityCtx ...*TaskVerbosity) (*Task, error) {
	var taskRunner RunnerConfig
	callerRunnerID := normalizeRunnerID(runnerID)
	var perDeviceMode string

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
		// Resolve which runner to use for this task.
		//
		// Order:
		//   1. Caller's explicit runnerID (mobile/web "I picked codex")
		//   2. Convex userSettings.primaryRunnerByDevice for THIS device
		//      — lets the dashboard's "set primary for this machine"
		//      choice flow without restarting the agent. Cached 30s.
		//   3. tm.runner (resolved at boot from global userSettings.runnerId)
		effectiveRunnerID := callerRunnerID
		var perDeviceModel string
		if effectiveRunnerID == "" {
			if pref := resolvePrimaryRunnerPrefForSelf(context.Background(), nil); pref.RunnerID != "" {
				effectiveRunnerID = pref.RunnerID
				perDeviceModel = pref.Model
				perDeviceMode = pref.Mode
			}
		}

		taskRunner = tm.runner // default (could be custom)
		currentRunnerID := normalizeRunnerID(tm.runner.RunnerID)
		if effectiveRunnerID != "" && effectiveRunnerID != currentRunnerID {
			if r, ok := builtinRunners[effectiveRunnerID]; ok {
				taskRunner = r
			} else if effectiveRunnerID == "custom" {
				taskRunner = tm.runner
			} else {
				return nil, fmt.Errorf("unknown runner: %s", runnerID)
			}
		}

		// Inherit the per-device model only when the caller left both
		// runner and model empty. An explicit caller-supplied runner is
		// allowed to keep its caller-supplied (or empty) model.
		if model == "" && perDeviceModel != "" && callerRunnerID == "" {
			model = perDeviceModel
		}

		// If the caller left the runner unspecified and the resolved
		// default isn't actually installed on this host, fall back to
		// the first installed builtin. Without this the task would spawn
		// a missing binary, crash with <100 bytes of output, and enter
		// the 4x auto-restart loop until the runner gets marked down in
		// Convex — visible to the user as "Agent process crashed —
		// restarting (attempt N/4)" with no recourse. Only applies when
		// the caller didn't pick a runner — explicit picks must surface
		// "runner not ready" instead of silently switching binaries.
		if !tm.DummyMode && callerRunnerID == "" {
			if err := CheckRunnerBinary(taskRunner.Command); err != nil {
				if alt, ok := firstInstalledBuiltinRunner(); ok {
					log.Printf("[runner] configured default %q not installed (%v) — falling back to %q for this task", taskRunner.Command, err, alt.RunnerID)
					taskRunner = alt
				}
			}
		}
	}

	// Pre-flight: verify the runner binary is available (skip in dummy mode).
	if !tm.DummyMode {
		if err := CheckRunnerBinary(taskRunner.Command); err != nil {
			return nil, fmt.Errorf("runner not ready: %w", err)
		}
	}

	if strings.TrimSpace(opts.Mode) == "" && callerRunnerID == "" && strings.TrimSpace(perDeviceMode) != "" {
		opts.Mode = strings.TrimSpace(perDeviceMode)
	}

	// Thread the per-task mode (build / plan / custom) onto the runner
	// before it's frozen on the Task struct. buildRunnerArgs reads it
	// to splice `--agent <mode>` into opencode invocations.
	if strings.TrimSpace(opts.Mode) != "" {
		taskRunner.Mode = strings.TrimSpace(opts.Mode)
	}

	if source == "" {
		source = "mobile"
	}
	id := uuid.New().String()[:8]

	now := time.Now()
	initialTurnContent := strings.TrimSpace(opts.InitialUserPrompt)
	if initialTurnContent == "" {
		initialTurnContent = strings.TrimSpace(description)
	}
	if initialTurnContent == "" {
		initialTurnContent = strings.TrimSpace(title)
	}
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
		eventCh:                     make(chan map[string]interface{}, 32),
		doneCh:                      make(chan struct{}),
		WorkDir:                     strings.TrimSpace(opts.WorkDir),
		SliceContract:               opts.SliceContract,
		TaskViewport:                opts.Viewport,
		GuestUserID:                 opts.GuestUserID,
		GuestUseHostAPIKeys:         opts.GuestUseHostAPIKeys,
		GuestAllowGuestProvidedKeys: opts.GuestAllowGuestProvidedKeys,
		GuestRequireIsolation:       opts.GuestRequireIsolation,
		GuestCPULimitPercent:        opts.GuestCPULimitPercent,
		GuestRAMLimitMB:             opts.GuestRAMLimitMB,
		GuestSharedStorageMounts:    append([]string{}, opts.GuestSharedStorageMounts...),
		VideoEnabled:                opts.VideoEnabled,
		VideoSource:                 opts.VideoSource,
		AskFreely:                   opts.AskFreely,
		AskMode:                     opts.AskMode,
		RedactPII:                   opts.RedactPII,
		ResumeLast:                  opts.ResumeLast,
		SessionID:                   opts.ResumeSessionID,
		Turns: []ConversationTurn{
			{Role: "user", Content: initialTurnContent, Timestamp: now},
		},
	}
	if len(verbosityCtx) > 0 && verbosityCtx[0] != nil {
		task.TaskVerbosity = verbosityCtx[0]
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
		// Surface the start-time error into the task itself so the web/
		// mobile chat bubble shows a readable message instead of a bare
		// "(failed)" — this is the surface the user actually reads. The
		// preflight checks in CheckRunnerReady (workDir-not-writable,
		// runner-not-authed, sandbox-blocked) all flow through here.
		now := time.Now()
		failureMsg := fmt.Sprintf("Could not start %s: %v\n", taskRunner.Name, err)
		task.Status = TaskStatusFailed
		task.Output = failureMsg
		task.ResultText = strings.TrimSpace(failureMsg)
		task.FinishedAt = &now
		tm.mu.Lock()
		tm.persist()
		tm.mu.Unlock()
		// Best-effort emit so any already-subscribed SSE stream sees
		// the failure line; the channel may be closed if nobody opened
		// /tasks/<id>/output yet, which is fine.
		func() {
			defer func() { _ = recover() }()
			select {
			case task.outputCh <- failureMsg:
			default:
			}
			close(task.outputCh)
		}()
		return task, fmt.Errorf("start process: %w", err)
	}
	log.Printf("[task %s] %s process started (PID %d)", id, taskRunner.Name, task.cmd.Process.Pid)

	return task, nil
}

func taskEnv(task *Task) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+expandedPath())
	if task != nil {
		env = append(env, "YAVER_TASK_SOURCE="+strings.TrimSpace(task.Source))
		// Stdio MCP children read YAVER_TASK_ID to know which task to
		// associate `yaver_ask_user` calls with. Empty when there is no
		// task in flight (e.g. a CLI MCP probe), in which case the tool
		// returns a clean error rather than registering an orphan
		// question.
		if strings.TrimSpace(task.ID) != "" {
			env = append(env, "YAVER_TASK_ID="+task.ID)
		}
		switch task.Source {
		case terminalLocalTaskSource, "attach", "cli":
			env = append(env, "YAVER_SESSION_MODE=terminal", "YAVER_SOURCE_SURFACE=terminal", "YAVER_WORKSPACE_LOCATION=local")
		case terminalRemoteTaskSource, "connect":
			env = append(env, "YAVER_SESSION_MODE=terminal", "YAVER_SOURCE_SURFACE=terminal", "YAVER_WORKSPACE_LOCATION=remote")
		default:
			env = append(env, "YAVER_SESSION_MODE=remote", "YAVER_SOURCE_SURFACE="+firstNonEmpty(strings.TrimSpace(task.Source), "unknown"))
		}
		if task.runner.OutputMode == "raw" {
			env = append(env, "TERM=xterm-256color", "CLICOLOR_FORCE=1", "FORCE_COLOR=1")
		}
	}
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
	// Co-equal local-model / on-prem / Salad-hosted-model lane: if the runtime
	// vault carries a runner-provider config, point this runner's endpoint at
	// it. Appended last so the explicit runtime config wins over any inherited
	// ANTHROPIC_BASE_URL/OPENAI_BASE_URL. Returns nil (no-op) on the default
	// OAuth-subscription path. Reached only by owner / guest-with-host-keys.
	if task != nil {
		runnerID := task.RunnerID
		if runnerID == "" {
			runnerID = task.runner.RunnerID
		}
		env = append(env, runnerProviderEnv(runnerID)...)
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
	task.Status = taskSuccessStatus(task)
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
//	{model}  — optional, substituted from the runner config's Model
//	           field. None of the first-class runners use this in their
//	           default Args today (claude/codex/opencode all consume
//	           --model as a separate flag), but it's kept so callers can
//	           build custom RunnerConfigs without losing the substitution.
func buildRunnerArgs(runner RunnerConfig, prompt string) []string {
	return buildRunnerArgsWithWorkDir(runner, prompt, "")
}

// buildRunnerArgsWithWorkDir extends buildRunnerArgs with `{workDir}`
// substitution. Required for codex 0.123.0+: passing `-C <DIR>` adds
// the project path to the workspace-write sandbox's writable allowlist
// so apply_patch / sed / inplace edits succeed. Without this, codex's
// banner reports `workdir: /root/.codex/.tmp/plugins` and any write
// to the actual project path is rejected as "outside the writable
// sandbox" / "Read-only file system".
func buildRunnerArgsWithWorkDir(runner RunnerConfig, prompt, workDir string) []string {
	args := make([]string, len(runner.Args))
	for i, a := range runner.Args {
		a = strings.ReplaceAll(a, "{prompt}", prompt)
		if runner.Model != "" {
			a = strings.ReplaceAll(a, "{model}", runner.Model)
		}
		// {workDir} substitutes to the task-resolved project dir; if
		// the caller didn't pass one, leave the placeholder empty so
		// the runner gets a literal "" (codex tolerates -C "" by
		// ignoring it).
		a = strings.ReplaceAll(a, "{workDir}", workDir)
		args[i] = a
	}
	// Codex-specific: splice `-C <workDir>` immediately after the `exec`
	// subcommand so writes to the task's project path are added to
	// codex's workspace-write sandbox allowlist. Without this, codex's
	// banner reports `workdir: /root/.codex/.tmp/plugins`, the project
	// path is treated as Read-only, and apply_patch / sed inplace edits
	// fail with "writing outside of the project; rejected by user
	// approval settings". Verified locally with codex 0.123.0:
	//
	//   $ codex exec --full-auto --skip-git-repo-check -C /tmp/X \
	//       "Update /tmp/X/version.txt to 1.0.1"
	//   → file rewritten 1.0.0 → 1.0.1, diff emitted, success.
	if runner.RunnerID == "codex" && strings.TrimSpace(workDir) != "" {
		out := make([]string, 0, len(args)+2)
		injected := false
		for _, a := range args {
			out = append(out, a)
			if !injected && a == "exec" {
				out = append(out, "-C", strings.TrimSpace(workDir))
				injected = true
			}
		}
		if !injected {
			// Defensive: if the runner's Args don't begin with `exec`
			// (custom user override), still surface -C so the choice
			// isn't silently dropped.
			out = append([]string{"-C", strings.TrimSpace(workDir)}, out...)
		}
		args = out
	}
	// Codex-specific: when the resolved workDir (or, if empty, the
	// agent's spawn cwd) isn't inside a git repo, codex 0.123.0 aborts
	// before running anything with:
	//   "Not inside a trusted directory and --skip-git-repo-check
	//   was not specified."
	// That surfaces on yaver-test-ephemeral and any clean VPS where
	// the task lands in /root or /home/<user>. We can't keep
	// --skip-git-repo-check on by default — codex's workspace-write
	// sandbox depends on the git-walk to add the project to the
	// writable allowlist (without it, apply_patch is rejected as
	// Read-only; see the -C splice above for the prior incident).
	// Conditional injection threads the needle: real repos still get
	// the workspace detection, /root-style cwd's no longer hard-fail
	// on `Run ls`.
	if runner.RunnerID == "codex" {
		probe := strings.TrimSpace(workDir)
		if probe == "" {
			if cwd, cerr := os.Getwd(); cerr == nil {
				probe = cwd
			}
		}
		if probe != "" && !isInsideGitRepo(probe) {
			alreadyHas := false
			for _, a := range args {
				if a == "--skip-git-repo-check" {
					alreadyHas = true
					break
				}
			}
			if !alreadyHas {
				out := make([]string, 0, len(args)+1)
				inserted := false
				for _, a := range args {
					out = append(out, a)
					if !inserted && a == "exec" {
						out = append(out, "--skip-git-repo-check")
						inserted = true
					}
				}
				if !inserted {
					// No `exec` token (custom args) — append at end so
					// the flag still reaches the codex CLI.
					out = append(out, "--skip-git-repo-check")
				}
				args = out
			}
		}
	}
	// Opencode-specific: splice `--agent <mode>` immediately after the
	// `run` subcommand when the user picked a build/plan/custom agent.
	// Hardcoded here rather than templated because opencode is the only
	// runner with this concept and we don't want to mint a generic
	// "drop-paired-args-when-placeholder-empty" syntax that other
	// runners might trip over later.
	if runner.RunnerID == "opencode" && strings.TrimSpace(runner.Mode) != "" {
		out := make([]string, 0, len(args)+2)
		injected := false
		for _, a := range args {
			out = append(out, a)
			if !injected && a == "run" {
				out = append(out, "--agent", strings.TrimSpace(runner.Mode))
				injected = true
			}
		}
		if !injected {
			// Defensive: if `run` wasn't found (custom args), still
			// surface the agent flag so the choice isn't silently
			// dropped — opencode tolerates --agent anywhere on the line.
			out = append([]string{"--agent", strings.TrimSpace(runner.Mode)}, out...)
		}
		args = out
	}
	return args
}

// isInsideGitRepo reports whether dir (or any ancestor) contains a
// `.git` entry — the same check `git rev-parse --is-inside-work-tree`
// performs, but without shelling out (the runner-args build path is
// hot, and we don't want a per-task git invocation just to flip one
// flag). Symlinked .git files (worktrees, submodules) count.
func isInsideGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
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
	// Auto-switch only when the caller didn't pin a workDir. Mobile's
	// feedback flow + the vibingify reshape already resolve the right
	// project path from yaverInheritedGuestProjectPath / projectName —
	// running autoSwitchProject on top of that lets prompt-word matches
	// like "codex" (a runner name commonly echoed in the prompt) hijack
	// the workDir to /root/.codex/.tmp/plugins, which is read-only
	// inside codex's own sandbox. Net effect on test-ephemeral: every
	// vibe task got cmd.Dir=/root/.codex/.tmp/plugins, codex's
	// workspace-write sandbox treated the actual project as outside
	// the writable root, and apply_patch failed with "Read-only file
	// system". Five user iterations later we figured it out.
	if task.GuestUserID == "" && strings.TrimSpace(task.WorkDir) == "" {
		tm.autoSwitchProject(task, prompt)
	}

	// System prompt: behave as a remote terminal agent, tailored to the task source.
	prompt += taskSourcePromptSuffix(task.Source)

	contextDir := tm.workDir
	if task.WorkDir != "" {
		contextDir = task.WorkDir
	}

	// No-questions decision policy + vault-name hints. Default ON; opt-out
	// per task via Task.AskFreely (audit / risky-change reviews). Inserted
	// AFTER taskSourcePromptSuffix so the source-specific framing is read
	// first, then the policy clarifies "and don't ask in prose."
	if task.AskMode {
		// Ask mode reframes the run as explain-first deep analysis with a
		// confirm gate before acting — the opposite stance from the
		// no-questions preamble, so it replaces (not augments) it.
		prompt += askModePreamble()
	} else if !task.AskFreely {
		project := DetectProjectInfo(contextDir).Name
		hints := renderVaultHintsForTask(currentRuntimeVaultStore(), project)
		prompt += noQuestionsPreamble(hints)
		// Runner-agnostic "future work" contract: confirm cadence, then
		// schedule_self instead of looping. Skipped for scheduler-spawned
		// runs so a recurring task doesn't keep re-proposing its own
		// schedule on every fire.
		if task.Source != "scheduler" {
			prompt += schedulingPreamble()
		}
	}

	// "mobile-code" is the mobile UI's "yaver code mode" toggle: same
	// /tasks endpoint, same TaskManager, but the runner sees the
	// terminal-style prompt prefix (no markdown headings by default,
	// no canned bullet framing) instead of the mobile dev-server
	// hot-reload prefix. See mobile/src/lib/quic.ts::sendTask for the
	// caller-side documentation.
	if task.Source == "mcp" || task.Source == terminalLocalTaskSource || task.Source == terminalRemoteTaskSource || task.Source == "attach" || task.Source == "cli" || task.Source == "console" || task.Source == "connect" || task.Source == "mobile-code" || task.Source == "ask" || task.Source == "voice" {
		prompt += yaverWrapperCapabilityContext(contextDir, task.Source)
	}

	prompt += formatTaskSliceContract(task.SliceContract)

	// Only mobile-style tasks need the dev-server transport instructions.
	// "mobile-code" tasks deliberately skip this — they want CLI-style
	// runner output, not the Hermes / Metro / dev-server scaffold.
	if task.Source == "mobile" {
		prompt += yaverDevServerContext(contextDir)
	}

	// Viewport hint — tells Claude what surface this output will be
	// read on (HUD vs desktop vs tmux split vs voice readback) so the
	// response shape matches. Built from the Voice/mobile/web/spatial
	// /MentraOS caller's runtime context.
	if vp := task.TaskViewport; vp != nil {
		prompt += formatViewportHint(vp)
	}

	// Verbosity level (0-10)
	if vc := task.TaskVerbosity; vc != nil && vc.Verbosity != nil {
		v := *vc.Verbosity
		var line string
		switch {
		case v <= 2:
			line = fmt.Sprintf("\n[Verbosity: %d/10] The user prefers very brief responses. Just confirm what was done, report any errors, skip all implementation details.", v)
		case v <= 4:
			line = fmt.Sprintf("\n[Verbosity: %d/10] The user prefers concise responses. Summarize what you did in 2-3 sentences.", v)
		case v <= 6:
			line = fmt.Sprintf("\n[Verbosity: %d/10] The user prefers moderate detail. Show key changes, explain reasoning briefly.", v)
		case v <= 8:
			line = fmt.Sprintf("\n[Verbosity: %d/10] The user wants detailed responses. Show code changes, explain your approach.", v)
		default:
			line = fmt.Sprintf("\n[Verbosity: %d/10] The user wants full detail. Stream everything: all code changes, diffs, reasoning, alternatives.", v)
		}
		prompt += line
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
	// Per-task model override flows into the runner's {model}
	// placeholder (if any). Must happen before buildRunnerArgs or
	// the placeholder lands in the argv literally.
	if task.Model != "" {
		runner.Model = task.Model
	}
	// Resolve the task's effective workDir for the runner's sandbox
	// allowlist (codex uses -C <DIR> to add it). Without this, codex's
	// workspace-write sandbox treats the project path as Read-only and
	// rejects apply_patch / sed inplace edits.
	taskDirForArgs := tm.workDir
	if task.WorkDir != "" {
		taskDirForArgs = task.WorkDir
	}
	// Yaver-action sentinel instruction: tells the runner it can emit
	// `<<yaver-action: reload <slug>>>` to drive the user's paired
	// phone. Only relevant when the user is actually talking through
	// the mobile app (Tasks tab) — CLI / connect / autodev sessions
	// don't have a phone to talk to and don't need the noise. Prepended
	// rather than threaded as a flag because codex / opencode don't
	// have a clean --append-system-prompt; one channel for all
	// runners keeps the dispatch path simple.
	if task.Source == "mobile" || task.Source == "mobile-code" {
		prompt = YaverActionSystemPrompt + "\n\n---\n\n" + prompt
	}
	// Company dataPolicy.redactPII: scrub PII/secrets from the fully-assembled
	// prompt as the LAST step before it reaches the runner (Claude Code / Codex
	// / OpenCode / a local model). No-op unless the task is under a redaction
	// policy. This is the on-runtime enforcement of the resolved data policy.
	if task.RedactPII {
		if redacted, n := RedactPII(prompt); n > 0 {
			log.Printf("[task %s] dataPolicy.redactPII: scrubbed %d PII/secret span(s) from prompt", task.ID, n)
			prompt = redacted
		}
	}
	args := buildRunnerArgsWithWorkDir(runner, prompt, taskDirForArgs)

	// Recurring-schedule resume: when the scheduler re-fires a schedule with
	// resume enabled, pick up the prior session on this first spawn. Takes
	// precedence over (and suppresses) the warm-session resume below so the
	// schedule's own session wins over the global warm one.
	resumedForSchedule := false
	if task.ResumeLast {
		if newArgs, ok := resumeTransform(runner, args, prompt, taskDirForArgs, task.SessionID); ok {
			args = newArgs
			resumedForSchedule = true
			log.Printf("[task %s] Recurring schedule: resuming prior %s session (id=%q)", task.ID, runner.RunnerID, task.SessionID)
		}
	}

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
	if !resumedForSchedule && warmSID != "" && runner.ResumeSupported && len(runner.ResumeArgs) > 0 {
		for _, ra := range runner.ResumeArgs {
			args = append(args, strings.ReplaceAll(ra, "{sessionId}", warmSID))
		}
		// Claude Code 2.1.80+ requires --fork-session with --session-id when resuming
		if runner.RunnerID == "claude" || runner.RunnerID == "glm" {
			args = append(args, "--fork-session", "--session-id", uuid.New().String())
		}
		log.Printf("[task %s] Resuming warm session %s (age=%v)", task.ID, warmSID, warmAge.Round(time.Second))
	}

	// Override model if specified on the task (e.g. "opus", "sonnet",
	// "haiku", "gpt-5-codex"). Falls back to runner.Model when the task
	// didn't pin one — without this, codex would inherit Codex CLI's own
	// default (`o3-mini`) which fails on ChatGPT-account auth.
	effectiveModel := task.Model
	if effectiveModel == "" {
		effectiveModel = runner.Model
	}
	if effectiveModel != "" {
		modelOverride := false
		for i, a := range args {
			if a == "--model" && i+1 < len(args) {
				args[i+1] = effectiveModel
				modelOverride = true
				break
			}
		}
		if !modelOverride {
			args = append(args, "--model", effectiveModel)
		}
	}

	// Determine working directory
	taskDir := tm.workDir
	if task.WorkDir != "" {
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
		// Hosted coding CLIs like Codex / Claude Code / OpenCode / Aider are
		// installed and authenticated on the host machine, not inside Yaver's
		// generic Docker image. Running them in the container makes even valid
		// host setups fail with "command not found". Keep guest isolation as-is,
		// but execute these host-owned runners directly on the host.
		if useContainer && task.GuestUserID == "" && runnerRequiresHostRuntime(runner.RunnerID) {
			useContainer = false
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
			go tm.readRawOutput(task, stdout, stderr)
		} else {
			go tm.readStreamJSON(task, stdout)
			go func() {
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					log.Printf("[task %s] [container stderr] %s", task.ID, scanner.Text())
				}
			}()
		}
	} else {
		// ── Direct execution (default) ──────────────────────────────────

		// Optional tmux-attach mode: route eligible runners through an
		// existing user-owned tmux session so they inherit that session's
		// auth context (notably macOS Keychain unlocking for Claude Code,
		// which we can't get from a launchd/ssh-launched daemon).
		// Opt-in via YAVER_TMUX_RUNNER=<session-name>; falls through to
		// direct exec when off, tmux missing, or session absent.
		var cmd *exec.Cmd
		var tmuxEnvAdditions []string
		if session := tmuxRunnerReady(); session != "" && tmuxRunnerEligible(runner.RunnerID) {
			log.Printf("[task %s] tmux mode: dispatching %s into session %q",
				task.ID, runner.Command, session)
			cmd, tmuxEnvAdditions = buildTmuxRunnerCommand(ctx, session, task.ID, runner.Command, args)
		} else {
			cmd = exec.CommandContext(ctx, runner.Command, args...)
		}
		cmd.Dir = taskDir

		// Ensure common tool paths are in PATH for background processes.
		cmd.Env = taskEnv(task)
		if len(tmuxEnvAdditions) > 0 {
			cmd.Env = append(cmd.Env, tmuxEnvAdditions...)
		}

		// Log the first two argv tokens for context (subcommand + first
		// flag). Some runners' Args templates collapse to a single token
		// after substitution — guard so a debug log line can't crash the
		// whole HTTP server.
		previewN := 2
		if len(args) < previewN {
			previewN = len(args)
		}
		log.Printf("[task %s] Launching: %s %v (dir=%s)", task.ID, runner.Command, args[:previewN], taskDir)

		// Dev log: task launch
		go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-launch",
			fmt.Sprintf("Launching task %s: %s", task.ID, task.Title),
			map[string]interface{}{"runner": runner.RunnerID, "model": task.Model, "argCount": len(args)})

		// On Android, run the task inside the proot rootfs (no-op elsewhere).
		// Skipped in tmux mode (Android never takes that branch) so we don't
		// proot-wrap a `tmux send-keys` invocation.
		if len(tmuxEnvAdditions) == 0 {
			cmd = sandboxWrapCmd(cmd)
		}

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
			go tm.readRawOutput(task, stdout, stderr)
		} else {
			go tm.readStreamJSON(task, stdout)
			go func() {
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					log.Printf("[task %s stderr] %s", task.ID, scanner.Text())
				}
			}()
		}
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
					task.eventCh = make(chan map[string]interface{}, 32)
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

				// Soft-failure heuristic: codex CLI 0.123.0 (research preview)
				// frequently exits non-zero on perfectly-functional runs —
				// EOF on stdin after streaming the response, model rate limits
				// at the tail end, etc. — but still produces a useful answer
				// and prints its banner. If the runner's banner is in the
				// output AND we have substantial content AND the process
				// wasn't killed by a signal, treat the task as completed
				// rather than red-flag FAILED. This matches the user's
				// expectation: "the run worked, the answer is there, why is
				// the row screaming red".
				if isSoftRunnerFailure(task.runner.RunnerID, task.Output, err) {
					task.Status = taskSuccessStatus(task)
					log.Printf("[task %s] %s soft failure (exit: %v, output_len=%d) — marking finished", task.ID, task.runner.Name, err, outputLen)
				} else {
					// Real failure — mark runner as down in Convex
					go func() {
						if tm.ConvexURL != "" {
							detail := fmt.Sprintf("all %d retries exhausted, exit: %v", maxProcessRetries, err)
							_ = ReportDeviceEvent(tm.ConvexURL, tm.AuthToken, tm.DeviceID, "crash", detail)
							_ = SetRunnerDown(tm.ConvexURL, tm.AuthToken, tm.DeviceID, true)
						}
					}()

					// Auth-error detection: if stdout/stderr indicates the
					// runner's OAuth token was rejected by the API (401 /
					// invalid bearer / not logged in), invalidate that
					// runner's status cache so DeviceDetails / dashboard
					// flip from ✓ signed in to ⚠️ Sign in on next heartbeat
					// instead of waiting for the user to discover the
					// stale state by failing another task. Mirrors the
					// mobile ErrorMessage.detectRunnerAuthFailure patterns.
					if hitRunner := IsRunnerAuthFailureOutput(task.Output); hitRunner != "" {
						MarkRunnerAuthInvalid(hitRunner)
						log.Printf("[task %s] auth-failure pattern detected for runner %q — invalidated runner auth status", task.ID, hitRunner)
						// Next periodic heartbeat (~30s) propagates the
						// new state to Convex; mobile/web pick up the
						// flipped pill on their next /runner-auth/status
						// poll. The user already gets immediate feedback
						// via the failure-card "Sign in to Claude Code"
						// CTA.
					}

					task.Status = TaskStatusFailed
					log.Printf("[task %s] %s process failed: %v", task.ID, task.runner.Name, err)
				}
			} else {
				task.Status = taskSuccessStatus(task)
				log.Printf("[task %s] %s process finished successfully (output_len=%d)", task.ID, task.runner.Name, len(task.Output))
				// Task succeeded on the first try — if the device was
				// previously stuck in runnerDown=true from an old failure
				// (pre-fix Claude-Code-without-auth loop, or any prior
				// 4x-exhausted binary crash), clear it so the machine
				// isn't greyed out forever in the web + SDK device
				// pickers. Cheap best-effort async call.
				go func(convexURL, token, deviceID string) {
					if convexURL == "" || deviceID == "" {
						return
					}
					_ = SetRunnerDown(convexURL, token, deviceID, false)
				}(tm.ConvexURL, tm.AuthToken, tm.DeviceID)
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
			if (task.Status == TaskStatusReview || task.Status == TaskStatusFinished) && len(task.PendingFollowUps) > 0 {
				next := task.PendingFollowUps[0]
				task.PendingFollowUps = task.PendingFollowUps[1:]
				oldDoneCh := task.doneCh
				task.Turns = append(task.Turns, ConversationTurn{Role: "user", Content: next.Input, Timestamp: time.Now()})
				if len(next.Images) > 0 {
					newPaths := saveImages(task.ID, next.Images)
					task.ImagePaths = append(task.ImagePaths, newPaths...)
				}
				if runnerID := normalizeRunnerID(next.Options.RunnerID); runnerID != "" {
					prevRunner := normalizeRunnerID(task.RunnerID)
					runner := GetRunnerConfig(runnerID)
					task.runner = runner
					task.RunnerID = runner.RunnerID
					if runner.RunnerID != prevRunner {
						task.SessionID = ""
					}
				}
				if model := strings.TrimSpace(next.Options.Model); model != "" {
					task.Model = model
				}
				if mode := strings.TrimSpace(next.Options.Mode); mode != "" {
					runner := task.runner
					if runner.Command == "" {
						runner = tm.runner
					}
					runner.Mode = mode
					task.runner = runner
				}
				task.Output = ""
				task.ResultText = ""
				task.FinishedAt = nil
				task.Status = TaskStatusQueued
				task.outputCh = make(chan string, 512)
				task.eventCh = make(chan map[string]interface{}, 32)
				task.doneCh = make(chan struct{})
				tm.persist()
				tm.mu.Unlock()
				close(oldDoneCh)
				if err := tm.startResume(task, next.Input); err != nil {
					tm.mu.Lock()
					task.Status = TaskStatusFailed
					now := time.Now()
					task.FinishedAt = &now
					tm.persist()
					tm.fireTaskDone(task)
					tm.mu.Unlock()
					close(task.doneCh)
				}
				return
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
		// Engine-side fallback for the runner-agnostic "future work" capability:
		// if the original request implied recurring work and nothing got
		// scheduled, offer to schedule it (non-blocking; guards inside).
		tm.maybeProposeSchedule(task)
		// Save session file for recent history (non-blocking)
		go saveSessionFile(task, task.runner.Name, tm.workDir)
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	return nil
}

func runnerRequiresHostRuntime(runnerID string) bool {
	switch normalizeRunnerID(runnerID) {
	case "claude", "codex", "opencode", "glm":
		return true
	default:
		return false
	}
}

// readRawOutput reads plain text lines from stdout (for non-JSON runners).
func (tm *TaskManager) readRawOutput(task *Task, stdout, stderr io.Reader) {
	var output strings.Builder
	var outputMu sync.Mutex
	tm.mu.RLock()
	output.WriteString(task.Output)
	tm.mu.RUnlock()

	// Per-runner stream rewriting. opencode's TUI ships ANSI escapes
	// and CLI-style `$ <cmd>` markers that the chat renderer in
	// mobile + web doesn't recognize on its own — see
	// opencodeStreamFilter for the full rationale. One filter
	// instance is shared across stdout + stderr so a `$` line
	// arriving on stderr (rare, but possible when the underlying
	// shell is in pipe mode) still gets the same treatment.
	var ocFilter *opencodeStreamFilter
	if normalizeRunnerID(task.runner.RunnerID) == "opencode" {
		ocFilter = &opencodeStreamFilter{task: task}
	}
	// Other raw-mode runners — codex in particular ships its banner
	// + sandbox status lines ANSI-coloured. Without a per-chunk strip
	// those `\x1b[…m` codes shipped through task.outputCh as literal
	// text and only got cleaned by stripPromptEcho on completion, so
	// mobile + web saw "[1m[33mcodex" mid-stream until the run ended.
	// stripANSI is idempotent so the completion-time scrub still
	// runs harmlessly on already-clean text.
	//
	// Best-effort: an ANSI sequence split exactly at an 8 KB chunk
	// boundary leaks the partial code (the regex needs a complete
	// `\x1b[…m` to match). Codex flushes lines aggressively so this
	// is rare in practice — and the same partial would have shipped
	// raw before this change, so we never regress.
	stripLiveANSI := ocFilter == nil && normalizeRunnerID(task.runner.RunnerID) != ""

	var wg sync.WaitGroup
	readStream := func(name string, r io.Reader) {
		defer wg.Done()
		if r == nil {
			return
		}
		buf := make([]byte, 8192)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				payload := buf[:n]
				if ocFilter != nil {
					payload = ocFilter.process(payload)
				} else if stripLiveANSI {
					payload = []byte(stripANSI(string(payload)))
				}
				if len(payload) > 0 {
					outputMu.Lock()
					tm.emit(task, &output, string(payload))
					outputMu.Unlock()
					// Best-effort: recover codex/opencode session id from raw
					// output so follow-ups / recurring schedules can resume.
					// A miss is harmless (opencode falls back to --continue,
					// codex to carry-memo).
					tm.mu.RLock()
					haveSID := task.SessionID != ""
					tm.mu.RUnlock()
					if !haveSID {
						if sid := parseRawSessionID(task.runner.RunnerID, string(payload)); sid != "" {
							tm.mu.Lock()
							task.SessionID = sid
							tm.mu.Unlock()
						}
					}
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[task %s] raw %s read error: %v", task.ID, name, err)
				}
				return
			}
		}
	}

	wg.Add(1)
	go readStream("stdout", stdout)
	if stderr != nil {
		wg.Add(1)
		go readStream("stderr", stderr)
	}
	wg.Wait()
	// Flush any partial line still buffered in the opencode filter —
	// happens when the process closes stdout without a trailing
	// newline (rare, but can drop a final log line otherwise).
	if ocFilter != nil {
		if rem := ocFilter.flush(); len(rem) > 0 {
			outputMu.Lock()
			tm.emit(task, &output, string(rem))
			outputMu.Unlock()
		}
	}
	close(task.outputCh)

	tm.mu.Lock()
	// task.Output keeps the full raw stream for logs/debug; ResultText
	// gets the cleaned answer so persisted reads (web/MCP/mobile) don't
	// leak our own injected system context or Codex's banner+config dump.
	// Mirrors mobile-side stripPromptEcho in mobile/app/(tabs)/tasks.tsx.
	task.ResultText = stripPromptEcho(task.Output)
	tm.mu.Unlock()

	log.Printf("[task %s] Raw output reader finished (output_len=%d, result_len=%d)",
		task.ID, output.Len(), len(task.ResultText))
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
	// Fallback question detection: when the runner ignores the
	// yaver_ask_user MCP tool and asks in prose anyway, this catches
	// the question and re-presents it through the same Q&A surface
	// the MCP path uses. AskFreely-tagged tasks are exempt — the user
	// explicitly opted into prose questions and would not want them
	// hijacked into a structured sheet.
	if !task.AskFreely {
		tm.maybeDetectSoftQuestion(task, text)
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

	// Structured command-card events (command_events.go). Claude's
	// stream-json is serial — one Bash tool_use runs to its
	// tool_use_result before the next — so a single "pending" command
	// is enough to correlate the result back to its start. cmdSeq makes
	// the id stable + unique per task.
	cmdSeq := 0
	pendingCmdID := ""
	var pendingCmdStart time.Time
	startCmd := func(cmd string) {
		cmdSeq++
		pendingCmdID = fmt.Sprintf("%s-c%d", task.ID, cmdSeq)
		pendingCmdStart = time.Now()
		tm.mu.RLock()
		cwd := task.WorkDir
		tm.mu.RUnlock()
		emitCommandStart(task, pendingCmdID, cmd, nil, cwd, "claude")
	}
	endCmd := func(stdout, stderr string, interrupted bool) {
		if pendingCmdID == "" {
			return
		}
		emitCommandOutput(task, pendingCmdID, "stdout", stdout, 0)
		emitCommandOutput(task, pendingCmdID, "stderr", stderr, 1)
		var dur int64
		if !pendingCmdStart.IsZero() {
			dur = time.Since(pendingCmdStart).Milliseconds()
		}
		// claude-code stream-json tool_use_result carries no exit code,
		// only an `interrupted` flag → exitKnown=false (neutral badge),
		// truncated=interrupted.
		emitCommandEnd(task, pendingCmdID, 0, false, dur, interrupted)
		pendingCmdID = ""
	}

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
						startCmd(bi.Command)
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
								startCmd(bi.Command)
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
				// Close the structured command card with its captured
				// stdout/stderr (P2P only — never Convex).
				endCmd(event.ToolUseResult.Stdout, event.ToolUseResult.Stderr, event.ToolUseResult.Interrupted)
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
					if event.Usage != nil {
						task.InputTokens = event.Usage.InputTokens +
							event.Usage.CacheCreationInputTokens +
							event.Usage.CacheReadInputTokens
						task.OutputTokens = event.Usage.OutputTokens
					}
					inT, outT := task.InputTokens, task.OutputTokens
					tm.mu.Unlock()
					log.Printf("[task %s result] cost=$%.4f len=%d tokens=%d→%d", task.ID, event.TotalCost, len(resultStr), inT, outT)
				}
			}
		}
	}

	// Stream ended with a command still open (no tool_use_result —
	// process crashed mid-command, or a non-result terminal). Close the
	// card so the UI doesn't show it spinning forever.
	if pendingCmdID != "" {
		endCmd("", "", true)
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

	// Unpark any agent_question that's waiting on a human; the
	// /tasks/{id}/question handler returns immediately and the
	// runner's MCP tool call gets a cancellation result instead of
	// hanging until the question's TTL expires. Drop the soft-
	// question scratchpad too so a re-launched task with the same
	// ID starts fresh.
	globalQuestionRegistry.CancelTask(id)
	dropSoftQuestionState(id)

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
	return tm.ResumeTaskWithOptions(id, input, images, TaskResumeOptions{})
}

func (tm *TaskManager) ResumeTaskWithOptions(id, input string, images []ImageAttachment, opts TaskResumeOptions) (*Task, error) {
	tm.mu.Lock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.Unlock()
		return nil, fmt.Errorf("task %s not found", id)
	}
	if task.Status == TaskStatusRunning || task.Status == TaskStatusQueued {
		// Queue the follow-up onto the running task. The drain runs
		// after the current response finishes (see startTask / startResume
		// completion blocks). Works for any task source so phones can
		// text mid-stream the way Codex/Claude Code do.
		task.PendingFollowUps = append(task.PendingFollowUps, PendingFollowUp{
			Input:   strings.TrimSpace(input),
			Images:  append([]ImageAttachment{}, images...),
			Options: opts,
		})
		queuedNote := "\n[Follow-up queued; it will run after the current response finishes.]\n"
		task.Output += queuedNote
		if task.outputCh != nil {
			select {
			case task.outputCh <- queuedNote:
			default:
			}
		}
		tm.persist()
		tm.mu.Unlock()
		return task, nil
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
	if runnerID := normalizeRunnerID(opts.RunnerID); runnerID != "" {
		prevRunner := normalizeRunnerID(task.RunnerID)
		runner := GetRunnerConfig(runnerID)
		task.runner = runner
		task.RunnerID = runner.RunnerID
		if runner.RunnerID != prevRunner {
			task.SessionID = ""
		}
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		task.Model = model
	}
	if mode := strings.TrimSpace(opts.Mode); mode != "" {
		runner := task.runner
		if runner.Command == "" {
			runner = tm.runner
		}
		runner.Mode = mode
		task.runner = runner
	}

	// Clear output for the new run — turns track conversation history
	task.Output = ""
	task.ResultText = "" // Clear previous result — new one will come
	task.FinishedAt = nil
	task.Status = TaskStatusQueued

	// Re-create channels for the new run
	task.outputCh = make(chan string, 512)
	task.eventCh = make(chan map[string]interface{}, 32)
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

	contextDir := tm.workDir
	if task.WorkDir != "" {
		contextDir = task.WorkDir
	}
	if task.Source == "mcp" || task.Source == terminalLocalTaskSource || task.Source == terminalRemoteTaskSource || task.Source == "attach" || task.Source == "cli" || task.Source == "console" || task.Source == "connect" || task.Source == "mobile-code" || task.Source == "ask" || task.Source == "voice" {
		prompt += yaverWrapperCapabilityContext(contextDir, task.Source)
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

	// Use task's runner if set, otherwise fall back to global
	runner := task.runner
	if runner.Command == "" {
		runner = tm.runner
	}

	// Resume reuses the same workDir resolution as initial spawn so
	// codex's -C sandbox allowlist stays consistent across follow-ups.
	resumeWorkDir := tm.workDir
	if task.WorkDir != "" {
		resumeWorkDir = task.WorkDir
	}
	args := buildRunnerArgsWithWorkDir(runner, prompt, resumeWorkDir)

	// Resume the prior conversation (this is always a follow-up). resumeTransform
	// handles claude/glm (--resume <id>), opencode (--continue), codex (exec
	// resume <id>), and generic ResumeArgs runners; it falls back (ok=false)
	// when the runner can't resume with what we captured, so we spawn fresh.
	if newArgs, ok := resumeTransform(runner, args, prompt, resumeWorkDir, task.SessionID); ok {
		args = newArgs
		log.Printf("[task %s] Resuming %s session (id=%q)", task.ID, runner.RunnerID, task.SessionID)
	} else if runner.RunnerID == "claude" || runner.RunnerID == "glm" {
		// New claude/glm session — give it a unique id so future follow-ups
		// can resume it.
		args = append(args, "--session-id", uuid.New().String())
	}

	cmd := exec.CommandContext(ctx, runner.Command, args...)
	cmd.Dir = tm.workDir

	// On Android, run the forked runner inside the proot rootfs (no-op elsewhere).
	cmd = sandboxWrapCmd(cmd)

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

	if runner.OutputMode == "raw" {
		go tm.readRawOutput(task, stdout, stderr)
	} else {
		go tm.readStreamJSON(task, stdout)
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				log.Printf("[task %s stderr] %s", task.ID, scanner.Text())
			}
		}()
	}

	go func() {
		err := cmd.Wait()
		tm.mu.Lock()
		if task.Status == TaskStatusRunning {
			if err != nil {
				task.Status = TaskStatusFailed
			} else {
				task.Status = taskSuccessStatus(task)
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
			if (task.Status == TaskStatusReview || task.Status == TaskStatusFinished) && len(task.PendingFollowUps) > 0 {
				next := task.PendingFollowUps[0]
				task.PendingFollowUps = task.PendingFollowUps[1:]
				oldDoneCh := task.doneCh
				task.Turns = append(task.Turns, ConversationTurn{Role: "user", Content: next.Input, Timestamp: time.Now()})
				if len(next.Images) > 0 {
					newPaths := saveImages(task.ID, next.Images)
					task.ImagePaths = append(task.ImagePaths, newPaths...)
				}
				task.Output = ""
				task.ResultText = ""
				task.FinishedAt = nil
				task.Status = TaskStatusQueued
				task.outputCh = make(chan string, 512)
				task.eventCh = make(chan map[string]interface{}, 32)
				task.doneCh = make(chan struct{})
				tm.persist()
				tm.mu.Unlock()
				close(oldDoneCh)
				if err := tm.startResume(task, next.Input); err != nil {
					tm.mu.Lock()
					task.Status = TaskStatusFailed
					now := time.Now()
					task.FinishedAt = &now
					tm.persist()
					tm.fireTaskDone(task)
					tm.mu.Unlock()
					close(task.doneCh)
				}
				return
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
	case terminalLocalTaskSource, terminalRemoteTaskSource, "attach", "cli", "console", "connect":
		return "\n\nYou are running inside an interactive terminal attached to Yaver. Show what you are doing step by step. Use terminal commands when needed. Be concise." + consoleTaskResponseContext()
	default:
		return "\n\nYou are running tasks from a remote mobile device. Show what you are doing step by step. Use only terminal commands. Be concise." + mobileTaskResponseContext()
	}
}

func consoleTaskResponseContext() string {
	return `

[Console response contract]
The human is reading this in a terminal session, not a rich markdown surface.
- Write plain terminal text by default.
- Do NOT use markdown headings, tables, or fenced code blocks unless the user explicitly asks for them.
- Keep progress updates short and concrete.
- Prefer natural status lines over template bullets.
- Keep the final answer brief, direct, and agent-agnostic unless the user asked about a specific tool.

[Inspection commands — show raw output]
When the user asks you to run a short read-only inspection command — e.g. "run ls", "ls", "pwd", "cat <file>", "git status", "git log -5", "find …", "grep …", "ps aux", "uname -a", "df -h", "head/tail <file>", "which <bin>", "<tool> --version", "echo …", "wc …", "tree …" — the answer the human wants IS the command's stdout.
- Paste the actual output verbatim inside a fenced block.
- Do NOT paraphrase ("50+ entries including backend, cli, desktop…").
- Do NOT replace the listing with a summary like "checked: working dir is …".
- Trim only when the output exceeds ~100 lines, and say what you trimmed (e.g. "first 80 lines, 423 more").
- A one-line lead-in before the block is fine ("here's the listing:") but the block itself is the answer.

[Long-running / build / test / deploy output]
For commands whose value is success/failure (build, test, deploy, migration, install) the rule above does NOT apply — summarize the outcome and surface only the lines that explain failures. The "show raw output" rule is specifically for inspection asks where the human wants to read the output themselves.`
}

func mobileTaskResponseContext() string {
	return `

[Mobile response contract]
The human is reading this on a phone. Optimize for fast scanning, not rich markdown.
- Keep progress updates short and concrete. Prefer one short sentence over long narration.
- Start the final answer with a plain-language outcome sentence.
- After that, use at most three short bullets chosen from: changed, checked, blocked, next.
- Do NOT use tables.
- Keep markdown light: short bullets and inline code are fine; avoid heavy heading stacks and long fenced blocks unless truly necessary.
- Stay agent-agnostic in wording. Do not mention a specific coding assistant brand unless the user asked about it.
- Never hide important failures, commands, or file changes. Be concise without dropping critical information.

[Inspection commands — show raw output]
When the user asks you to run a short read-only inspection command — e.g. "run ls", "ls", "pwd", "cat <file>", "git status", "git log -5", "find …", "grep …", "ps aux", "uname -a", "df -h", "head/tail <file>", "which <bin>", "<tool> --version", "echo …", "wc …", "tree …" — the answer the human wants IS the command's stdout.
- Paste the actual output verbatim inside a fenced block.
- Do NOT paraphrase ("50+ entries including backend, cli, desktop…").
- Do NOT replace the listing with a summary like "checked: working dir is …".
- On a phone the screen is small, so cap raw output at ~50 lines: paste the first 50 and add a one-line "(N more — ask 'show all' to see the rest)" footer when truncating.
- A short outcome sentence above the block is allowed and welcome ("here are the 27 entries in the repo root:").

[Long-running / build / test / deploy output]
For commands whose value is success/failure (build, test, deploy, migration, install) the rule above does NOT apply — summarize the outcome and surface only the lines that explain failures. The "show raw output" rule is specifically for inspection asks where the human wants to read the output themselves.`
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
			InputTokens:    t.InputTokens,
			OutputTokens:   t.OutputTokens,
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
			VideoEnabled:   t.VideoEnabled,
			VideoSource:    t.VideoSource,
			VideoClipID:    t.VideoClipID,
			VideoStatus:    t.VideoStatus,
			AskFreely:      t.AskFreely,
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

func (tm *TaskManager) SetTaskVideoState(id, clipID, status string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok := tm.tasks[id]
	if !ok || task == nil {
		return
	}
	if strings.TrimSpace(clipID) != "" {
		task.VideoClipID = strings.TrimSpace(clipID)
	}
	if strings.TrimSpace(status) != "" {
		task.VideoStatus = strings.TrimSpace(status)
	}
	tm.persist()
}

func (tm *TaskManager) CompleteTask(id string) error {
	tm.mu.RLock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.RUnlock()
		return fmt.Errorf("task %s not found", id)
	}
	isRunning := task.Status == TaskStatusRunning || task.Status == TaskStatusQueued
	doneCh := task.doneCh
	tm.mu.RUnlock()

	// Auto-stop running tasks so the user's "mark complete" gesture
	// from mobile doesn't leave the runner eating tokens after the
	// status flips. Mirrors DeleteTask's auto-stop pattern.
	if isRunning {
		if err := tm.StopTask(id); err != nil {
			log.Printf("[task %s] Stop failed during complete: %v", id, err)
		}
		if doneCh != nil {
			select {
			case <-doneCh:
			case <-time.After(3 * time.Second):
				log.Printf("[task %s] Timed out waiting for process exit during complete", id)
			}
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok = tm.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	now := time.Now()
	task.Status = TaskStatusFinished
	if task.FinishedAt == nil {
		task.FinishedAt = &now
	}
	tm.persist()
	return nil
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
func (tm *TaskManager) CreateChainedTasks(tasks []ChainedTaskInput, model, source, runnerID string, autoRetry bool, viewport *TaskViewport) ([]*Task, error) {
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
			eventCh:      make(chan map[string]interface{}, 32),
			doneCh:       make(chan struct{}),
			ChainID:      chainID,
			ChainOrder:   i,
			AutoRetry:    autoRetry,
			AutoRetryMax: retryMax,
			TaskViewport: viewport, // set before startProcess so task 0 gets the hint
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
				ID:           t.ID,
				Title:        t.Title,
				Status:       t.Status,
				ChainID:      t.ChainID,
				ChainOrder:   t.ChainOrder,
				CreatedAt:    t.CreatedAt,
				StartedAt:    t.StartedAt,
				FinishedAt:   t.FinishedAt,
				ResultText:   t.ResultText,
				CostUSD:      t.CostUSD,
				InputTokens:  t.InputTokens,
				OutputTokens: t.OutputTokens,
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
	task.eventCh = make(chan map[string]interface{}, 32)
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
