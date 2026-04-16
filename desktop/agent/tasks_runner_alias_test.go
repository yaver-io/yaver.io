package main

import "testing"

func TestGetRunnerConfigNormalizesClaudeCodeAlias(t *testing.T) {
	got := GetRunnerConfig("claude-code")
	if got.RunnerID != "claude" {
		t.Fatalf("expected claude-code alias to resolve to claude runner, got %q", got.RunnerID)
	}
}
