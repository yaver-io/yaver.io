package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// rawOutputMaxBytes caps the in-memory raw runner-stdout tail retained on
// the Task for the console view's `?rawSince=` replay. Raw bytes are dense
// (ANSI + TUI redraws can exceed the groomed text 10x), so an uncapped
// tail would hold a multi-hour opencode run hostage in RAM — same defect
// the streamBuffer caps exist for. Keep the tail; the head is rarely
// useful by 512KB of terminal bytes.
const rawOutputMaxBytes = 512 * 1024

// rawOutputTruncatedMarker marks the head of a tail-capped raw replay so a
// client opening the console mid-run knows the earliest bytes were dropped.
const rawOutputTruncatedMarker = "\n…[console replay truncated — earlier terminal bytes dropped]…\n"

const (
	TaskStatusQueued  TaskStatus = "queued"
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusReady means the current runner turn ended and the same native
	// runner conversation can accept another message. It is deliberately not
	// Running (the runner is not coding) and not Review (the runner has not
	// claimed the requested work is fully complete).
	TaskStatusReady    TaskStatus = "ready"
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
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// Mode is a runner-specific subcommand selector. Currently only
	// honored by opencode where it maps to `--agent <mode>` (build /
	// plan / any custom agent the user has defined in their
	// opencode.json config). Empty = runner default. Other runners
	// ignore it.
	Mode string `json:"mode,omitempty"`
	// Goal is the Yaver goal-mode objective (opencode goal plugin). When
	// set on a task, startProcess wraps the opencode prompt so the runner
	// opens a persistent goal (create_goal) and keeps working toward it.
	Goal         string `json:"goal,omitempty"`
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
}

var activeTaskManager *TaskManager

func ActiveTaskManager() *TaskManager {
	return activeTaskManager
}

// builtinRunners defines yaver's first-class runner configurations.
// claude-code, codex, and opencode are binary runners we ship support for;
// everything else (Ollama, OpenRouter, GLM, ZAI, DeepSeek, …) reaches the
// system through opencode's BYOK provider config rather than yaver
// shipping a dedicated wrapper for each CLI. "remoteless" is the hosted-
// model lane (default DeepSeek): its interim backend is the opencode
// binary with a DeepSeek key, and it later swaps to an in-process Go loop
// with no binary at all — callers pin the stable id and never care.
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
		Model:       "claude-opus-4-8",
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
		// `--full-auto` was REMOVED from `codex exec` in 0.144.x: it mapped to
		// approval policy "on-failure", which the current binary rejects —
		//   error: invalid value 'on-failure' for '--ask-for-approval …'
		// so EVERY codex task failed on a flag parse the user cannot act on
		// (observed live 2026-07-27, codex-cli 0.144.1). `--sandbox
		// workspace-write` is the same non-interactive policy in the flag set
		// that version actually offers [read-only|workspace-write|
		// danger-full-access], and it keeps the workspace-write sandbox the
		// -C splice below depends on.
		Args: []string{"exec", "--sandbox", "workspace-write", "{prompt}"},
		// Keep this aligned with the backend model catalogue used by
		// /agent/runners. Older "gpt-5.3-codex" ChatGPT-account runs now
		// fail with "model is not supported". gpt-5.6-sol was probed
		// successfully with the subscription login and matches the fallback
		// catalogue returned by /agent/runners.
		Model:      "gpt-5.6-sol",
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
	"remoteless": {
		RunnerID: "remoteless",
		Name:     "Remoteless AI (DeepSeek)",
		// The hosted-model lane. Interim backend: the opencode binary with
		// a DeepSeek BYOK key (opencode.json provider.deepseek or the
		// DEEPSEEK_API_KEY env/vault). The id is the STABLE contract —
		// callers pin "remoteless"; the backend later swaps to an
		// in-process Go loop with no binary at all (see
		// docs/architecture/REMOTELESS_AI.md). --model is injected by
		// startProcess (the opencode splice below), so a per-task model
		// override wins over the deepseek default.
		Command:     "opencode",
		Args:        []string{"run", "--dangerously-skip-permissions", "{prompt}"},
		Model:       "deepseek/deepseek-v4-flash",
		OutputMode:  "raw",
		ExitCommand: "/quit",
	},
}

// GetRunnerConfig returns the RunnerConfig for a given runner ID.
// Falls back to defaultRunner if not found.
func GetRunnerConfig(runnerID string) RunnerConfig {
	runnerID = normalizeRunnerID(runnerID)
	if reason, retired := retiredRunnerReason(runnerID); retired {
		return RunnerConfig{RunnerID: runnerID, Name: reason}
	}
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
// order for "default installed runner" fallbacks. "remoteless" is the
// hosted-model lane (interim backend = opencode + DeepSeek key); it
// comes last so a working subscription binary still wins the default
// fallback.
var supportedRunnerIDs = []string{"claude", "codex", "opencode", "remoteless"}

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
		return strings.HasPrefix(m, "claude") || m == "opus" || m == "sonnet" || m == "haiku"
	case "codex":
		return strings.HasPrefix(m, "gpt") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
	case "opencode", "remoteless":
		provider, modelName, ok := strings.Cut(m, "/")
		return ok && strings.TrimSpace(provider) != "" && strings.TrimSpace(modelName) != ""
	}
	// Unknown runner → don't second-guess.
	return true
}

// effectiveModelFor picks the model to splice into a runner's argv:
// the task's pinned model first, the runner's configured fallback second —
// and NOTHING when the pick is incompatible with the runner being spawned.
// A compatible model that this account already refused routes to the current
// Convex-managed Yaver default for that runner.
func effectiveModelFor(runnerID, taskModel, runnerModel string) string {
	m := strings.TrimSpace(taskModel)
	if m == "" {
		m = strings.TrimSpace(runnerModel)
	}
	if m == "" {
		return ""
	}
	if !runnerModelCompatible(runnerID, m) {
		log.Printf("[task] dropping model %q — incompatible with runner %q; the CLI's own default wins", m, runnerID)
		return ""
	}
	// AUTOFIX (2026-08-02). runnerModelCompatible is a NAME heuristic — it
	// answers "is this model plausibly for this runner", which cannot answer
	// "can THIS ACCOUNT run it". Entitlement lives on the provider's side, so
	// the only thing that knows is the operation, and the operation already
	// told us once: a 400 "The 'gpt-5.4' model is not supported when using
	// Codex with a ChatGPT account". Without this the user re-ran the same
	// doomed task forever, changing nothing, getting the identical error.
	//
	// Apply the current Yaver global default. See model_support_ledger.go.
	if globalModelSupport.Refused(runnerID, m) {
		fallback := yaverDefaultModelForRunner(runnerID)
		if fallback != "" && !strings.EqualFold(fallback, m) && !globalModelSupport.Refused(runnerID, fallback) {
			log.Printf("[task] model %q was refused by %s on this machine; using Yaver default %q", m, normalizeRunnerID(runnerID), fallback)
			return fallback
		}
		log.Printf("[task] dropping model %q — %s refused it and no unrefused Yaver default remains", m, normalizeRunnerID(runnerID))
		return ""
	}
	return m
}

// cachedModels stores models fetched from Convex for the /agent/runners endpoint.
var (
	cachedModelsMu sync.RWMutex
	cachedModels   []BackendModel
)

