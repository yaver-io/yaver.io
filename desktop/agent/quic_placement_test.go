package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQUICCloudRequiredMessageDefersBeforeLocalTask(t *testing.T) {
	var seen []string
	var recordPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			if err := json.NewDecoder(r.Body).Decode(&recordPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-recorded",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"action": "wake_scheduled",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true
	server := NewQUICServer(0, "owner-token", "host", tm, WithQUICPlacementBackend(backend.URL, "local-dev"))
	msg := IncomingMessage{
		Title:         "build apk",
		Description:   "build apk",
		Source:        "cli-remote",
		Runner:        "codex",
		PlacementKind: "build",
	}
	meta := quicTaskPlacementRequest(msg, msg.Source, tm.workDir, "local-dev")

	resp, placement := server.cloudRequiredMessage(context.Background(), msg, meta)
	if resp == nil {
		t.Fatal("expected cloud required response")
	}
	if resp.Action != "cloud_workspace_required" {
		t.Fatalf("action = %q", resp.Action)
	}
	if resp.PendingTaskID == "" || !strings.HasPrefix(resp.PendingTaskID, "pending-cloud:") {
		t.Fatalf("pendingTaskId = %q", resp.PendingTaskID)
	}
	if placement == nil || placement.TargetDeviceID != "cloud-dev" {
		t.Fatalf("placement = %#v", placement)
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	if len(seen) != 3 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
	if taskID, _ := recordPayload["taskId"].(string); !strings.HasPrefix(taskID, "pending-cloud:") {
		t.Fatalf("record taskId = %#v", recordPayload["taskId"])
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := recordPayload[forbidden]; ok {
			t.Fatalf("record payload leaked %q: %#v", forbidden, recordPayload)
		}
	}
}

func TestQUICCloudRequiredMessagePreservesActivationBlocker(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-recorded",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":             false,
				"action":         "runner_auth_required",
				"targetDeviceId": "cloud-dev",
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before tasks can run.",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true
	server := NewQUICServer(0, "owner-token", "host", tm, WithQUICPlacementBackend(backend.URL, "local-dev"))
	msg := IncomingMessage{
		Title:         "build apk",
		Description:   "build apk",
		Source:        "cli-remote",
		Runner:        "codex",
		PlacementKind: "build",
	}
	meta := quicTaskPlacementRequest(msg, msg.Source, tm.workDir, "local-dev")

	resp, _ := server.cloudRequiredMessage(context.Background(), msg, meta)
	if resp == nil {
		t.Fatal("expected cloud required response")
	}
	if resp.Activation == nil || resp.Activation["action"] != "runner_auth_required" {
		t.Fatalf("activation = %#v", resp.Activation)
	}
	if resp.Activation["targetDeviceId"] != "cloud-dev" {
		t.Fatalf("activation target = %#v", resp.Activation["targetDeviceId"])
	}
}

func TestQUICCloudRequiredErrorFromMessage(t *testing.T) {
	err := quicCloudRequiredErrorFromMessage(OutgoingMessage{
		Type:          "error",
		Action:        "cloud_workspace_required",
		PendingTaskID: "pending-cloud:abc",
		Reason:        "wake scheduled",
		Placement: &TaskPlacementMetadata{
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
	})
	if err == nil {
		t.Fatal("expected cloud workspace error")
	}
	if err.PendingTaskID != "pending-cloud:abc" {
		t.Fatalf("pendingTaskId = %q", err.PendingTaskID)
	}
	if err.Placement == nil || err.Placement.TargetDeviceID != "cloud-dev" {
		t.Fatalf("placement = %#v", err.Placement)
	}
	if !strings.Contains(err.Error(), "wake scheduled") {
		t.Fatalf("error text = %q", err.Error())
	}
}
