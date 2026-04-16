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
		return "", fmt.Errorf("ai-generator: no AI CLI on PATH (looked for claude, codex, aider, ollama)")
	}

	fmt.Fprintf(os.Stderr, "[ai-gen] using %s\n", cli)
	ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
	defer cancel()

	switch cli {
	case "claude":
		return runAIGeneratorClaude(ctx, spec)
	case "codex":
		return runAIGeneratorCodex(ctx, spec)
	case "aider":
		return runAIGeneratorAider(ctx, spec)
	case "ollama":
		return runAIGeneratorOllama(ctx, spec)
	default:
		return "", fmt.Errorf("ai-generator: unsupported CLI %q", cli)
	}
}

func pickAIGeneratorCLI(spec AIGeneratorSpec) string {
	have := func(bin string) bool { _, err := osexec.LookPath(bin); return err == nil }

	// Explicit --runner takes precedence.
	switch strings.ToLower(strings.TrimSpace(spec.Runner)) {
	case "claude", "claude-code":
		if have("claude") {
			return "claude"
		}
	case "codex":
		if have("codex") {
			return "codex"
		}
	case "aider", "aider-ollama":
		if have("aider") {
			return "aider"
		}
	}
	if strings.HasPrefix(strings.ToLower(spec.Runner), "ollama") {
		if have("ollama") {
			return "ollama"
		}
	}

	// Engine bias.
	switch strings.ToLower(strings.TrimSpace(spec.Engine)) {
	case "hybrid":
		// Planner = claude; fall through to standard order if missing.
	}

	// Fallback chain.
	for _, bin := range []string{"claude", "codex", "aider", "ollama"} {
		if have(bin) {
			return bin
		}
	}
	return ""
}

func runAIGeneratorClaude(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	cmd := osexec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--add-dir", spec.WorkDir,
	)
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
	cmd := osexec.CommandContext(ctx, "codex", "--quiet", "--full-auto", "-")
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

func runAIGeneratorAider(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	cmd := osexec.CommandContext(ctx, "aider",
		"--no-pretty", "--yes-always",
		"--message", spec.Prompt,
	)
	cmd.Dir = spec.WorkDir
	cmd.Stderr = os.Stderr
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("aider: %w", err)
	}
	return buf.String(), nil
}

func runAIGeneratorOllama(ctx context.Context, spec AIGeneratorSpec) (string, error) {
	model := envOr("YAVER_OLLAMA_MODEL", "qwen2.5-coder:14b")
	if strings.HasPrefix(strings.ToLower(spec.Runner), "ollama:") {
		model = strings.TrimPrefix(spec.Runner, "ollama:")
	}
	cmd := osexec.CommandContext(ctx, "ollama", "run", model)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Stderr = os.Stderr
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	return buf.String(), nil
}