// LoadRunnersFromBackend populates builtinRunners from Convex backend data.
func LoadRunnersFromBackend(runners []backendRunnerFull) {
	for _, r := range runners {
		// Normalize BEFORE anything keys off the id. Convex ships Claude as
		// "claude-code" while builtinRunners is keyed "claude", so the raw id
		// missed the builtin lookup below and registered a second Claude — and
		// the runner list dedups on the raw id too (httpserver.go:3561), so
		// every picker showed "Claude Code" twice. Normalizing on ingestion is
		// the boundary fix: an alias can never enter the map as a key, which
		// closes the list, the MCP list_runners verb, and env_profile at once.
		id := normalizeRunnerID(r.RunnerID)
		if r.Command == "" || id == "custom" {
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
		if IsSupportedRunner(id) {
			if existing, ok := builtinRunners[id]; ok {
				log.Printf("  Runner loaded: %s (%s) — using local builtin (ignoring backend args)", existing.Name, existing.RunnerID)
				continue
			}
		}
		rc := RunnerConfig{
			RunnerID:        id,
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
		builtinRunners[id] = rc
		log.Printf("  Runner loaded: %s (%s)", rc.Name, rc.RunnerID)
	}
}

// LoadModelsFromBackend caches models fetched from Convex.
func LoadModelsFromBackend(models []BackendModel) {
	cachedModelsMu.Lock()
	defer cachedModelsMu.Unlock()
	cachedModels = normalizeBackendModelsWithYaverDefaults(models)
}

// GetCachedModels returns models loaded from Convex.
func GetCachedModels() []BackendModel {
	cachedModelsMu.RLock()
	defer cachedModelsMu.RUnlock()
	return append([]BackendModel(nil), cachedModels...)
}

type runnerBinaryCheckEntry struct {
	path string
	at   time.Time
}

var (
	runnerBinaryCheckCache    sync.Map // map[string]runnerBinaryCheckEntry
	runnerBinaryCheckCacheTTL = 30 * time.Second
	// A successful runner probe remains useful evidence after the short cache
	// TTL.  Status polling and task creation can race under load: one
	// `<runner> --version` child may answer while a sibling misses its deadline
	// and is SIGKILLed with empty output.  Rejecting the task in that state is a
	// false negative -- the same binary just proved it runs, and the real task
	// launch is the operation that ultimately matters.
	//
	// Keep the last success long enough to bridge a transient cold/contended
	// probe, but only use it when the resolved file is still executable and the
	// new failure is specifically a deadline.  Missing/replaced binaries and
	// immediate non-zero exits still fail normally.
	runnerBinaryCheckStaleSuccessTTL = 10 * time.Minute
)

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
	// Hidden keeps product-owned bootstrap context available to the runner
	// without pretending the user typed it in the visible conversation.
	Hidden bool `json:"hidden,omitempty"`
}

// maxSeededForkTurns bounds how much parent history a fork copies into a child
// for display continuity. A whole-thread copy per fork would compound into
// unbounded persisted state across a long fork chain; the last ~40 turns is
// plenty for the "don't lose the summary" thread the user actually reads.
const maxSeededForkTurns = 40

// seedForkTurns returns the tail of `turns` bounded to maxSeededForkTurns.
// Nil-safe; never mutates the input.
func seedForkTurns(turns []ConversationTurn) []ConversationTurn {
	if len(turns) <= maxSeededForkTurns {
		return turns
	}
	return turns[len(turns)-maxSeededForkTurns:]
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
	if containsHardRunnerFailure(runnerID, output) {
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
	case "claude":
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

func containsHardRunnerFailure(runnerID, output string) bool {
	// A task's PTY contains arbitrary source code, diffs and test fixtures. The
	// runner's own terminal ending is the only evidence allowed to turn a
	// useful non-zero Codex exit into a hard failure. In particular, never let
	// a Codex task that happens to read Claude's `/login` text become a Claude
	// auth incident.
	tail := runnerAuthClassifyTail(output)
	if rejected, _ := ClassifyRunnerAuthFailureFor(runnerID, tail); rejected {
		return true
	}
	lower := strings.ToLower(tail)
	for _, needle := range []string{
		"invalid_request_error",
		"unsupported model",
		"model is not supported",
		"failed to authenticate",
		"oauth access token has been revoked",
		"provided authentication token is expired",
		"token_expired",
		"refresh_token_reused",
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
	AuthConfigured bool   `json:"authConfigured"`
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
// runnerInventoryReady is deliberately stricter than a PATH/configuration
// check. A credential file can exist while the provider rejects it; only the
// provider-exercised AuthVerified verdict may put a first-class coding runner
// in the green state.
func runnerInventoryReady(id string, rs RunnerRuntimeStatus) bool {
	id = normalizeRunnerID(id)
	if id == "codex" || id == "claude" || id == "opencode" {
		return rs.Ready && rs.AuthConfigured && rs.AuthVerified
	}
	return rs.Ready
}

func runnerVerificationWarning(id string, rs RunnerRuntimeStatus) string {
	id = normalizeRunnerID(id)
	if (id == "codex" || id == "claude" || id == "opencode") && rs.AuthConfigured && !rs.AuthVerified {
		return "Credentials are present but have not completed a provider operation yet"
	}
	return ""
}

func (tm *TaskManager) GetRunnerInfos() []RunnerInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	infos := make([]RunnerInfo, 0) // never nil — Convex expects [] not null
	seenRunner := map[string]bool{}
	decorate := func(info *RunnerInfo, id string) {
		info.CheckedAt = time.Now().UnixMilli()
		cfg, ok := builtinRunners[id]
		if !ok {
			return
		}
		if _, err := exec.LookPath(cfg.Command); err != nil {
			info.Installed = false
			return
		}
		rs := DetectRunnerRuntimeStatus(cfg, tm.workDir)
		info.Installed = true
		info.Ready = runnerInventoryReady(id, rs)
		info.AuthConfigured = rs.AuthConfigured
		info.AuthPresent = rs.AuthPresent
		info.AuthVerified = rs.AuthVerified
		info.AuthVerifiedAt = runnerAuthVerifiedAtMillis(id)
		info.AuthSource = rs.AuthSource
		info.Warning = rs.Warning
		if !info.Ready && info.Error == "" && rs.AuthConfigured && !rs.AuthVerified {
			info.Warning = "Credentials are present but have not completed a provider operation yet"
		}
		info.Error = rs.Error
	}
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
			info := RunnerInfo{
				TaskID:   t.ID,
				RunnerID: t.RunnerID,
				Model:    t.Model,
				PID:      pid,
				Status:   status,
				Title:    t.Title,
			}
			decorate(&info, normalizeRunnerID(t.RunnerID))
			infos = append(infos, info)
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
			// Claude USED to be excluded here, on the grounds that its
			// keychain-backed auth could not be probed on macOS so
			// AuthConfigured stayed false even for a signed-in user. That
			// exemption is now a bug in the other direction: detectClaudeStatus
			// asks `claude auth status --json`, which answers on every
			// platform, and MarkRunnerAuthInvalidReason clears AuthConfigured
			// on an observed 401. Keeping claude out of this branch meant a
			// REVOKED claude still shipped status:"ready" to Convex — the
			// heartbeat half of the same false green.
			healthStatus = "needs-auth"
		case !rs.AuthVerified && (id == "codex" || id == "claude" || id == "opencode"):
			// A local auth file is inventory, not proof. Keep the runner out of
			// the green ready state until a real provider operation has answered.
			healthStatus = "needs-verification"
		}
		infos = append(infos, RunnerInfo{
			TaskID:   "",
			RunnerID: id,
			Status:   healthStatus,
			Title:    "",
			// CheckedAt was set only on rows decorated for a LIVE task, so
			// every synthetic row — i.e. every row on an idle box, which is
			// most of them — reached Convex with no timestamp at all. A
			// consumer could not tell a verdict measured a second ago from one
			// measured at boot. Persisting auth state without a clock is how a
			// false green moves from memory into the database.
			CheckedAt:      time.Now().UnixMilli(),
			Installed:      true,
			Ready:          runnerInventoryReady(id, rs),
			AuthConfigured: rs.AuthConfigured,
			AuthPresent:    rs.AuthPresent,
			AuthVerified:   rs.AuthVerified,
			AuthVerifiedAt: runnerAuthVerifiedAtMillis(id),
			AuthSource:     rs.AuthSource,
			Warning:        firstNonEmpty(rs.Warning, runnerVerificationWarning(id, rs)),
			Error:          rs.Error,
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
//	"car-audio" | "car-android-auto" | "car-carplay"
//	"tv-living-room" | "tv-android" | "tv-apple"
//	"mcp" | "cli" | "" (no hint)
//
// All fields optional. nil = no viewport hint, default behavior.
type TaskViewport struct {
	Surface string `json:"surface,omitempty"`
	// Interaction is the dominant input mode on the consuming surface:
	// "voice", "dpad", "touch", "keyboard", "approval", "stream".
	Interaction string `json:"interaction,omitempty"`
	PaneCount   int    `json:"paneCount,omitempty"` // parallel Claude sessions visible
	PaneCols    int    `json:"paneCols,omitempty"`  // approx pane width in mono chars
	PaneRows    int    `json:"paneRows,omitempty"`  // approx pane height in rows
	Voice       bool   `json:"voice,omitempty"`     // task originated from voice (STT)
	TTSBudget   int    `json:"ttsBudget,omitempty"` // max chars in TTS readback (0 = 280 default)
	// VisualBudget tunes how much visible detail the surface can carry:
	// "none" (audio-only), "glance", "panel", or "full".
	VisualBudget string `json:"visualBudget,omitempty"`
	// RiskPolicy names a product policy for confirmations and sensitive output:
	// "normal", "driving", "watch", "shared-tv", "mcp".
	RiskPolicy string `json:"riskPolicy,omitempty"`

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
	// Codex accepts the model-scoped level returned by app-server model/list.
	// GPT-5.6 Sol/Terra currently extend the core levels with max and ultra.
	ReasoningEffort string
	// SessionStartedFrom is the product entry point that created the shared
	// Yaver session: tasks, vibing, new-application, or mobile-workspace.
	// StartedFromSurface records the initiating client; neither changes when
	// another surface later attaches to the same session.
	SessionStartedFrom string
	StartedFromSurface string
	SessionSettings    *ClientSessionSettings
	WorkDir            string
	ProjectSessionID   string

	// InitialUserPrompt is WHAT THE USER TYPED (or said, or shook their phone
	// about). It becomes the first stored ConversationTurn — the chat bubble
	// on mobile, web, and every other transcript.
	//
	// Set it. Always. When it is empty the turn falls back to Description and
	// then Title, and any producer that put scaffolding in those fields has
	// just written Yaver's own briefing into the user's bubble.
	InitialUserPrompt string
	// InitialUserPromptHidden is for product-owned kickoff turns (for example,
	// the mobile app builder handoff). The runner still receives the prompt,
	// while every transcript can start with the assistant's first response.
	InitialUserPromptHidden bool

	// PromptText is the TRANSPORT prompt — the scaffolded text the runner
	// reads. Empty means "the runner reads Title (+ Description)", which is
	// the right default for producers that add no scaffolding at all.
	//
	// Producers that DO add scaffolding put it here and leave Title /
	// Description / InitialUserPrompt as the user's own words. See
	// Task.PromptText for the full rule.
	PromptText string

	SliceContract *TaskSliceContract
	Placement     *TaskPlacementMetadata

	// ProjectName is the portable project identity selected by the surface.
	// It is intentionally not a path: a phone/web surface may select Medici
	// from a Mac, while the task actually runs on a Hetzner Ubuntu box whose
	// checkout lives elsewhere. The runner machine resolves this against its
	// own discovered projects before spawning.
	ProjectName string

	// MCPServers is the per-task external MCP allowlist. Empty means no
	// external MCPs; the runner still gets Yaver's own MCP doorway.
	MCPServers []string

	// IncludeYaverMcp controls whether the runner sees Yaver's own `yaver mcp`
	// doorway for this task. Defaults true. When the user explicitly deselects
	// it on a surface (web/mobile MCP chips), the runner gets ONLY the external
	// MCPs in MCPServers — possibly none, which is a real "no MCP tools" task.
	IncludeYaverMcp bool

	// SeedTurns is prior conversation history to PREPEND to the new task's
	// Turns purely for DISPLAY continuity — it is NOT re-sent to the runner
	// (the runner receives its context via the prompt/handoff). A fork sets
	// this to the parent's turns so the child renders as one continuous
	// WhatsApp-style thread instead of an orphaned single exchange (the
	// 2026-07-21 "I lost the summary — every follow-up starts a fresh chat"
	// report). Bounded by seedForkTurns before use so a long parent thread
	// can't bloat every child's persisted state.
	SeedTurns []ConversationTurn

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
	// decisions in prose.
	AskFreely bool

	// ResumeLast + ResumeSessionID wire native session resume into the FIRST
	// spawn (used by the scheduler for recurring schedules with resume on).
	// ResumeSessionID seeds task.SessionID so claude/glm/codex can resume by
	// id; OpenCode also uses its captured id via `run --session`. Default zero = fresh.
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
	AskMode bool

	// RedactPII enforces the company dataPolicy.redactPII control on this
	// runtime: the assembled prompt is scrubbed of high-confidence PII/secrets
	// (RedactPII()) before any runner sees it. Set ONLY from the server-stamped
	// X-Yaver-RedactPII header (which is derived from the validated SDK token's
	// `policy:redactPII` scope) — never from a client body, so a caller cannot
	// turn the privacy control off.
	RedactPII bool

	// RawRunnerCommand sends a slash-command prompt directly to the selected
	// runner. No Yaver project/context/policy prompt is appended. This is for
	// runner-native commands like /goal and /exit, where adding instructions
	// changes the command semantics.
	RawRunnerCommand bool

	// Goal arms Yaver goal-mode on the task (opencode only, via the
	// opencode-goal-plugin's create_goal tool / /goal command). When set, the
	// opencode prompt frame is wrapped with the goal instruction so the
	// runner opens a persistent goal and keeps working toward it across
	// turns until complete/blocked/limited. Persisted on the Task + surfaced
	// via TaskInfo.goal so every surface can render a Goal chip and drive
	// /goal status|resume|clear.
	Goal string

	// Runner/render split fields — see the same-named Task fields and
	// task_ensure_clone.go. The createTask handler is primary-owner only.
	GitRemote string
	GitBranch string
	AutoPush  string
}

type TaskResumeOptions struct {
	RunnerID string `json:"runnerId,omitempty"`
	Model    string `json:"model,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// ClientSessionSettings is mutable provenance/capability state for the client
// currently driving a Yaver session. It describes the real transport/render
// lane instead of asking the agent to infer it from task source strings.
type ClientSessionSettings struct {
	AppName       string    `json:"appName,omitempty"`
	AppVersion    string    `json:"appVersion,omitempty"`
	BuildNumber   string    `json:"buildNumber,omitempty"`
	Surface       string    `json:"surface,omitempty"`
	ClientSurface string    `json:"clientSurface,omitempty"`
	Platform      string    `json:"platform,omitempty"`
	DeviceClass   string    `json:"deviceClass,omitempty"`
	Lane          string    `json:"lane,omitempty"`
	RuntimeMode   string    `json:"runtimeMode,omitempty"`
	Dogfood       bool      `json:"dogfood"`
	UsageMode     string    `json:"usageMode,omitempty"`
	ChatEnabled   bool      `json:"chatEnabled"`
	RenderEnabled bool      `json:"renderEnabled"`
	Revision      int64     `json:"revision"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type TaskFailureFix struct {
	Type      string `json:"type"`
	RunnerID  string `json:"runnerId,omitempty"`
	TestAfter bool   `json:"testAfter,omitempty"`
}

type TaskFailureDiagnosis struct {
	Kind       string          `json:"kind"`
	Code       string          `json:"code"`
	Title      string          `json:"title"`
	Reason     string          `json:"reason"`
	Remedy     string          `json:"remedy"`
	RunnerID   string          `json:"runnerId,omitempty"`
	Model      string          `json:"model,omitempty"`
	Probe      string          `json:"probe,omitempty"`
	DetectedAt time.Time       `json:"detectedAt"`
	Fix        *TaskFailureFix `json:"fix,omitempty"`
}

type PendingFollowUp struct {
	Input     string            `json:"input"`
	Images    []ImageAttachment `json:"images,omitempty"`
	Options   TaskResumeOptions `json:"options,omitempty"`
	Timestamp time.Time         `json:"timestamp,omitempty"`
}

func recordSessionMessage(task *Task, role string, at time.Time) {
	if task == nil {
		return
	}
	switch role {
	case "user":
		if task.FirstUserMessageAt == nil {
			first := at
			task.FirstUserMessageAt = &first
		}
		last := at
		task.LastUserMessageAt = &last
	case "assistant":
		if task.FirstAgentResponseAt == nil {
			first := at
			task.FirstAgentResponseAt = &first
		}
		last := at
		task.LastAgentResponseAt = &last
	}
	task.LastActiveAt = at
}

// TaskExecutionIdentity is the one cross-surface answer to "which
// conversation and terminal seat will my next message use?". Runner session
// IDs and tmux IDs are different namespaces: the former carries model context;
// the latter identifies the observable process seat. Surfaces must show both
// and must never infer one from the other.
type TaskExecutionIdentity struct {
	YaverSessionID       string                 `json:"yaverSessionId"`
	TaskID               string                 `json:"taskId"`
	RemoteBoxID          string                 `json:"remoteBoxId,omitempty"`
	RunnerName           string                 `json:"runnerName,omitempty"`
	RunnerID             string                 `json:"runnerId,omitempty"`
	RunnerSessionID      string                 `json:"runnerSessionId,omitempty"`
	HostKind             string                 `json:"hostKind,omitempty"`
	StartedFrom          string                 `json:"startedFrom,omitempty"`
	StartedFromSurface   string                 `json:"startedFromSurface,omitempty"`
	InitialSurface       string                 `json:"initialSurface,omitempty"`
	SessionStartedAt     time.Time              `json:"sessionStartedAt"`
	LastSurface          string                 `json:"lastSurface,omitempty"`
	LastActiveAt         time.Time              `json:"lastActiveAt"`
	FirstUserMessageAt   *time.Time             `json:"firstUserMessageAt,omitempty"`
	FirstAgentResponseAt *time.Time             `json:"firstAgentResponseAt,omitempty"`
	LastUserMessageAt    *time.Time             `json:"lastUserMessageAt,omitempty"`
	LastAgentResponseAt  *time.Time             `json:"lastAgentResponseAt,omitempty"`
	SessionSettings      *ClientSessionSettings `json:"sessionSettings,omitempty"`
	DeletedAt            *time.Time             `json:"deletedAt,omitempty"`
	Resumable            bool                   `json:"resumable"`
	TmuxSession          string                 `json:"tmuxSession,omitempty"`
	TmuxSessionID        string                 `json:"tmuxSessionId,omitempty"`
	TmuxWindowIndex      string                 `json:"tmuxWindowIndex,omitempty"`
	TmuxWindowName       string                 `json:"tmuxWindowName,omitempty"`
	TmuxPaneIndex        string                 `json:"tmuxPaneIndex,omitempty"`
	TmuxPaneID           string                 `json:"tmuxPaneId,omitempty"`
}

// TaskContinuationConflict is returned when "continue" would actually have
// to create or switch a native runner session. The caller gets the identity it
// attempted to resume, so every surface can name the missing/mismatched seat.
type TaskContinuationConflict struct {
	Code     string
	Reason   string
	Identity TaskExecutionIdentity
}

func (e *TaskContinuationConflict) Error() string { return e.Reason }

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`

	// PromptText is the TRANSPORT prompt: the exact text the runner is asked
	// to act on, scaffolding and all. Title/Description are the DISPLAY
	// fields: what the user typed, and what every surface renders.
	//
	// Before this field existed the two were the same string. A producer that
	// needed to brief the runner — the vibing execution context, the
	// security context, the watch/car surface contract, the feedback-report
	// body — had exactly one place to put it: Title. So Yaver's own briefing
	// became the task's name in the list, and (because the first stored
	// ConversationTurn falls back Title→Description when no InitialUserPrompt
	// was given) it also became the user's own chat bubble. The user's report,
	// 2026-07-27: "do NOT pollute the UI with our prefix prompt … show what
	// the user actually wrote."
	//
	// The rule for any producer that adds scaffolding:
	//
	//	Title             → a short human label, or the user's own words
	//	Description       → the user's own words (or empty)
	//	InitialUserPrompt → the user's own words, ALWAYS
	//	PromptText        → the scaffolded thing the runner should read
	//
	// The json tag is PERSISTENCE only — the wire DTO is TaskInfo, which has
	// no PromptText field and never will. That is the structural guarantee:
	// a surface cannot render the scaffolding because it is never sent one.
	PromptText string `json:"promptText,omitempty"`
	Source     string `json:"source,omitempty"` // "mobile", "mcp", "cli"
	Model      string `json:"model,omitempty"`
	// Per-turn Codex setting. It is task-scoped so changing it never rewrites
	// config.toml or another live conversation.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	RunnerID        string `json:"runnerId,omitempty"` // which runner is executing this task
	// YaverSessionID is Yaver's stable, entry-point-independent conversation
	// handle. Tasks/Chat, Vibing/render, and new-application/workspace views all
	// attach to this identity; runner and tmux IDs remain child namespaces.
	YaverSessionID       string                 `json:"yaverSessionId,omitempty"`
	RemoteBoxID          string                 `json:"remoteBoxId,omitempty"`
	RunnerName           string                 `json:"runnerName,omitempty"`
	SessionStartedFrom   string                 `json:"sessionStartedFrom,omitempty"`
	StartedFromSurface   string                 `json:"startedFromSurface,omitempty"`
	InitialSurface       string                 `json:"initialSurface,omitempty"`
	SessionStartedAt     time.Time              `json:"sessionStartedAt,omitempty"`
	LastSurface          string                 `json:"lastSurface,omitempty"`
	LastActiveAt         time.Time              `json:"lastActiveAt,omitempty"`
	DeletedAt            *time.Time             `json:"deletedAt,omitempty"`
	FirstUserMessageAt   *time.Time             `json:"firstUserMessageAt,omitempty"`
	FirstAgentResponseAt *time.Time             `json:"firstAgentResponseAt,omitempty"`
	LastUserMessageAt    *time.Time             `json:"lastUserMessageAt,omitempty"`
	LastAgentResponseAt  *time.Time             `json:"lastAgentResponseAt,omitempty"`
	SessionSettings      *ClientSessionSettings `json:"sessionSettings,omitempty"`
	// Transport records the protocol that actually executed the task. It lets
	// surfaces and doctor distinguish native ACP from the compatibility CLI
	// lane without guessing from runner output.
	Transport string `json:"transport,omitempty"`
	// TransportReason is a compact, safe explanation of an intentional
	// compatibility fallback. It is structured task state so every client can
	// explain what happened without scraping logs or terminal output.
	TransportReason string `json:"transportReason,omitempty"`
	// Goal is the Yaver goal-mode objective (opencode goal plugin). Empty =
	// one-shot task. Set = persistent goal the runner keeps working toward.
	Goal      string `json:"goal,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// HostKind identifies the local conversation keeper without exposing
	// process arguments or paths. terminal_tmux is implemented today;
	// desktop_gui is reserved for native Codex/Claude/OpenCode adapters.
	HostKind         string `json:"hostKind,omitempty"`
	ProjectSessionID string `json:"projectSessionId,omitempty"`
	// ResumeLast asks startProcess to resume the prior session on the FIRST
	// spawn (not just on follow-ups). Set by the scheduler when a recurring
	// schedule with resume enabled re-fires, so the run picks up where the
	// previous fire left off (claude/glm via SessionID, opencode via
	// --session, codex via exec resume). Default false = fresh spawn.
	ResumeLast bool   `json:"-"`
	Output     string `json:"output"`
	// RawOutput is the raw runner stdout tail (ANSI intact) retained for
	// the console view's `?rawSince=` replay. Mirrors `Output` — written
	// by readRawOutput BEFORE the grooming filters, tail-capped to
	// rawOutputMaxBytes so a runaway TUI can't hold memory forever. Never
	// shipped in task listings (surfaces get it via the SSE raw frames /
	// the raw replay endpoint), so it is deliberately not `json:"-"`-
	// hidden here: the field lives on the in-memory Task only.
	RawOutput  string `json:"-"`
	ResultText string // Extracted clean result text from Claude
	// Presentation is the bounded, semantic runner narrative consumed by
	// remote surfaces. Raw terminal bytes remain in RawOutput; command and diff
	// detail remain in their structured/folded lanes.
	Presentation    []TaskPresentationMessage `json:"presentation,omitempty"`
	PresentationSeq int64                     `json:"presentationSeq,omitempty"`
	// ReviewRequested is set only by the runner's structured
	// yaver_report_complete MCP call. Process exit, an idle tmux pane, and a
	// line of terminal text must never promote a task into Review.
	ReviewRequested bool                  `json:"reviewRequested,omitempty"`
	ReviewSummary   string                `json:"reviewSummary,omitempty"`
	Failure         *TaskFailureDiagnosis `json:"failure,omitempty"`
	CostUSD         float64               // Total API cost
	InputTokens     int                   // Tokens consumed (prompt + cache reads + cache creation)
	OutputTokens    int                   // Tokens produced by the model
	Turns           []ConversationTurn    // Full conversation history
	CreatedAt       time.Time             `json:"created_at"`
	StartedAt       *time.Time            `json:"started_at,omitempty"`
	FinishedAt      *time.Time            `json:"finished_at,omitempty"`

	WorkDir string `json:"workDir,omitempty"` // per-task workDir (auto-detected from prompt)
	// ProjectName is the portable project identity selected by the user. It is
	// safe to echo because it is a basename/display name, not an absolute path.
	ProjectName string `json:"projectName,omitempty"`

	// MCPServers is deliberately not echoed to generic task JSON. It controls
	// runner spawn scope only; UI state owns what it selected.
	MCPServers []string `json:"-"`

	// IncludeYaverMcp echoes the per-task Yaver-MCP opt-in. New tasks default
	// false; a fork may inherit the parent's explicit scope.
	IncludeYaverMcp bool `json:"includeYaverMcp,omitempty"`

	// Runner/render machine split (task_ensure_clone.go): git identity the
	// surface passed so THIS box can materialize its own clone when it was
	// chosen as the runner but lacks the source, and the push policy that
	// converges the result back through git afterwards.
	GitRemote       string `json:"gitRemote,omitempty"`
	GitBranch       string `json:"gitBranch,omitempty"`
	AutoPush        string `json:"autoPush,omitempty"`      // never|ask|always ("" = no policy)
	TmuxSession     string `json:"tmuxSession,omitempty"`   // tmux session name (for adopted sessions)
	TmuxSessionID   string `json:"tmuxSessionId,omitempty"` // tmux session_id, e.g. "$1"
	TmuxWindowIndex string `json:"tmuxWindowIndex,omitempty"`
	TmuxWindowName  string `json:"tmuxWindowName,omitempty"`
	TmuxPaneIndex   string `json:"tmuxPaneIndex,omitempty"`
	TmuxPaneID      string `json:"tmuxPaneId,omitempty"` // tmux pane_id, e.g. "%17"
	IsAdopted       bool   `json:"isAdopted,omitempty"`  // true if adopted from an existing tmux session

	// Chained tasks: execute in order, next starts when previous completes
	ChainID    string `json:"chainId,omitempty"`    // shared ID linking tasks in a chain
	ChainOrder int    `json:"chainOrder,omitempty"` // 0-based position in the chain

	// Auto-retry: retry failed tasks with error context
	AutoRetry      bool `json:"autoRetry,omitempty"`      // enable auto-retry on task failure
	AutoRetryCount int  `json:"autoRetryCount,omitempty"` // how many task-level retries so far
	AutoRetryMax   int  `json:"autoRetryMax,omitempty"`   // max task-level retries (default 3)

	// Viewport — surface + pane geometry hints. Prompt wrapper uses
	// this to add a one-line display-context note for Claude so
	// response shape matches the screen.
	TaskViewport *TaskViewport `json:"viewport,omitempty"`

	// Image paths saved to disk for this task (not persisted in tasks.json)
	ImagePaths []string `json:"-"`

	SliceContract *TaskSliceContract     `json:"sliceContract,omitempty"`
	Placement     *TaskPlacementMetadata `json:"placement,omitempty"`

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
	ProofStatus  string `json:"proofStatus,omitempty"` // capturing|ready|failed

	CommitSHA     string `json:"commitSha,omitempty"`
	CommitSubject string `json:"commitSubject,omitempty"`
	CommitBranch  string `json:"commitBranch,omitempty"`
	DiffShortstat string `json:"diffShortstat,omitempty"`
	FeedbackID    string `json:"feedbackId,omitempty"`

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

	// RawRunnerCommand bypasses every Yaver prompt wrapper for runner-native
	// slash commands. See TaskCreateOptions.RawRunnerCommand.
	RawRunnerCommand bool `json:"rawRunnerCommand,omitempty"`

	PendingFollowUps []PendingFollowUp `json:"pendingFollowUps,omitempty"`

	runner RunnerConfig // the runner config used for this task (not persisted)
	// codexLastMsgPath: for embedded chat mode we run codex with
	// --output-last-message <file> and read ONLY the final assistant message
	// from it as ResultText — no reasoning, tool-log, or banner pollution.
	codexLastMsgPath string
	cmd              *exec.Cmd
	cancel           context.CancelFunc
	stdin            io.WriteCloser
	outputCh         chan string
	// rawOutputCh carries the runner's RAW stdout bytes — ANSI escape
	// sequences, cursor addressing, TUI box-drawing, everything — as they
	// arrived from the process, BEFORE the per-runner grooming filters
	// (opencodeStreamFilter / stripANSI) turn them into chat text. The
	// console view on mobile + web feeds these exact bytes into xterm.js,
	// so a task run under opencode looks the way it does in a real
	// terminal instead of a flattened paragraph. Drops on full (same
	// policy as outputCh) — the raw replay endpoint (`?rawSince=`) is the
	// reliable recovery path.
	rawOutputCh chan []byte
	// echoGuard suppresses a raw-mode runner's verbatim echo of the
	// Yaver-framed prompt before it reaches task.Output or the live stream.
	// Armed by startProcess / startResume with the exact bytes we sent; nil
	// for stream-json runners, which never echo. See prompt_echo_guard.go.
	echoGuard *promptEchoGuard
	// eventCh carries structured (non-text) events for this task —
	// agent_question, agent_answered, agent_question_cancelled, …
	// The SSE writer in handleTaskByID/streamOutput selects on this
	// alongside outputCh and forwards each event verbatim. Old
	// clients that only know `{type:"output"}` and `{type:"done"}`
	// silently ignore unknown types, so adding new event kinds is
	// backwards-compatible. Buffered so a transient SSE backpressure
	// on a phone doesn't block the agent_question registration; the
	// emitter (emitTaskEvent) drops on full rather than stalling.
	eventCh                chan map[string]interface{}
	doneCh                 chan struct{}
	retryCount             int  // Number of auto-restart attempts so far
	modelFallbackAttempted bool // one same-runner retry on the Yaver global default
	// autoPushFired guards the once-per-task converge hook in fireTaskDone
	// (a restart path may reach a terminal state more than once).
	autoPushFired bool
}

func (tm *TaskManager) effectiveTaskWorkDir(task *Task) string {
	if task != nil {
		// ProjectName is portable identity; WorkDir is only a machine-local
		// hint from the surface. Resolve the runner's own checkout first so a
		// phone cannot carry a valid path from a different box into this task.
		if strings.TrimSpace(task.ProjectName) != "" {
			if resolved := resolveTaskProjectOnThisMachine(task.ProjectName, task.WorkDir); resolved != "" {
				return resolved
			}
		}
		if dir := strings.TrimSpace(task.WorkDir); isScannableProjectDir(dir) {
			return dir
		}
		if strings.TrimSpace(task.WorkDir) != "" {
			return strings.TrimSpace(task.WorkDir)
		}
	}
	return tm.workDir
}

func resolveTaskProjectOnThisMachine(projectName, pathHint string) string {
	want := map[string]bool{}
	candidateNames := map[string]bool{}
	for _, raw := range []string{projectName, pathHint, basenameSlug(pathHint)} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		want[strings.ToLower(raw)] = true
		if slug := basenameSlug(raw); slug != "" {
			want[strings.ToLower(slug)] = true
			candidateNames[slug] = true
		}
	}
	if len(want) == 0 {
		return ""
	}
	for _, p := range listDiscoveredProjects() {
		path := strings.TrimSpace(p.Path)
		if path == "" || !isScannableProjectDir(path) {
			continue
		}
		base := strings.ToLower(filepath.Base(path))
		if want[base] || want[strings.ToLower(path)] {
			return path
		}
	}
	// The cache is intentionally refreshed out of band: a filesystem sweep is
	// advisory work and must never hold a task POST hostage. Still, the common
	// checkout shape is a project directly under Workspace/Projects/Code/etc.
	// Probe those exact candidate paths synchronously so a just-booted agent can
	// recover from a phone's foreign (Mac/Windows) path without spawning Codex
	// in that nonexistent directory.
	for _, root := range projectDiscoveryRoots() {
		for name := range candidateNames {
			if name == "" || name == "." || name == string(filepath.Separator) {
				continue
			}
			candidate := filepath.Join(root, name)
			if isScannableProjectDir(candidate) {
				return candidate
			}
		}
	}
	return ""
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
	case "mobile", "mobile-code", "mobile-feedback", "feedback-sdk", "feedback-console", "vibing", "web", "native-guest-shake":
		return true
	default:
		return task.SessionSettings != nil && task.SessionSettings.Dogfood
	}
}

// taskOwnsRecoverableTmuxSeat reports whether Yaver created the exact tmux
// session recorded on this task. A runner turn ending (successfully or not)
// does not resolve the user's task and must not erase this seat; only explicit
// Complete, Stop, and Delete lifecycle actions tear it down.
func taskOwnsRecoverableTmuxSeat(task *Task) bool {
	return taskOwnsNamedTmuxSeat(task)
}

func taskSuccessStatus(task *Task) TaskStatus {
	if task != nil && task.ReviewRequested {
		return TaskStatusReview
	}
	if taskAwaitsManualCompletion(task) {
		return TaskStatusReady
	}
	return TaskStatusFinished
}

// RequestTaskReview records a runner-authored, structured claim that the
// requested work is fully complete. It intentionally leaves a running turn
// running: the process still needs to exit and any final failure wins. This is
// the only automatic route to Review; all other successful conversational
// turns land in Ready and retain their exact runner/tmux session.
func (tm *TaskManager) RequestTaskReview(id, summary string) error {
	tm.mu.Lock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.Unlock()
		return fmt.Errorf("task %s not found", id)
	}
	if task.Status != TaskStatusQueued && task.Status != TaskStatusRunning {
		tm.mu.Unlock()
		return fmt.Errorf("task %s is not actively running", id)
	}
	task.ReviewRequested = true
	task.ReviewSummary = strings.TrimSpace(summary)
	text := "The agent reports the requested work is fully complete; finishing this turn."
	if task.ReviewSummary != "" {
		text = text + " " + trimPresentationText(task.ReviewSummary)
	}
	ev := tm.presentLocked(task, taskPresentationInput{
		ID: "runner-complete", Kind: "status", Text: text, Phase: "review", State: "pending",
	})
	tm.persistAsync()
	tm.mu.Unlock()
	if ev.Message != nil {
		emitTaskEvent(task, map[string]interface{}{
			"type": "presentation", "schema": ev.Schema, "op": ev.Op, "seq": ev.Seq, "message": ev.Message,
		})
	}
	return nil
}

// taskUnresolvedStatus no longer hides a failed turn behind Review. A retained
// runner seat and a successful turn are independent facts: failures stay
// failed, while successful resumable turns become Ready in taskSuccessStatus.
// Review is reserved for an explicit, structured fully-complete claim.
func taskUnresolvedStatus(task *Task, status TaskStatus) TaskStatus {
	return status
}

type TaskCreditEstimate struct {
	Unit                string `json:"unit,omitempty"`
	EstimatedCents      int    `json:"estimatedCents,omitempty"`
	HourlyCents         int    `json:"hourlyCents,omitempty"`
	EstimatedMinutes    int    `json:"estimatedMinutes,omitempty"`
	IncludedHoursBucket int    `json:"includedHoursBucket,omitempty"`
	BillingScope        string `json:"billingScope,omitempty"`
	ResourceClass       string `json:"resourceClass,omitempty"`
	Display             string `json:"display,omitempty"`
}

type TaskPlacementMetadata struct {
	PlacementID        string              `json:"id,omitempty"`
	Lane               string              `json:"lane,omitempty"`
	ResourceClass      string              `json:"resourceClass,omitempty"`
	TargetDeviceID     string              `json:"targetDeviceId,omitempty"`
	CloudMachineID     string              `json:"cloudMachineId,omitempty"`
	SubscriptionPlan   string              `json:"subscriptionPlan,omitempty"`
	Entitlement        string              `json:"entitlement,omitempty"`
	Status             string              `json:"status,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	WakeRequired       bool                `json:"wakeRequired,omitempty"`
	WakeTargetMs       int                 `json:"wakeTargetMs,omitempty"`
	EstimatedCostCents int                 `json:"estimatedCreditCost,omitempty"`
	CreditEstimate     *TaskCreditEstimate `json:"creditEstimate,omitempty"`
}

// TaskInfo is the JSON-safe subset returned in listings.
type TaskInfo struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          TaskStatus `json:"status"`
	RunnerID        string     `json:"runnerId,omitempty"`
	Transport       string     `json:"transport,omitempty"`
	TransportReason string     `json:"transportReason,omitempty"`
	// Goal is the Yaver goal-mode objective (opencode goal plugin). Empty =
	// one-shot task; set = persistent goal. Surfaced so every surface can
	// render a Goal chip + drive /goal status|resume|clear.
	Goal string `json:"goal,omitempty"`
	// Model is the model id the task launched with (claude-opus-4-8,
	// gpt-5.4, "opus", etc.). Without this on the public Task API
	// the mobile UI couldn't tell whether a task that's been around
	// for a while ran with the user's expected model — it had to
	// guess from whatever picker state was current, which produced
	// "Claude Code · GPT-5.4" mislabels on cross-device tasks.
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// ProjectName scopes Vibing topic cards without exposing an absolute path.
	ProjectName string `json:"projectName,omitempty"`
	// DeviceName is the agent's hostname at the time the task was
	// created. Mobile clients render this on the per-task header and
	// in the task list card; without it, the focused-device name
	// leaked into every label and a task that ran on a sibling box
	// looked like it ran on whichever device the phone was focused
	// on at view time.
	DeviceName       string `json:"deviceName,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	HostKind         string `json:"hostKind,omitempty"`
	ProjectSessionID string `json:"projectSessionId,omitempty"`
	Output           string `json:"output,omitempty"`
	// RawOutput is the tail of the runner's RAW stdout (ANSI escape
	// sequences, TUI redraws, box-drawing — everything the grooming filters
	// strip) retained for the console/terminal view. Only populated on the
	// task-detail endpoint, and wire-capped below; the SSE stream is the
	// live path (`?rawSince=` replay + `raw` frames).
	RawOutput string `json:"rawOutput,omitempty"`
	// RawOffset is the byte length of the FULL retained raw tail at
	// snapshot time — the cursor a client passes to `?rawSince=` to resume
	// the raw stream without re-fetching bytes it already rendered.
	RawOffset    int                       `json:"rawOffset,omitempty"`
	ResultText   string                    `json:"resultText,omitempty"`
	Presentation []TaskPresentationMessage `json:"presentation,omitempty"`
	Failure      *TaskFailureDiagnosis     `json:"failure,omitempty"`
	CostUSD      float64                   `json:"costUsd,omitempty"`
	InputTokens  int                       `json:"inputTokens,omitempty"`
	OutputTokens int                       `json:"outputTokens,omitempty"`
	Turns        []ConversationTurn        `json:"turns,omitempty"`
	// PendingFollowUps lets chat surfaces render user messages that were
	// accepted while the runner was still working. The agent already owns the
	// queue; hiding it made a successful second send look dropped after the
	// next task-detail refresh.
	PendingFollowUps []PendingFollowUp `json:"pendingFollowUps,omitempty"`
	// TurnCount lets a list view show "12 turns" without shipping the
	// transcript to render a number. The list handler nils Turns and sets this;
	// the detail endpoint leaves Turns intact and this stays 0.
	TurnCount int `json:"turnCount,omitempty"`
	// TranscriptTruncated is set when ResultText or turn contents were
	// tail-capped for transport. Output was already capped at 10KB, but
	// ResultText and Turns shipped whole — after one long analysis turn the
	// task detail grew to 3.5MB and the web UI polled it every 2s over the
	// relay (2026-07-27, ubuntu-4gb box), which is what "stuck" looked like
	// from the browser. Surfaces render tails anyway (web keeps 240 lines).
	TranscriptTruncated bool                   `json:"transcriptTruncated,omitempty"`
	Source              string                 `json:"source,omitempty"`
	TmuxSession         string                 `json:"tmuxSession,omitempty"`
	TmuxSessionID       string                 `json:"tmuxSessionId,omitempty"`
	TmuxWindowIndex     string                 `json:"tmuxWindowIndex,omitempty"`
	TmuxWindowName      string                 `json:"tmuxWindowName,omitempty"`
	TmuxPaneIndex       string                 `json:"tmuxPaneIndex,omitempty"`
	TmuxPaneID          string                 `json:"tmuxPaneId,omitempty"`
	ExecutionSession    TaskExecutionIdentity  `json:"executionSession"`
	SessionSettings     *ClientSessionSettings `json:"sessionSettings,omitempty"`
	IsAdopted           bool                   `json:"isAdopted,omitempty"`
	CreatedAt           time.Time              `json:"createdAt"`
	StartedAt           *time.Time             `json:"startedAt,omitempty"`
	FinishedAt          *time.Time             `json:"finishedAt,omitempty"`
	ChainID             string                 `json:"chainId,omitempty"`
	ChainOrder          int                    `json:"chainOrder,omitempty"`
	AutoRetry           bool                   `json:"autoRetry,omitempty"`
	AutoRetryCount      int                    `json:"autoRetryCount,omitempty"`
	AutoRetryMax        int                    `json:"autoRetryMax,omitempty"`
	VideoEnabled        bool                   `json:"videoEnabled,omitempty"`
	VideoSource         string                 `json:"videoSource,omitempty"`
	VideoClipID         string                 `json:"videoClipId,omitempty"`
	VideoStatus         string                 `json:"videoStatus,omitempty"`
	VideoClipURL        string                 `json:"videoClipUrl,omitempty"`
	VideoPosterURL      string                 `json:"videoPosterUrl,omitempty"`
	ProofStatus         string                 `json:"proofStatus,omitempty"`
	ProofURL            string                 `json:"proofUrl,omitempty"`
	CommitSHA           string                 `json:"commitSha,omitempty"`
	CommitSubject       string                 `json:"commitSubject,omitempty"`
	CommitBranch        string                 `json:"commitBranch,omitempty"`
	DiffShortstat       string                 `json:"diffShortstat,omitempty"`
	FeedbackID          string                 `json:"feedbackId,omitempty"`
	AskFreely           bool                   `json:"askFreely,omitempty"`
	Placement           *TaskPlacementMetadata `json:"placement,omitempty"`
}

func newYaverSessionID() string {
	return "ys_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:20]
}

func taskHostKind(task *Task) string {
	if task == nil {
		return ""
	}
	if task.HostKind != "" {
		return task.HostKind
	}
	if task.TmuxSession != "" || task.TmuxSessionID != "" || task.TmuxPaneID != "" {
		return "terminal_tmux"
	}
	return "runner_process"
}

func (tm *TaskManager) taskExecutionIdentity(task *Task) TaskExecutionIdentity {
	if task == nil {
		return TaskExecutionIdentity{}
	}
	runner := task.runner
	if runner.Command == "" {
		runner = GetRunnerConfig(task.RunnerID)
	}
	firstUser, firstAgent := task.FirstUserMessageAt, task.FirstAgentResponseAt
	lastUser, lastAgent := task.LastUserMessageAt, task.LastAgentResponseAt
	for _, turn := range task.Turns {
		ts := turn.Timestamp
		switch turn.Role {
		case "user":
			if turn.Hidden {
				continue
			}
			if firstUser == nil {
				first := ts
				firstUser = &first
			}
			if lastUser == nil || ts.After(*lastUser) {
				last := ts
				lastUser = &last
			}
		case "assistant":
			if firstAgent == nil {
				first := ts
				firstAgent = &first
			}
			if lastAgent == nil || ts.After(*lastAgent) {
				last := ts
				lastAgent = &last
			}
		}
	}
	startedAt := task.SessionStartedAt
	if startedAt.IsZero() {
		startedAt = task.CreatedAt
	}
	lastActiveAt := task.LastActiveAt
	if lastActiveAt.IsZero() {
		lastActiveAt = startedAt
	}
	return TaskExecutionIdentity{
		YaverSessionID:       task.YaverSessionID,
		TaskID:               task.ID,
		RemoteBoxID:          firstNonEmpty(task.RemoteBoxID, tm.DeviceID),
		RunnerName:           firstNonEmpty(task.RunnerName, runner.Name),
		RunnerID:             normalizeRunnerID(task.RunnerID),
		RunnerSessionID:      strings.TrimSpace(task.SessionID),
		HostKind:             taskHostKind(task),
		StartedFrom:          firstNonEmpty(task.SessionStartedFrom, "tasks"),
		StartedFromSurface:   firstNonEmpty(task.StartedFromSurface, task.Source),
		InitialSurface:       firstNonEmpty(task.InitialSurface, task.StartedFromSurface, task.Source),
		SessionStartedAt:     startedAt,
		LastSurface:          firstNonEmpty(task.LastSurface, task.InitialSurface, task.StartedFromSurface, task.Source),
		LastActiveAt:         lastActiveAt,
		FirstUserMessageAt:   firstUser,
		FirstAgentResponseAt: firstAgent,
		LastUserMessageAt:    lastUser,
		LastAgentResponseAt:  lastAgent,
		SessionSettings:      cloneClientSessionSettings(task.SessionSettings),
		DeletedAt:            task.DeletedAt,
		Resumable:            resumeCanCarryContext(runner, task.SessionID),
		TmuxSession:          task.TmuxSession,
		TmuxSessionID:        task.TmuxSessionID,
		TmuxWindowIndex:      task.TmuxWindowIndex,
		TmuxWindowName:       task.TmuxWindowName,
		TmuxPaneIndex:        task.TmuxPaneIndex,
		TmuxPaneID:           task.TmuxPaneID,
	}
}

type taskStore interface {
	Save(tasks map[string]*Task)
	SaveRecords(records []persistedTask)
	Load() map[string]*Task
}

// TaskManager manages the lifecycle of tasks.
type TaskManager struct {
	mu          sync.RWMutex
	tasks       map[string]*Task
	workDir     string
	store       taskStore
	runner      RunnerConfig
	TmuxMgr     *TmuxManager  // manages tmux session adoption (nil if tmux unavailable)
	Sandbox     SandboxConfig // Command sandbox configuration
	WaitForSlot bool          // If true, wait for other Claude Code sessions to finish before starting
	DummyMode   bool          // If true, use fake responses instead of launching a real runner

	// Container isolation (optional — set by httpserver when enabled)
	ContainerRunner   *ContainerRunner
	ContainerizeHost  bool
	ContainerCPU      string
	ContainerMemory   string
	ContainerImage    string
	ContainerNetwork  string   // "host" (default), "bridge", "none"
	ContainerReadOnly bool     // read-only root filesystem
	ContainerMounts   []string // extra volume mounts from config

	// Callbacks (set after construction)
	OnTaskDone func(task *Task) // called when a task finishes (completed/failed/stopped)

	// Convex reporting (set after construction)
	ConvexURL    string
	AuthToken    string
	DeviceID     string
	OwnerEmail   string // for dev logging
	ownerIsOwner bool   // server-computed ownerAllowlist gate for preview-only runners

}

// NewTaskManager creates a new TaskManager. If store is non-nil, previously
// persisted tasks are loaded from disk. Direct-exec running tasks become a
// named restart failure; tmux-backed tasks stay pending until the tmux startup
// reconciler probes their exact seat.
func NewTaskManager(workDir string, store taskStore, runner RunnerConfig) *TaskManager {
	tasks := make(map[string]*Task)
	if store != nil {
		tasks = store.Load()
	}
	// Mark orphaned direct-exec tasks as failed. A task-owned tmux runner can
	// outlive this process, so do not overwrite its state before reconciliation.
	now := time.Now()
	for _, t := range tasks {
		if strings.TrimSpace(t.YaverSessionID) == "" {
			t.YaverSessionID = newYaverSessionID()
		}
		if t.SessionStartedAt.IsZero() {
			t.SessionStartedAt = t.CreatedAt
		}
		if t.LastActiveAt.IsZero() {
			t.LastActiveAt = t.SessionStartedAt
		}
		if t.Status == TaskStatusRunning && !t.IsAdopted && !taskOwnsRecoverableTmuxSeat(t) {
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
	if finalizeVibingThreadTitle(task) {
		// fireTaskDone is called while the task-manager lock is held. Persist the
		// generated display title before callbacks copy the terminal task.
		tm.persist()
	}
	// Runner/render split converge hook: a task reaching a RENDERABLE
	// terminal state (completed/review) with a push policy commits/pushes
	// per that policy (task_ensure_clone.go) — the push is what lets the
	// render box's pre-build-pull pick the work up. Once per task.
	if (task.Status == TaskStatusFinished || task.Status == TaskStatusReview) && task.AutoPush != "" && !task.autoPushFired {
		task.autoPushFired = true
		go tm.autoPushAfterTask(task)
	}
	if tm.OnTaskDone != nil {
		// Copy fields under lock to avoid races
		t := *task
		go tm.OnTaskDone(&t)
	}
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

// Shutdown stops all running tasks. It is the explicit destructive shutdown
// used by tests and callers that own the workloads as well as the manager.
func (tm *TaskManager) Shutdown() {
	stopped := tm.StopAllTasks()
	if stopped > 0 {
		log.Printf("[shutdown] Stopped %d running task(s)", stopped)
	}

	clearForkedPIDs()
}

// ShutdownForAgentRestart releases direct subprocesses while deliberately
// leaving recoverable tmux runner seats alive. A daemon restart, package
// upgrade, phone disconnect, or relay reconnect is not a user lifecycle
// gesture; turning it into StopTask used to kill the exact session that
// ReAdoptOnStartup is designed to recover on the next boot.
//
// Explicit Complete, Stop, Delete and confirmed tmux Kill still use their
// existing teardown paths. This method only changes daemon ownership loss.
func (tm *TaskManager) ShutdownForAgentRestart() {
	tm.mu.RLock()
	var stopIDs []string
	preserved := 0
	for id, task := range tm.tasks {
		if task == nil || (task.Status != TaskStatusRunning && task.Status != TaskStatusQueued) {
			continue
		}
		if taskHasRecoverableTmuxSeat(task) {
			preserved++
			continue
		}
		stopIDs = append(stopIDs, id)
	}
	tm.mu.RUnlock()

	stopped := 0
	for _, id := range stopIDs {
		if err := tm.StopTask(id); err == nil {
			stopped++
		}
	}
	if preserved > 0 {
		log.Printf("[shutdown] Preserved %d recoverable tmux runner seat(s) for startup re-adoption", preserved)
	}
	if stopped > 0 {
		log.Printf("[shutdown] Stopped %d non-recoverable running task(s)", stopped)
	}
	// The wrapper PIDs belong to the departing process and may finish naturally;
	// the durable ownership address is the tmux session+pane persisted on Task.
	// Never retain raw PIDs across restart because PID reuse could later target
	// an unrelated process.
	clearForkedPIDs()
}

func taskHasRecoverableTmuxSeat(task *Task) bool {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return false
	}
	return task.IsAdopted || taskOwnsRecoverableTmuxSeat(task)
}

// persist saves the current task map to disk if a store is configured.
// Must be called while tm.mu is held (read or write).
func (tm *TaskManager) persist() {
	if tm.store != nil {
		tm.store.Save(tm.tasks)
	}
	requestConvexLifecycleSync()
}

// persistAsync snapshots the task store state while the caller holds tm.mu,
// then writes it in the background. This keeps large historical task stores
// off the POST /tasks critical path.
func (tm *TaskManager) persistAsync() {
	if tm.store == nil {
		requestConvexLifecycleSync()
		return
	}
	records := snapshotPersistedTasks(tm.tasks)
	go tm.store.SaveRecords(records)
	requestConvexLifecycleSync()
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
func (tm *TaskManager) CreateTask(title, description, model, source, runnerID, customCommand string, images []ImageAttachment) (*Task, error) {
	return tm.CreateTaskWithOptions(title, description, model, source, runnerID, customCommand, images, TaskCreateOptions{})
}

const remotelessOwnerOnlyError = "remoteless is temporarily available only to the Yaver owner account"

// normalizeCodexReasoningEffort accepts the friendly label from a surface but
// emits Codex's config value. Keep it task-local; global config mutation would
// leak one task's xhigh choice into every later session.
func normalizeCodexReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "low", "medium", "high", "max", "ultra":
		return strings.ToLower(strings.TrimSpace(value))
	case "xhigh", "extra-high", "extra high":
		return "xhigh"
	case "more-reasoning", "more reasoning":
		return "max"
	default:
		return ""
	}
}

func codexReasoningEffort(runnerID, value string) string {
	if normalizeRunnerID(runnerID) != "codex" {
		return ""
	}
	if effort := normalizeCodexReasoningEffort(value); effort != "" {
		return effort
	}
	if effort := yaverDefaultReasoningEffortForRunner(runnerID); effort != "" {
		return effort
	}
	return "medium"
}

// validateCodexReasoningEffortForModel keeps the task boundary aligned with
// the catalog Convex advertised to every surface. The built-in table is only
// the offline fallback; when Convex supplies a row, its matrix wins so a model
// rollout changes clients and admission together without a client release.
func validateCodexReasoningEffortForModel(model, effort string) error {
	model = strings.TrimSpace(model)
	effort = normalizeCodexReasoningEffort(effort)
	if model == "" || effort == "" {
		return nil
	}

	for _, candidate := range GetCachedModels() {
		if normalizeRunnerID(candidate.RunnerID) != "codex" || candidate.ModelID != model {
			continue
		}
		if len(candidate.SupportedReasoningEffort) == 0 {
			return nil
		}
		for _, supported := range candidate.SupportedReasoningEffort {
			if normalizeCodexReasoningEffort(supported) == effort {
				return nil
			}
		}
		return fmt.Errorf("reasoning effort %q is not supported by Codex model %q", effort, model)
	}

	for _, candidate := range fallbackRunnerModels("codex") {
		if candidate.ID != model || len(candidate.SupportedReasoningEffort) == 0 {
			continue
		}
		for _, supported := range candidate.SupportedReasoningEffort {
			if normalizeCodexReasoningEffort(supported.ReasoningEffort) == effort {
				return nil
			}
		}
		return fmt.Errorf("reasoning effort %q is not supported by Codex model %q", effort, model)
	}
	// A newly released live model may arrive before Convex's metadata refresh.
	// Do not invent a matrix for it; the live runner remains authoritative.
	return nil
}

func validateRemotelessRunnerAccess(ownerIsOwner bool, runnerID string) error {
	if normalizeRunnerID(runnerID) == "remoteless" && !ownerIsOwner {
		return errors.New(remotelessOwnerOnlyError)
	}
	return nil
}

func (tm *TaskManager) ownerPreviewAccessAllowed() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.ownerIsOwner
}

func (tm *TaskManager) setOwnerPreviewAccess(allowed bool) {
	tm.mu.Lock()
	tm.ownerIsOwner = allowed
	tm.mu.Unlock()
}

// A fresh per-device runner preference is useful, but it is advisory: an
// unavailable settings service must never hold POST /tasks open. The task can
// still use the runner resolved at agent startup and the next request can pick
// up the refreshed preference through the normal settings cache.
const taskRunnerPreferenceResolveBudget = 250 * time.Millisecond

var resolvePrimaryRunnerPreferenceFn = resolvePrimaryRunnerPrefForSelf

func resolvePrimaryRunnerPrefForTaskAdmission() primaryRunnerPreference {
	ctx, cancel := context.WithTimeout(context.Background(), taskRunnerPreferenceResolveBudget)
	defer cancel()
	return resolvePrimaryRunnerPreferenceFn(ctx, nil)
}

func (tm *TaskManager) CreateTaskWithOptions(title, description, model, source, runnerID, customCommand string, images []ImageAttachment, opts TaskCreateOptions) (*Task, error) {
	var taskRunner RunnerConfig
	callerRunnerID := normalizeRunnerID(runnerID)
	ownerPreviewAccess := tm.ownerPreviewAccessAllowed()
	if err := validateRemotelessRunnerAccess(ownerPreviewAccess, callerRunnerID); err != nil {
		return nil, err
	}
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
			if pref := resolvePrimaryRunnerPrefForTaskAdmission(); pref.RunnerID != "" {
				effectiveRunnerID = pref.RunnerID
				perDeviceModel = pref.Model
				perDeviceMode = pref.Mode
			}
		}
		if err := validateRemotelessRunnerAccess(ownerPreviewAccess, effectiveRunnerID); err != nil {
			return nil, err
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
	if err := validateRemotelessRunnerAccess(ownerPreviewAccess, taskRunner.RunnerID); err != nil {
		return nil, err
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
	// Resolve the current Convex-backed Yaver default at task creation time, so
	// an owner update applies without restarting this agent. A request/device
	// model is already in `model` and wins. OpenCode's local opencode.json is
	// also explicit user configuration and wins over the product default.
	if model == "" {
		id := normalizeRunnerID(taskRunner.RunnerID)
		if id == "opencode" && openCodeHasUserModelConfiguration() {
			taskRunner.Model = ""
		} else if fallback := yaverDefaultModelForRunner(id); fallback != "" {
			taskRunner.Model = fallback
			taskRunner.ReasoningEffort = yaverDefaultReasoningEffortForRunner(id)
		}
	}

	// Goal-mode objective (opencode goal plugin): persisted on the Task so
	// every surface can render a Goal chip, and the opencode prompt frame
	// wraps it into a create_goal call (startProcess). Empty = one-shot.
	if strings.TrimSpace(opts.Goal) != "" {
		taskRunner.Goal = strings.TrimSpace(opts.Goal)
	}

	if source == "" {
		source = "mobile"
	}
	if model != "" && !runnerModelCompatible(taskRunner.RunnerID, model) {
		if normalizeRunnerID(taskRunner.RunnerID) == "opencode" {
			if cfg, err := loadOpenCodeConfigSummary(); err == nil {
				replacement := strings.TrimSpace(cfg.Model)
				if replacement == "" {
					replacement = strings.TrimSpace(cfg.BuildModel)
				}
				if replacement == "" {
					replacement = strings.TrimSpace(cfg.PlanModel)
				}
				for _, candidate := range cfg.Models {
					if replacement == "" && candidate.IsDefault {
						replacement = strings.TrimSpace(candidate.ID)
					}
				}
				if replacement != "" && runnerModelCompatible(taskRunner.RunnerID, replacement) {
					log.Printf("[task] model %q is incompatible with runner %q; using OpenCode config model %q", model, taskRunner.RunnerID, replacement)
					model = replacement
				}
			}
		}
		if model != "" && !runnerModelCompatible(taskRunner.RunnerID, model) {
			return nil, fmt.Errorf("model %q is not compatible with runner %q", model, taskRunner.RunnerID)
		}
	}
	if normalizeRunnerID(taskRunner.RunnerID) == "codex" {
		effectiveModel := firstNonEmpty(strings.TrimSpace(model), strings.TrimSpace(taskRunner.Model))
		if err := validateCodexReasoningEffortForModel(effectiveModel, opts.ReasoningEffort); err != nil {
			return nil, err
		}
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
	// Prepend any seeded display history (a fork carries the parent's turns
	// so the child reads as one continuous thread). Bounded, and we drop a
	// trailing seed turn that duplicates the incoming user turn so the chat
	// doesn't show the same message twice at the fork seam.
	initialTurns := make([]ConversationTurn, 0, len(opts.SeedTurns)+1)
	for _, st := range seedForkTurns(opts.SeedTurns) {
		if strings.TrimSpace(st.Content) == "" {
			continue
		}
		initialTurns = append(initialTurns, st)
	}
	if n := len(initialTurns); n > 0 {
		last := initialTurns[n-1]
		if last.Role == "user" && strings.TrimSpace(last.Content) == initialTurnContent {
			initialTurns = initialTurns[:n-1]
		}
	}
	initialTurns = append(initialTurns, ConversationTurn{
		Role: "user", Content: initialTurnContent, Timestamp: now, Hidden: opts.InitialUserPromptHidden,
	})
	rawRunnerCommand := opts.RawRunnerCommand ||
		isRawRunnerCommand(initialTurnContent) ||
		isRawRunnerCommand(description) ||
		isRawRunnerCommand(title)
	task := &Task{
		ID:                 id,
		Title:              title,
		Description:        description,
		PromptText:         opts.PromptText,
		Status:             TaskStatusQueued,
		Source:             source,
		Model:              model,
		ReasoningEffort:    codexReasoningEffort(taskRunner.RunnerID, opts.ReasoningEffort),
		RunnerID:           taskRunner.RunnerID,
		YaverSessionID:     newYaverSessionID(),
		RemoteBoxID:        strings.TrimSpace(tm.DeviceID),
		RunnerName:         taskRunner.Name,
		SessionStartedFrom: firstNonEmpty(strings.TrimSpace(opts.SessionStartedFrom), "tasks"),
		StartedFromSurface: firstNonEmpty(strings.TrimSpace(opts.StartedFromSurface), source),
		InitialSurface:     firstNonEmpty(strings.TrimSpace(opts.StartedFromSurface), source),
		SessionStartedAt:   now,
		LastSurface:        firstNonEmpty(strings.TrimSpace(opts.StartedFromSurface), source),
		LastActiveAt:       now,
		SessionSettings:    normalizeClientSessionSettings(opts.SessionSettings, 1, now),
		Goal:               taskRunner.Goal,
		runner:             taskRunner,
		CreatedAt:          now,
		outputCh:           make(chan string, 512),
		rawOutputCh:        make(chan []byte, 256),
		eventCh:            make(chan map[string]interface{}, 32),
		doneCh:             make(chan struct{}),
		WorkDir:            strings.TrimSpace(opts.WorkDir),
		ProjectName:        strings.TrimSpace(opts.ProjectName),
		MCPServers:         append([]string{}, opts.MCPServers...),
		IncludeYaverMcp:    opts.IncludeYaverMcp,
		GitRemote:          strings.TrimSpace(opts.GitRemote),
		GitBranch:          strings.TrimSpace(opts.GitBranch),
		AutoPush:           strings.TrimSpace(opts.AutoPush),
		SliceContract:      opts.SliceContract,
		Placement:          opts.Placement,
		TaskViewport:       opts.Viewport,
		VideoEnabled:       opts.VideoEnabled,
		VideoSource:        opts.VideoSource,
		AskFreely:          opts.AskFreely,
		AskMode:            opts.AskMode,
		RedactPII:          opts.RedactPII,
		RawRunnerCommand:   rawRunnerCommand,
		ResumeLast:         opts.ResumeLast,
		SessionID:          opts.ResumeSessionID,
		ProjectSessionID:   strings.TrimSpace(opts.ProjectSessionID),
		Turns:              initialTurns,
	}
	if !opts.InitialUserPromptHidden {
		recordSessionMessage(task, "user", now)
	}
	if len(images) > 0 {
		task.ImagePaths = saveImages(id, images)
	}

	tm.mu.Lock()
	tm.tasks[id] = task
	tm.persistAsync()
	tm.mu.Unlock()
	// Give every accepted task a conversational response immediately. Runner
	// startup (credential refresh, clone/pull, ACP handshake) can take seconds;
	// the user should never stare at raw commands or an empty chat meanwhile.
	tm.present(task, taskAcceptedPresentation(task))

	// Dummy mode: stream fake response without launching a real process.
	if tm.DummyMode {
		log.Printf("[task %s] DUMMY MODE — streaming fake response for: %s", id, title)
		go tm.runDummyTask(task)
		return task, nil
	}

	// Runner/render split: if this box was picked as the runner but the
	// project isn't materialized here yet, clone first — asynchronously,
	// narrated into the task stream — then spawn (task_ensure_clone.go).
	if plan := tm.clonePlanForTask(task); plan != nil {
		log.Printf("[task %s] workDir missing — ensure-clone of %s into %s before spawn", id, sanitizeRemoteForLog(plan.Remote), plan.Dest)
		go tm.runCloneThenStart(task, plan)
		return task, nil
	}
	// Existing clone on a split task: fast-forward it first so commits
	// pushed from the render box (or anywhere) are present before the
	// runner reads the tree. Bounded + non-fatal (task_ensure_clone.go).
	if task.GitRemote != "" {
		tm.pullBeforeSpawn(task)
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
		tm.persistAsync()
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
	if task.cmd != nil && task.cmd.Process != nil {
		log.Printf("[task %s] %s process started (PID %d, transport=%s)", id, taskRunner.Name, task.cmd.Process.Pid, firstNonEmpty(task.Transport, taskTransportCLI))
	} else {
		log.Printf("[task %s] %s task started (transport=%s)", id, taskRunner.Name, firstNonEmpty(task.Transport, taskTransportCLI))
	}

	return task, nil
}

// CreateTaskInProjectSession binds a task to one isolated checkout while
// reusing the current task creation pipeline and runner policy.
func (tm *TaskManager) CreateTaskInProjectSession(title, description, model, reasoningEffort, source, runnerID, customCommand, mode, projectSessionID, workDir string) (*Task, error) {
	projectSessionID = strings.TrimSpace(projectSessionID)
	if projectSessionID == "" {
		return nil, fmt.Errorf("project session ID is required")
	}
	info, err := os.Stat(workDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project session checkout is unavailable")
	}
	return tm.CreateTaskWithOptions(
		title, description, model, source, runnerID, customCommand, nil,
		TaskCreateOptions{
			WorkDir:          filepath.Clean(workDir),
			ProjectSessionID: projectSessionID,
			Mode:             mode,
		},
	)
}

// isRootProcess reports whether the agent runs as uid 0. os.Geteuid returns -1
// on Windows, so this is false there (correct — the root caveat is Unix-only).
func isRootProcess() bool { return os.Geteuid() == 0 }

func isRawRunnerCommand(input string) bool {
	return strings.HasPrefix(strings.TrimLeft(input, " \t\r\n"), "/")
}

func rawRunnerPromptForTask(task *Task, fallback string) string {
	if task != nil {
		for i := len(task.Turns) - 1; i >= 0; i-- {
			if strings.EqualFold(task.Turns[i].Role, "user") && isRawRunnerCommand(task.Turns[i].Content) {
				return strings.TrimLeft(task.Turns[i].Content, " \t\r\n")
			}
		}
		for _, candidate := range []string{task.Description, task.Title} {
			if isRawRunnerCommand(candidate) {
				return strings.TrimLeft(candidate, " \t\r\n")
			}
		}
	}
	return strings.TrimLeft(fallback, " \t\r\n")
}

func taskEnv(task *Task) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+expandedPath())
	// Claude Code (and the glm runner, which is the claude binary) refuses
	// `--dangerously-skip-permissions` when running as root unless IS_SANDBOX=1
	// is set ("cannot be used with root/sudo privileges for security reasons").
	// Many cloud boxes run the agent as root via systemd, so without this every
	// claude/glm/trio-hybrid task silently fails there. The agent IS the
	// sandbox/automation context this flag is meant for. This is a SAFETY NET:
	// the preferred posture for remote dev is a NON-ROOT runtime user (see the
	// non-root-remote-dev follow-up) — but until provisioning defaults to that,
	// root boxes must still work. Gated on root so non-root machines keep
	// claude's normal behavior; codex/opencode ignore the var.
	if isRootProcess() {
		env = append(env, "IS_SANDBOX=1")
	}
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
		// Video on + a web task → record every browser session the agent opens
		// automatically (browser_open reads this; see the MCP handler). The clip
		// is linked back to the task via a marker on completion.
		if task.VideoEnabled && autoDetectVideoSource(task) == string(VibeClipSourceBrowser) {
			env = append(env, "YAVER_TASK_RECORD_BROWSER=1")
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
	// OAuth-subscription path.
	if task != nil {
		runnerID := task.RunnerID
		if runnerID == "" {
			runnerID = task.runner.RunnerID
		}
		env = append(env, runnerProviderEnv(runnerID)...)
	}
	return env
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
	return strings.Join(paths, string(os.PathListSeparator))
}

// expandedPath returns PATH with common extra binary locations prepended.
func expandedPath() string {
	extra := commonExtraPaths()
	current := os.Getenv("PATH")
	if extra == "" {
		return current
	}
	if current == "" {
		return extra
	}
	return extra + string(os.PathListSeparator) + current
}

// CheckRunnerBinary checks if a runner binary is available in PATH or common locations.
// If found outside PATH, logs a hint about adding it to PATH.
func CheckRunnerBinary(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("runner command is empty")
	}
	if path, ok := cachedRunnerBinaryPath(command); ok {
		storeResolvedRunnerBinary(command, path)
		return nil
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), runnerVersionProbeTimeout)
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
		// A runner that ANSWERED and then failed to exit is working.
		//
		// The question this probe asks is "does this binary run?", and a version
		// string IS the answer — whether the process then lingers is a fact about
		// the CLI's shutdown, not about its health. exec.CommandContext SIGKILLs on
		// deadline, so a slow-exiting runner came back as `signal: killed` and was
		// declared broken while its version sat right there in the output.
		//
		// That was not theoretical: opencode 1.14.41 printed its version and did
		// not exit, so this returned `opencode found but not working: signal:
		// killed (output: 1.14.41)` at exactly the 10s mark and killed a whole
		// autorun — with the answer in hand. Trusting the answer over the exit is
		// the difference between a loop that runs and one that gives up.
		if ctx.Err() == context.DeadlineExceeded && looksLikeRunnerVersion(out) {
			log.Printf("[runner-check] %s at %s — answered %q but did not exit within %s; treating as ready",
				command, path, strings.TrimSpace(string(out)), runnerVersionProbeTimeout)
			storeRunnerBinaryPath(command, path)
			storeResolvedRunnerBinary(command, path)
			return nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			if stalePath, ok := recentSuccessfulRunnerBinaryPath(command); ok && stalePath == path {
				log.Printf("[runner-check] %s at %s timed out after %s with no usable answer; a successful probe is still recent, so attempting the real runner operation",
					command, path, runnerVersionProbeTimeout)
				storeResolvedRunnerBinary(command, path)
				return nil
			}
		}
		return fmt.Errorf("%s found but not working: %v (output: %s)", command, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		log.Printf("[runner-check] %s at %s — ok", command, path)
	} else {
		log.Printf("[runner-check] %s at %s — %s", command, path, strings.TrimSpace(string(out)))
	}
	storeRunnerBinaryPath(command, path)
	storeResolvedRunnerBinary(command, path)
	return nil
}

// runnerVersionProbeTimeout bounds the `--version` probe. It is a liveness
// bound, not a correctness one: a runner that answers and lingers is still a
// working runner (see the DeadlineExceeded branch above).
var runnerVersionProbeTimeout = 10 * time.Second

func cachedRunnerBinaryPath(command string) (string, bool) {
	v, ok := runnerBinaryCheckCache.Load(command)
	if !ok {
		return "", false
	}
	entry, _ := v.(runnerBinaryCheckEntry)
	if time.Since(entry.at) >= runnerBinaryCheckCacheTTL {
		return "", false
	}
	if entry.path == "" || !isExecutableFile(entry.path) {
		runnerBinaryCheckCache.Delete(command)
		return "", false
	}
	return entry.path, true
}

// recentSuccessfulRunnerBinaryPath returns the last known-good path even after
// the short skip-probe cache expires. It is only a fallback for a fresh probe
// that timed out; callers must not use it to avoid probing indefinitely.
func recentSuccessfulRunnerBinaryPath(command string) (string, bool) {
	v, ok := runnerBinaryCheckCache.Load(command)
	if !ok {
		return "", false
	}
	entry, _ := v.(runnerBinaryCheckEntry)
	if entry.path == "" || time.Since(entry.at) >= runnerBinaryCheckStaleSuccessTTL || !isExecutableFile(entry.path) {
		runnerBinaryCheckCache.Delete(command)
		return "", false
	}
	return entry.path, true
}

func storeRunnerBinaryPath(command, path string) {
	command = strings.TrimSpace(command)
	path = strings.TrimSpace(path)
	if command == "" || path == "" {
		return
	}
	runnerBinaryCheckCache.Store(command, runnerBinaryCheckEntry{
		path: path,
		at:   time.Now(),
	})
}

func clearRunnerBinaryCheckCache() {
	runnerBinaryCheckCache = sync.Map{}
}

// looksLikeRunnerVersion reports whether a probe's output is a real answer rather
// than noise. Deliberately narrow: a version has a digit in it. A binary that
// hangs while printing a banner, a stack trace, or nothing at all has not
// answered, and must still be reported broken — "it wrote something before we
// killed it" is not health.
func looksLikeRunnerVersion(out []byte) bool {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return false
	}
	return strings.ContainsAny(s, "0123456789")
}

// findInExpandedPath searches for a command in common binary locations beyond PATH.
func findInExpandedPath(command string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	searchDirs := filepath.SplitList(commonExtraPaths())
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
	tm.present(task, taskRunningPresentation(task))

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
	task.LastActiveAt = finishNow
	task.ResultText = output.String()
	tm.presentLocked(task, taskPresentationInput{
		ID: taskAssistantPresentationID(task), Kind: "message", Role: "assistant",
		Text: task.ResultText, Phase: "complete", State: "completed",
	})
	task.Turns = append(task.Turns, ConversationTurn{
		Role:      "assistant",
		Content:   task.ResultText,
		Timestamp: finishNow,
	})
	recordSessionMessage(task, "assistant", finishNow)
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
		if probe != "" && !runnerWorkDirInsideGitRepo(probe) {
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
	return applyModeArgs(runner, runner.Mode, args)
}

// applyModeArgs adds the mode syntax understood by runners that expose modes.
// "chat"/"chat:<surface>" is the embedded Q&A mode (see
// chatTaskResponseContext), not an opencode agent name.
func applyModeArgs(runner RunnerConfig, mode string, args []string) []string {
	mode = strings.TrimSpace(mode)
	if runner.RunnerID != "opencode" || mode == "" || mode == "chat" || strings.HasPrefix(mode, "chat:") {
		return args
	}

	out := make([]string, 0, len(args)+2)
	injected := false
	for _, arg := range args {
		out = append(out, arg)
		if !injected && arg == "run" {
			out = append(out, "--agent", mode)
			injected = true
		}
	}
	if !injected {
		// Custom opencode args may omit `run`; keep the selected agent visible.
		out = append([]string{"--agent", mode}, out...)
	}
	return out
}

func insertRunnerFlagAfter(args []string, after, flag, value string) []string {
	out := make([]string, 0, len(args)+2)
	inserted := false
	for _, a := range args {
		out = append(out, a)
		if !inserted && a == after {
			out = append(out, flag, value)
			inserted = true
		}
	}
	if !inserted {
		out = append([]string{flag, value}, out...)
	}
	return out
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

// runnerWorkDirInsideGitRepo is the task-argument decision seam. Tests must
// not inherit a machine-wide /tmp/.git (a valid setup on shared workers),
// which otherwise turns every t.TempDir fixture into a repository.
var runnerWorkDirInsideGitRepo = isInsideGitRepo

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
	// Keep the runner's credential alive before we spend anything on this spawn.
	//
	// startProcess is the ONE seam every dispatch passes through — new tasks,
	// follow-ups, MCP calls, webhooks, the scheduler, voice. Putting the
	// keep-alive here rather than at each call site is the difference between
	// fixing the path the 2026-08-02 report happened to describe (a follow-up
	// from the phone) and fixing the class.
	//
	// Free when there is nothing to do: one file read and a base64 decode, no
	// fork, no network, no tokens (see refreshCodexCredentialIfNeeded). It only
	// reaches the network inside the renewal window — precisely when a spawn
	// would otherwise be about to 401.
	//
	// Deliberately NON-FATAL. A renewal that cannot happen must not stop a task
	// whose credential is still valid; the callers that need to REFUSE a
	// dispatch (continueTask parks the prompt) decide that themselves with the
	// same verdict. Blocking here on a network blip would invent an outage.
	if res := ensureRunnerCredentialFreshForTurn(context.Background(), task.RunnerID); !res.Healthy() {
		log.Printf("[task %s] runner credential not renewable before spawn (%s): %s", task.ID, res.Outcome, res.Reason)
	}

	// Wait for other Claude Code sessions to finish (if --wait-for-session is set)
	tm.waitForSessionSlot(task)

	// The transport prompt. PromptText, when a producer set it, is the whole
	// story — the scaffolded text the runner should read — and Title /
	// Description are then free to hold nothing but the user's own words for
	// the UI to render. Producers that add no scaffolding leave it empty and
	// get the historical Title (+ Description) behaviour unchanged.
	prompt := strings.TrimSpace(task.PromptText)
	if prompt == "" {
		prompt = task.Title
		if task.Description != "" && task.Description != task.Title {
			prompt = task.Title + "\n\n" + task.Description
		}
	}
	rawRunnerCommand := task.RawRunnerCommand || isRawRunnerCommand(prompt)
	if rawRunnerCommand {
		prompt = rawRunnerPromptForTask(task, prompt)
	}

	// Auto-detect project from task text and switch workDir if needed.
	// This enables "start BentoApp" from Yaver mobile when serving from ~.
	//
	// Auto-switch only when the caller didn't pin a workDir. Mobile's
	// feedback flow + the vibingify reshape already resolve the right
	// project path from projectName —
	// running autoSwitchProject on top of that lets prompt-word matches
	// like "codex" (a runner name commonly echoed in the prompt) hijack
	// the workDir to /root/.codex/.tmp/plugins, which is read-only
	// inside codex's own sandbox. Net effect on test-ephemeral: every
	// vibe task got cmd.Dir=/root/.codex/.tmp/plugins, codex's
	// workspace-write sandbox treated the actual project as outside
	// the writable root, and apply_patch failed with "Read-only file
	// system". Five user iterations later we figured it out.
	if !rawRunnerCommand && strings.TrimSpace(task.WorkDir) == "" {
		tm.autoSwitchProject(task, prompt)
	}

	// EMBEDDED "chat" mode: a third party (Talos web chat / WhatsApp / voice)
	// drives Yaver as a plain-language Q&A brain for a NON-TECHNICAL end user.
	// That user must see ONLY a clean, surface-encoded answer — never the
	// coding-agent framing, terminal narration, decision/scheduling preambles,
	// or this prompt. So chat mode gets its own clean contract and skips all the
	// coding-agent context blocks below.
	chatMode := isChatTaskMode(task.runner.Mode)

	// The whole prompt frame — armed, because startProcess starts a NEW task's
	// runner: the first turn of a task, a chain step, an auto-retry, a crash
	// restart, a fork. Follow-ups go through startResume, which arms only when
	// the resume cannot actually carry the conversation. See
	// task_prompt_frame.go for the rule.
	//
	// The warm-session and ResumeLast paths below can attach this spawn to an
	// EARLIER session for rate-limit or recurring-schedule reasons. They stay
	// armed on purpose: that session belongs to a different task (a different
	// workDir, source and viewport), so its briefing is not this
	// task's briefing. Cheap relative to being wrong about what the runner knows.
	chatModeArg := ""
	if chatMode {
		chatModeArg = task.runner.Mode
	}
	// claude takes the armed frame through --append-system-prompt; codex and
	// opencode have no such channel and get it in band. Same bytes either way —
	// composeTurn is the only assembler.
	// Goal-mode (opencode goal plugin): when the task carries a goal
	// objective, instruct the runner to open a persistent goal and keep
	// working toward it — the plugin's create_goal tool + idle auto-continue
	// keep the session alive across turns until complete/blocked/limited.
	if task.Goal != "" && normalizeRunnerID(task.runner.RunnerID) == "opencode" && !rawRunnerCommand {
		goalInstruction := "\n\n<yaver_goal>\n" +
			"Open a persistent goal for this task using the create_goal tool with this objective:\n" +
			"\"" + task.Goal + "\"\n" +
			"Keep working toward the goal across turns until it is complete (with evidence), blocked, or a safety limit is reached. Report goal status when done.\n" +
			"</yaver_goal>"
		prompt = strings.TrimSpace(prompt) + goalInstruction
	}

	systemFrame, prompt := tm.composeTurn(task, prompt, promptFramePolicy{
		ArmPreamble:        true,
		RawRunnerCommand:   rawRunnerCommand,
		ChatMode:           chatModeArg,
		NativeSystemPrompt: !rawRunnerCommand && runnerSupportsNativeSystemPrompt(task.runner.RunnerID),
	})
	// The frame is a TRANSPORT artifact — it goes on the wire to the runner and
	// nowhere else. Raw-mode runners echo stdin to stdout, which would put the
	// whole preamble on the user's screen as if the assistant had said it, so
	// arm the guard with the exact bytes we are about to send.
	tm.armPromptEchoGuard(task, prompt)

	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel

	runner := task.runner
	// Per-task model override flows into the runner's {model}
	// placeholder (if any). Must happen before buildRunnerArgs or
	// the placeholder lands in the argv literally.
	if task.Model != "" {
		runner.Model = task.Model
	}

	// Every Codex conversation needs a real assistant message, not a terminal
	// transcript posing as chat. --output-last-message is Codex's structured
	// boundary: ResultText/presentation read only the final answer while raw
	// stdout remains available in the folded evidence lane. This used to be
	// limited to embedded chat, leaving Yaver Tasks with the unreadable stream.
	if strings.EqualFold(runner.RunnerID, "codex") {
		task.codexLastMsgPath = filepath.Join(os.TempDir(), "yaver-codex-last-"+task.ID+".txt")
		injected := make([]string, 0, len(runner.Args)+2)
		for _, a := range runner.Args {
			if a == "{prompt}" {
				injected = append(injected, "--output-last-message", task.codexLastMsgPath)
			}
			injected = append(injected, a)
		}
		runner.Args = injected
		if effort := normalizeCodexReasoningEffort(task.ReasoningEffort); effort != "" {
			// Codex's -c is process-local. Put it before `exec` so it remains
			// valid for both fresh and `exec resume` turns.
			runner.Args = append([]string{"--config", fmt.Sprintf("model_reasoning_effort=%q", effort)}, runner.Args...)
		}
	}
	// Resolve the task's effective workDir for the runner's sandbox
	// allowlist (codex uses -C <DIR> to add it). Without this, codex's
	// workspace-write sandbox treats the project path as Read-only and
	// rejects apply_patch / sed inplace edits.
	taskDirForArgs := tm.effectiveTaskWorkDir(task)
	args := buildRunnerArgsWithWorkDir(runner, prompt, taskDirForArgs)
	// Out-of-band frame for runners that have the channel. Appended AFTER
	// buildRunnerArgs so the frame can never be caught by the `{prompt}` /
	// `{model}` / `{workDir}` placeholder substitution above — a frame that
	// happened to contain "{model}" would otherwise be rewritten.
	args = append(args, nativeSystemPromptArgs(runner.RunnerID, systemFrame)...)

	// Recurring-schedule resume: when the scheduler re-fires a schedule with
	// resume enabled, pick up the prior session on this first spawn.
	resumedForSchedule := false
	if task.ResumeLast {
		if newArgs, ok := resumeTransform(runner, args, prompt, taskDirForArgs, task.SessionID); ok {
			args = newArgs
			resumedForSchedule = true
			log.Printf("[task %s] Recurring schedule: resuming prior %s session (id=%q)", task.ID, runner.RunnerID, task.SessionID)
		}
	}

	// Override model if specified on the task (e.g. "opus", "sonnet",
	// "haiku", "gpt-5-codex"). Falls back to runner.Model when the task
	// didn't pin one — without this, codex would inherit Codex CLI's own
	// default (`o3-mini`) which fails on ChatGPT-account auth.
	// effectiveModelFor also re-applies the compatibility guard HERE, at the
	// last gate before argv: creation guards task.Model, but the
	// runner.Model fallback (boot-time global pref) bypassed it and spliced
	// a stale codex model into `opencode run --model gpt-5.4` — every task
	// on the box failed with "Model not found: gpt-5.4/".
	effectiveModel := effectiveModelFor(runner.RunnerID, task.Model, runner.Model)
	if task.Model == "" && effectiveModel != "" {
		// Persist what actually launched so every surface and later refusal
		// classification sees the operation, not an empty preference slot.
		task.Model = effectiveModel
	}
	if resumedForSchedule {
		// resumeTransform rebuilds Codex argv and every first-class runner
		// starts a new resume process. Carry the typed task selection into the
		// real next operation instead of merely updating Task.Model in storage.
		args = applyResumeRunnerSelection(runner.RunnerID, args, effectiveModel, task.ReasoningEffort)
	} else if effectiveModel != "" {
		modelOverride := false
		for i, a := range args {
			if a == "--model" && i+1 < len(args) {
				args[i+1] = effectiveModel
				modelOverride = true
				break
			}
		}
		if !modelOverride {
			switch runner.RunnerID {
			case "opencode", "remoteless":
				// remoteless shares opencode's argv shape (`opencode run …`),
				// so the deepseek model splices in the same place.
				args = insertRunnerFlagAfter(args, "run", "--model", effectiveModel)
			case "codex":
				args = insertRunnerFlagAfter(args, "exec", "--model", effectiveModel)
			default:
				args = append(args, "--model", effectiveModel)
			}
		}
	}

	// Determine working directory
	taskDir := tm.effectiveTaskWorkDir(task)
	// Project policy is resolved only when the runner is about to start. This
	// keeps agent boot light and lets every runner launch path (Claude Code,
	// Codex, and OpenCode) receive the same project-scoped MCP
	// selection. Explicit task MCPs remain additive, while the manifest can
	// require or disable named adapters.
	if selection, err := projectMCPSelection(taskDir, task.MCPServers); err != nil {
		cancel()
		return fmt.Errorf("project MCP policy: %w", err)
	} else {
		task.MCPServers = selection.Servers
	}
	includeYaverMcp := "1"
	if !task.IncludeYaverMcp {
		includeYaverMcp = "0"
	}
	if err := CheckRunnerReady(runner, taskDir); err != nil {
		cancel()
		return fmt.Errorf("runner not ready: %w", err)
	}

	// ACP is native for OpenCode and local-adapter backed for Codex/Claude.
	// Startup remains transactional until session/new succeeds, so failure here
	// can safely use the established CLI/tmux lane without running the prompt
	// twice or requiring an API key.
	if use, reason := shouldUseRunnerACP(task, runner, effectiveModel, rawRunnerCommand); use {
		started, acpErr := tm.tryStartRunnerACP(ctx, task, prompt, taskDir, acpTaskOptions{
			Model: effectiveModel, Mode: runner.Mode, ReasoningEffort: task.ReasoningEffort,
		})
		if started {
			return nil
		}
		task.TransportReason = acpErr.Error()
		log.Printf("[task %s] %s ACP unavailable before prompt (%v) — using CLI/PTY", task.ID, runner.RunnerID, acpErr)
		emitTaskEvent(task, map[string]interface{}{
			"type": "runner_transport", "schema": 1,
			"runner": normalizeRunnerID(runner.RunnerID), "transport": taskTransportCLI,
			"fallbackFrom": taskTransportACP, "reason": acpErr.Error(),
		})
	} else if normalizeRunnerID(runner.RunnerID) == "opencode" || normalizeRunnerID(runner.RunnerID) == "codex" || normalizeRunnerID(runner.RunnerID) == "claude" {
		task.TransportReason = reason
		log.Printf("[task %s] %s ACP not selected (%s) — using CLI/PTY", task.ID, runner.RunnerID, reason)
	}
	task.Transport = taskTransportCLI

	mcpScope := prepareRunnerMCPScope(runner.RunnerID, taskDir, task.MCPServers, []string{includeYaverMcp})
	switch normalizeRunnerID(runner.RunnerID) {
	case "codex":
		args = insertArgsAfter(args, "exec", mcpScope.Args)
	case "claude", "glm":
		args = append(args, mcpScope.Args...)
	case "opencode":
		args = append(args, mcpScope.Args...)
	}

	// ── Container execution (optional) ──────────────────────────────
	// If containerization is enabled for this task type, run inside Docker.
	useContainer := false
	if tm.ContainerRunner != nil && tm.ContainerRunner.IsAvailable() {
		if tm.ContainerizeHost {
			useContainer = true
		}
		// Hosted coding CLIs like Codex / Claude Code / OpenCode / Aider are
		// installed and authenticated on the host machine, not inside Yaver's
		// generic Docker image. Running them in the container makes even valid
		// host setups fail with "command not found".
		if useContainer && runnerRequiresHostRuntime(runner.RunnerID) {
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

	// Process completion is not output completion: Wait can return while the
	// pipe readers still hold the provider's final error. Join the reader before
	// deciding whether a short failure is a crash or a model refusal.
	outputDone := make(chan struct{})
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
		// Check for project-specific Dockerfile.yaver first, then config override
		if projectImage := tm.ContainerRunner.DetectProjectImage(ctx, taskDir); projectImage != "" {
			opts.CustomImage = projectImage
		} else if tm.ContainerImage != "" {
			opts.CustomImage = tm.ContainerImage
		}
		opts.ExtraMounts = append([]string{}, tm.ContainerMounts...)

		cmd, stdout, stderr, err := tm.ContainerRunner.RunTask(ctx, opts)
		if err != nil {
			cancel()
			return fmt.Errorf("container start: %w", err)
		}

		task.cmd = cmd
		now := time.Now()
		task.StartedAt = &now
		task.Status = TaskStatusRunning
		tm.present(task, taskRunningPresentation(task))
		trackForkedPID(cmd.Process.Pid)

		if runner.OutputMode == "raw" {
			go func() { defer close(outputDone); tm.readRawOutput(task, stdout, stderr) }()
		} else {
			go func() { defer close(outputDone); tm.readStreamJSON(task, stdout) }()
			go func() {
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					log.Printf("[task %s] [container stderr] %s", task.ID, scanner.Text())
				}
			}()
		}
	} else {
		// ── Direct execution (default) ──────────────────────────────────

		// Tmux-backed task mode: every first-class runner task gets one exact,
		// attachable session by default. YAVER_TMUX_RUNNER preserves the legacy
		// shared-session override; YAVER_TASK_TMUX=0 is the explicit opt-out.
		// Missing tmux still falls through to direct exec so coding remains
		// available on constrained hosts.
		var cmd *exec.Cmd
		var err error
		var tmuxEnvAdditions []string
		tmuxTarget := tmuxRunnerTargetForTask(task, runner.RunnerID)
		if tmuxTarget.Session != "" {
			log.Printf("[task %s] tmux mode: dispatching %s into session %q",
				task.ID, runner.Command, tmuxTarget.Session)
			task.TmuxSession = tmuxTarget.Session
			task.TmuxSessionID = ""
			task.TmuxWindowIndex = ""
			task.TmuxWindowName = ""
			task.TmuxPaneIndex = ""
			task.TmuxPaneID = ""
			cmd, tmuxEnvAdditions = buildTmuxRunnerCommand(ctx, tmuxTarget, task.ID, runner.RunnerID, taskDir, runner.Command, args, mcpScope.Env)
		} else {
			task.TmuxSession = ""
			task.TmuxSessionID = ""
			task.TmuxWindowIndex = ""
			task.TmuxWindowName = ""
			task.TmuxPaneIndex = ""
			task.TmuxPaneID = ""
			if normalizeRunnerID(runner.RunnerID) == "claude" {
				if err := preflightClaudeMacKeychainForHeadlessLaunch(); err != nil {
					cancel()
					return fmt.Errorf("runner not ready: %w", err)
				}
			}
			cmd = exec.CommandContext(ctx, runner.Command, args...)
		}
		cmd.Dir = taskDir

		// Ensure common tool paths are in PATH for background processes.
		cmd.Env = taskEnv(task)
		if len(tmuxEnvAdditions) > 0 {
			cmd.Env = append(cmd.Env, tmuxEnvAdditions...)
		}
		if len(mcpScope.Env) > 0 {
			cmd.Env = append(cmd.Env, mcpScope.Env...)
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

		if task.TmuxSession != "" {
			pane := waitForTmuxTaskPane(task.TmuxSession, task.ID, 500*time.Millisecond)
			if pane.PaneID != "" {
				tm.mu.Lock()
				task.TmuxSessionID = pane.SessionID
				task.TmuxWindowIndex = pane.WindowIndex
				task.TmuxWindowName = pane.WindowName
				task.TmuxPaneIndex = pane.PaneIndex
				task.TmuxPaneID = pane.PaneID
				tm.persistAsync()
				tm.mu.Unlock()
			}
		}

		now := time.Now()
		task.StartedAt = &now
		task.Status = TaskStatusRunning
		tm.present(task, taskRunningPresentation(task))
		reportMachineActivity() // idle auto-shutdown: managed box is in use

		trackForkedPID(cmd.Process.Pid)

		go SendDevLog(tm.ConvexURL, tm.AuthToken, tm.OwnerEmail, "task-started",
			fmt.Sprintf("Claude PID %d started for task %s", cmd.Process.Pid, task.ID), nil)

		// Monitor stdout based on output mode.
		if runner.OutputMode == "raw" {
			go func() { defer close(outputDone); tm.readRawOutput(task, stdout, stderr) }()
		} else {
			go func() { defer close(outputDone); tm.readStreamJSON(task, stdout) }()
			go func() {
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					log.Printf("[task %s stderr] %s", task.ID, scanner.Text())
				}
			}()
		}
	} // end else (direct execution)

	// Silence narration: output is evidence, not a deadline. A subscription
	// runner may legitimately wait behind provider/token queues for far longer
	// than a conventional subprocess, especially on a remote or small machine.
	// Do not kill work merely because it has not printed yet. Instead publish a
	// truthful semantic state so every client can keep showing progress and the
	// human can stop the task deliberately if it is genuinely wedged.
	go func() {
		time.Sleep(30 * time.Second)
		tm.mu.RLock()
		hasOutput := len(task.Output) > 0
		status := task.Status
		tm.mu.RUnlock()
		if !hasOutput && status == TaskStatusRunning {
			log.Printf("[task %s] no output after 30s — keeping subscription runner alive", task.ID)
			tm.present(task, taskPresentationInput{
				ID: task.ID + "-activity", Kind: "status",
				Text: "Waiting for the coding runner to respond.", Phase: "waiting", State: "waiting",
			})
		}

		// Do not turn an extended provider/token wait into a false failure. The
		// old four-minute kill made a slow-but-healthy subscription task vanish
		// from a phone exactly when its owner was waiting away from the machine.
		// Keep one explicit long-wait status instead; the process remains under
		// the user's Stop control and normal process-exit/error handling.
		const noOutputDeadline = 4 * time.Minute
		time.Sleep(noOutputDeadline - 30*time.Second)
		tm.mu.RLock()
		stillSilent := len(task.Output) == 0 && task.Status == TaskStatusRunning
		tm.mu.RUnlock()
		if stillSilent {
			log.Printf("[task %s] still waiting after %s — leaving runner alive", task.ID, noOutputDeadline)
			tm.present(task, taskPresentationInput{
				ID: task.ID + "-activity", Kind: "warning",
				Text:  "Still waiting for the runner. It remains active; token or provider queues can take a while.",
				Phase: "waiting", State: "waiting",
			})
		}
	}()

	// Wait for process to exit; auto-restart on unexpected crash.
	go func() {
		err := task.cmd.Wait()
		<-outputDone
		if task.cmd.Process != nil {
			untrackForkedPID(task.cmd.Process.Pid)
		}
		tm.mu.Lock()
		if task.Status == TaskStatusRunning {
			refusedModel, refusalReason := classifyUnsupportedModelForAttempt(task.Model, task.Output+"\n"+task.ResultText)
			// Provider entitlement is an operation result, not a process-status
			// guess. Some adapters report the rejected model and still exit zero;
			// those must take the same one-shot recovery path.
			if err != nil || refusedModel != "" {
				outputLen := len(task.Output)
				retries := task.retryCount
				if refusedModel != "" {
					globalModelSupport.Record(task.RunnerID, refusedModel, refusalReason)
					fallback, canFallback := modelFallbackForRefusal(task.RunnerID, refusedModel, task.modelFallbackAttempted)
					if canFallback && !globalModelSupport.Refused(task.RunnerID, fallback.Model) {
						task.modelFallbackAttempted = true
						task.Model = fallback.Model
						if normalizeRunnerID(task.RunnerID) == "codex" {
							task.ReasoningEffort = firstNonEmpty(normalizeCodexReasoningEffort(fallback.ReasoningEffort), "medium")
						}
						task.SessionID = ""
						task.ResumeLast = false
						task.Failure = nil
						task.Status = TaskStatusQueued
						task.FinishedAt = nil
						notice := fmt.Sprintf("\nModel %s was rejected by this account. Retrying once with Yaver default %s", refusedModel, fallback.Model)
						if task.ReasoningEffort != "" {
							notice += " · " + task.ReasoningEffort
						}
						notice += ".\n"
						task.Output += notice
						task.outputCh = make(chan string, 512)
						task.rawOutputCh = make(chan []byte, 256)
						task.eventCh = make(chan map[string]interface{}, 32)
						task.outputCh <- notice
						tm.persist()
						tm.mu.Unlock()
						log.Printf("[task %s] model %q rejected — retrying once with Yaver default %q", task.ID, refusedModel, fallback.Model)
						if restartErr := tm.startProcess(task); restartErr != nil {
							tm.mu.Lock()
							task.Status = TaskStatusFailed
							now := time.Now()
							task.FinishedAt = &now
							task.ResultText = restartErr.Error()
							task.Failure = diagnoseTaskFailure(task, now)
							tm.persist()
							tm.mu.Unlock()
							closeTaskStream(task.outputCh)
							closeTaskDone(task.doneCh)
						}
						return
					}
				}

				// Auto-restart if the process crashed with little/no output
				// and we haven't exhausted retries. This covers cases where
				// Claude gets OOM-killed, segfaults, or is terminated externally.
				if refusedModel == "" && retries < maxProcessRetries && outputLen < 100 {
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
					task.rawOutputCh = make(chan []byte, 256)
					task.eventCh = make(chan map[string]interface{}, 32)
					task.doneCh = make(chan struct{})

					if restartErr := tm.startProcess(task); restartErr != nil {
						log.Printf("[task %s] Auto-restart failed: %v", task.ID, restartErr)
						tm.mu.Lock()
						task.Status = TaskStatusFailed
						finishNow := time.Now()
						task.FinishedAt = &finishNow
						task.Failure = diagnoseTaskFailure(task, finishNow)
						task.Status = taskUnresolvedStatus(task, task.Status)
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
					// A provider rejecting one model does not make the runner down.
					// Only genuine process failures affect machine runner health.
					if refusedModel == "" {
						go func() {
							if tm.ConvexURL != "" {
								detail := fmt.Sprintf("all %d retries exhausted, exit: %v", maxProcessRetries, err)
								_ = ReportDeviceEvent(tm.ConvexURL, tm.AuthToken, tm.DeviceID, "crash", detail)
								_ = SetRunnerDown(tm.ConvexURL, tm.AuthToken, tm.DeviceID, true)
							}
						}()
					}

					// Auth-error detection: if stdout/stderr indicates the
					// runner's OAuth token was rejected by the API (401 /
					// invalid bearer / not logged in), invalidate that
					// runner's status cache so DeviceDetails / dashboard
					// flip from ✓ signed in to ⚠️ Sign in on next heartbeat
					// instead of waiting for the user to discover the
					// stale state by failing another task. Mirrors the
					// mobile ErrorMessage.detectRunnerAuthFailure patterns.
					// AUTOFIX (2026-08-02): learn a model the ACCOUNT cannot
					// run, so the next task routes to Yaver's current global
					// default. Without this the user re-ran the identical
					// doomed task forever — same prompt, same model, same 400 —
					// because nothing in the loop remembered the refusal.
					// effectiveModelFor already implements the remedy; this
					// gives it the fact it was missing. See
					// model_support_ledger.go.
					if refusedModel == "" {
						noteRunnerOutputForModelSupport(task.RunnerID, task.Output+"\n"+task.ResultText)
					}

					// Tail only — see runnerAuthClassifyTail. Scanning the whole
					// output let a task that merely PRINTED an auth string (this
					// repo's own source is full of them) sign a healthy runner out.
					if ok, reason := ClassifyRunnerAuthFailureFor(task.RunnerID, runnerAuthClassifyTail(task.Output)); ok {
						hitRunner := normalizeRunnerID(task.RunnerID)
						MarkRunnerAuthInvalidReason(hitRunner, reason)
						log.Printf("[task %s] auth-failure pattern detected for runner %q — invalidated runner auth status: %s", task.ID, hitRunner, reason)
					}

					task.Status = TaskStatusFailed
					log.Printf("[task %s] %s process failed: %v", task.ID, task.runner.Name, err)
				}
			} else if isEmptyRunnerReply(task.Output, task.ResultText) {
				// Clean exit, zero content. A runner that says NOTHING did not
				// succeed — observed 2026-07-26: opencode on zai glm-4.7 exits 0
				// in seconds with no output, and the task landed in REVIEW as a
				// silent card the user had to discover was empty. The no-output
				// watchdog only covers RUNNING tasks; a fast clean exit beat it.
				task.Status = TaskStatusFailed
				task.ResultText = "The runner exited without producing any reply. " +
					"This usually means the model silently refused the request — " +
					"if this runner uses zai glm-4.7, switch it to glm-5.2 (Settings → Runner → Model), then retry."
				log.Printf("[task %s] %s exited cleanly with EMPTY output — marking failed, not review", task.ID, task.runner.Name)
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
			task.LastActiveAt = finishNow
			// EVERY terminal outcome passes through here — failed, soft-failed,
			// empty-reply, and succeeded. The auth classifier above only ran in
			// the hard-failure branch, which is precisely why the 2026-07-27
			// revocation went unseen: Claude Code printed
			// "Please run /login · API Error: 401 OAuth access token has been
			// revoked." and exited ZERO. A runner that reports its own auth
			// death politely must not be believed about everything else.
			ObserveRunnerAuthFromOutput(task.RunnerID, task.Output+"\n"+task.ResultText, string(task.Status))
			task.Failure = diagnoseTaskFailure(task, finishNow)
			task.Status = taskUnresolvedStatus(task, task.Status)
			// Save assistant response as conversation turn
			if task.ResultText != "" {
				task.Turns = append(task.Turns, ConversationTurn{
					Role:      "assistant",
					Content:   task.ResultText,
					Timestamp: finishNow,
					Hidden:    !taskHasSemanticAssistantTextLocked(task, task.ResultText),
				})
				recordSessionMessage(task, "assistant", finishNow)
			}
			if (task.Status == TaskStatusReady || task.Status == TaskStatusReview || task.Status == TaskStatusFinished) && len(task.PendingFollowUps) > 0 {
				next := task.PendingFollowUps[0]
				task.PendingFollowUps = task.PendingFollowUps[1:]
				oldDoneCh := task.doneCh
				queuedAt := next.Timestamp
				if queuedAt.IsZero() {
					queuedAt = time.Now()
				}
				task.Turns = append(task.Turns, ConversationTurn{Role: "user", Content: next.Input, Timestamp: queuedAt})
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
					task.Output = err.Error()
					task.ResultText = err.Error()
					now := time.Now()
					task.FinishedAt = &now
					task.Failure = diagnoseTaskFailure(task, now)
					task.Status = taskUnresolvedStatus(task, task.Status)
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
		go saveSessionFile(task, task.runner.Name, tm.effectiveTaskWorkDir(task))
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	return nil
}

func runnerRequiresHostRuntime(runnerID string) bool {
	switch normalizeRunnerID(runnerID) {
	case "claude", "codex", "opencode":
		return true
	default:
		return false
	}
}

// taskUsesOpenCodeCLI reports whether a raw task is backed by OpenCode even
// when the user-facing lane has a different stable id (currently
// "remoteless"). The executable is the capability boundary here: both lanes
// have the same stdout/stderr contract and must get the same semantic output.
func taskUsesOpenCodeCLI(task *Task) bool {
	if task == nil {
		return false
	}
	id := normalizeRunnerID(task.runner.RunnerID)
	if id == "" {
		id = normalizeRunnerID(task.RunnerID)
	}
	if id == "opencode" || id == "remoteless" {
		return true
	}
	command := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.TrimSpace(task.runner.Command))), ".exe")
	return command == "opencode"
}

// readRawOutput reads plain text lines from stdout (for non-JSON runners).
func (tm *TaskManager) readRawOutput(task *Task, stdout, stderr io.Reader) {
	var output strings.Builder
	var semanticStdout strings.Builder
	var outputMu sync.Mutex
	tm.mu.RLock()
	output.WriteString(task.Output)
	presentationMessageID := taskAssistantPresentationID(task)
	tm.mu.RUnlock()

	// Per-runner stream rewriting. opencode's TUI ships ANSI escapes
	// and CLI-style `$ <cmd>` markers that the chat renderer in
	// mobile + web doesn't recognize on its own — see
	// opencodeStreamFilter for the full rationale. One filter
	// stdout and stderr need independent line buffers. They are independent
	// pipes, so sharing one buffer can splice a partial assistant sentence from
	// stdout into a command line from stderr. Each filter gets a source suffix
	// so command event ids remain unique if both streams contain tool evidence.
	var ocFilters map[string]*opencodeStreamFilter
	if taskUsesOpenCodeCLI(task) {
		ocFilters = map[string]*opencodeStreamFilter{
			"stdout": {task: task, tm: tm, source: "stdout"},
			"stderr": {task: task, tm: tm, source: "stderr"},
		}
	}
	// Codex's normal renderer supplies human phase rows but not typed tool
	// events. Turn only those known phase rows into semantic activity; raw
	// console evidence remains folded and untouched.
	var rawActivities map[string]*rawTaskActivityNarrator
	if normalizeRunnerID(task.runner.RunnerID) == "codex" {
		rawActivities = map[string]*rawTaskActivityNarrator{
			"stdout": {tm: tm, task: task}, "stderr": {tm: tm, task: task},
		}
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
	stripLiveANSI := ocFilters == nil && normalizeRunnerID(task.runner.RunnerID) != ""
	// OpenCode 1.18.25's default `run` formatter has a useful native channel
	// boundary: the assistant reply is stdout, while the model banner, tool
	// commands, diffs and command output are stderr. Keep both in RawOutput, but
	// retain stdout separately so chat never has to regex a terminal transcript.
	openCodeSemanticStdout := taskUsesOpenCodeCLI(task)

	// Armed by startProcess / startResume with this turn's exact prompt bytes.
	tm.mu.RLock()
	echoGuard := task.echoGuard
	tm.mu.RUnlock()

	var wg sync.WaitGroup
	readStream := func(name string, r io.Reader) {
		defer wg.Done()
		defer func() {
			if narrator := rawActivities[name]; narrator != nil {
				narrator.flush()
			}
		}()
		if r == nil {
			return
		}
		buf := make([]byte, 8192)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				payload := buf[:n]
				if openCodeSemanticStdout && name == "stdout" {
					outputMu.Lock()
					semanticStdout.WriteString(stripANSI(string(payload)))
					outputMu.Unlock()
				}
				// Retain the RAW bytes BEFORE any grooming filter runs. The
				// console view on mobile + web feeds these exact bytes to
				// xterm.js, so an opencode run paints its TUI the way it
				// does in a real terminal. See emitRaw.
				tm.emitRaw(task, payload)
				if ocFilter := ocFilters[name]; ocFilter != nil {
					payload = ocFilter.process(payload)
				} else if stripLiveANSI {
					payload = []byte(stripANSI(string(payload)))
				}
				if narrator := rawActivities[name]; narrator != nil {
					narrator.observe(string(payload))
				}
				// Drop the runner's verbatim echo of the Yaver-framed prompt
				// BEFORE it reaches task.Output or task.outputCh — i.e. before
				// any surface can render it. Deliberately after the ANSI/
				// opencode filters: an escape sequence spliced through the
				// boundary sentinel would defeat the match. Bounded three ways
				// and flushed below, so it can never hold output forever.
				// See prompt_echo_guard.go.
				if echoGuard != nil && len(payload) > 0 {
					outputMu.Lock()
					payload = []byte(echoGuard.filter(string(payload)))
					outputMu.Unlock()
				}
				if len(payload) > 0 {
					outputMu.Lock()
					tm.emit(task, &output, string(payload))
					outputMu.Unlock()
					// Best-effort: recover codex/opencode session id from raw
					// output so follow-ups / recurring schedules can resume.
					// A miss is surfaced on follow-up rather than guessing a
					// different session (especially OpenCode's "last in cwd").
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
	if ocFilters != nil {
		for _, name := range []string{"stderr", "stdout"} {
			if rem := ocFilters[name].flush(); len(rem) > 0 {
				outputMu.Lock()
				tm.emit(task, &output, string(rem))
				outputMu.Unlock()
			}
		}
	}
	// Unconditional release of anything the echo guard is still holding. A
	// stream that ended mid-echo (runner crashed, sentinel never echoed) must
	// surface what it did say rather than vanish: a guard that can swallow a
	// crash message is the silent-failure defect wearing a new hat.
	if echoGuard != nil {
		if rem := echoGuard.flush(); rem != "" {
			outputMu.Lock()
			tm.emit(task, &output, rem)
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
	semanticFinal := false
	if openCodeSemanticStdout {
		if final := strings.TrimSpace(stripPromptEcho(semanticStdout.String())); final != "" {
			task.ResultText = final
			semanticFinal = true
		}
	}
	// Embedded chat + codex: prefer codex's own final-message file (written via
	// --output-last-message). It contains ONLY the final answer — no reasoning,
	// no tool-call logs, no banner — which is exactly what a normie should see.
	if task.codexLastMsgPath != "" {
		if b, err := os.ReadFile(task.codexLastMsgPath); err == nil {
			if final := strings.TrimSpace(string(b)); final != "" {
				task.ResultText = final
				semanticFinal = true
			}
		}
		_ = os.Remove(task.codexLastMsgPath)
	}
	resultText := task.ResultText
	tm.mu.Unlock()
	if semanticFinal && strings.TrimSpace(resultText) != "" {
		tm.present(task, taskPresentationInput{
			ID: presentationMessageID, Kind: "message", Role: "assistant",
			Text: resultText, Phase: "complete", State: "completed",
		})
	}

	log.Printf("[task %s] Raw output reader finished (output_len=%d, result_len=%d)",
		task.ID, output.Len(), len(task.ResultText))
}

// composeRunnerPrompt joins a Yaver-authored runner briefing to the user's own
// title/description the same way startProcess would have joined them.
//
// Returns "" when there is no briefing — which is the signal to startProcess to
// use its historical Title (+ Description) path. That "" matters: it means a
// producer that adds no scaffolding keeps byte-identical behaviour, so this
// split cannot change what any unbriefed runner reads.
func composeRunnerPrompt(briefing, title, description string) string {
	if strings.TrimSpace(briefing) == "" {
		return ""
	}
	prompt := briefing + title
	if description != "" && description != title {
		prompt += "\n\n" + description
	}
	return prompt
}

// armPromptEchoGuard records the exact prompt bytes this turn sends so
// readRawOutput can recognise — and drop — the runner's echo of them.
//
// Armed for every turn regardless of runner: it is CONSUMED only by
// readRawOutput, which is the raw-stdout path by construction. Stream-json
// runners (claude) go through readStreamJSON and never touch it, so there is
// no branch here that could get the runner classification wrong.
func (tm *TaskManager) armPromptEchoGuard(task *Task, prompt string) {
	if task == nil {
		return
	}
	tm.mu.Lock()
	task.echoGuard = newPromptEchoGuard(prompt)
	tm.mu.Unlock()
}

// emitRaw retains the runner's RAW stdout bytes — ANSI and all — for the
// console view, BEFORE the per-runner grooming filters strip them. Called
// once per raw chunk in readRawOutput, immediately after the read, so the
// retained tail is byte-for-byte what the process wrote to the terminal.
//
// Two consumers, both tail-capped:
//   - task.rawOutputCh carries the chunk to live SSE subscribers (drops on
//     full, same policy as outputCh; the replay endpoint is the recovery
//     path).
//   - task.RawOutput accumulates a tail-capped byte buffer that
//     `GET /tasks/{id}/output?rawSince=` replays to a late-joining or
//     reconnecting console client.
func (tm *TaskManager) emitRaw(task *Task, chunk []byte) {
	if task == nil || len(chunk) == 0 {
		return
	}
	// Copy before any async send: chunk aliases the read buffer, which the
	// next Read call reuses.
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	// Retain BEFORE the live send, so by the time a subscriber drains the
	// channel the retained tail already includes this chunk — the `offset`
	// the SSE raw frame carries is then a cursor that can never point past
	// bytes the subscriber is about to receive.
	tm.mu.Lock()
	task.RawOutput += string(chunk)
	if len(task.RawOutput) > rawOutputMaxBytes {
		task.RawOutput = rawOutputTruncatedMarker +
			task.RawOutput[len(task.RawOutput)-rawOutputMaxBytes:]
	}
	tm.mu.Unlock()
	select {
	case task.rawOutputCh <- cp:
	default:
	}
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
	if reason := runtimeRenderReasonFromTaskOutput(text); reason != "" {
		emitRuntimeRenderRequested(task, reason, text)
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
	presentationMessageID := taskAssistantPresentationID(task)
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
		tm.presentCommandActivity(task, cmd)
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
					// Keep partial runner text in its evidence lane. A command, diff,
					// or code fence can cross a token boundary; only the completed
					// answer enters the agent-owned human presentation stream.
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
					tm.present(task, taskPresentationInput{
						ID: presentationMessageID, Kind: "message", Role: "assistant",
						Text: resultStr, Phase: "complete", State: "completed",
					})
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
	tm.closeTaskOwnedTmuxSeat(id)

	return nil
}

// closeTaskOwnedTmuxSeat gracefully exits a runner when it is still present,
// then removes the exact tmux session Yaver created for this task. This is an
// explicit lifecycle operation used by Complete, Stop, and Delete; automatic
// runner success/failure never calls it. User-owned/adopted tmux sessions are
// deliberately excluded.
func (tm *TaskManager) closeTaskOwnedTmuxSeat(id string) {
	tm.mu.RLock()
	task, ok := tm.tasks[id]
	if !ok || task == nil || task.IsAdopted {
		tm.mu.RUnlock()
		return
	}
	session := strings.TrimSpace(task.TmuxSession)
	runnerID := normalizeRunnerID(task.RunnerID)
	paneID := strings.TrimSpace(task.TmuxPaneID)
	isOwned := taskOwnsRecoverableTmuxSeat(task)
	tm.mu.RUnlock()
	if !isOwned || !tmuxSessionExists(session) {
		return
	}

	target := session
	if paneID != "" && tmuxTargetExists(paneID) {
		target = paneID
	}
	if exitCmd := tmuxRunnerExitCommand(target, runnerID); exitCmd != "" {
		if err := sendTmuxLine(target, exitCmd); err != nil {
			log.Printf("[task %s] graceful runner exit in %s failed: %v", id, target, err)
		} else {
			waitForTmuxRunnerExit(target, 4*time.Second)
		}
	}
	log.Printf("[task %s] Closing task-owned tmux session %q", id, session)
	if out, err := exec.Command(tmuxCmdName(), "kill-session", "-t", session).CombinedOutput(); err != nil && tmuxSessionExists(session) {
		log.Printf("[task %s] Task-owned tmux cleanup failed: %v: %s", id, err, strings.TrimSpace(string(out)))
	}
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
	// A client may retry after the agent committed the tombstone but the HTTP
	// response was lost. The lifecycle operation is idempotent.
	if task.DeletedAt != nil {
		tm.mu.RUnlock()
		return nil
	}
	isRunning := task.Status == TaskStatusRunning || task.Status == TaskStatusQueued
	isAdoptedTmux := task.IsAdopted && task.TmuxSession != "" && tm.TmuxMgr != nil
	taskOwnedTmux := taskOwnsNamedTmuxSeat(task)
	tm.mu.RUnlock()

	// Auto-stop running tasks before deleting
	if isRunning {
		if isAdoptedTmux {
			log.Printf("[task %s] Closing adopted tmux runner before delete", id)
			if err := tm.TmuxMgr.CloseAdoptedTask(id); err != nil {
				return fmt.Errorf("close retained coding session: %w", err)
			}
		} else {
			log.Printf("[task %s] Stopping running task before delete", id)
			if err := tm.StopTask(id); err != nil {
				return fmt.Errorf("stop task before delete: %w", err)
			}
		}
		// Wait briefly for process cleanup
		select {
		case <-task.doneCh:
		case <-time.After(3 * time.Second):
			log.Printf("[task %s] Timed out waiting for process exit during delete", id)
		}
	}
	// Review tasks deliberately retain their exact task-owned seat for a
	// follow-up. Delete is also a lifecycle boundary, so remove it here even
	// when the task is no longer running.
	if taskOwnedTmux {
		tm.closeTaskOwnedTmuxSeat(id)
		if tmuxSessionExists(strings.TrimSpace(task.TmuxSession)) {
			return fmt.Errorf("coding session %s is still present; task was not deleted", task.TmuxSession)
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	deletedAt := time.Now()
	identity := tm.taskExecutionIdentity(task)
	// Keep only the non-context session tombstone. Prompts, transcript,
	// output, paths, attachments and runtime handles are private task context
	// and are purged structurally by replacing the object, not by trying to
	// remember an ever-growing list of private fields to clear.
	*task = Task{
		ID: id, Status: TaskStatusStopped, Source: task.Source,
		RunnerID: identity.RunnerID, YaverSessionID: identity.YaverSessionID,
		RemoteBoxID: identity.RemoteBoxID, RunnerName: identity.RunnerName,
		SessionStartedFrom: identity.StartedFrom, StartedFromSurface: identity.StartedFromSurface,
		InitialSurface: identity.InitialSurface, SessionStartedAt: identity.SessionStartedAt,
		LastSurface: identity.LastSurface, LastActiveAt: deletedAt, DeletedAt: &deletedAt,
		FirstUserMessageAt: identity.FirstUserMessageAt, FirstAgentResponseAt: identity.FirstAgentResponseAt,
		LastUserMessageAt: identity.LastUserMessageAt, LastAgentResponseAt: identity.LastAgentResponseAt,
		SessionID:   identity.RunnerSessionID,
		TmuxSession: identity.TmuxSession, TmuxSessionID: identity.TmuxSessionID,
		TmuxWindowIndex: identity.TmuxWindowIndex, TmuxWindowName: identity.TmuxWindowName,
		TmuxPaneIndex: identity.TmuxPaneIndex, TmuxPaneID: identity.TmuxPaneID,
		CreatedAt: identity.SessionStartedAt, FinishedAt: &deletedAt,
	}
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
	tm.mu.RLock()
	var ids []string
	for id, t := range tm.tasks {
		if t.DeletedAt == nil && t.Status != TaskStatusRunning && t.Status != TaskStatusQueued {
			ids = append(ids, id)
		}
	}
	tm.mu.RUnlock()

	// Use the single-task lifecycle path so completed task-owned tmux seats are
	// closed too. Removing only the history row would orphan every persistent
	// vibe terminal introduced for follow-up continuity.
	deleted := 0
	for _, id := range ids {
		if err := tm.DeleteTask(id); err == nil {
			deleted++
		}
	}
	return deleted
}

// ResumeTask resumes an existing task in-place with a follow-up prompt.
// Output is concatenated and the same task, runner session, and task-owned tmux
// seat are kept for every supported runner.
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
	identity := tm.taskExecutionIdentity(task)
	existingRunner := normalizeRunnerID(task.RunnerID)
	requestedRunner := normalizeRunnerID(opts.RunnerID)
	if requestedRunner != "" && requestedRunner != existingRunner {
		tm.mu.Unlock()
		return nil, &TaskContinuationConflict{
			Code:     "task_runner_session_mismatch",
			Reason:   fmt.Sprintf("This task belongs to %s. Start a new task to use %s; a follow-up cannot switch runner sessions.", existingRunner, requestedRunner),
			Identity: identity,
		}
	}
	if task.Status == TaskStatusRunning || task.Status == TaskStatusQueued {
		queuedAt := time.Now()
		// Queue the follow-up onto the running task. The drain runs
		// after the current response finishes (see startTask / startResume
		// completion blocks). Works for any task source so phones can
		// text mid-stream the way Codex/Claude Code do.
		task.PendingFollowUps = append(task.PendingFollowUps, PendingFollowUp{
			Input:     input,
			Images:    append([]ImageAttachment{}, images...),
			Options:   opts,
			Timestamp: queuedAt,
		})
		recordSessionMessage(task, "user", queuedAt)
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
	// A follow-up is allowed only when the native runner can address the exact
	// prior conversation. Falling through to a cold process under the same task
	// ID is still a new session, merely hidden from the user.
	if !identity.Resumable {
		tm.mu.Unlock()
		return nil, &TaskContinuationConflict{
			Code:     "runner_session_unavailable",
			Reason:   fmt.Sprintf("Yaver cannot resume this %s conversation because its runner session ID was not captured. Start a new task; this follow-up was not sent.", existingRunner),
			Identity: identity,
		}
	}

	// Append follow-up to conversation history
	turn := ConversationTurn{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now(),
	}
	task.Turns = append(task.Turns, turn)
	recordSessionMessage(task, "user", turn.Timestamp)

	// Save new images if any
	if len(images) > 0 {
		newPaths := saveImages(id, images)
		task.ImagePaths = append(task.ImagePaths, newPaths...)
	}
	// The runner is immutable for an in-place continuation. An explicit switch
	// was rejected above; rehydrate legacy in-memory runner config only.
	if task.runner.Command == "" && existingRunner != "" {
		task.runner = GetRunnerConfig(existingRunner)
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
	task.rawOutputCh = make(chan []byte, 256)
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
//
// This is the FOLLOW-UP path — the second and every later message of one
// conversation, from mobile, web, CLI, or the pending-follow-up drain. Its
// defining property: when the runner really is resuming, the process already
// read the Yaver preamble on turn 1 and still has it, so the user's words go
// through essentially verbatim. See task_prompt_frame.go for the rule and for
// what "essentially" leaves in (attachments, per-turn context, the boundary).
func (tm *TaskManager) startResume(task *Task, prompt string) error {
	// A follow-up is already accepted and present in Turns before this function
	// runs. Publish its assistant slot before any runner/session handshake so
	// every conversation turn receives an immediate first response.
	tm.present(task, taskAcceptedPresentation(task))
	// Use task's runner if set, otherwise fall back to global
	runner := task.runner
	if runner.Command == "" {
		runner = tm.runner
	}
	// Follow-ups remain on the established resume-capable CLI lane in this
	// first ACP slice. Record the actual transport instead of leaving the
	// first turn's ACP value attached to a different execution path.
	task.Transport = taskTransportCLI

	// The one decision. Not "is this the first message" (the UI cannot know
	// what the runner process holds) but "will the process we are about to
	// spawn still carry this conversation". When it will not — no captured
	// session id, a runner switch, a CLI that cannot resume — the follow-up is
	// really a cold first message and must be briefed like one.
	carriesContext := resumeCanCarryContext(runner, task.SessionID)
	if !carriesContext {
		return &TaskContinuationConflict{
			Code:     "runner_session_unavailable",
			Reason:   fmt.Sprintf("refusing cold follow-up: %s task %s has no resumable runner session", runner.RunnerID, task.ID),
			Identity: tm.taskExecutionIdentity(task),
		}
	}
	chatModeArg := ""
	if isChatTaskMode(task.runner.Mode) {
		chatModeArg = task.runner.Mode
	}
	rawFollowUpCommand := isRawRunnerCommand(prompt)
	systemFrame, prompt := tm.composeTurn(task, prompt, promptFramePolicy{
		ArmPreamble:      false,
		RawRunnerCommand: rawFollowUpCommand,
		ChatMode:         chatModeArg,
		// A follow-up that re-arms (cold process, runner switch, unresumable
		// session) briefs the same way a first message does — including through
		// the native channel when the runner has one.
		NativeSystemPrompt: false,
	})
	// Follow-ups echo too — codex reproduces stdin on EVERY turn, not just the
	// first — so the guard is re-armed per turn with that turn's exact bytes.
	tm.armPromptEchoGuard(task, prompt)

	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel

	// Keep follow-ups on the same clean-answer contract as the first Codex
	// turn. The file is per task and removed by readRawOutput after each turn.
	if strings.EqualFold(runner.RunnerID, "codex") {
		task.codexLastMsgPath = filepath.Join(os.TempDir(), "yaver-codex-last-"+task.ID+".txt")
		injected := make([]string, 0, len(runner.Args)+2)
		for _, arg := range runner.Args {
			if arg == "{prompt}" {
				injected = append(injected, "--output-last-message", task.codexLastMsgPath)
			}
			injected = append(injected, arg)
		}
		runner.Args = injected
	}

	// Resume reuses the same workDir resolution as initial spawn so
	// codex's -C sandbox allowlist stays consistent across follow-ups.
	resumeWorkDir := tm.effectiveTaskWorkDir(task)
	args := buildRunnerArgsWithWorkDir(runner, prompt, resumeWorkDir)
	args = append(args, nativeSystemPromptArgs(runner.RunnerID, systemFrame)...)

	// Resume the prior conversation (this is always a follow-up). resumeTransform
	// handles claude (--resume <id>), opencode (--session <id>), codex (exec
	// resume <id>), and generic ResumeArgs runners; it falls back (ok=false)
	// when the runner can't resume with what we captured, so we spawn fresh.
	if newArgs, ok := resumeTransform(runner, args, prompt, resumeWorkDir, task.SessionID); ok {
		args = applyResumeRunnerSelection(
			runner.RunnerID,
			newArgs,
			effectiveModelFor(runner.RunnerID, task.Model, runner.Model),
			task.ReasoningEffort,
		)
		log.Printf("[task %s] Resuming %s session (id=%q)", task.ID, runner.RunnerID, task.SessionID)
	} else if runner.RunnerID == "claude" {
		// New claude session — give it a unique id so future follow-ups
		// can resume it.
		args = append(args, "--session-id", uuid.New().String())
	}

	var cmd *exec.Cmd
	var tmuxEnvAdditions []string
	tmuxTarget := tmuxRunnerTargetForTask(task, runner.RunnerID)
	if tmuxTarget.Session != "" {
		log.Printf("[task %s] tmux mode: dispatching %s follow-up into session %q",
			task.ID, runner.Command, tmuxTarget.Session)
		// The standard task-owned target persists across successful turns. Keep
		// its last-known exact seat visible while the next turn starts; clearing
		// these fields made the client briefly lose the identity and, before the
		// persistent wrapper, hid that the session/pane had actually changed.
		sameTmuxSeat := task.TmuxSession == tmuxTarget.Session
		task.TmuxSession = tmuxTarget.Session
		if !sameTmuxSeat {
			task.TmuxSessionID = ""
			task.TmuxWindowIndex = ""
			task.TmuxWindowName = ""
			task.TmuxPaneIndex = ""
			task.TmuxPaneID = ""
		}
		cmd, tmuxEnvAdditions = buildTmuxRunnerCommand(ctx, tmuxTarget, task.ID, runner.RunnerID, resumeWorkDir, runner.Command, args, nil)
	} else {
		task.TmuxSession = ""
		task.TmuxSessionID = ""
		task.TmuxWindowIndex = ""
		task.TmuxWindowName = ""
		task.TmuxPaneIndex = ""
		task.TmuxPaneID = ""
		cmd = exec.CommandContext(ctx, runner.Command, args...)
	}
	cmd.Dir = resumeWorkDir
	cmd.Env = taskEnv(task)
	if len(tmuxEnvAdditions) > 0 {
		cmd.Env = append(cmd.Env, tmuxEnvAdditions...)
	}

	// On Android, run the forked runner inside the proot rootfs (no-op elsewhere).
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

	if task.TmuxSession != "" {
		pane := waitForTmuxTaskPane(task.TmuxSession, task.ID, 500*time.Millisecond)
		if pane.PaneID != "" {
			tm.mu.Lock()
			task.TmuxSessionID = pane.SessionID
			task.TmuxWindowIndex = pane.WindowIndex
			task.TmuxWindowName = pane.WindowName
			task.TmuxPaneIndex = pane.PaneIndex
			task.TmuxPaneID = pane.PaneID
			tm.persistAsync()
			tm.mu.Unlock()
		}
	}

	now := time.Now()
	task.StartedAt = &now
	task.Status = TaskStatusRunning
	tm.present(task, taskRunningPresentation(task))
	reportMachineActivity() // idle auto-shutdown: managed box is in use

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
			task.LastActiveAt = now
			task.Failure = diagnoseTaskFailure(task, now)
			task.Status = taskUnresolvedStatus(task, task.Status)
			// Save the latest result as a conversation turn
			if task.ResultText != "" {
				task.Turns = append(task.Turns, ConversationTurn{
					Role:      "assistant",
					Content:   task.ResultText,
					Timestamp: now,
					Hidden:    !taskHasSemanticAssistantTextLocked(task, task.ResultText),
				})
				recordSessionMessage(task, "assistant", now)
			}
			if (task.Status == TaskStatusReady || task.Status == TaskStatusReview || task.Status == TaskStatusFinished) && len(task.PendingFollowUps) > 0 {
				next := task.PendingFollowUps[0]
				task.PendingFollowUps = task.PendingFollowUps[1:]
				oldDoneCh := task.doneCh
				queuedAt := next.Timestamp
				if queuedAt.IsZero() {
					queuedAt = time.Now()
				}
				task.Turns = append(task.Turns, ConversationTurn{Role: "user", Content: next.Input, Timestamp: queuedAt})
				if len(next.Images) > 0 {
					newPaths := saveImages(task.ID, next.Images)
					task.ImagePaths = append(task.ImagePaths, newPaths...)
				}
				task.Output = ""
				task.ResultText = ""
				task.FinishedAt = nil
				task.Status = TaskStatusQueued
				task.outputCh = make(chan string, 512)
				task.rawOutputCh = make(chan []byte, 256)
				task.eventCh = make(chan map[string]interface{}, 32)
				task.doneCh = make(chan struct{})
				tm.persist()
				tm.mu.Unlock()
				close(oldDoneCh)
				if err := tm.startResume(task, next.Input); err != nil {
					tm.mu.Lock()
					task.Status = TaskStatusFailed
					task.Output = err.Error()
					task.ResultText = err.Error()
					now := time.Now()
					task.FinishedAt = &now
					task.Failure = diagnoseTaskFailure(task, now)
					task.Status = taskUnresolvedStatus(task, task.Status)
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
		go saveSessionFile(task, task.runner.Name, tm.effectiveTaskWorkDir(task))
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	return nil
}

func taskSourcePromptSuffix(source string) string {
	switch source {
	case "mcp":
		return autonomousTaskResponseContext()
	case terminalLocalTaskSource, terminalRemoteTaskSource, "attach", "cli", "console", "connect":
		return "\n\nYou are running inside an interactive terminal attached to Yaver. Show what you are doing step by step. Use terminal commands when needed. Be concise." + consoleTaskResponseContext()
	default:
		return autonomousTaskResponseContext()
	}
}

// autonomousTaskResponseContext is intentionally small. Presentation is a
// Yaver-agent responsibility: the runner should work, while the agent splits
// its answer, terminal bytes, commands, and patches into the versioned stream
// contract. Large client-specific formatting prompts caused the very noisy
// mobile output this layer exists to prevent.
func autonomousTaskResponseContext() string {
	return `

[Yaver task]
Proceed independently and keep working until the requested work is complete.
Do not ask a question unless an irreversible decision has no safe default.
At completion, state the outcome and any real blocker plainly.`
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

// chatTaskResponseContext is the EMBEDDED-mode contract. A third party drives
// Yaver as a Q&A assistant for a non-technical end user, so the output must be a
// clean, human-readable answer — no coding-agent framing, no terminal narration,
// no tool logs, no engine internals, and never this prompt. The answer is encoded
// for the target surface (task.Surface): web markdown, WhatsApp markup, or plain
// prose for voice.
func chatTaskResponseContext(mode string) string {
	base := "\n\nYou are answering as Talos, an assistant for a manufacturing/ERP business, speaking to a NON-TECHNICAL user. Answer their question directly and completely." +
		"\n- Reply in the SAME language as the user's message (Turkish or English)." +
		"\n- Give ONLY the final answer. No step-by-step narration, no terminal/command output, no \"checked/next\" status bullets, no mention of tools, runners, engines, files, or how you obtained the data." +
		"\n- Use the available Talos data tools to fetch REAL data; fetch ALL relevant rows and aggregate fully before answering — never report a partial slice or stop early on a large result set." +
		"\n- Be accurate and concrete: real names, codes, quantities, currencies, dates. If a total is asked, compute the complete total."
	// Surface is encoded as a suffix on the mode: "chat" (web markdown, default),
	// "chat:whatsapp", "chat:voice". mode is a no-op for codex so this is safe.
	surface := ""
	if i := strings.IndexByte(mode, ':'); i >= 0 {
		surface = strings.ToLower(strings.TrimSpace(mode[i+1:]))
	}
	switch surface {
	case "whatsapp", "wa":
		return base + "\n- This is sent over WhatsApp: use WhatsApp formatting ONLY — *bold* with single asterisks, _italic_. NO markdown tables and NO headings. Present rows as short \"label: value\" lines or \"- \" bullets. Keep it tight and scannable."
	case "plain", "voice", "tts":
		return base + "\n- This will be read aloud: reply in plain prose sentences. No markdown, no tables, no bullets, no code."
	default: // web_markdown — Talos web/mobile chat bubble
		return base + "\n- This renders as GitHub-flavored Markdown in a chat bubble: use clean Markdown — tables, \"- \" bullets, **bold** — exactly like a normal chat answer."
	}
}

// ListTasks returns info about all tasks.
func (tm *TaskManager) ListTasks() []TaskInfo {
	if tm == nil {
		return nil
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]TaskInfo, 0, len(tm.tasks))
	hostname, _ := os.Hostname()
	for _, t := range tm.tasks {
		if t.DeletedAt != nil {
			continue
		}
		// Only include last 2000 chars of output in listings.
		output := t.Output
		if len(output) > 2000 {
			output = output[len(output)-2000:]
		}
		result = append(result, TaskInfo{
			ID:               t.ID,
			Title:            t.Title,
			Description:      t.Description,
			Status:           t.Status,
			RunnerID:         t.RunnerID,
			Goal:             t.Goal,
			Model:            t.Model,
			ReasoningEffort:  t.ReasoningEffort,
			ProjectName:      t.ProjectName,
			DeviceName:       hostname,
			Transport:        t.Transport,
			TransportReason:  t.TransportReason,
			SessionID:        t.SessionID,
			HostKind:         taskHostKind(t),
			ProjectSessionID: t.ProjectSessionID,
			Output:           output,
			ResultText:       t.ResultText,
			Presentation:     taskPresentationListSnapshot(t),
			Failure:          t.Failure,
			CostUSD:          t.CostUSD,
			InputTokens:      t.InputTokens,
			OutputTokens:     t.OutputTokens,
			Turns:            t.Turns,
			PendingFollowUps: append([]PendingFollowUp{},
				t.PendingFollowUps...),
			Source:           t.Source,
			TmuxSession:      t.TmuxSession,
			TmuxSessionID:    t.TmuxSessionID,
			TmuxWindowIndex:  t.TmuxWindowIndex,
			TmuxWindowName:   t.TmuxWindowName,
			TmuxPaneIndex:    t.TmuxPaneIndex,
			TmuxPaneID:       t.TmuxPaneID,
			ExecutionSession: tm.taskExecutionIdentity(t),
			SessionSettings:  cloneClientSessionSettings(t.SessionSettings),
			IsAdopted:        t.IsAdopted,
			CreatedAt:        t.CreatedAt,
			StartedAt:        t.StartedAt,
			FinishedAt:       t.FinishedAt,
			ChainID:          t.ChainID,
			ChainOrder:       t.ChainOrder,
			AutoRetry:        t.AutoRetry,
			AutoRetryCount:   t.AutoRetryCount,
			AutoRetryMax:     t.AutoRetryMax,
			VideoEnabled:     t.VideoEnabled,
			VideoSource:      t.VideoSource,
			VideoClipID:      t.VideoClipID,
			VideoStatus:      t.VideoStatus,
			ProofStatus:      t.ProofStatus,
			CommitSHA:        t.CommitSHA,
			CommitSubject:    t.CommitSubject,
			CommitBranch:     t.CommitBranch,
			DiffShortstat:    t.DiffShortstat,
			FeedbackID:       t.FeedbackID,
			AskFreely:        t.AskFreely,
			Placement:        t.Placement,
		})
	}
	return result
}

// GetTask returns a single task by ID.
func (tm *TaskManager) GetTask(id string) (*Task, bool) {
	if tm == nil {
		return nil, false
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.tasks[id]
	return t, ok && t.DeletedAt == nil
}

// TouchTaskSession records the most recent client surface to view or act on a
// shared Yaver session. This is provenance only and is never authorization.
func (tm *TaskManager) TouchTaskSession(id, surface string) {
	surface = strings.TrimSpace(surface)
	if tm == nil || surface == "" || surface == string(SurfaceUnknown) {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok := tm.tasks[id]
	if !ok || task == nil || task.DeletedAt != nil {
		return
	}
	now := time.Now()
	if task.InitialSurface == "" {
		task.InitialSurface = surface
	}
	if task.SessionStartedAt.IsZero() {
		task.SessionStartedAt = task.CreatedAt
	}
	task.LastSurface = surface
	task.LastActiveAt = now
	tm.persist()
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

func (tm *TaskManager) SetTaskProofState(id, status string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok := tm.tasks[id]
	if !ok || task == nil {
		return
	}
	if strings.TrimSpace(status) != "" {
		task.ProofStatus = strings.TrimSpace(status)
	}
	tm.persist()
}

func (tm *TaskManager) SetTaskCommitEvidence(id, sha, subject, branch, shortstat string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok := tm.tasks[id]
	if !ok || task == nil {
		return
	}
	task.CommitSHA = strings.TrimSpace(sha)
	task.CommitSubject = strings.TrimSpace(subject)
	task.CommitBranch = strings.TrimSpace(branch)
	task.DiffShortstat = strings.TrimSpace(shortstat)
	tm.persist()
}

func (tm *TaskManager) FindTaskByFeedbackID(feedbackID string) (*Task, bool) {
	feedbackID = strings.TrimSpace(feedbackID)
	if feedbackID == "" {
		return nil, false
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for _, task := range tm.tasks {
		if task != nil && strings.TrimSpace(task.FeedbackID) == feedbackID {
			return task, true
		}
	}
	return nil, false
}

func (tm *TaskManager) CompleteTask(id string) error {
	tm.mu.RLock()
	task, ok := tm.tasks[id]
	if !ok {
		tm.mu.RUnlock()
		return fmt.Errorf("task %s not found", id)
	}
	isRunning := task.Status == TaskStatusRunning || task.Status == TaskStatusQueued
	isAdoptedTmux := task.IsAdopted && task.TmuxSession != "" && tm.TmuxMgr != nil
	isTaskOwnedTmux := taskOwnsNamedTmuxSeat(task)
	doneCh := task.doneCh
	tm.mu.RUnlock()

	// Auto-stop running tasks so the user's "mark complete" gesture
	// from mobile doesn't leave the runner eating tokens after the
	// status flips. Mirrors DeleteTask's auto-stop pattern.
	if isRunning {
		var err error
		if isAdoptedTmux {
			err = tm.TmuxMgr.CloseAdoptedTask(id)
		} else if isTaskOwnedTmux {
			tm.closeTaskOwnedTmuxSeat(id)
		} else {
			err = tm.GracefulStopTask(id)
		}
		if err != nil {
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
	if isTaskOwnedTmux {
		tm.closeTaskOwnedTmuxSeat(id)
	} else if !isRunning {
		if isAdoptedTmux {
			if err := tm.TmuxMgr.CloseAdoptedTask(id); err != nil {
				log.Printf("[task %s] Adopted tmux close failed during complete: %v", id, err)
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
			ID:                 id,
			Title:              input.Title,
			Description:        input.Description,
			Status:             TaskStatusQueued,
			Source:             source,
			Model:              model,
			RunnerID:           taskRunner.RunnerID,
			YaverSessionID:     newYaverSessionID(),
			RemoteBoxID:        strings.TrimSpace(tm.DeviceID),
			RunnerName:         taskRunner.Name,
			SessionStartedFrom: "tasks",
			StartedFromSurface: source,
			InitialSurface:     source,
			SessionStartedAt:   now,
			LastSurface:        source,
			LastActiveAt:       now,
			FirstUserMessageAt: &now,
			LastUserMessageAt:  &now,
			runner:             taskRunner,
			CreatedAt:          now,
			outputCh:           make(chan string, 512),
			rawOutputCh:        make(chan []byte, 256),
			eventCh:            make(chan map[string]interface{}, 32),
			doneCh:             make(chan struct{}),
			ChainID:            chainID,
			ChainOrder:         i,
			AutoRetry:          autoRetry,
			AutoRetryMax:       retryMax,
			TaskViewport:       viewport, // set before startProcess so task 0 gets the hint
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
				Presentation: taskPresentationSnapshot(t),
				Failure:      t.Failure,
				CostUSD:      t.CostUSD,
				InputTokens:  t.InputTokens,
				OutputTokens: t.OutputTokens,
				Placement:    t.Placement,
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

	// Send the retry prompt; SHOW a one-line narration of what just happened.
	//
	// This block used to do the exact opposite of what it says. The retry
	// prompt was appended as a turn with Role "user" — so every surface
	// rendered "The previous attempt failed. Here is the error output: ```…2000
	// chars of stack trace…```" inside the human's own chat bubble — while
	// startProcess went on to rebuild its prompt from task.Title and never read
	// it. The error context was displayed and not sent; now it is sent and not
	// displayed.
	task.PromptText = retryPrompt
	task.Turns = append(task.Turns, ConversationTurn{
		Role: "assistant",
		Content: fmt.Sprintf("⟳ The previous attempt failed. Retrying (%d/%d) with the error output.",
			task.AutoRetryCount, task.AutoRetryMax),
		Timestamp: time.Now(),
		Hidden:    true,
	})
	tm.present(task, taskPresentationInput{
		ID: task.ID + "-activity", Kind: "status",
		Text:  fmt.Sprintf("The runner is retrying after a failed attempt (%d/%d).", task.AutoRetryCount, task.AutoRetryMax),
		Phase: "retry", State: "running",
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
