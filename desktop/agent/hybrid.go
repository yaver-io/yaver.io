package main

// hybrid.go — planner/implementer orchestration for cost-efficient
// autonomous coding.
//
// The idea: expensive frontier models (Claude Code / Codex) are
// excellent at turning a fuzzy user goal into a concrete, ordered
// task list. Cheap local models (Qwen-Coder 14B via Ollama + Aider)
// are perfectly capable of executing a well-scoped single-file edit.
// Splitting those phases keeps the dollar spend on the 100:1 compression
// step (planning) and pushes the bulky file-editing work down to a
// free, private, on-device implementer.
//
// Flow
//
//   ┌─────────────┐ plan JSON ┌────────────────────────┐ edits ┌──────────┐
//   │  Planner    │──────────►│  Hybrid orchestrator   │──────►│ workdir  │
//   │ (claude /   │           │  (this file)           │       │ (git)    │
//   │  codex)     │           │                        │       └──────────┘
//   └─────────────┘           │ for each subtask:      │
//                             │   aider --model        │
//                             │     ollama_chat/qwen…  │
//                             └────────────────────────┘
//
// The planner is asked for a single JSON array of subtasks with
// {title, files, prompt} — the contract is deliberately minimal so
// even a compact Qwen could stand in as the planner if the user
// wants fully-local operation.
//
// Nothing here writes to Convex. Task data is conversation-local;
// the caller is responsible for persisting HybridReport if desired.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HybridEvent is one structured update the orchestrator emits as a
// run progresses. The SSE handler streams these to clients so the
// UI doesn't have to block for minutes on /hybrid/run. Events are
// JSON-marshalled before being sent as the data of an SSE message.
type HybridEvent struct {
	// Type is one of: "plan_started", "plan_done", "subtask_started",
	// "subtask_done", "replan_started", "replan_done", "run_done", "error".
	Type      string             `json:"type"`
	At        time.Time          `json:"at"`
	Message   string             `json:"message,omitempty"`
	Index     int                `json:"index,omitempty"`      // 1-based subtask index when applicable
	Total     int                `json:"total,omitempty"`      // current subtask count
	Subtask   *HybridSubtask     `json:"subtask,omitempty"`    // set on subtask_* events
	Result    *HybridStepResult  `json:"result,omitempty"`     // set on subtask_done
	Plan      []HybridSubtask    `json:"plan,omitempty"`       // set on plan_done / replan_done
	Report    *HybridReport      `json:"report,omitempty"`     // set on run_done
	Retry     int                `json:"retry,omitempty"`      // 0-based attempt number on subtask_started retries
}

// HybridProgress is the callback RunHybrid calls as it works. Pass nil
// for a fully synchronous run (old behaviour preserved). Implementations
// should be non-blocking — the SSE handler drops events on a slow
// client rather than stalling the run.
type HybridProgress func(HybridEvent)

// HybridSpec describes a single hybrid run. Zero values fall back to
// sensible defaults for a laptop with Ollama + qwen2.5-coder:14b.
type HybridSpec struct {
	// Planner is a runner ID from builtinRunners that can emit a
	// structured JSON task list. Defaults to "claude". Any tool-using
	// runner works — the planner does not edit files.
	Planner string `json:"planner"`
	// Implementer is the runner used to execute each subtask.
	// Defaults to "aider-ollama".
	Implementer string `json:"implementer"`
	// Model overrides the implementer's LLM backend. For
	// aider-ollama this becomes the --model flag (e.g.
	// "ollama_chat/qwen2.5-coder:14b").
	Model string `json:"model,omitempty"`
	// BaseURL overrides the implementer's LLM endpoint. For
	// aider-ollama this is exported as OLLAMA_API_BASE.
	BaseURL string `json:"baseUrl,omitempty"`
	// WorkDir is the project root. Must exist and be writable.
	WorkDir string `json:"workDir"`
	// Prompt is the user's feature request in plain English.
	Prompt string `json:"prompt"`
	// MaxSubtasks caps how many subtasks the planner is allowed to
	// emit. Defaults to 20 — protects the user from a runaway
	// planner that slices a feature into 200 trivial edits.
	MaxSubtasks int `json:"maxSubtasks,omitempty"`
	// MaxRetries is how many times a failing subtask is re-attempted
	// with a stricter "try again, be careful" reminder before being
	// marked permanently failed. 0 = no retries (fail fast). Default 1.
	MaxRetries int `json:"maxRetries,omitempty"`
	// MaxConsecutiveFailures caps how many subtasks can fail in a
	// row before the orchestrator stops trusting the current plan
	// and asks the planner to replan. 0 = never replan. Default 3.
	// Replan is capped to one attempt per run to prevent infinite loops.
	MaxConsecutiveFailures int `json:"maxConsecutiveFailures,omitempty"`
	// Timeout applies to the whole run. Defaults to 30 min.
	Timeout time.Duration `json:"-"`
}

