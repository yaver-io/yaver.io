package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetTaskSnapshotSyncState(t *testing.T, previous *convexSyncer) {
	t.Helper()
	globalTaskSnapshotSync.mu.Lock()
	globalTaskSnapshotSync.lastHash = 0
	globalTaskSnapshotSync.lastSentAt = time.Time{}
	globalTaskSnapshotSync.sent = false
	globalTaskSnapshotSync.mu.Unlock()
	globalConvexSync = previous
}

func TestReconcileTaskTombstonesClosesTaskAfterBoxReconnects(t *testing.T) {
	const token = "session-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/task-tombstones" || r.URL.Query().Get("deviceId") != "box one" {
			t.Fatalf("unexpected tombstone request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("missing bearer authentication")
		}
		_, _ = w.Write([]byte(`[{"taskId":"offline-task","deletedAt":123}]`))
	}))
	defer server.Close()
	tm := &TaskManager{tasks: map[string]*Task{
		"offline-task": {ID: "offline-task", Status: TaskStatusReview, CreatedAt: time.Now()},
	}}
	syncer := &convexSyncer{convexURL: server.URL, authToken: token, deviceID: "box one", client: server.Client()}
	syncer.reconcileTaskTombstonesFromConvex(context.Background(), tm)
	if tm.tasks["offline-task"].DeletedAt == nil {
		t.Fatal("central tombstone did not close and tombstone the local task")
	}
}

func TestTaskSnapshotConvexPayloadIsPromptFreeAndDeduplicated(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	previous := globalConvexSync
	globalConvexSync = &convexSyncer{deviceID: "device-1"}
	defer resetTaskSnapshotSyncState(t, previous)

	now := time.Now()
	tm := &TaskManager{tasks: map[string]*Task{
		"task-1": {
			ID: "task-1", YaverSessionID: "session-1", Status: TaskStatusRunning,
			Title: "/Users/private/secret title", Description: "private prompt",
			PromptText: "private source and credentials", Output: "private output",
			CreatedAt: now, LastActiveAt: now,
		},
	}}

	syncTaskSnapshotToConvex(context.Background(), tm)
	tm.mu.Lock()
	tm.tasks["task-1"].LastActiveAt = now.Add(time.Minute)
	tm.mu.Unlock()
	syncTaskSnapshotToConvex(context.Background(), tm)
	if len(*buf) != 1 {
		t.Fatalf("output-only activity made %d Convex mutations, want 1", len(*buf))
	}
	rec := (*buf)[0]
	if rec.Path != taskSnapshotRecorderPath {
		t.Fatalf("mutation path = %q", rec.Path)
	}
	raw, err := json.Marshal(rec.Args)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	rec.Args = generic
	assertNoForbiddenFields(t, rec)
	assertNoAbsolutePaths(t, rec)

	rows, ok := generic["tasks"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("tasks payload = %#v", generic["tasks"])
	}
	row := rows[0].(map[string]interface{})
	if len(row) != 5 || row["taskId"] != "task-1" || row["yaverSessionId"] != "session-1" || row["status"] != "running" || row["hostKind"] != "runner_process" {
		t.Fatalf("unexpected lifecycle row: %#v", row)
	}

	tm.mu.Lock()
	tm.tasks["task-1"].Status = TaskStatusReview
	tm.mu.Unlock()
	syncTaskSnapshotToConvex(context.Background(), tm)
	if len(*buf) != 2 {
		t.Fatalf("lifecycle transition made %d mutations, want 2", len(*buf))
	}
}

