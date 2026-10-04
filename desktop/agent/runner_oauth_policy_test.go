package main

import "testing"

func TestRemoteRunnerOAuthPolicyCannotBeEnabledByPlatformConfig(t *testing.T) {
	applyProductPolicy(PlatformProductPolicy{})
	if remoteRunnerOAuthEnabled() || runnerSupportsBrowserAuth("claude") || runnerSupportsBrowserAuth("codex") {
		t.Fatal("remote runner OAuth must default off")
	}

	applyProductPolicy(PlatformProductPolicy{RemoteRunnerOAuthEnabled: true})
	if remoteRunnerOAuthEnabled() || runnerSupportsBrowserAuth("claude") || runnerSupportsBrowserAuth("codex") {
		t.Fatal("server-side config must not re-enable runner OAuth transport")
	}
}

func TestOpenCodeIsTheAgentFallback(t *testing.T) {
	if defaultRunner.RunnerID != "opencode" {
		t.Fatalf("default runner = %q, want opencode", defaultRunner.RunnerID)
	}
	if got := yaverDefaultModelForRunner("opencode"); got != "deepseek/deepseek-chat" {
		t.Fatalf("default OpenCode model = %q", got)
	}
}
