package main

import "testing"

func TestChooseHotReloadPullRunnerPrefersReadyDefault(t *testing.T) {
	rows := []runnerAuthStatusRow{
		{ID: "claude", Installed: true, Ready: true, AuthConfigured: true},
		{ID: "codex", Installed: true, Ready: true, AuthConfigured: true},
	}
	if got := chooseHotReloadPullRunner("codex", rows); got != "codex" {
		t.Fatalf("chooseHotReloadPullRunner default-ready = %q, want codex", got)
	}
}

func TestChooseHotReloadPullRunnerFallsBackToOtherReadyRunner(t *testing.T) {
	rows := []runnerAuthStatusRow{
		{ID: "claude", Installed: true, Ready: false, AuthConfigured: false},
		{ID: "codex", Installed: true, Ready: true, AuthConfigured: true},
		{ID: "opencode", Installed: true, Ready: false, AuthConfigured: true},
	}
	if got := chooseHotReloadPullRunner("claude", rows); got != "codex" {
		t.Fatalf("chooseHotReloadPullRunner fallback = %q, want codex", got)
	}
}

func TestChooseHotReloadPullRunnerRequiresInstalledReadyAndAuthed(t *testing.T) {
	rows := []runnerAuthStatusRow{
		{ID: "claude", Installed: true, Ready: true, AuthConfigured: false},
		{ID: "codex", Installed: false, Ready: true, AuthConfigured: true},
		{ID: "opencode", Installed: true, Ready: false, AuthConfigured: true},
	}
	if got := chooseHotReloadPullRunner("claude", rows); got != "" {
		t.Fatalf("chooseHotReloadPullRunner no-ready = %q, want empty", got)
	}
}

func TestInterpretHotReloadPullResult(t *testing.T) {
	tests := []struct {
		text    string
		status  string
		updated bool
	}{
		{"PULLED: updated main", "pulled", true},
		{"UP_TO_DATE: already current", "up_to_date", false},
		{"SKIPPED: dirty tree", "skipped", false},
		{"FAILED: auth error", "failed", false},
		{"something else", "unknown", false},
	}
	for _, tc := range tests {
		status, updated := interpretHotReloadPullResult(tc.text)
		if status != tc.status || updated != tc.updated {
			t.Fatalf("interpretHotReloadPullResult(%q) = (%q,%v), want (%q,%v)", tc.text, status, updated, tc.status, tc.updated)
		}
	}
}
