package main

import "testing"

func TestParseRunnerAuthQuickFlow(t *testing.T) {
	target, runner, extra, ok := parseRunnerAuthQuickFlow([]string{"test", "codex"})
	if !ok {
		t.Fatal("expected quick flow to parse")
	}
	if target != "test" {
		t.Fatalf("target = %q, want test", target)
	}
	if runner != "codex" {
		t.Fatalf("runner = %q, want codex", runner)
	}
	if len(extra) != 0 {
		t.Fatalf("extra = %v, want empty", extra)
	}
}

func TestParseRunnerAuthQuickFlowClaudeCodeAlias(t *testing.T) {
	target, runner, extra, ok := parseRunnerAuthQuickFlow([]string{"@mini", "claude-code"})
	if !ok {
		t.Fatal("expected quick flow to parse")
	}
	if target != "@mini" {
		t.Fatalf("target = %q, want @mini", target)
	}
	if runner != "claude" {
		t.Fatalf("runner = %q, want claude", runner)
	}
	if len(extra) != 0 {
		t.Fatalf("extra = %v, want empty", extra)
	}
}

func TestParseRunnerAuthQuickFlowSkipsSubcommands(t *testing.T) {
	if _, _, _, ok := parseRunnerAuthQuickFlow([]string{"status", "codex"}); ok {
		t.Fatal("status subcommand should not be parsed as quick flow")
	}
	if _, _, _, ok := parseRunnerAuthQuickFlow([]string{"setup", "codex"}); ok {
		t.Fatal("setup subcommand should not be parsed as quick flow")
	}
}

func TestParseRunnerAuthQuickFlowRejectsUnsupportedRunner(t *testing.T) {
	if _, _, _, ok := parseRunnerAuthQuickFlow([]string{"test", "opencode"}); ok {
		t.Fatal("opencode should not use the browser-auth quick flow")
	}
}
