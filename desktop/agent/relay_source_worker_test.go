package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRelaySourceWorkerEnabledRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("YAVER_RELAY_SOURCE_WORKER", "")
	if relaySourceWorkerEnabled(&Config{}) {
		t.Fatal("worker should default off")
	}
	if !relaySourceWorkerEnabled(&Config{RelaySourceWorker: &RelaySourceWorkerConfig{Enabled: true}}) {
		t.Fatal("config enabled should turn worker on")
	}
	t.Setenv("YAVER_RELAY_SOURCE_WORKER", "0")
	if relaySourceWorkerEnabled(&Config{RelaySourceWorker: &RelaySourceWorkerConfig{Enabled: true}}) {
		t.Fatal("env false should override config")
	}
	t.Setenv("YAVER_RELAY_SOURCE_WORKER", "1")
	if !relaySourceWorkerEnabled(&Config{}) {
		t.Fatal("env true should turn worker on")
	}
}

func TestRelaySourceWorkerIntervalIsClamped(t *testing.T) {
	t.Setenv("YAVER_RELAY_SOURCE_WORKER_INTERVAL", "")
	if got := relaySourceWorkerInterval(&Config{}); got != defaultRelaySourceWorkerInterval {
		t.Fatalf("default interval = %s", got)
	}
	if got := relaySourceWorkerInterval(&Config{RelaySourceWorker: &RelaySourceWorkerConfig{IntervalSeconds: 1}}); got != minRelaySourceWorkerInterval {
		t.Fatalf("min clamp = %s", got)
	}
	if got := relaySourceWorkerInterval(&Config{RelaySourceWorker: &RelaySourceWorkerConfig{IntervalSeconds: 9999}}); got != maxRelaySourceWorkerInterval {
		t.Fatalf("max clamp = %s", got)
	}
	t.Setenv("YAVER_RELAY_SOURCE_WORKER_INTERVAL", "6s")
	if got := relaySourceWorkerInterval(&Config{}); got != 6*time.Second {
		t.Fatalf("duration env interval = %s", got)
	}
}

func TestRelaySourceWorkerTickPreparesPromptFreeIntent(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	statuses := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/relay-source-intents/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "local-1",
				"status":      "claimed",
				"branch":      "yaver/source/worker",
				"baseBranch":  "main",
			})
		case "/tasks/relay-source-intents/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if s, _ := body["status"].(string); s != "" {
				statuses = append(statuses, s)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "local-1",
				"status":      body["status"],
				"branch":      "yaver/source/worker",
				"baseBranch":  "main",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{
		token:     "tok",
		convexURL: backend.URL,
		deviceID:  "device-1",
		taskMgr:   &TaskManager{workDir: workDir},
	}
	result, err := relaySourceWorkerTick(context.Background(), s, "", "relay-test")
	if err != nil {
		t.Fatalf("relaySourceWorkerTick: %v", err)
	}
	if result == nil || !result.OK || result.Prepare == nil || result.Plan == nil || result.Plan.Mode != "prepare_only" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Prepare.Commit != meta.LastCommit {
		t.Fatalf("prepared commit = %q, want %q", result.Prepare.Commit, meta.LastCommit)
	}
	if len(statuses) != 1 || statuses[0] != "handoff_ready" {
		t.Fatalf("statuses = %+v", statuses)
	}
	if out, err := managedGitCmd("", "--git-dir", meta.BarePath, "show-ref", "--verify", "--quiet", "refs/heads/yaver/source/worker"); err != nil {
		t.Fatalf("branch not prepared: %s: %v", out, err)
	}
}

func TestRelaySourceWorkerTickMarksFeedbackItemBranchCreated(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	feedbackStatuses := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/relay-source-intents/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "feedback:feedback-1",
				"status":      "claimed",
				"branch":      "yaver/source/from-feedback",
				"baseBranch":  "main",
			})
		case "/tasks/relay-source-intents/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "feedback:feedback-1",
				"status":      "handoff_ready",
				"branch":      "yaver/source/from-feedback",
				"baseBranch":  "main",
			})
		case "/feedback-work-items/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["itemId"] != "feedback-1" {
				t.Fatalf("feedback itemId = %v", body["itemId"])
			}
			if _, leaked := body["body"]; leaked {
				t.Fatal("relay worker must not send feedback body to feedback status update")
			}
			if status, _ := body["status"].(string); status != "" {
				feedbackStatuses = append(feedbackStatuses, status)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": body["status"],
				"branch": body["branch"],
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{
		token:     "tok",
		convexURL: backend.URL,
		deviceID:  "device-1",
		taskMgr:   &TaskManager{workDir: workDir},
	}
	result, err := relaySourceWorkerTick(context.Background(), s, "", "relay-test")
	if err != nil {
		t.Fatalf("relaySourceWorkerTick: %v", err)
	}
	if result == nil || result.Prepare == nil || result.Prepare.Commit != meta.LastCommit {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(feedbackStatuses) != 1 || feedbackStatuses[0] != "branch_created" {
		t.Fatalf("feedback statuses = %+v", feedbackStatuses)
	}
}
