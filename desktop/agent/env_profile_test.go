package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEnvironmentProfileEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/agent/env-profile", "tok", "")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}
	profile, ok := body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected profile object, got %T", body["profile"])
	}
	if profile["platform"] == "" || profile["platform"] == nil {
		t.Fatal("expected profile.platform to be set")
	}
	if profile["generatedAt"] == "" || profile["generatedAt"] == nil {
		t.Fatal("expected profile.generatedAt to be set")
	}
	if profile["sourceDeviceId"] != "test-device-id" {
		t.Fatalf("expected sourceDeviceId=test-device-id, got %v", profile["sourceDeviceId"])
	}
}

func TestEnvironmentProfileApplyDryRunManualStepsAndSync(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := NewHTTPServer(0, "tok", "user", "device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	profile := EnvironmentProfile{
		Platform: "darwin",
		Binaries: []DetectedBinary{
			{Name: "git", Path: "/usr/bin/git"},
		},
		Runners: []EnvironmentRunnerSummary{
			{ID: "codex", Installed: true, Ready: true},
			{ID: "claude", Installed: true, Ready: true},
		},
		DiscoveredProjects: []EnvironmentProjectSummary{
			{Path: "/Users/test/work/project-a"},
		},
	}
	syncPayload := map[string][]SyncItem{
		"flags": []SyncItem{
			{
				Key:       "feature.managedClone",
				Value:     json.RawMessage(`true`),
				UpdatedAt: 123,
				UpdatedBy: "source-device",
			},
		},
	}
	if err := saveGitCredentials([]GitCredential{
		{Host: "gitlab.com", Username: "oauth2", Token: "old-token"},
	}); err != nil {
		t.Fatalf("seed git credentials: %v", err)
	}

	result := applyEnvironmentProfile(context.Background(), srv.convexURL, profile, false, syncPayload, []GitCredential{
		{Host: "github.com", Username: "x-access-token", Token: "ghp_test"},
	}, true, true)
	if !result.OK {
		t.Fatal("expected ok=true")
	}
	if result.TargetPlatform == "" {
		t.Fatal("expected target platform")
	}
	if len(result.ProjectHints) != 1 || result.ProjectHints[0] != "/Users/test/work/project-a" {
		t.Fatalf("unexpected project hints: %#v", result.ProjectHints)
	}
	if len(result.ImportedSyncKinds) != 1 || result.ImportedSyncKinds[0] != "flags" {
		t.Fatalf("unexpected imported sync kinds: %#v", result.ImportedSyncKinds)
	}
	manual := strings.Join(result.ManualSteps, "\n")
	if !strings.Contains(manual, "Codex") {
		t.Fatalf("expected Codex manual step, got %#v", result.ManualSteps)
	}
	if !strings.Contains(manual, "Claude Code") {
		t.Fatalf("expected Claude manual step, got %#v", result.ManualSteps)
	}
	notes := strings.Join(result.Notes, "\n")
	if !strings.Contains(notes, "Cross-platform clone") {
		t.Fatalf("expected cross-platform note, got %#v", result.Notes)
	}
	if len(result.ImportedGitHosts) != 1 || result.ImportedGitHosts[0] != "github.com" {
		t.Fatalf("unexpected imported git hosts: %#v", result.ImportedGitHosts)
	}
	if len(result.RemovedGitHosts) != 1 || result.RemovedGitHosts[0] != "gitlab.com" {
		t.Fatalf("unexpected removed git hosts: %#v", result.RemovedGitHosts)
	}
}
