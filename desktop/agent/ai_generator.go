package main

// ai_generator.go — runner-agnostic "ask the AI for text" helper.
// autoinit / autoideas / autodev refill / autodev_harden_prompt
// drivers all need to spawn an AI runner with a prompt and read
// back its assistant text. Without this helper they'd each
// hardcode `claude --print` and skip ollama / qwen / aider / codex.
//
// Resolution order matches user expectation:
//   1. Caller's explicit --engine / --runner picks first
//   2. Hybrid mode → use the planner CLI (claude when available)
//   3. claude → codex → aider → ollama  (whichever is on PATH)
//   4. Surface a clear error listing missing CLIs
//
// All runners are wrapped in the same stdin-prompt + stream-json
// (or plain --print) shape so the chat-event publisher sees their
// output regardless of which one ran.

import (
	"bytes"
	"context"
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
	// Engine is the high-level autodev engine selector ("claude",
	// "hybrid", "" = default). When set to "hybrid" we use the
	// planner CLI (Claude) since the cheap implementer can't handle
	// generation tasks well.
	Engine string

	// Runner is an explicit runner ID override (e.g. "codex",
	// "ollama:qwen2.5-coder:32b"). Takes precedence over Engine.
	Runner string

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
	case "claude":
		return runAIGeneratorClaude(ctx, spec)
	case "codex":
		return runAIGeneratorCodex(ctx, spec)
	case "opencode":
		return runAIGeneratorOpenCode(ctx, spec)
	default:
		return "", fmt.Errorf("ai-generator: unsupported CLI %q (use claude, codex, or opencode)", cli)
	}
}

func pickAIGeneratorCLI(spec AIGeneratorSpec) string {
	have := func(bin string) bool { _, err := osexec.LookPath(bin); return err == nil }
	runnerID, _ := splitAgentSpec(spec.Runner)

	// Explicit --runner takes precedence.
	switch strings.ToLower(strings.TrimSpace(runnerID)) {
	case "claude", "claude-code":
		if have("claude") {
			return "claude"
		}
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
		// Planner = claude; fall through to standard order if missing.
	}

	// Fallback chain — yaver's three first-class runners only.
	for _, bin := range []string{"claude", "codex", "opencode"} {
		if have(bin) {
			return bin
		}
	}
	return ""
}

func runAIGeneratorClaude(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	if err := CheckRunnerReady(GetRunnerConfig("claude"), spec.WorkDir); err != nil {
		return "", err
	}
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--add-dir", spec.WorkDir,
	}
	if _, model := splitAgentSpec(spec.Runner); model != "" {
		args = append(args, "--model", model)
	}
	cmd := osexec.CommandContext(ctx, "claude", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("spawn claude: %w", err)
	}
	resp, _, perr := parseClaudeStream(stdout)
	if waitErr := cmd.Wait(); waitErr != nil && resp == nil {
		return "", fmt.Errorf("claude: %w", waitErr)
	}
	if perr != nil && resp == nil {
		return "", perr
	}
	if resp == nil {
		return "", fmt.Errorf("claude: no result event")
	}
	return resp.Summary, nil
}

func runAIGeneratorCodex(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	if err := CheckRunnerReady(GetRunnerConfig("codex"), spec.WorkDir); err != nil {
		return "", err
	}
	cmd := osexec.CommandContext(ctx, "codex", "exec", "--full-auto", "-")
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
	// Current opencode uses `opencode run <message>` for non-interactive
	// runs. The old `--message` flag was removed in sst/opencode.
	args := []string{"run", "--dangerously-skip-permissions"}
	model := strings.TrimSpace(envOr("YAVER_OPENCODE_MODEL", ""))
	if _, runnerModel := splitAgentSpec(spec.Runner); runnerModel != "" {
		model = runnerModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, spec.Prompt)
	cmd := osexec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stderr = os.Stderr
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("opencode: %w", err)
	}
	return buf.String(), nil
}
