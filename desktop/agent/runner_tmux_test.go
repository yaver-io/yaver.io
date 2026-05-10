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

func TestTmuxRunnerReadyOffByDefault(t *testing.T) {
	// Ensure no env leakage promotes the feature on by default.
	t.Setenv(tmuxRunnerEnvVar, "")
	if got := tmuxRunnerReady(); got != "" {
		t.Fatalf("tmuxRunnerReady() with empty env: want empty, got %q", got)
	}
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
	for _, ok := range []string{"claude", "Claude", "CLAUDE-CODE", "claude-code"} {
		if !tmuxRunnerEligible(ok) {
			t.Errorf("expected %q to be eligible", ok)
		}
	}
	for _, no := range []string{"codex", "opencode", "", "claude2", "yaver"} {
		if tmuxRunnerEligible(no) {
			t.Errorf("expected %q to be ineligible", no)
		}
	}
}

func TestBuildTmuxRunnerCommandShape(t *testing.T) {
	cmd, env := buildTmuxRunnerCommand(
		context.Background(),
		"yaver-claude",
		"task-abc-def-ghi-jkl",
		"claude",
		[]string{"-p", "say hi"},
	)
	if cmd.Args[0] != "sh" || cmd.Args[1] != "-c" {
		t.Fatalf("expected sh -c invocation, got %v", cmd.Args)
	}
	if !strings.Contains(cmd.Args[2], "tmux new-window") {
		t.Fatal("script body missing tmux new-window")
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