// HybridSubtask is one unit of implementer work, as returned by the
// planner. `Files` is the list of paths aider should add to its
// editable set so it does not wander outside its scope.
type HybridSubtask struct {
	Title  string   `json:"title"`
	Files  []string `json:"files"`
	Prompt string   `json:"prompt"`
}

// HybridStepResult records the outcome of one implementer invocation.
type HybridStepResult struct {
	Subtask  HybridSubtask `json:"subtask"`
	Status   string        `json:"status"` // "ok" | "error" | "skipped"
	Output   string        `json:"output,omitempty"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"durationMs"`
}

// HybridReport is what a hybrid run returns to the caller.
type HybridReport struct {
	Spec        HybridSpec         `json:"spec"`
	Subtasks    []HybridSubtask    `json:"subtasks"`
	Results     []HybridStepResult `json:"results"`
	PlanOutput  string             `json:"planOutput,omitempty"`
	PlanError   string             `json:"planError,omitempty"`
	// Replanned is true when the orchestrator gave up on the
	// original plan mid-run and asked the planner for a new one.
	Replanned bool `json:"replanned,omitempty"`
	// Retries tallies how many subtask re-attempts happened across
	// the run (across all subtasks).
	Retries     int       `json:"retries,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	OK          bool      `json:"ok"`
	FailedSteps int       `json:"failedSteps"`
}

// applyHybridDefaults fills zero-valued fields with defaults chosen
// for a 24 GB Apple Silicon laptop. Kept small on purpose — defaults
// belong here, not scattered across callers.
func applyHybridDefaults(s *HybridSpec) error {
	if strings.TrimSpace(s.WorkDir) == "" {
		return errors.New("hybrid: workDir is required")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return errors.New("hybrid: prompt is required")
	}
	if s.Planner == "" {
		s.Planner = "claude"
	}
	if s.Implementer == "" {
		s.Implementer = "aider-ollama"
	}
	if s.Implementer == "aider-ollama" {
		if s.Model == "" {
			s.Model = "ollama_chat/qwen2.5-coder:14b"
		}
		if s.BaseURL == "" {
			s.BaseURL = "http://127.0.0.1:11434"
		}
	}
	if s.MaxSubtasks == 0 {
		s.MaxSubtasks = 20
	}
	if s.MaxRetries < 0 {
		s.MaxRetries = 0
	}
	if s.MaxRetries == 0 {
		// Most small-model failures (malformed output, missing
		// terminator) flip on a single retry. Default to 1 so the
		// orchestrator is forgiving without blowing timeouts.
		s.MaxRetries = 1
	}
	if s.MaxConsecutiveFailures == 0 {
		s.MaxConsecutiveFailures = 3
	}
	if s.Timeout == 0 {
		s.Timeout = 30 * time.Minute
	}
	if _, err := os.Stat(s.WorkDir); err != nil {
		return fmt.Errorf("hybrid: workDir %q: %w", s.WorkDir, err)
	}
	return nil
}

// plannerPrompt is the instruction we hand the planner. It is
// deliberately strict about the output contract — the orchestrator
// parses JSON, not prose. When the planner is a tool-using agent
// (Claude/Codex) it still respects the "only emit JSON" rule because
// the stream-json parser already handles surrounding narration.
func plannerPrompt(workDir, userPrompt, implementer, model string, maxSubtasks int) string {
	impl := implementer
	if model != "" {
		impl = impl + " driving " + model
	}
	return fmt.Sprintf(`You are the PLANNER in a TWO-AGENT coding system.
You are the smart one. The IMPLEMENTER is not.

WHO EXECUTES YOUR PLAN
  The implementer is: %s
  It is a small, local, open-weights model with NO reasoning chain,
  NO web access, NO repo-wide context, and a tiny context window
  (roughly 16K tokens). It will faithfully follow instructions but
  it WILL fail if you give it:
    - vague goals ("add validation", "clean up the component")
    - cross-file reasoning ("make sure this matches the schema in X")
    - architectural decisions ("pick the best pattern")
    - anything that requires reading more than 1–2 short files
  Assume the implementer has the IQ of a diligent intern who has
  never seen this repo before. If the instruction is not obvious
  from the file contents alone, the implementer WILL hallucinate.

YOUR JOB
  Convert the user request into AT MOST %d hyper-explicit subtasks.
  Each subtask must be a single, mechanical edit the implementer
  can perform without thinking. All thinking is YOUR job, done now,
  once, before any code is written.

CONTRACT PER SUBTASK
  - "title": <8 words, imperative, e.g. "Add zod schema for Portfolio">
  - "files": EXACT relative paths the implementer will touch.
      Prefer ONE file per subtask. Never more than 3.
      If a file must be created, include it anyway (aider creates it).
  - "prompt": the full instruction. This is what the implementer sees.
      It MUST include:
        (a) the precise change to make, function-by-function or
            block-by-block, as if describing a diff in prose
        (b) the exact identifiers, types, imports, and function
            signatures to use — DO NOT leave naming to the implementer
        (c) any code snippet the implementer should paste verbatim
            (fenced in triple backticks inside the prompt string)
        (d) the acceptance criterion in one sentence ("the file now
            exports a function X with signature Y")
      It MUST NOT include:
        - references to "the other file" or "see elsewhere"
        - design questions
        - ambiguous words ("appropriate", "sensible", "proper")

ORDERING
  Subtasks are executed sequentially. Put schema / type / constant
  files first, then the modules that import them, then wiring. Never
  ask the implementer to touch a file that depends on code a later
  subtask will introduce.

HARD RULES
  - Emit AT MOST %d subtasks. Fewer is better.
  - Do NOT edit any files yourself. Do NOT run shell commands.
  - Do NOT include scaffolding already present in the repo.
  - Output ONLY a single JSON object on the last line of your reply:

{"subtasks":[
  {"title":"...","files":["path/a.ts"],"prompt":"..."},
  ...
]}

Project working directory: %s

USER REQUEST (expand, disambiguate, then decompose):
%s
`, impl, maxSubtasks, maxSubtasks, workDir, userPrompt)
}

// parseHybridPlan extracts the last JSON object from planner stdout
// and decodes it into a subtask list. Planners frequently wrap the
// JSON in narration or a ```json fence; we strip both.
func parseHybridPlan(raw string, max int) ([]HybridSubtask, error) {
	cleaned := strings.ReplaceAll(raw, "```json", "```")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	start := strings.LastIndex(cleaned, "{")
	for start >= 0 {
		candidate := cleaned[start:]
		var wrapper struct {
			Subtasks []HybridSubtask `json:"subtasks"`
		}
		if err := json.Unmarshal([]byte(candidate), &wrapper); err == nil && len(wrapper.Subtasks) > 0 {
			if len(wrapper.Subtasks) > max {
				wrapper.Subtasks = wrapper.Subtasks[:max]
			}
			return wrapper.Subtasks, nil
		}
		start = strings.LastIndex(cleaned[:start], "{")
	}
	return nil, errors.New("planner did not emit a parseable {\"subtasks\":[…]} block")
}

// runPlanner invokes the configured planner runner with plannerPrompt
// and returns its stdout for parseHybridPlan to chew on.
//
// We intentionally bypass the task/tmux pipeline here: the planner's
// job is short (a few hundred tokens of JSON) and we want a synchronous,
// blocking result. For long-running interactive planning the user can
// still fall back to `yaver loop` with their own spec.
func runPlanner(ctx context.Context, spec HybridSpec) (string, error) {
	cfg, ok := builtinRunners[spec.Planner]
	if !ok {
		return "", fmt.Errorf("hybrid: unknown planner %q", spec.Planner)
	}
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return "", fmt.Errorf("hybrid: planner command %q not on PATH: %w", cfg.Command, err)
	}
	prompt := plannerPrompt(spec.WorkDir, spec.Prompt, spec.Implementer, spec.Model, spec.MaxSubtasks)

	// Substitute {prompt} in the runner's args template.
	args := make([]string, 0, len(cfg.Args))
	substituted := false
	for _, a := range cfg.Args {
		if strings.Contains(a, "{prompt}") {
			args = append(args, strings.ReplaceAll(a, "{prompt}", prompt))
			substituted = true
		} else {
			args = append(args, a)
		}
	}
	cmd := exec.CommandContext(ctx, cfg.Command, args...)
	cmd.Dir = spec.WorkDir
	cmd.Stderr = os.Stderr
	if !substituted {
		// Runners that expect stdin (codex with `-`) get the prompt on stdin.
		cmd.Stdin = strings.NewReader(prompt)
	}
	out, err := cmd.Output()
	return string(out), err
}

// runImplementer invokes Aider (or a future implementer) for one
// subtask. Files named by the planner are added to aider's editable
// set via positional args — this is the correct way to scope aider's
// attention in a one-shot run.
func runImplementer(ctx context.Context, spec HybridSpec, st HybridSubtask) HybridStepResult {
	started := time.Now()
	result := HybridStepResult{Subtask: st}

	cfg, ok := builtinRunners[spec.Implementer]
	if !ok {
		result.Status = "error"
		result.Error = fmt.Sprintf("unknown implementer %q", spec.Implementer)
		result.Duration = time.Since(started)
		return result
	}
	if _, err := exec.LookPath(cfg.Command); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("%s not on PATH — run `yaver install aider`", cfg.Command)
		result.Duration = time.Since(started)
		return result
	}

	// Build argv from the runner template, injecting the subtask
	// prompt and the files the planner scoped. For aider the
	// pattern is: <base-args> --model X --message <prompt> <files...>
	argv := make([]string, 0, len(cfg.Args)+8)
	for _, a := range cfg.Args {
		if strings.Contains(a, "{prompt}") {
			argv = append(argv, strings.ReplaceAll(a, "{prompt}", st.Prompt))
		} else {
			argv = append(argv, a)
		}
	}
	model := spec.Model
	if model == "" {
		model = cfg.Model
	}
	if model != "" {
		// Prepend --model so it applies before --message.
		argv = append([]string{"--model", model}, argv...)
	}
	for _, f := range st.Files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		argv = append(argv, f)
	}

	cmd := exec.CommandContext(ctx, cfg.Command, argv...)
	cmd.Dir = spec.WorkDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	base := spec.BaseURL
	if base == "" {
		base = cfg.BaseURL
	}
	if base != "" && strings.HasPrefix(strings.ToLower(model), "ollama") {
		cmd.Env = append(cmd.Env, "OLLAMA_API_BASE="+base)
	}

	if err := cmd.Run(); err != nil {
		result.Status = "error"
		result.Output = stdout.String()
		result.Error = strings.TrimSpace(err.Error() + "\n" + stderr.String())
		result.Duration = time.Since(started)
		return result
	}
	result.Status = "ok"
	result.Output = stdout.String()
	result.Duration = time.Since(started)
	return result
}

