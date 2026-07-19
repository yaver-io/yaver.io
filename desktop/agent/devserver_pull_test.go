package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestHotReloadPullSkipsWhenCloudPlacementSelected(t *testing.T) {
	var seen []string
	var payload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if r.URL.Path != "/tasks/placement/preview" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "placement-preview",
			"lane":           "cloud_build",
			"resourceClass":  "build",
			"targetDeviceId": "cloud-dev",
			"wakeRequired":   true,
		})
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	skip, summary := s.shouldSkipHotReloadPullForCloudPlacement(context.Background(), t.TempDir(), "codex")
	if !skip {
		t.Fatalf("skip = false, summary = %q", summary)
	}
	if summary == "" {
		t.Fatal("expected skip summary")
	}
	if len(seen) != 1 || seen[0] != "/tasks/placement/preview" {
		t.Fatalf("paths = %#v", seen)
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("metadata payload leaked %q: %#v", forbidden, payload)
		}
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local helper task, got %d", len(tasks))
	}
}
