package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFinalizeBlocksInitialTaskWhenCloudPlacementSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const objective = "ship the login fix in auth.ts"
	var seen []string
	var bodies []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_wake",
				"targetDeviceId": "cloud-device",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-recorded",
				"lane":           "cloud_wake",
				"targetDeviceId": "cloud-device",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"activation": map[string]any{"status": "queued"},
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	mgr := NewFinalizeManager(tm)
	mgr.SetPlacementConfig(TaskIngressPlacementConfig{
		ConvexURL:     backend.URL,
		Token:         "owner-token",
		LocalDeviceID: "relay-device",
		WorkDir:       t.TempDir(),
	})
	now := time.Now().UTC().Format(time.RFC3339)
	run := &FinalizeRun{
		ID:              "fin-test",
		Objective:       objective,
		Runner:          "codex",
		WorkDir:         t.TempDir(),
		Status:          FinalizeQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
		MaxIterations:   3,
		MaxWallClockMin: 60,
		KickIntervalSec: 1,
	}
	mgr.mu.Lock()
	mgr.runs[run.ID] = run
	mgr.mu.Unlock()

	mgr.tickRun(context.Background(), run.ID)

	got, ok := mgr.GetRun(run.ID)
	if !ok {
		t.Fatal("finalize run disappeared")
	}
	if got.Status != FinalizeBlocked {
		t.Fatalf("status = %s, want %s", got.Status, FinalizeBlocked)
	}
	if !strings.HasPrefix(got.TaskID, "pending-cloud:") {
		t.Fatalf("task id = %q, want pending-cloud id", got.TaskID)
	}
	if !strings.Contains(got.LastError, "Cloud Workspace is selected") {
		t.Fatalf("last error = %q, want Cloud Workspace blocker", got.LastError)
	}
	if len(tm.ListTasks()) != 0 {
		t.Fatalf("local task count = %d, want 0", len(tm.ListTasks()))
	}
	wantSeen := []string{"/tasks/placement/preview", "/tasks/placement/record", "/tasks/placement/activate"}
	if strings.Join(seen, ",") != strings.Join(wantSeen, ",") {
		t.Fatalf("seen paths = %v, want %v", seen, wantSeen)
	}
	for i, body := range bodies {
		for _, forbidden := range []string{objective, "auth.ts", "login fix"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("placement body leaked objective fragment %q: %s", forbidden, body)
			}
		}
		if i < 2 && !strings.Contains(body, `"requestedRunner":"codex"`) {
			t.Fatalf("placement body missing requested runner: %s", body)
		}
	}
}