// RunHybrid is the blocking entry point. Equivalent to
// RunHybridWithProgress with a nil callback — preserved for existing
// CLI / MCP callers that just want the final report.
func RunHybrid(ctx context.Context, spec HybridSpec) (*HybridReport, error) {
	return RunHybridWithProgress(ctx, spec, nil)
}

// RunHybridWithProgress plans, then implements each subtask. On bad
// output it retries up to spec.MaxRetries with a stricter reminder.
// If spec.MaxConsecutiveFailures subtasks fail in a row it asks the
// planner for a replacement plan (once per run, to bound the blast
// radius of a misbehaving planner). progress is invoked with every
// structured event for SSE clients; pass nil to run silently.
func RunHybridWithProgress(ctx context.Context, spec HybridSpec, progress HybridProgress) (*HybridReport, error) {
	if err := applyHybridDefaults(&spec); err != nil {
		return nil, err
	}
	emit := func(ev HybridEvent) {
		if progress == nil {
			return
		}
		ev.At = time.Now()
		progress(ev)
	}
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	rep := &HybridReport{Spec: spec, StartedAt: time.Now()}

	emit(HybridEvent{Type: "plan_started", Message: "planner reading the request"})
	planOut, err := runPlanner(runCtx, spec)
	rep.PlanOutput = planOut
	if err != nil {
		rep.PlanError = err.Error()
		rep.FinishedAt = time.Now()
		emit(HybridEvent{Type: "error", Message: "planner failed: " + err.Error(), Report: rep})
		return rep, fmt.Errorf("hybrid: planner failed: %w", err)
	}
	subtasks, perr := parseHybridPlan(planOut, spec.MaxSubtasks)
	if perr != nil {
		rep.PlanError = perr.Error()
		rep.FinishedAt = time.Now()
		emit(HybridEvent{Type: "error", Message: perr.Error(), Report: rep})
		return rep, fmt.Errorf("hybrid: %w", perr)
	}
	rep.Subtasks = subtasks
	emit(HybridEvent{Type: "plan_done", Total: len(subtasks), Plan: subtasks})

	// Use an index we can rewrite mid-loop so the replan escape hatch
	// can substitute the tail of the plan without affecting already
	// finished steps.
	consecFails := 0
	replanned := false
	i := 0
	for i < len(rep.Subtasks) {
		st := rep.Subtasks[i]
		if runCtx.Err() != nil {
			rep.Results = append(rep.Results, HybridStepResult{
				Subtask: st,
				Status:  "skipped",
				Error:   runCtx.Err().Error(),
			})
			rep.FailedSteps++
			i++
			continue
		}

		// Try the step up to MaxRetries+1 times; only the first attempt
		// uses the planner's original prompt, subsequent attempts
		// prepend a corrective reminder.
		var r HybridStepResult
		for attempt := 0; attempt <= spec.MaxRetries; attempt++ {
			emit(HybridEvent{
				Type: "subtask_started", Index: i + 1, Total: len(rep.Subtasks),
				Subtask: &st, Retry: attempt,
			})
			st2 := st
			if attempt > 0 {
				st2.Prompt = retryReminder(attempt) + "\n\n" + st.Prompt
				rep.Retries++
			}
			r = runImplementer(runCtx, spec, st2)
			if r.Status == "ok" {
				break
			}
			if runCtx.Err() != nil {
				break
			}
		}
		rep.Results = append(rep.Results, r)
		emit(HybridEvent{
			Type: "subtask_done", Index: i + 1, Total: len(rep.Subtasks),
			Subtask: &st, Result: &r,
		})

		if r.Status != "ok" {
			rep.FailedSteps++
			consecFails++
		} else {
			consecFails = 0
		}

		// Replan escape hatch: if N in a row fail and we haven't
		// already replanned on this run, ask the planner to look at
		// the failure context and produce a fresh plan for the
		// remaining work. Bounded to one replan per run.
		if consecFails >= spec.MaxConsecutiveFailures && !replanned && spec.MaxConsecutiveFailures > 0 {
			replanned = true
			rep.Replanned = true
			emit(HybridEvent{Type: "replan_started", Message: fmt.Sprintf("%d subtasks failed in a row; asking planner to replan", consecFails)})
			newPlan, rerr := replan(runCtx, spec, rep.Results)
			if rerr == nil && len(newPlan) > 0 {
				// Keep everything we've already done; replace the tail.
				rep.Subtasks = append(rep.Subtasks[:i+1], newPlan...)
				emit(HybridEvent{Type: "replan_done", Total: len(rep.Subtasks), Plan: newPlan})
				consecFails = 0
			} else {
				// If replan itself fails, log and keep marching through
				// whatever subtasks remain — partial progress beats
				// throwing away completed work.
				msg := "replan failed"
				if rerr != nil {
					msg = "replan failed: " + rerr.Error()
				}
				emit(HybridEvent{Type: "error", Message: msg})
			}
		}

		i++
	}
	rep.OK = rep.FailedSteps == 0
	rep.FinishedAt = time.Now()
	emit(HybridEvent{Type: "run_done", Report: rep})
	return rep, nil
}