func TestTaskSnapshotPostsToAuthenticatedSiteRoute(t *testing.T) {
	const token = "yaver-session-token"
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/task-snapshots" {
			t.Errorf("path = %q, want /task-snapshots", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		} else {
			if len(body) != 3 {
				t.Errorf("body keys = %#v, want only deviceId, observedAt, tasks", body)
			}
			for _, key := range []string{"deviceId", "observedAt", "tasks"} {
				if _, ok := body[key]; !ok {
					t.Errorf("body missing %q: %#v", key, body)
				}
			}
			if body["deviceId"] != "device-http" {
				t.Errorf("deviceId = %#v", body["deviceId"])
			}
			rows, ok := body["tasks"].([]interface{})
			if !ok || len(rows) != 1 {
				t.Errorf("tasks = %#v", body["tasks"])
			}
		}
		requestSeen <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	previous := globalConvexSync
	globalConvexSync = &convexSyncer{
		convexURL: server.URL + "/",
		authToken: token,
		deviceID:  "device-http",
		client:    server.Client(),
	}
	defer resetTaskSnapshotSyncState(t, previous)
	tm := &TaskManager{tasks: map[string]*Task{
		"task-http": {
			ID: "task-http", Status: TaskStatusReady,
			Title: "private title", PromptText: "private prompt", Output: "private output",
			CreatedAt: time.Now(),
		},
	}}

	syncTaskSnapshotToConvex(context.Background(), tm)
	select {
	case <-requestSeen:
	default:
		t.Fatal("snapshot request was not received")
	}
}

func TestTaskSnapshotSuccessClearsPriorErrorAndKeepsCounters(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt++
		if attempt == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	syncer := &convexSyncer{convexURL: server.URL, client: server.Client()}
	payload := map[string]interface{}{
		"deviceId": "device-counters", "observedAt": time.Now().UnixMilli(), "tasks": []interface{}{},
	}
	if syncer.publishTaskSnapshot(context.Background(), payload) {
		t.Fatal("first publish succeeded, want HTTP failure")
	}
	if syncer.lastError == "" {
		t.Fatal("failed publish did not record lastError")
	}
	if !syncer.publishTaskSnapshot(context.Background(), payload) {
		t.Fatal("second publish failed, want success")
	}
	if syncer.failCount != 1 || syncer.successCount != 1 {
		t.Fatalf("counters = success %d, fail %d; want 1 each", syncer.successCount, syncer.failCount)
	}
	if syncer.lastError != "" {
		t.Fatalf("successful publish left stale lastError %q", syncer.lastError)
	}
}

func TestTaskSnapshotConstructionErrorsAreCounted(t *testing.T) {
	tests := []struct {
		name        string
		syncer      *convexSyncer
		payload     map[string]interface{}
		wantMessage string
	}{
		{
			name:        "marshal",
			syncer:      &convexSyncer{},
			payload:     map[string]interface{}{"unsupported": make(chan int)},
			wantMessage: "marshal payload",
		},
		{
			name:        "request",
			syncer:      &convexSyncer{convexURL: "http://[::1"},
			payload:     map[string]interface{}{},
			wantMessage: "construct request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.syncer.publishTaskSnapshot(context.Background(), tt.payload) {
				t.Fatal("publish succeeded, want construction failure")
			}
			if tt.syncer.failCount != 1 || tt.syncer.successCount != 0 {
				t.Fatalf("counters = success %d, fail %d; want 0 success and 1 fail", tt.syncer.successCount, tt.syncer.failCount)
			}
			if !strings.Contains(tt.syncer.lastError, tt.wantMessage) {
				t.Fatalf("lastError = %q, want %q", tt.syncer.lastError, tt.wantMessage)
			}
		})
	}
}

func TestTaskSnapshotExcludesDeletedTasks(t *testing.T) {
	deletedAt := time.Now()
	tm := &TaskManager{tasks: map[string]*Task{
		"live": {ID: "live", YaverSessionID: "session-live", Status: TaskStatusReady, CreatedAt: deletedAt},
		"gone": {ID: "gone", YaverSessionID: "session-gone", Status: TaskStatusStopped, CreatedAt: deletedAt, DeletedAt: &deletedAt},
	}}
	rows := localTaskLifecycleSnapshot(tm)
	if len(rows) != 1 || rows[0].TaskID != "live" {
		t.Fatalf("snapshot should contain only live task, got %#v", rows)
	}
}
