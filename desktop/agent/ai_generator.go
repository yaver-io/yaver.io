package main

// ai_generator.go — runner-agnostic "ask the AI for text" helper.
// autoinit / autoideas / autodev refill / autodev_harden_prompt
// drivers all need to spawn an AI runner with a prompt and read
// back its assistant text. Without this helper they'd each
// hardcode a spawn and skip opencode / codex.
//
// Resolution order matches user expectation:
//   1. Caller's explicit --engine / --runner picks first
//   2. OpenCode, the product default, when no runner is requested
//   3. Codex only as an installed fallback
//   4. Surface a clear error listing missing CLIs
//
// All runners are wrapped in the same stdin-prompt + stream-json
// (or plain --print) shape so the chat-event publisher sees their
// output regardless of which one ran.
//
// CHAT-ONLY PHASE 3: the claude runner was removed — see
// docs/CHAT_ONLY.md. Codex removal is a separate step.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"time"
)

// AIGeneratorSpec is what the caller hands us. Engine / Runner are
// optional knobs that bias which CLI gets picked.
type AIGeneratorSpec struct {
	// Engine is the high-level autodev engine selector ("hybrid",
	// "" = default). When set to "hybrid" we use the planner CLI
	// since the cheap implementer can't handle generation tasks well.
	Engine string

	// Runner is an explicit runner ID override (e.g. "codex",
	// "ollama:qwen2.5-coder:32b"). Takes precedence over Engine.
	Runner string

	// Model and Mode are separate onboarding choices. Model is the runner's
	// canonical model id; Mode is currently meaningful for OpenCode agents.
	Model string
	Mode  string

	// WorkDir is the project root the runner sees. Required.
	WorkDir string

	// Prompt is fed via stdin. Required.
	Prompt string

	// Timeout caps the whole run (default 5 min).
	Timeout time.Duration
}

// RunAIGenerator picks an AI CLI to run, feeds it the prompt, and
// returns the assistant text. Mirrors stdout to os.Stderr so the
// run is visible in the autodev stream / log.
func RunAIGenerator(spec AIGeneratorSpec) (string, error) {
	if strings.TrimSpace(spec.Prompt) == "" {
		return "", fmt.Errorf("ai-generator: empty prompt")
	}
	if spec.WorkDir == "" {
		return "", fmt.Errorf("ai-generator: workDir required")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = 5 * time.Minute
	}
	cli := pickAIGeneratorCLI(spec)
	if cli == "" {
		return "", fmt.Errorf("ai-generator: no AI CLI on PATH (looked for claude, codex, opencode, aider, ollama)")
	}

	fmt.Fprintf(os.Stderr, "[ai-gen] using %s\n", cli)
	ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
	defer cancel()

	switch cli {
	case "codex":
		return runAIGeneratorCodex(ctx, spec)
	case "opencode":
		return runAIGeneratorOpenCode(ctx, spec)
	default:
		return "", fmt.Errorf("ai-generator: unsupported CLI %q (use codex or opencode)", cli)
	}
}

func pickAIGeneratorCLI(spec AIGeneratorSpec) string {
	have := func(bin string) bool { _, err := osexec.LookPath(bin); return err == nil }
	runnerID, _ := splitAgentSpec(spec.Runner)

	// Explicit --runner takes precedence. The requested "claude" alias is
	// translated to the claude CLI so RunAIGenerator surfaces the single
	// phase-3 refusal instead of the generic no-CLI-on-PATH error.
	switch strings.ToLower(strings.TrimSpace(runnerID)) {
	case "claude", "claude-code":
		return "claude"
	case "codex":
		if have("codex") {
			return "codex"
		}
	case "opencode":
		if have("opencode") {
			return "opencode"
		}
	}

	// Engine bias.
	switch strings.ToLower(strings.TrimSpace(spec.Engine)) {
	case "hybrid":
		// Planner = the strongest subscription lane; fall through to
		// standard order if it is missing.
	}

	// Fallback chain — yaver's chat-only runners only. An installed but
	// signed out Claude binary must not steal an otherwise healthy OpenCode
	// request.
	for _, bin := range []string{"opencode", "codex"} {
		if have(bin) {
			return bin
		}
	}
	return ""
}

// claude runner removed in chat-only phase 3 — see docs/CHAT_ONLY.md
func runAIGeneratorClaude(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	return "", errors.New("ai generator: the claude runner was removed in chat-only phase 3 (see docs/CHAT_ONLY.md)")
}

func runAIGeneratorCodex(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	if err := CheckRunnerReady(GetRunnerConfig("codex"), spec.WorkDir); err != nil {
		return "", err
	}
	args := []string{"exec", "--full-auto"}
	if model := strings.TrimSpace(spec.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-")
	cmd := osexec.CommandContext(ctx, "codex", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Stderr = os.Stderr
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex: %w", err)
	}
	return buf.String(), nil
}

func runAIGeneratorOpenCode(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	if err := CheckRunnerReady(GetRunnerConfig("opencode"), spec.WorkDir); err != nil {
		return "", err
	}
	model := strings.TrimSpace(spec.Model)
	if model == "" {
		model = strings.TrimSpace(envOr("YAVER_OPENCODE_MODEL", ""))
	}
	if _, runnerModel := splitAgentSpec(spec.Runner); model == "" && runnerModel != "" {
		model = runnerModel
	}
	// Use the same launcher as Mobile Workspace edits and runner readiness.
	// It supplies the task environment, scoped MCP config, stdin handling, and
	// model grammar that a real task uses; a special one-off command here was a
	// false green when /tasks worked but prompt generation hung.
	meta, err := newOpenCodeSandboxRunner(sandboxRunnerSelection{
		Model: model,
		Mode:  strings.TrimSpace(spec.Mode),
	})(ctx, spec.WorkDir, spec.Prompt)
	if err != nil {
		return "", err
	}
	return meta.rationale, nil
}
