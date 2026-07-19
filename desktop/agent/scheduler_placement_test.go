package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSchedulerPausesClassicTaskWhenCloudPlacementSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var seen []string
	var metadataPayloads []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch r.URL.Path {
		case "/tasks/placement/preview":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_standard",
				"resourceClass":  "standard",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_standard",
				"resourceClass":  "standard",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			metadataPayloads = append(metadataPayloads, body)
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":             false,
				"action":         "runner_auth_required",
				"targetDeviceId": "cloud-dev",
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before scheduled tasks can run.",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewScheduler(tm)
	s.SetPlacementBackend(backend.URL, "owner-token", "local-dev")
	st := &ScheduledTask{
		ID:          "sched-1",
		Title:       "summarize private project",
		Description: "read the private repo and summarize",
		Runner:      "codex",
		Status:      "scheduled",
		NextRunAt:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		CreatedAt:   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}

	s.executeScheduled(st)

	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local scheduled task, got %d", len(tasks))
	}
	if st.Status != "paused" {
		t.Fatalf("status = %q, want paused", st.Status)
	}
	if st.LastTaskID == "" || !strings.HasPrefix(st.LastTaskID, "pending-cloud:") {
		t.Fatalf("LastTaskID = %q", st.LastTaskID)
	}
	if !strings.Contains(st.PausedReason, "cloud workspace required") {
		t.Fatalf("PausedReason = %q", st.PausedReason)
	}
	if len(st.History) != 1 || st.History[0].Status != "cloud_workspace_required" {
		t.Fatalf("history = %#v", st.History)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	if len(seen) != 3 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}
