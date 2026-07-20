package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestShellQuoteRoundTrip(t *testing.T) {
	cases := []string{
		`hello`,
		`hello world`,
		`it's me`,
		`'single'`,
		`$HOME`,
		`a"b`,
		`a;b`,
		``,
	}
	for _, c := range cases {
		quoted := shellQuoteStrict(c)
		// `sh -c 'printf %s '"$quoted"` would re-print c verbatim. We
		// invoke sh and confirm the output equals the input.
		out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
		if err != nil {
			t.Fatalf("shell exec for %q (quoted=%s) failed: %v", c, quoted, err)
		}
		if string(out) != c {
			t.Fatalf("roundtrip mismatch: input=%q quoted=%s got=%q", c, quoted, string(out))
		}
	}
}

func TestShellJoinPreservesArgBoundaries(t *testing.T) {
	args := []string{"claude", "-p", "hello world", "--model", "opus"}
	joined := shellJoin(args)
	// `sh -c 'printf "[%s]" arg1 arg2 ...'` lets us see how sh tokenized.
	out, err := exec.Command("sh", "-c", `printf "[%s]" `+joined).Output()
	if err != nil {
		t.Fatalf("sh -c failed: %v", err)
	}
	want := `[claude][-p][hello world][--model][opus]`
	if string(out) != want {
		t.Fatalf("shellJoin lost arg boundaries: want %q got %q (joined=%s)", want, string(out), joined)
	}
}

func TestShortTaskKeyClampAndSanitize(t *testing.T) {
	cases := map[string]string{
		"abc":              "abc",
		"abcdefghijklmno":  "abcdefghijkl", // 12-char clamp
		"task/with/slash":  "task-with-sl", // sanitized then clamped
		"weird!@#$%^&*()_": "weird-------",
		"keep_under-score": "keep_under-s",
	}
	for in, want := range cases {
		got := shortTaskKey(in)
		if got != want {
			t.Errorf("shortTaskKey(%q): want %q got %q", in, want, got)
		}
		if len(got) > 12 {
			t.Errorf("shortTaskKey(%q) = %q exceeds 12 chars", in, got)
		}
	}
}

func TestTmuxRunnerReadyDefaultsToAutoSession(t *testing.T) {
	// With no explicit YAVER_TMUX_RUNNER, tmuxRunnerReady() falls back to
	// the auto-provisioned default session on hosts where tmux is available
	// (so every task can be attached to via `tmux attach -t yaver-tasks`).
	// On hosts without tmux it must still return "" so callers exec directly.
	t.Setenv(tmuxRunnerEnvVar, "")
	got := tmuxRunnerReady()
	if _, err := exec.LookPath("tmux"); err != nil {
		if got != "" {
			t.Fatalf("tmuxRunnerReady() without tmux available: want empty, got %q", got)
		}
		return
	}
	if got != defaultTmuxRunnerSession {
		t.Fatalf("tmuxRunnerReady() with tmux available: want %q, got %q", defaultTmuxRunnerSession, got)
	}
	// Best-effort cleanup — the auto-create above may have started a real
	// session on the developer's machine.
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", defaultTmuxRunnerSession).Run()
	})
}

func TestTmuxRunnerReadyAbsentSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; nothing to assert")
	}
	t.Setenv(tmuxRunnerEnvVar, "yaver-test-no-such-session-"+os.Getenv("USER"))
	if got := tmuxRunnerReady(); got != "" {
		t.Fatalf("tmuxRunnerReady() for nonexistent session: want empty, got %q", got)
	}
}

func TestTmuxRunnerEligibleClaudeOnly(t *testing.T) {
	for _, ok := range []string{"claude", "Claude", "CLAUDE-CODE", "claude-code", "opencode", "OpenCode", "codex", "Codex"} {
		if !tmuxRunnerEligible(ok) {
			t.Errorf("expected %q to be eligible", ok)
		}
	}
	for _, no := range []string{"", "claude2", "yaver"} {
		if tmuxRunnerEligible(no) {
			t.Errorf("expected %q to be ineligible", no)
		}
	}
}

func TestBuildTmuxRunnerCommandShape(t *testing.T) {
	cmd, env, win := buildTmuxRunnerCommand(
		context.Background(),
		"yaver-claude",
		"task-abc-def-ghi-jkl",
		"claude",
		[]string{"-p", "say hi"},
	)
	if win == "" || !strings.HasPrefix(win, "yaver-task-") {
		t.Errorf("expected window name yaver-task-<short>, got %q", win)
	}
	if cmd.Args[0] != "sh" || cmd.Args[1] != "-c" {
		t.Fatalf("expected sh -c invocation, got %v", cmd.Args)
	}
	if !strings.Contains(cmd.Args[2], "tmux new-window") {
		t.Fatal("script body missing tmux new-window")
	}
	if !strings.Contains(cmd.Args[2], "alternate-screen off") {
		t.Fatal("script body must disable tmux alternate-screen for inspectable scrollback")
	}
	if !strings.Contains(cmd.Args[2], "tmux send-keys") {
		t.Fatal("script body must send the runner command after window options are applied")
	}
	if !strings.Contains(cmd.Args[2], "tmux wait-for") {
		t.Fatal("script body missing tmux wait-for")
	}
	if !strings.Contains(cmd.Args[2], "trap cleanup") {
		t.Fatal("script body missing cleanup trap (would leak panes on cancel)")
	}
	wantInner := "'claude' '-p' 'say hi'"
	var sawInner, sawSession bool
	for _, kv := range env {
		if kv == "YAVER_TMUX_SESSION=yaver-claude" {
			sawSession = true
		}
		if strings.HasPrefix(kv, "YAVER_TMUX_INNER=") && strings.Contains(kv, wantInner) {
			sawInner = true
		}
	}
	if !sawSession {
		t.Errorf("env missing YAVER_TMUX_SESSION: %v", env)
	}
	if !sawInner {
		t.Errorf("env missing properly-quoted YAVER_TMUX_INNER (want contains %q): %v", wantInner, env)
	}
}
