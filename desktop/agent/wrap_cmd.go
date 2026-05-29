package main

// wrap_cmd.go — `yaver wrap <runner>` launches one of yaver's three
// first-class coding runners (claude / codex / opencode) interactively in
// the current directory with its yolo flag, after registering Yaver as an
// MCP server inside that runner. The user lands in the runner's own TUI
// with every yaver_* MCP tool available.
//
// This is the interactive sibling of headless task execution
// (tasks.go::builtinRunners): same runner set, same dangerous/yolo posture
// (feedback_runners_always_dangerous), but NO `-p`/prompt — it's a straight
// passthrough into the live runner (feedback_no_headless_p_mode). Auth is
// whatever the runner already has (subscription OAuth — Claude Max /
// ChatGPT Plus); wrap never injects an API key
// (feedback_no_api_keys_subscription_only).
//
// Reachable both as `yaver wrap codex` and as `wrap codex` inside the
// interactive shell (shell_repl.go re-execs the real binary).

import (
	"fmt"
	"os"
	"os/exec"
)

// interactiveRunnerArgs returns the argv (minus the binary) that launches a
// runner in INTERACTIVE mode with its yolo/dangerous flag. Unlike
// builtinRunners[id].Args — which are headless (`-p` / `exec` / `run` plus
// `{prompt}`) — these drop the user straight into the runner's TUI. Kept
// here (not in builtinRunners) precisely because the interactive invocation
// differs from the task-execution one.
func interactiveRunnerArgs(runnerID string) []string {
	switch runnerID {
	case "claude":
		// Interactive Claude Code TUI, edit-approval prompts bypassed.
		return []string{"--dangerously-skip-permissions"}
	case "codex":
		// Codex interactive TUI, yolo. The headless path uses
		// `codex exec --full-auto`, but `--full-auto` is an `exec`-only
		// flag — the interactive bypass flag is
		// --dangerously-bypass-approvals-and-sandbox (verified against
		// codex CLI `--help`).
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "opencode":
		// Plain `opencode` opens its interactive TUI. The
		// --dangerously-skip-permissions flag belongs to `opencode run`
		// (headless) and is rejected in interactive mode, so wrap omits it.
		return nil
	}
	return nil
}

func runWrap(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printWrapUsage()
		return
	}

	runnerID := normalizeRunnerID(args[0])
	if !IsSupportedRunner(runnerID) {
		fmt.Fprintf(os.Stderr, "✗ unsupported runner %q — use claude, codex, or opencode\n", args[0])
		os.Exit(1)
	}

	cfg, ok := builtinRunners[runnerID]
	if !ok {
		cfg = GetRunnerConfig(runnerID)
	}

	if err := CheckRunnerBinary(cfg.Command); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s is not installed (%v)\n  Install it with: yaver install %s\n", cfg.Command, err, runnerID)
		os.Exit(1)
	}

	// Register Yaver as an MCP server inside the runner (idempotent — the
	// setup* helpers no-op when it's already configured).
	yaverPath := findYaverBinary()
	fmt.Printf("▸ ensuring Yaver MCP in %s…\n", cfg.Name)
	switch runnerID {
	case "claude":
		setupClaudeCode(yaverPath, false)
	case "codex":
		setupCodex(yaverPath, false)
	case "opencode":
		setupOpenCode(yaverPath, false)
	}

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	runArgs := append(interactiveRunnerArgs(runnerID), args[1:]...)
	fmt.Printf("▸ launching %s (yolo) in %s\n\n", cfg.Command, cwd)

	cmd := exec.Command(cfg.Command, runArgs...)
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "✗ %s exited: %v\n", cfg.Command, err)
		os.Exit(1)
	}
}

func printWrapUsage() {
	fmt.Print("yaver wrap — launch a coding runner wrapped by Yaver (MCP attached)\n\n" +
		"Usage:\n" +
		"  yaver wrap claude     Launch Claude Code in this directory (yolo)\n" +
		"  yaver wrap codex      Launch OpenAI Codex in this directory (yolo)\n" +
		"  yaver wrap opencode   Launch opencode in this directory (yolo)\n\n" +
		"Anything after the runner is passed straight through, e.g.\n" +
		"  yaver wrap codex --model gpt-5.4\n\n" +
		"Yaver is registered as an MCP server inside the runner first, so every\n" +
		"yaver_* tool is available from inside the session. Auth uses whatever\n" +
		"subscription the runner already has (Claude Max / ChatGPT Plus).\n")
}
