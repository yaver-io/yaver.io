package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HandoffSpec describes a "pass session to yaver" request.
//
// Source identifies the AI session being handed off:
//   - SourceTaskID: a Yaver-managed task ID
//   - SourceSessionFile: a path to a Claude Code / Aider / Codex session file
//   - SourceBundle: an already-exported TransferBundle (used by remote handoff)
//
// Engine selects how Yaver continues the work:
//   - "claude" / "" — runner=claude-code, full frontier model
//   - "hybrid"      — planner+local implementer (cheap)
//   - "runner"      — an arbitrary single runner (Runner field)
//
// Target is empty for local handoff, otherwise a device hint (id/hostname).
type HandoffSpec struct {
	SourceTaskID      string          `json:"sourceTaskId,omitempty"`
	SourceSessionFile string          `json:"sourceSessionFile,omitempty"`
	SourceBundle      *TransferBundle `json:"sourceBundle,omitempty"`

	Target string `json:"target,omitempty"` // remote device hint; empty = local

	Engine      string `json:"engine,omitempty"` // claude | hybrid | runner
	Runner      string `json:"runner,omitempty"` // when engine=runner
	WorkDir     string `json:"workDir,omitempty"`
	MaxKicks    int    `json:"maxKicks,omitempty"`
	DeadlineSec int    `json:"deadlineSec,omitempty"`

	// StopSource: when true, attempt to stop the source Yaver task before
	// kicking the new loop (only meaningful when SourceTaskID is set).
	StopSource bool `json:"stopSource,omitempty"`

	// ExtraPrompt, when non-empty, is appended to the resume prompt.
	ExtraPrompt string `json:"extraPrompt,omitempty"`

	// SkipInitialKick, when true, suppresses the async first kick. Used
	// only by tests so they can inspect persisted loop state without
	// racing a goroutine that tries to invoke an AI runner binary.
	SkipInitialKick bool `json:"-"`

	// Autodev, when true, expands the resume prompt: finish the existing
	// remained items AND mine the session for new improvements/tests/ideas
	// to also implement. This is the `yaver handoff autodev` mode — Yaver
	// becomes a proactive successor, not just a continuation.
	Autodev bool `json:"autodev,omitempty"`

	// CallerPID is the PID of the AI agent that called us (Claude Code,
	// Codex, Aider, …). When > 0, RunHandoff schedules SIGTERM/SIGKILL
	// after the response is sent, turning the cooperative "exitNow"
	// signal into an actual takeover. Set explicitly via the MCP
	// `caller_pid` arg, or auto-detected from stdio MCP parent / HTTP
	// loopback peer port (see handoff_pid.go).
	CallerPID int `json:"callerPid,omitempty"`

	// Hours / Load mirror `yaver autodev`'s knobs:
	//   Hours:  "8" / "inf" — wall-clock cap for the resumed loop.
	//   Load:   "lite" | "low" | "burst" — "lite" respects the dev's
	//           Claude/Codex 5h windows and stretches kicks to one
	//           every ~5 minutes; "burst" runs as fast as the runner
	//           allows. Empty defaults to "lite" so the handoff can't
	//           steal the dev's interactive AI session by accident.
	Hours string `json:"hours,omitempty"`
	Load  string `json:"load,omitempty"`

	// --- autodev parity knobs (mirror `yaver autodev` flags) ----------------

	// Prompt is the FOCUS prompt for the loop ("focus on the purchase
	// flow"). Distinct from ExtraPrompt: Prompt replaces the loop's
	// inline prompt; ExtraPrompt is appended after the auto-generated
	// resume context. Set Prompt when you want full control over what
	// the runner sees.
	Prompt string `json:"prompt,omitempty"`

	// LoopTarget overrides the auto-detected loop target
	// (web|ios-sim|android-emu). Distinct from the top-level Target
	// field which is the remote-handoff device hint. Empty = auto.
	LoopTarget string `json:"loopTarget,omitempty"`

	// Branch / AutoBranch follow autodev's --branch / --auto-branch.
	// AutoBranch=true creates "autodev/<loop>-<YYYYMMDD>" off main so
	// overnight runs can be PR-reviewed before merging.
	Branch     string `json:"branch,omitempty"`
	AutoBranch bool   `json:"autoBranch,omitempty"`

	// Deploy maps to LoopSpec.Ship.Deploy via normalizeDeploy.
	// Default is "both" — handoff loops ship to every configured
	// platform unless the user explicitly turns deploy off.
	//   "" / "all" / "yes" / "true" / "1"  → "both" (default)
	//   "no" / "false" / "0" / "none"      → never deploy
	//   "testflight" / "playstore"         → that platform only
	//   "web"                              → web/cloudflare only
	Deploy string `json:"deploy,omitempty"`

	// Notify sends a mobile notification when the loop ends.
	Notify bool `json:"notify,omitempty"`

	// NoAutotest skips the interleaved autotest pass that normally
	// runs after each successful develop kick. Default false (= keep
	// regression checking on, matching `yaver autodev` defaults).
	NoAutotest bool `json:"noAutotest,omitempty"`

	// AutoIdeas caps the number of times the loop is allowed to auto-
	// generate a fresh batch of ideas when the checklist runs dry.
	// Default 999 (effectively unlimited, like autodev). Set 0 to
	// quit the moment the queue is empty.
	AutoIdeas int `json:"autoIdeas,omitempty"`

	// RemainedFile is an optional path to a `remained.md` checklist
	// the loop pulls work items from (one per kick). Relative paths
	// are resolved against WorkDir.
	RemainedFile string `json:"remainedFile,omitempty"`

	// Harden — autodev hardening preset (security|memory|perf|
	// quality|all). When set, the autodev plan that wraps the
	// resumed session uses the curated focus prompt for that area
	// (layered on top of Prompt if both are given).
	Harden string `json:"harden,omitempty"`

	// Model — Claude model alias (sonnet|opus|haiku) or full id.
	// Threaded through YAVER_CLAUDE_MODEL so spawnClaudeCode adds
	// --model <id>. Sonnet is the cheap-default (~5x slower bucket
	// burn than Opus on Max plans).
	Model string `json:"model,omitempty"`

	// Planner / Implementer — hybrid layering (agent[:model]).
	// Either one set forces engine=hybrid. Same shape as autodev's
	// --planner / --implementer (e.g. "claude:opus" + "codex").
	Planner     string `json:"planner,omitempty"`
	Implementer string `json:"implementer,omitempty"`
}

