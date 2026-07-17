package main

// Tmux-attach mode for runner spawn.
//
// Why: when the yaver daemon is started outside the user's GUI login session
// (launchd, ssh, headless), Claude Code can't read the user's macOS Keychain
// item "Claude Code-credentials" — even when running as the same UNIX user
// — because Keychain unlocking is per login session. Result: every task
// fails with "Not logged in · Please run /login". The user's own interactive
// `claude` running in a tmux pane *is* authenticated. Routing tasks into
// that tmux server's environment lets them inherit that auth.
//
// Activation is strictly opt-in via the YAVER_TMUX_RUNNER env var on the
// daemon (e.g. `YAVER_TMUX_RUNNER=yaver-claude yaver serve`). When set, and
// the session exists, and the runner is eligible (claude only, for now),
// startProcess wraps the spawn in a shell orchestration that:
//
//   1. opens a fresh window in the configured tmux session,
//   2. runs the runner inside that window,
//   3. mirrors the pane via `pipe-pane` to a logfile,
//   4. tails the logfile to our own stdout so the existing readStreamJSON
//      / readRawOutput pipeline keeps working unchanged,
//   5. blocks via `tmux wait-for` until the inner runner exits,
//   6. recovers the inner exit code from a marker line and propagates it.
//
// On task kill / ctx cancel, the wrapper sh's EXIT trap calls
// `tmux kill-window` so we don't leak panes.
//
// Limitation: stdout and stderr merge inside the pane. For claude
// stream-json output mode this means JSON lines and human stderr text
// interleave; readStreamJSON tolerates non-JSON lines, so it's livable for
// a first cut.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const tmuxRunnerEnvVar = "YAVER_TMUX_RUNNER"

// tmuxRunnerSession returns the opt-in session name from the daemon's env
// (empty string = feature off).
func tmuxRunnerSession() string {
	return strings.TrimSpace(os.Getenv(tmuxRunnerEnvVar))
}

// tmuxRunnerEligible: which runners benefit from tmux dispatch. Claude
// needs the user's interactive login session for macOS Keychain access.
// OpenCode also benefits because its interactive UI otherwise redraws in
// ways that are hard to inspect after the fact; the wrapper disables the
// tmux alternate screen so normal copy-mode scrollback keeps the transcript.
func tmuxRunnerEligible(runnerID string) bool {
	switch strings.ToLower(strings.TrimSpace(runnerID)) {
	case "claude", "claude-code", "opencode":
		return true
	case "codex":
		// Codex doesn't need the login-session Keychain, but tmux dispatch
		// gives its `exec` runs the same pane-mirrored live view + post-hoc
		// scrollback the other runners get — and the pane is adoptable from
		// the phone (tmux_adopt_session). Opt-in via YAVER_TMUX_RUNNER as
		// with every runner.
		return true
	}
	return false
}

// tmuxRunnerReady checks that tmux is available and the configured session
// exists. Returns the session name on success, "" on any failure (so the
// caller can fall through to the direct exec path without surfacing an
// error to the user). Cheap enough to call on every task start.
func tmuxRunnerReady() string {
	session := tmuxRunnerSession()
	if session == "" {
		return ""
	}
	if !tmuxAvailable() {
		return ""
	}
	if exec.Command(tmuxCmdName(), "has-session", "-t", session).Run() != nil {
		return ""
	}
	return session
}

// shellQuoteStrict single-quotes a value safely for sh, escaping any embedded
// single quotes with the standard POSIX shell sequence.
func shellQuoteStrict(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuoteStrict(a)
	}
	return strings.Join(parts, " ")
}

// shortTaskKey derives a filesystem- and tmux-safe short id from a task
// id (12 chars, [A-Za-z0-9_-] only). The full task id can contain hyphens
// already; we keep them, swap anything else to '-'.
func shortTaskKey(taskID string) string {
	short := taskID
	if len(short) > 12 {
		short = short[:12]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, short)
}

