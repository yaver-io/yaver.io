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

// autorunTmuxSessionName names the TUI for one task AND one runner. The runner
// belongs in the name because a session outlives the runner that created it: with
// a master/doer split both roles drive a TUI for the same task at the same time,
// and on failover the successor inherits a session still running its predecessor's
// binary. Keying on the task alone sends one runner's prompt into another
// runner's TUI — silently, since tmux happily accepts the keystrokes.
func autorunTmuxSessionName(taskPath, runnerID string) string {
	name := "yaver-autorun-" + autorunTaskName(taskPath)
	if id := normalizeRunnerID(strings.TrimSpace(runnerID)); id != "" {
		name += "-" + id
	}
	return name
}

func autorunTmuxAvailable(ctx context.Context, workDir string) bool {
	return autorunExec(ctx, tmuxCmdName(), []string{"-V"}, workDir).Err == nil
}

func autorunTmuxHasSession(ctx context.Context, session, workDir string) bool {
	return autorunExec(ctx, tmuxCmdName(), []string{"has-session", "-t", session}, workDir).Err == nil
}

func autorunTmuxCapture(ctx context.Context, session, workDir string) string {
	return autorunExec(ctx, tmuxCmdName(), []string{"capture-pane", "-p", "-t", session}, workDir).Output
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
	res := autorunExec(ctx, tmuxCmdName(), []string{
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
	if res := autorunExec(ctx, tmuxCmdName(), []string{"send-keys", "-t", session, "-l", instruction}, workDir); res.Err != nil {
		return autorunCommandResult{Output: res.Output, Err: fmt.Errorf("send prompt to %s: %w", session, res.Err)}
	}
	if res := autorunExec(ctx, tmuxCmdName(), []string{"send-keys", "-t", session, "Enter"}, workDir); res.Err != nil {
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

// autorunTmuxGoalReadyTimeout bounds the wait for a freshly-created TUI to be
// able to accept input. A slash command typed into a TUI that has not finished
// booting is silently swallowed, and the goal would then never be armed — a
// failure that is invisible until the run overshoots.
const autorunTmuxGoalReadyTimeout = 60 * time.Second

// autorunTmuxSetGoal arms the runner's own `/goal` loop on a freshly-created
// TUI session.
//
// `/goal <condition>` (claude >= 2.1.139) makes the runner re-enter itself after
// each turn until a judge model agrees the condition holds. It is a SLASH
// COMMAND, so unlike the task prompt it cannot be staged to a file and read —
// it has to be typed into the composer.
//
// Note how this composes with autorun's gate: autorun verifies and commits
// between ITERATIONS, but /goal loops inside a single turn, so a long goal run
// reaches the gate once and a gate failure stashes the whole run rather than the
// last increment. Prefer a narrow condition over one sweeping goal.
//
// No-ops for codex and opencode, which have no /goal and would take it as
// literal prompt text.
func autorunTmuxSetGoal(ctx context.Context, session, goal string, runner RunnerConfig, workDir string) autorunCommandResult {
	goal = strings.TrimSpace(goal)
	if goal == "" || !autorunRunsClaudeBinary(runner) {
		return autorunCommandResult{}
	}
	if !autorunTmuxWaitComposerReady(ctx, session, workDir) {
		return autorunCommandResult{
			Output: autorunTmuxCapture(ctx, session, workDir),
			Err:    fmt.Errorf("runner TUI %s never became ready to accept /goal", session),
		}
	}
	// -l sends literally; Enter must be a SEPARATE send-keys or the command sits
	// unsubmitted in the composer (same reason as autorunTmuxKick).
	if res := autorunExec(ctx, tmuxCmdName(), []string{"send-keys", "-t", session, "-l", "/goal " + goal}, workDir); res.Err != nil {
		return autorunCommandResult{Output: res.Output, Err: fmt.Errorf("send /goal to %s: %w", session, res.Err)}
	}
	if res := autorunExec(ctx, tmuxCmdName(), []string{"send-keys", "-t", session, "Enter"}, workDir); res.Err != nil {
		return autorunCommandResult{Output: res.Output, Err: fmt.Errorf("submit /goal to %s: %w", session, res.Err)}
	}
	return autorunCommandResult{}
}

// autorunTmuxWaitComposerReady waits until the TUI is showing its input
// composer. We look for the composer's prompt glyph rather than for an absence
// of the busy marker, because a TUI that is still booting shows neither.
func autorunTmuxWaitComposerReady(ctx context.Context, session, workDir string) bool {
	deadline := time.Now().Add(autorunTmuxGoalReadyTimeout)
	for time.Now().Before(deadline) {
		pane := autorunTmuxCapture(ctx, session, workDir)
		// The composer is up once the TUI draws its input line and is not busy.
		if strings.Contains(pane, ">") && !strings.Contains(pane, autorunTmuxBusyMarker) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}
