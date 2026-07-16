package main

import (
	"strings"
	"testing"
)

// An unattended loop that kicks a runner every few minutes turns a stray
// ANTHROPIC_API_KEY into an unbounded bill — silently, because the CLI just
// works. The key must not survive into a subscription runner's environment.
func TestSanitizeRunnerEnvStripsMeteredKeysFromSubscriptionRunners(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-should-never-reach-the-runner",
		"ANTHROPIC_AUTH_TOKEN=also-metered",
		"HOME=/Users/dev",
	}
	clean, stripped := sanitizeRunnerEnv(env, "claude")

	for _, kv := range clean {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN") {
			t.Fatalf("metered key survived into claude's environment: %q", kv)
		}
	}
	if len(clean) != 2 {
		t.Fatalf("non-billing env must be preserved, got %q", clean)
	}
	if len(stripped) != 2 {
		t.Fatalf("stripping must be reported, not silent: %q", stripped)
	}
	// The report names the variable; it must never carry the secret itself.
	for _, s := range stripped {
		if strings.Contains(s, "sk-") || strings.Contains(s, "=") {
			t.Fatalf("stripped report leaked a credential value: %q", s)
		}
	}
}

func TestSanitizeRunnerEnvStripsOpenAIKeyFromCodex(t *testing.T) {
	clean, stripped := sanitizeRunnerEnv([]string{"OPENAI_API_KEY=sk-x", "PATH=/usr/bin"}, "codex")
	if len(stripped) != 1 || stripped[0] != "OPENAI_API_KEY" {
		t.Fatalf("codex must run on its sub-login, got stripped=%q", stripped)
	}
	if len(clean) != 1 || clean[0] != "PATH=/usr/bin" {
		t.Fatalf("unexpected clean env: %q", clean)
	}
}

// GLM is key-based by design; the guard must not break it.
func TestSanitizeRunnerEnvLeavesKeyBasedRunnersAlone(t *testing.T) {
	env := []string{"ANTHROPIC_API_KEY=sk-x", "ZAI_API_KEY=z", "PATH=/usr/bin"}
	for _, runner := range []string{"glm", "opencode"} {
		clean, stripped := sanitizeRunnerEnv(env, runner)
		if len(stripped) != 0 {
			t.Fatalf("%s is key-based by design and must keep its env, stripped=%q", runner, stripped)
		}
		if len(clean) != len(env) {
			t.Fatalf("%s env was modified: %q", runner, clean)
		}
	}
}
