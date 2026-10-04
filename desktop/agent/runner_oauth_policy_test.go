package main

import "testing"

func TestRemoteRunnerOAuthPolicyDefaultsClosedAndFollowsPlatformConfig(t *testing.T) {
	applyProductPolicy(PlatformProductPolicy{})
	if remoteRunnerOAuthEnabled() || runnerSupportsBrowserAuth("claude") || runnerSupportsBrowserAuth("codex") {
		t.Fatal("remote runner OAuth must default off")
	}

	applyProductPolicy(PlatformProductPolicy{RemoteRunnerOAuthEnabled: true})
	t.Cleanup(func() { applyProductPolicy(PlatformProductPolicy{}) })
	if !remoteRunnerOAuthEnabled() || !runnerSupportsBrowserAuth("claude") || !runnerSupportsBrowserAuth("codex") {
		t.Fatal("Convex product policy must be able to re-enable the dormant compatibility flow")
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
