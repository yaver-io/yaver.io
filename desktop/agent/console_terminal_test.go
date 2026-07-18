package main

import (
	"strings"
	"testing"
)

func TestTerminalLaunchCommand(t *testing.T) {
	tests := []struct {
		runner string
		want   []string
	}{
		{"claude", []string{"tmux new-session -A -s yaver-claude", "claude --dangerously-skip-permissions"}},
		{"claude-code", []string{"tmux new-session -A -s yaver-claude", "claude --dangerously-skip-permissions"}},
		{"codex", []string{"tmux new-session -A -s yaver-codex", "codex --dangerously-bypass-approvals-and-sandbox"}},
		{"opencode", []string{"tmux new-session -A -s yaver-opencode", "opencode --auto"}},
	}

	for _, tt := range tests {
		got := terminalLaunchCommand(tt.runner)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Fatalf("terminalLaunchCommand(%q) = %q, want substring %q", tt.runner, got, want)
			}
		}
	}

	if got := terminalLaunchCommand("bash"); got != "" {
		t.Fatalf("terminalLaunchCommand for unsupported runner = %q, want empty", got)
	}
}