// retryReminder builds the stricter-than-before instruction prepended
// to a subtask prompt when its first attempt produced non-working
// output. Kept short — small models glaze over long preambles.
func retryReminder(attempt int) string {
	return fmt.Sprintf(`IMPORTANT — ATTEMPT %d.
Your previous attempt produced output that did not achieve the goal.
Before writing any code, re-read the instruction below in full.
Output ONLY the final file contents as aider would. No markdown
fences. No prose. No preamble. If the instruction says "exactly
these lines", it means EXACTLY those lines with no additions.`, attempt+1)
}

// replan asks the planner to produce a replacement subtask list given
// the failures so far. The planner prompt reminds it which files have
// been touched and what's already working, so it doesn't regenerate
// identical subtasks that will just fail again.
func replan(ctx context.Context, spec HybridSpec, results []HybridStepResult) ([]HybridSubtask, error) {
	// Summarise failures compactly — the planner doesn't need full
	// aider stdout, just what was attempted and how it went.
	var b strings.Builder
	b.WriteString("PREVIOUS ATTEMPT FAILED. Results so far:\n")
	for i, r := range results {
		b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, r.Status, r.Subtask.Title))
		if r.Status != "ok" && r.Error != "" {
			line := r.Error
			if nl := strings.IndexByte(line, '\n'); nl > 0 {
				line = line[:nl]
			}
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			b.WriteString("   error: " + line + "\n")
		}
	}
	b.WriteString("\nRewrite the remaining plan. Change approach. Assume previous prompts were too ambiguous for the implementer. Make new subtasks even more explicit.\n\nOriginal user request:\n" + spec.Prompt)

	replanSpec := spec
	replanSpec.Prompt = b.String()
	planOut, err := runPlanner(ctx, replanSpec)
	if err != nil {
		return nil, err
	}
	return parseHybridPlan(planOut, spec.MaxSubtasks)
}
