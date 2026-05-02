package main

import "testing"

func TestNormalizePrimaryRunnerQuickArg(t *testing.T) {
	if got := normalizePrimaryRunnerQuickArg("codex"); got != "codex" {
		t.Fatalf("codex => %q, want codex", got)
	}
	if got := normalizePrimaryRunnerQuickArg("claude-code"); got != "claude" {
		t.Fatalf("claude-code => %q, want claude", got)
	}
	if got := normalizePrimaryRunnerQuickArg("set"); got != "" {
		t.Fatalf("set => %q, want empty", got)
	}
}
