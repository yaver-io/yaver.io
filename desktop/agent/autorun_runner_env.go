package main

import "strings"

// Subscription-only enforcement for the runners that have a subscription.
//
// claude and codex must run on the developer's OWN sub-login, never on a metered
// API key. Yaver is a self-usage dev tool: an API key turns an unattended loop
// that kicks a runner every few minutes into an unbounded bill, and it does it
// silently — the CLI just works, and the invoice arrives later.
//
// This is a guard, not a convention. If ANTHROPIC_API_KEY is exported (a stray
// shell profile, a CI leftover, an MCP server's env), `claude` will happily use
// it. So autorun strips the key from the child's environment: the CLI then has
// no choice but the sub-login. The failure mode we want is "please log in", not
// a surprise invoice.
//
// It deliberately does NOT touch opencode/glm — GLM is key-based by design
// (feedback_subscription_cli_only_compliance: sub token CLI-only, Hermes=GLM).

// apiKeyEnvBannedFor lists the env vars that would route a subscription runner
// onto metered billing.
var apiKeyEnvBannedFor = map[string][]string{
	"claude": {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
	"codex":  {"OPENAI_API_KEY"},
}

// sanitizeRunnerEnv removes the metered-billing keys for subscription runners,
// returning the environment the runner should actually get plus what was
// stripped (so the loop can say so rather than silently changing behavior).
func sanitizeRunnerEnv(env []string, runnerID string) (clean []string, stripped []string) {
	banned := apiKeyEnvBannedFor[normalizeRunnerID(runnerID)]
	if len(banned) == 0 {
		return env, nil
	}
	clean = make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		drop := false
		for _, b := range banned {
			if name == b {
				drop = true
				break
			}
		}
		if drop {
			// Record the NAME only. The value is a live credential and must
			// never reach a log or a commit (project_vault_prompt_echo_bug).
			stripped = append(stripped, name)
			continue
		}
		clean = append(clean, kv)
	}
	return clean, stripped
}
