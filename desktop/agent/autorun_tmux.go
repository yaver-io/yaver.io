package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Autorun drives runner TUIs through tmux instead of headless `-p`.
//
// Proven on the Mac mini 2026-07-16: `claude -p` exits with "OAuth session
// expired and could not be refreshed" while the SAME user's interactive TUI on
// the SAME box, at the same moment, is fully authenticated and answers normally.
// Only the headless path is broken. A loop that speaks `-p` therefore cannot use
// claude at all — which is why autorun silently degraded to codex for hours.
// Speaking PTY is the fix, and it is what feedback_no_headless_p_mode has always
// been about.

const (
	// autorunTmuxBusyMarker is what a runner TUI shows while it is working. Its
	// absence — after it was once present — is our "turn finished" signal.
	autorunTmuxBusyMarker = "esc to interrupt"
	autorunTmuxPollEvery  = 5 * time.Second
	// autorunTmuxStartGrace bounds how long we wait for a submitted prompt to
	// visibly start working before concluding the runner answered instantly.
	autorunTmuxStartGrace = 45 * time.Second
)

// autorunTmuxArgs derives the interactive invocation from a runner's headless
// config: drop `-p`, the prompt placeholder, and the print-mode output flags,
// but keep the permission flag so the TUI is just as unattended.
func autorunTmuxArgs(runner RunnerConfig) []string {
	dropFlag := map[string]bool{
		"-p": true, "--print": true, "--verbose": true,
		"--include-partial-messages": true, "--output-format": true, "--tools": true,
	}
	dropFlagWithValue := map[string]bool{"--output-format": true, "--tools": true}

	var args []string
	if strings.TrimSpace(runner.Model) != "" {
		args = append(args, "--model", runner.Model)
	}
	skipNext := false
	for _, a := range runner.Args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "{prompt}" {
			continue
		}
		if dropFlag[a] {
			skipNext = dropFlagWithValue[a]
			continue
		}
		args = append(args, a)
	}
	return args
}

func autorunTmuxSessionName(taskPath string) string {
	return "yaver-autorun-" + autorunTaskName(taskPath)
}

func autorunTmuxAvailable(ctx context.Context, workDir string) bool {
	return autorunExec(ctx, "tmux", []string{"-V"}, workDir).Err == nil
}

func autorunTmuxHasSession(ctx context.Context, session, workDir string) bool {
	return autorunExec(ctx, "tmux", []string{"has-session", "-t", session}, workDir).Err == nil
}

func autorunTmuxCapture(ctx context.Context, session, workDir string) string {
	return autorunExec(ctx, "tmux", []string{"capture-pane", "-p", "-t", session}, workDir).Output
}

// shellQuoteSingle wraps a token for a POSIX shell, which is how tmux
// new-session interprets its command argument.
func shellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ensureAutorunTmuxSession brings the runner TUI up if it is not already
// running. Recreating a vanished session IS the self-heal for a crashed runner:
// the loop keeps its cadence instead of dying.
func ensureAutorunTmuxSession(ctx context.Context, session string, runner RunnerConfig, workDir string) (bool, error) {
	if autorunTmuxHasSession(ctx, session, workDir) {
		return false, nil
	}
	parts := append([]string{resolveRunnerBinary(runner.Command)}, autorunTmuxArgs(runner)...)
	quoted := make([]string, 0, len(parts)+4)
	// The TUI inherits the tmux SERVER's environment, which autorun does not
	// control, so the subscription-only guard has to travel on the command line:
	// `env -u ANTHROPIC_API_KEY claude ...` cannot be routed onto metered billing
	// no matter what the server env holds.
	if banned := apiKeyEnvBannedFor[normalizeRunnerID(runner.RunnerID)]; len(banned) > 0 {
		quoted = append(quoted, "env")
		for _, b := range banned {
			quoted = append(quoted, "-u", shellQuoteSingle(b))
		}
	}
	for _, p := range parts {
		quoted = append(quoted, shellQuoteSingle(p))
	}
	res := autorunExec(ctx, "tmux", []string{
		"new-session", "-d", "-s", session, "-c", workDir, strings.Join(quoted, " "),
	}, workDir)
	if res.Err != nil {
		return false, fmt.Errorf("start %s TUI in tmux: %w: %s", runner.RunnerID, res.Err, strings.TrimSpace(res.Output))
	}
	return true, nil
}

// autorunTmuxKick hands one iteration to the runner TUI and waits for its turn
// to finish.
//
// The prompt is written to a file and referenced by a ONE-LINE instruction
// rather than typed: an autorun prompt carries the whole task markdown and git
// log, and a multi-line send-keys submits at the first newline, mangling it.
func autorunTmuxKick(ctx context.Context, session, prompt, workDir string, timeout time.Duration) autorunCommandResult {
	promptPath := filepath.Join(os.TempDir(), session+"-prompt.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0600); err != nil {
		return autorunCommandResult{Err: fmt.Errorf("stage tmux prompt: %w", err)}
	}
	instruction := "Read " + promptPath + " and carry out the task it describes. Do not ask questions."

	// -l sends the text literally; Enter must be a SEPARATE send-keys, or the
	// TUI leaves the text sitting unsubmitted in its composer.
	if res := autorunExec(ctx, "tmux", []string{"send-keys", "-t", session, "-l", instruction}, workDir); res.Err != nil {
		return autorunCommandResult{Output: res.Output, Err: fmt.Errorf("send prompt to %s: %w", session, res.Err)}
	}
	if res := autorunExec(ctx, "tmux", []string{"send-keys", "-t", session, "Enter"}, workDir); res.Err != nil {
		return autorunCommandResult{Output: res.Output, Err: fmt.Errorf("submit prompt to %s: %w", session, res.Err)}
	}

	deadline := time.Now().Add(timeout)
	started := time.Now()
	sawBusy := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return autorunCommandResult{Output: autorunTmuxCapture(ctx, session, workDir), Err: ctx.Err()}
		case <-time.After(autorunTmuxPollEvery):
		}
		if !autorunTmuxHasSession(ctx, session, workDir) {
			return autorunCommandResult{Err: fmt.Errorf("runner TUI session %s vanished mid-turn", session)}
		}
		pane := autorunTmuxCapture(ctx, session, workDir)
		if strings.Contains(pane, autorunTmuxBusyMarker) {
			sawBusy = true
			continue
		}
		// Idle after having been busy = the turn completed.
		if sawBusy {
			return autorunCommandResult{Output: pane}
		}
		// Never went busy within the grace window: the runner answered without
		// visibly working. Treat it as a completed (probably no-op) turn — the
		// gate and git status decide what it was worth, not this heuristic.
		if time.Since(started) > autorunTmuxStartGrace {
			return autorunCommandResult{Output: pane}
		}
	}
	return autorunCommandResult{
		Output: autorunTmuxCapture(ctx, session, workDir),
		Err:    fmt.Errorf("runner TUI %s did not finish within %s", session, timeout),
	}
}
