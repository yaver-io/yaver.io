package main

import (
	"strings"
	"testing"
)

// claude's headless Args carry `-p {prompt}` plus print-mode output flags. The
// TUI invocation must drop exactly those and keep the permission flag — get this
// wrong and the TUI either never starts or starts asking for approval.
func TestAutorunTmuxArgsStripsHeadlessPrintMode(t *testing.T) {
	args := autorunTmuxArgs(GetRunnerConfig("claude"))
	joined := strings.Join(args, " ")

	for _, banned := range []string{"-p", "{prompt}", "--output-format", "stream-json", "--include-partial-messages", "--verbose"} {
		for _, a := range args {
			if a == banned {
				t.Fatalf("headless flag %q survived into the TUI invocation: %q", banned, joined)
			}
		}
	}
	if !strings.Contains(joined, "--dangerously-skip-permissions") {
		t.Fatalf("TUI must stay unattended: %q", joined)
	}
	if !strings.Contains(joined, "--model") {
		t.Fatalf("model override must survive into the TUI: %q", joined)
	}
}

// The whole point: claude must never be driven headless, with or without --tmux.
func TestAutorunUsesTmuxForClaudeAlways(t *testing.T) {
	if !autorunUsesTmux(autorunOptions{}, GetRunnerConfig("claude")) {
		t.Fatal("claude must default to the tmux TUI; its -p path fails auth even when the TUI is signed in")
	}
	if autorunUsesTmux(autorunOptions{}, GetRunnerConfig("codex")) {
		t.Fatal("codex works headless and should not pay for a TUI by default")
	}
	if !autorunUsesTmux(autorunOptions{Tmux: true}, GetRunnerConfig("codex")) {
		t.Fatal("--tmux must force the TUI for any runner")
	}
}

func TestAutorunTailLinesKeepsTheEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("THE ERROR")
	got := autorunTailLines(b.String(), 5)
	if !strings.Contains(got, "THE ERROR") {
		t.Fatalf("tail must keep the end, where the error is: %q", got)
	}
	if strings.Count(got, "\n") > 5 {
		t.Fatalf("tail must be bounded, got %d lines", strings.Count(got, "\n"))
	}
}

func TestAutorunTmuxSessionNameIsPerTask(t *testing.T) {
	a := autorunTmuxSessionName("/repo/tasks/fix-gate.md", "claude")
	b := autorunTmuxSessionName("/repo/tasks/other.md", "claude")
	if a == b {
		t.Fatal("two tasks must not share one runner TUI session")
	}
	if !strings.Contains(a, "fix-gate") {
		t.Fatalf("session name should name its task for attachability: %q", a)
	}
}

// With a master/doer split both seats drive a TUI for the SAME task at the same
// time. Keying the session on the task alone sends the doer's prompt into the
// master's TUI — tmux accepts the keystrokes and nothing reports it.
func TestAutorunTmuxSessionNameIsPerRunner(t *testing.T) {
	master := autorunTmuxSessionName("/repo/tasks/fix-gate.md", "claude")
	doer := autorunTmuxSessionName("/repo/tasks/fix-gate.md", "codex")
	if master == doer {
		t.Fatalf("two runners on one task must not share a TUI session: %q", master)
	}
	if !strings.Contains(doer, "codex") {
		t.Fatalf("session name should name its runner for attachability: %q", doer)
	}
}

// codex spells headless as a SUBCOMMAND, not a flag: `codex exec` is headless,
// bare `codex` is the TUI. A flag-only filter left `codex --model X exec
// --full-auto`, which exits on the spot — so every --tmux codex run died, the
// loop failed over down the chain, and the error named whichever runner came
// last. codex was the only authenticated runner on the mini and was never
// actually launchable. Verified live 2026-07-17: exec form dies, bare form runs.
func TestAutorunTmuxArgsDropsCodexExecSubcommand(t *testing.T) {
	args := autorunTmuxArgs(GetRunnerConfig("codex"))
	joined := strings.Join(args, " ")

	for _, a := range args {
		if a == "exec" {
			t.Fatalf("the exec subcommand selects headless mode and must not reach the TUI: %q", joined)
		}
		if a == "{prompt}" {
			t.Fatalf("prompt placeholder survived into the TUI invocation: %q", joined)
		}
		if a == "--full-auto" {
			t.Fatalf("--full-auto is exec-only (and deprecated); it must be mapped to the TUI equivalent: %q", joined)
		}
	}
	// Unattended is the whole point — a TUI sitting on an approval prompt is
	// just a slower way to fail.
	if !strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("codex TUI must stay unattended: %q", joined)
	}
	if !strings.Contains(joined, "--model") {
		t.Fatalf("model override must survive into the TUI: %q", joined)
	}
}

// Guard the general shape rather than one runner: whatever a runner's headless
// config looks like, the derived TUI invocation must not carry a placeholder or
// a headless selector.
func TestAutorunTmuxArgsNeverCarryPlaceholdersForAnyRunner(t *testing.T) {
	for _, id := range []string{"claude", "codex", "opencode"} {
		cfg := GetRunnerConfig(id)
		if strings.TrimSpace(cfg.Command) == "" {
			continue
		}
		for _, a := range autorunTmuxArgs(cfg) {
			if strings.Contains(a, "{") && strings.Contains(a, "}") {
				t.Fatalf("%s: unsubstituted placeholder %q reached the TUI invocation", id, a)
			}
		}
	}
}