// tmuxRunnerScript is the orchestration program. Reads parameters from
// env vars (set by buildTmuxRunnerCommand) so we don't have to deal with
// quoting them through `sh -c`.
//
// One quirk worth highlighting: the inner command (runner + args) is
// passed pre-shell-quoted in $YAVER_TMUX_INNER and we let tmux's own
// `sh -c` evaluate it. Anything dangerous is single-quoted by shellJoin
// at the Go layer, so $HOME and friends won't be expanded.
const tmuxRunnerScript = `set -u
SESSION=$YAVER_TMUX_SESSION
WIN=$YAVER_TMUX_WIN
SIG=$YAVER_TMUX_SIG
LOG=$YAVER_TMUX_LOG

cleanup() {
  if [ -n "${TAIL_PID:-}" ]; then kill "$TAIL_PID" 2>/dev/null || true; fi
  tmux kill-window -t "$SESSION:$WIN" 2>/dev/null || true
}
trap cleanup TERM INT HUP EXIT

: > "$LOG"
tmux kill-window -t "$SESSION:$WIN" 2>/dev/null || true

tmux new-window -d -t "$SESSION" -n "$WIN"
tmux set-option -q -t "$SESSION:$WIN" remain-on-exit on 2>/dev/null || true
tmux set-window-option -q -t "$SESSION:$WIN" alternate-screen off 2>/dev/null || true
tmux send-keys -t "$SESSION:$WIN" \
  "$YAVER_TMUX_INNER; rc=\$?; printf '\n__YAVER_EXIT__:%d\n' \"\$rc\"; tmux wait-for -S \"$SIG\"" Enter

tmux pipe-pane -o -t "$SESSION:$WIN" "cat >> '$LOG'"

tail -n +1 -F "$LOG" 2>/dev/null &
TAIL_PID=$!

tmux wait-for "$SIG"

sleep 0.2
kill "$TAIL_PID" 2>/dev/null || true
TAIL_PID=

EXIT=$(grep -E '^__YAVER_EXIT__:[0-9]+$' "$LOG" 2>/dev/null | tail -1 | sed -e 's/.*://')
tmux kill-window -t "$SESSION:$WIN" 2>/dev/null || true
exit "${EXIT:-1}"
`

// buildTmuxRunnerCommand returns an *exec.Cmd that, when run, dispatches
// the runner into a fresh window of the named tmux session and streams
// its merged output back via the wrapper sh's stdout. The returned cmd
// behaves like a normal subprocess for the rest of tasks.go: Wait blocks
// until the inner runner exits, the inner exit code surfaces as the
// wrapper's exit code, and Process.Kill / ctx-cancel tears down the
// pane via the trap in tmuxRunnerScript.
//
// Caller is expected to set cmd.Dir / cmd.Env (taskEnv) and to
// supplement cmd.Env with the YAVER_TMUX_* values returned here. We
// hand back the env additions rather than baking them in so the
// existing taskEnv() call site stays the source of truth for everything
// else.
func buildTmuxRunnerCommand(
	ctx context.Context,
	session string,
	taskID string,
	runnerCmd string,
	runnerArgs []string,
) (*exec.Cmd, []string) {
	short := shortTaskKey(taskID)
	win := "yaver-task-" + short
	sig := "yaver-done-" + short
	logPath := fmt.Sprintf("/tmp/yaver-tmux-%s.log", short)
	inner := shellJoin(append([]string{runnerCmd}, runnerArgs...))

	cmd := exec.CommandContext(ctx, "sh", "-c", tmuxRunnerScript)
	envAdditions := []string{
		"YAVER_TMUX_SESSION=" + session,
		"YAVER_TMUX_WIN=" + win,
		"YAVER_TMUX_SIG=" + sig,
		"YAVER_TMUX_LOG=" + logPath,
		"YAVER_TMUX_INNER=" + inner,
	}
	return cmd, envAdditions
}
