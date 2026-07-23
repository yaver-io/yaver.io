package main

import "testing"

// Per-session model detection reads the agent's argv — the runner-agnostic,
// reliable source (a live `claude` renames its process, but ps preserves argv).
func TestExtractModelFromArgv(t *testing.T) {
	cases := map[string]string{
		"claude --model claude-opus-4-8 --dangerously-skip-permissions": "claude-opus-4-8",
		"zsh -c opencode -m glm-5.2 run":                                "glm-5.2",
		"codex --model=gpt-5.5 exec":                                    "gpt-5.5",
		"opencode -m=zhipu/glm-4.6":                                     "zhipu/glm-4.6",
		"claude --dangerously-skip-permissions":                         "", // no explicit model
		"just a shell":                                                  "",
	}
	for cmd, want := range cases {
		if got := extractModelFromArgv(cmd); got != want {
			t.Fatalf("extractModelFromArgv(%q) = %q, want %q", cmd, got, want)
		}
	}
}
