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
	StartedAt   time.Time          `json:"startedAt"`
	FinishedAt  time.Time          `json:"finishedAt"`
	OK          bool               `json:"ok"`
	FailedSteps int                `json:"failedSteps"`
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

// RunHybrid is the entry point called by the CLI and HTTP layers. It
// plans, then implements each subtask sequentially. On the first hard
// planner failure it returns early; subtask failures are recorded but
// do not stop the loop (the caller decides what to do with partial
// results).
func RunHybrid(ctx context.Context, spec HybridSpec) (*HybridReport, error) {
	if err := applyHybridDefaults(&spec); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	rep := &HybridReport{Spec: spec, StartedAt: time.Now()}

	planOut, err := runPlanner(runCtx, spec)
	rep.PlanOutput = planOut
	if err != nil {
		rep.PlanError = err.Error()
		rep.FinishedAt = time.Now()
		return rep, fmt.Errorf("hybrid: planner failed: %w", err)
	}
	subtasks, perr := parseHybridPlan(planOut, spec.MaxSubtasks)
	if perr != nil {
		rep.PlanError = perr.Error()
		rep.FinishedAt = time.Now()
		return rep, fmt.Errorf("hybrid: %w", perr)
	}
	rep.Subtasks = subtasks

	for _, st := range subtasks {
		if runCtx.Err() != nil {
			rep.Results = append(rep.Results, HybridStepResult{
				Subtask: st,
				Status:  "skipped",
				Error:   runCtx.Err().Error(),
			})
			rep.FailedSteps++
			continue
		}
		r := runImplementer(runCtx, spec, st)
		rep.Results = append(rep.Results, r)
		if r.Status != "ok" {
			rep.FailedSteps++
		}
	}
	rep.OK = rep.FailedSteps == 0
	rep.FinishedAt = time.Now()
	return rep, nil
}