// HandoffResult is what the orchestrator returns to the caller (CLI/MCP/HTTP).
type HandoffResult struct {
	OK            bool     `json:"ok"`
	LocalTaskID   string   `json:"localTaskId,omitempty"`
	LoopName      string   `json:"loopName,omitempty"`
	Engine        string   `json:"engine"`
	Runner        string   `json:"runner"`
	SentinelFile  string   `json:"sentinelFile"`
	RemoteDevice  string   `json:"remoteDevice,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	Message       string   `json:"message"`
	ExitNow       bool     `json:"exitNow"` // signal to source agent to terminate
}

// HandoffSentinel is written to ~/.yaver/handoff/<loopName>.json so the
// source AI agent (Claude Code, etc.) — which may have called this via
// MCP — can poll/watch for it and exit cleanly without us force-killing
// an external CLI.
type HandoffSentinel struct {
	WrittenAt   string `json:"writtenAt"`
	LoopName    string `json:"loopName"`
	LocalTaskID string `json:"localTaskId,omitempty"`
	Engine      string `json:"engine"`
	Runner      string `json:"runner"`
	Message     string `json:"message"`
}

// resolveHandoffRunner maps an Engine choice to a concrete runner ID.
func resolveHandoffRunner(engine, runner string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "claude", "claude-code":
		return "claude-code", nil
	case "hybrid":
		return "hybrid", nil
	case "runner":
		if runner == "" {
			return "", fmt.Errorf("engine=runner requires --runner (e.g. aider, codex, ollama:qwen2.5-coder:14b)")
		}
		return runner, nil
	default:
		// Treat unknown engine as a literal runner id (forward-compat).
		return engine, nil
	}
}

// RunHandoff performs the local half of a handoff. For remote handoff the
// CLI/HTTP caller proxies the bundle to the target's /session/handoff
// endpoint, which then invokes RunHandoff there.
//
// Steps:
//  1. Resolve source → TransferBundle (export, read file, or use spec.SourceBundle)
//  2. Import bundle into local TaskManager
//  3. (Optional) StopTask on source Yaver task
//  4. Build a develop-mode LoopSpec keyed to the chosen engine/runner
//  5. Register the loop and persist its initial PromptInline (resume prompt)
//  6. Write a sentinel file the source agent can poll
//  7. Best-effort: kick the loop once so work starts immediately
func RunHandoff(s *HTTPServer, spec HandoffSpec) (*HandoffResult, error) {
	tm := s.taskMgr
	if tm == nil {
		return nil, fmt.Errorf("task manager unavailable")
	}

	runnerID, err := resolveHandoffRunner(spec.Engine, spec.Runner)
	if err != nil {
		return nil, err
	}

	// 1. Source → bundle ------------------------------------------------------
	var bundle *TransferBundle
	switch {
	case spec.SourceBundle != nil:
		bundle = spec.SourceBundle
	case spec.SourceTaskID != "":
		b, err := ExportSession(tm, spec.SourceTaskID, ExportOptions{IncludeWorkspace: false})
		if err != nil {
			return nil, fmt.Errorf("export source task: %w", err)
		}
		bundle = b
	case spec.SourceSessionFile != "":
		data, err := os.ReadFile(spec.SourceSessionFile)
		if err != nil {
			return nil, fmt.Errorf("read source session file: %w", err)
		}
		// Two accepted formats: a full TransferBundle JSON, or a raw Claude
		// session.jsonl which we wrap into a minimal bundle.
		var b TransferBundle
		if err := json.Unmarshal(data, &b); err != nil || b.Version == 0 {
			b = TransferBundle{
				Version:    1,
				ExportedAt: time.Now().UTC().Format(time.RFC3339),
				AgentType:  "claude",
				Task: TransferTask{
					Title:    "Handoff: " + filepath.Base(spec.SourceSessionFile),
					RunnerID: "claude",
					WorkDir:  spec.WorkDir,
				},
				AgentFiles: map[string]string{
					"claude/session.jsonl": encodeBase64(data),
				},
			}
		}
		bundle = &b
	default:
		// No source: still allow handoff (just spin up an autodev loop in
		// the workdir using an empty resume prompt). Caller usually wants
		// this when "resume" semantics aren't important.
		bundle = &TransferBundle{
			Version:    1,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			AgentType:  "unknown",
			Task: TransferTask{
				Title:    "Handoff: ad-hoc",
				RunnerID: runnerID,
				WorkDir:  spec.WorkDir,
			},
		}
	}

	// 2. Import ---------------------------------------------------------------
	importedTaskID, warnings, err := ImportSession(tm, bundle, ImportOptions{
		WorkDir: spec.WorkDir,
	})
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}

	// 3. Stop source if asked ------------------------------------------------
	if spec.StopSource && spec.SourceTaskID != "" {
		if err := tm.StopTask(spec.SourceTaskID); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop source task: %v", err))
		}
	}

	// 4. Build LoopSpec ------------------------------------------------------
	loopName := fmt.Sprintf("handoff-%s", time.Now().UTC().Format("20060102-150405"))

	// Schedule + kick caps mirror `yaver autodev`'s --load behavior:
	// "lite"/"low" stretches kicks to ~5min and caps the day at 20;
	// "burst" tightens to ~30s and lifts the cap to 200. Both honor
	// an explicit MaxKicks if the caller pinned one.
	tickEvery := "30s"
	maxKicks := spec.MaxKicks
	respectLimits := false
	switch strings.ToLower(spec.Load) {
	case "", "lite", "low":
		tickEvery = "5m"
		respectLimits = true
		if maxKicks <= 0 {
			maxKicks = 20
		}
	case "burst", "high":
		tickEvery = "30s"
		if maxKicks <= 0 {
			maxKicks = 200
		}
	default:
		if maxKicks <= 0 {
			maxKicks = 20
		}
	}

	loopWorkDir := spec.WorkDir
	if loopWorkDir == "" {
		loopWorkDir, _ = os.Getwd()
	}

	// Prompt: if the caller supplied an explicit Prompt, use it
	// verbatim and treat ExtraPrompt (if any) as additional context
	// pinned at the bottom. Otherwise build the auto-resume prompt
	// (autodev block + todos + ExtraPrompt as today).
	var prompt string
	if spec.Prompt != "" {
		prompt = spec.Prompt
		if spec.ExtraPrompt != "" {
			prompt += "\n\nAdditional context:\n" + spec.ExtraPrompt
		}
		if spec.Autodev {
			prompt += autodevPromptBlock()
		}
	} else {
		prompt = buildHandoffPrompt(bundle, spec.ExtraPrompt, spec.Autodev, s)
	}
	if dir := operatingDirectives(spec); dir != "" {
		prompt += dir
	}

	// Autodev parity: thread Harden / Model / Planner / Implementer
	// from the handoff spec into the same env vars autodev_cmd uses,
	// so the resumed loop honours every slicing/cost knob the user
	// would have on the local autodev CLI.
	if hp := autodevHardenPrompt(spec.Harden); hp != "" {
		prompt = hp + "\n\n" + prompt
	}
	if strings.TrimSpace(spec.Model) != "" {
		os.Setenv("YAVER_CLAUDE_MODEL", spec.Model)
	}
	if strings.TrimSpace(spec.Planner) != "" || strings.TrimSpace(spec.Implementer) != "" {
		runnerID = "hybrid"
		if spec.Planner != "" {
			os.Setenv("YAVER_HYBRID_PLANNER", spec.Planner)
		}
		if spec.Implementer != "" {
			os.Setenv("YAVER_HYBRID_IMPLEMENTER", spec.Implementer)
		}
	}

	target := spec.LoopTarget
	if target == "" {
		target = detectAutodevTarget(loopWorkDir)
	}
	branch := spec.Branch
	if branch == "" {
		branch = "main"
	}
	if spec.AutoBranch {
		branch = fmt.Sprintf("autodev/%s-%s", loopName, time.Now().UTC().Format("20060102"))
	}
	deploy := normalizeDeploy(spec.Deploy)

	respectVal := respectLimits
	lspec := LoopSpec{
		Name:   loopName,
		Mode:   LoopModeDevelop,
		Target: target,
		Schedule: LoopSchedule{
			Every:         tickEvery,
			MaxIterations: maxKicks,
		},
		Think: LoopThink{
			Runner:               runnerID,
			MaxKicksPerRun:       maxKicks,
			PromptInline:         prompt,
			RespectSessionLimits: &respectVal,
		},
		Ship: LoopShip{Branch: branch, Deploy: deploy},
	}
	// --hours becomes the loop's per-kick wall-clock cap so a runaway
	// prompt can't burn the entire AI session window. "inf" / "" =
	// unlimited.
	if h := strings.TrimSpace(spec.Hours); h != "" && h != "inf" && h != "infinite" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			lspec.Schedule.Timeout = fmt.Sprintf("%dh", n)
		}
	}
	applyLoopDefaults(&lspec)
	if err := validateLoopSpec(&lspec); err != nil {
		return nil, fmt.Errorf("invalid loop spec: %w", err)
	}

	// 5. Persist loop --------------------------------------------------------
	loops, err := loadLoops()
	if err != nil {
		return nil, fmt.Errorf("load loops: %w", err)
	}
	state := &LoopState{
		ID:           uuid.New().String(),
		Spec:         lspec,
		Status:       LoopStatusIdle,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		WorkDir:      loopWorkDir,
		PromptInline: prompt,
	}
	loops[loopName] = state
	if err := saveLoops(loops); err != nil {
		return nil, fmt.Errorf("save loop: %w", err)
	}

	// 6. Sentinel ------------------------------------------------------------
	sentinelPath, sentinelErr := writeHandoffSentinel(loopName, importedTaskID, runnerID, prompt)
	if sentinelErr != nil {
		warnings = append(warnings, fmt.Sprintf("write sentinel: %v", sentinelErr))
	}

	// 7a. Schedule cooperative termination of the source AI agent. The
	// goroutine waits ~5s before SIGTERM so the response (with sentinel
	// info) reaches the agent first. If the agent is gone or PID=0,
	// this is a no-op.
	if spec.CallerPID > 0 {
		scheduleCallerTermination(spec.CallerPID, 5, 10)
	}

	// 7b. Kick once (best-effort, async so the HTTP/CLI caller returns fast) -
	if !spec.SkipInitialKick {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, _, kerr := kickLoopOnce(ctx, loopName); kerr != nil {
				log.Printf("[handoff] initial kick failed: %v", kerr)
			}
		}()
	}

	log.Printf("[handoff] task=%s loop=%s engine=%s runner=%s sentinel=%s",
		importedTaskID, loopName, spec.Engine, runnerID, sentinelPath)

	return &HandoffResult{
		OK:           true,
		LocalTaskID:  importedTaskID,
		LoopName:     loopName,
		Engine:       firstNonEmpty(spec.Engine, "claude"),
		Runner:       runnerID,
		SentinelFile: sentinelPath,
		Warnings:     warnings,
		Message:      fmt.Sprintf("Yaver has taken over. Loop %q (%s) is running. Source agent should exit.", loopName, runnerID),
		ExitNow:      true,
	}, nil
}

// buildHandoffPrompt synthesises the resume prompt fed into the new loop.
// Sources of "what's left":
//   - The TransferBundle's last assistant turn (if any) — for context
//   - TodoListManager pending items — structured, authoritative
//   - Caller-supplied ExtraPrompt — overrides priority
func buildHandoffPrompt(bundle *TransferBundle, extra string, autodev bool, s *HTTPServer) string {
	var sb strings.Builder
	sb.WriteString("You are resuming a session that was handed off to Yaver. ")
	sb.WriteString("Continue the in-progress work to completion. ")
	sb.WriteString("Do not re-introduce yourself; pick up where the previous agent stopped.\n\n")

	if bundle.Task.Title != "" {
		sb.WriteString("Original task: " + bundle.Task.Title + "\n")
	}
	if bundle.AgentType != "" && bundle.AgentType != "unknown" {
		sb.WriteString("Previous agent: " + bundle.AgentType + "\n")
	}
	if n := len(bundle.Task.Turns); n > 0 {
		sb.WriteString(fmt.Sprintf("Conversation so far: %d turns (imported into Yaver task store).\n", n))
	}
	sb.WriteString("\n")

	if s != nil && s.todolistMgr != nil {
		pending := s.todolistMgr.PendingItems()
		if len(pending) > 0 {
			sb.WriteString("Uncompleted todos to address:\n")
			for i, item := range pending {
				if i >= 20 {
					sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(pending)-20))
					break
				}
				desc := strings.ReplaceAll(item.Description, "\n", " ")
				if len(desc) > 200 {
					desc = desc[:200] + "..."
				}
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, desc))
			}
			sb.WriteString("\n")
		}
	}

	if extra != "" {
		sb.WriteString("Additional instructions from caller:\n")
		sb.WriteString(extra)
		sb.WriteString("\n")
	}

	if autodev {
		sb.WriteString(autodevPromptBlock())
	}

	sb.WriteString("\nWork iteratively. After each meaningful change, commit and continue with the next item.")
	return sb.String()
}

// autodevPromptBlock returns the proactive-mode instructions appended
// to the resume prompt when Autodev=true. Extracted so callers that
// supply an explicit Prompt can still opt into autodev semantics.
func autodevPromptBlock() string {
	return `
AUTODEV MODE — be proactive, not just reactive:

1. First, finish every uncompleted item the previous agent left open
   (todos above, half-applied diffs, missing implementations).
2. Then mine the imported session for follow-up work the previous agent
   *discussed but never did*: TODO comments left in code, "we should
   also …" lines in the conversation, missing tests for new code paths,
   obvious next-iteration improvements.
3. For every change, add or update tests covering the behavior. Do not
   ship a feature without tests; do not ship a fix without a regression
   test.
4. Research the market around the session's subject. Call Yaver's
   'web_search' MCP tool (DuckDuckGo by default; Google or Bing if
   the host has API keys configured) to find competing products, read
   their docs / changelogs / pricing / user reviews, and identify gaps
   the market does not yet serve well.
   Bias toward smart differentiation: don't copy the leader, find the
   angle they ignore. Capture findings briefly in a commit message or
   a short note alongside the change so the next iteration can build
   on the research.
5. Propose 1-3 new improvements that fit the spirit of the session,
   are informed by the market research, and meaningfully differentiate
   our product. If they are small and safe, implement them. Commit
   each as a separate change so they can be reviewed independently.
6. Stop when there is genuinely nothing useful left to add — do not
   invent unrelated work.

`
}

// operatingDirectives renders the autodev-style flags (notify,
// no-autotest, auto-ideas, remained-file) that don't have a native
// LoopSpec home into a short prompt block the runner can act on.
// Returns "" when none of the relevant fields are set so the prompt
// stays clean for the simple cases.
func operatingDirectives(spec HandoffSpec) string {
	var lines []string
	if spec.NoAutotest {
		lines = append(lines, "- Do NOT run the autotest regression pass after each kick (--no-autotest equivalent).")
	}
	if spec.AutoIdeas > 0 && spec.AutoIdeas != 999 {
		lines = append(lines,
			fmt.Sprintf("- When the explicit checklist runs out, you may auto-generate up to %d fresh batches of ideas before stopping.", spec.AutoIdeas))
	}
	// AutoIdeas == 0 is treated as "use default (unlimited)" rather
	// than "stop on empty" — the int zero-value would otherwise leak
	// stop-on-empty semantics into every spec where the field is
	// just unspecified. Users wanting stop-on-empty pass a literal
	// -1 below, which we translate into the explicit directive.
	if spec.AutoIdeas < 0 {
		lines = append(lines, "- Stop the moment the explicit checklist is empty. Do not auto-generate new ideas.")
	}
	if spec.RemainedFile != "" {
		lines = append(lines,
			fmt.Sprintf("- Pull the next work item from %s. After finishing it, mark it complete in that file and commit.", spec.RemainedFile))
	}
	if spec.Notify {
		lines = append(lines, "- When the run ends, send a mobile notification via Yaver so the dev knows.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nOPERATING DIRECTIVES (handoff-specific):\n" + strings.Join(lines, "\n") + "\n"
}

// normalizeDeploy turns the user-facing --deploy value (which accepts
// truthy/falsy aliases as well as platform names) into one of the four
// LoopSpec.Ship.Deploy values the loop runner understands. Default is
// "both" — handoff loops ship to every configured platform unless the
// user explicitly disables it.
//
//	""          → "both"   (default: deploy everywhere)
//	"all"       → "both"
//	"yes/true/1/on/auto" → "both"
//	"no/false/0/off/none" → "none"
//	"testflight" / "playstore" / "both" / "web" → passed through
//	anything else → "both"  (forward-compat: unknown means "do it")
func normalizeDeploy(in string) string {
	v := strings.ToLower(strings.TrimSpace(in))
	switch v {
	case "", "all", "yes", "true", "1", "on", "auto":
		return "both"
	case "no", "false", "0", "off", "none", "skip", "disable", "disabled":
		return "none"
	case "testflight", "playstore", "both", "web":
		return v
	default:
		return "both"
	}
}

// detectAutodevTarget mirrors autodev_cmd.go's heuristic: ios-sim if a
// mobile/ios dir exists at the workdir, otherwise web. The target is only
// used by the loop runner for things like build/playtest defaults; for a
// pure handoff loop it rarely matters but a value is required by validate.
func detectAutodevTarget(wd string) string {
	if fileExists(filepath.Join(wd, "mobile", "ios")) {
		return "ios-sim"
	}
	return "web"
}

// writeHandoffSentinel writes the sentinel JSON the source agent can watch.
func writeHandoffSentinel(loopName, taskID, runner, message string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	sdir := filepath.Join(dir, "handoff")
	if err := os.MkdirAll(sdir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(sdir, loopName+".json")
	sentinel := HandoffSentinel{
		WrittenAt:   time.Now().UTC().Format(time.RFC3339),
		LoopName:    loopName,
		LocalTaskID: taskID,
		Runner:      runner,
		Message:     "Yaver has taken over this session. Source agent should exit.",
	}
	data, _ := json.MarshalIndent(sentinel, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	// Also maintain a stable "latest" pointer for poll-based watchers.
	_ = os.WriteFile(filepath.Join(sdir, "latest.json"), data, 0600)
	return path, nil
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
