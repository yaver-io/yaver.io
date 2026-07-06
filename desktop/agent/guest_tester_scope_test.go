package main

import "testing"

// TestSDKProjectTesterScope locks in the "tester" tier contract: an sdk-project
// guest can RUN the dev's pre-release app (reload/status/events) and file
// feedback, but must NOT reach code-exec, project enumeration, deploy, the
// full dev-server proxy subtree, or the vault. This is the invited-friend
// pre-release-testing surface — see guestSDKProjectAllowedPrefixes.
func TestSDKProjectTesterScope(t *testing.T) {
	mustAllow := []string{
		"/dev/reload",
		"/dev/reload-app",
		"/dev/status",
		"/dev/events",
		"/feedback",
		"/feedback/stream",
		"/blackbox/event",
		"/info",
		"/health",
	}
	for _, path := range mustAllow {
		if !isGuestAllowedPathForScope(path, GuestScopeSDKProject) {
			t.Errorf("tester (sdk-project) SHOULD reach %q (run app + feedback)", path)
		}
	}

	// The tester tier must NOT widen into code-exec / enumeration / proxy.
	// Note /dev/start and the bare /dev/ subtree stay blocked — only the
	// narrow reload/status/events endpoints are allowed, never the full
	// dev-server proxy.
	mustBlock := []string{
		"/tasks",
		"/tasks/abc-123",
		"/dev/",
		"/dev/start",
		"/dev/build-native",
		"/dev/native-bundle",
		"/builds",
		"/projects",
		"/repos/list",
		"/repos/clone",
		"/deploy/ship",
		"/vault",
		"/exec",
	}
	for _, path := range mustBlock {
		if isGuestAllowedPathForScope(path, GuestScopeSDKProject) {
			t.Errorf("tester (sdk-project) should NOT reach %q", path)
		}
	}
}

// TestCanVibeGate proves /vibing is gated on the per-grant canVibe flag for the
// tester tier, and that the flag can never widen any OTHER scope.
func TestCanVibeGate(t *testing.T) {
	vibePaths := []string{"/vibing", "/vibing/execute", "/vibing/task/abc"}

	// Tester WITHOUT canVibe: /vibing blocked.
	for _, p := range vibePaths {
		if isGuestAllowedPathForScopeVibe(p, GuestScopeSDKProject, false) {
			t.Errorf("tester without canVibe should NOT reach %q", p)
		}
	}
	// Tester WITH canVibe: /vibing allowed.
	for _, p := range vibePaths {
		if !isGuestAllowedPathForScopeVibe(p, GuestScopeSDKProject, true) {
			t.Errorf("tester with canVibe SHOULD reach %q", p)
		}
	}

	// canVibe must NOT leak the vibe surface into any other scope, even set true.
	for _, scope := range []string{GuestScopeFeedbackOnly, GuestScopeDeploy, GuestScopeSupport, GuestScopeCircuit} {
		for _, p := range vibePaths {
			if isGuestAllowedPathForScopeVibe(p, scope, true) {
				t.Errorf("canVibe=true must not unlock %q for scope %q", p, scope)
			}
		}
	}

	// canVibe on a tester must NOT accidentally widen non-vibe surfaces.
	if isGuestAllowedPathForScopeVibe("/exec", GuestScopeSDKProject, true) {
		t.Error("canVibe must not unlock /exec for tester")
	}
	if isGuestAllowedPathForScopeVibe("/tasks", GuestScopeSDKProject, true) {
		t.Error("canVibe must not unlock /tasks for tester")
	}
}

// TestFeedbackOnlyUnchangedBySplit guards the regression risk from splitting
// sdk-project out of the feedback-only allow-list: feedback-only must keep its
// exact tight surface (no /dev/reload leaked in).
func TestFeedbackOnlyUnchangedBySplit(t *testing.T) {
	if isGuestAllowedPathForScope("/dev/reload", GuestScopeFeedbackOnly) {
		t.Error("feedback-only must NOT reach /dev/reload after the tester split")
	}
	if isGuestAllowedPathForScopeVibe("/vibing", GuestScopeFeedbackOnly, true) {
		t.Error("feedback-only must NOT reach /vibing even with canVibe=true")
	}
	for _, p := range []string{"/feedback", "/blackbox/event", "/info", "/health"} {
		if !isGuestAllowedPathForScope(p, GuestScopeFeedbackOnly) {
			t.Errorf("feedback-only should still reach %q", p)
		}
	}
}

// TestGuestCanVibeHelper checks the config-flag reader defaults to false and
// reflects an explicit opt-in.
func TestGuestCanVibeHelper(t *testing.T) {
	tru := true
	fls := false
	mgr := &GuestConfigManager{configs: map[string]*GuestConfig{
		"none": {GuestUserID: "none"},
		"on":   {GuestUserID: "on", CanVibe: &tru},
		"off":  {GuestUserID: "off", CanVibe: &fls},
	}}
	if mgr.GuestCanVibe("none") {
		t.Error("nil CanVibe must default to false")
	}
	if !mgr.GuestCanVibe("on") {
		t.Error("CanVibe=true must report true")
	}
	if mgr.GuestCanVibe("off") {
		t.Error("CanVibe=false must report false")
	}
	if mgr.GuestCanVibe("missing") {
		t.Error("unknown guest must default to false")
	}
	var nilMgr *GuestConfigManager
	if nilMgr.GuestCanVibe("x") {
		t.Error("nil manager must report false")
	}
}

// TestIsSubscriptionRunner locks the classification that keeps a guest's vibe
// off the owner's personal plan: claude/codex are subscription (owner-only);
// glm/opencode are GLM/BYO (lendable).
func TestIsSubscriptionRunner(t *testing.T) {
	for _, id := range []string{"claude", "claude-code", "codex"} {
		if !isSubscriptionRunner(id) {
			t.Errorf("%q must be classified subscription (owner-only for guests)", id)
		}
	}
	for _, id := range []string{"glm", "opencode", "aider", "ollama"} {
		if isSubscriptionRunner(id) {
			t.Errorf("%q must NOT be subscription (GLM/BYO is lendable to guests)", id)
		}
	}
}
