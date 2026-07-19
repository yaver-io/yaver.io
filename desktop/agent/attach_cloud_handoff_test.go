package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAttachGetTaskPreservesRemoteHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/task-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Relay-Password"); got != "relay-secret" {
			t.Fatalf("relay password header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task": map[string]any{
				"id":     "task-1",
				"title":  "build",
				"status": "running",
				"output": "hello",
			},
		})
	}))
	defer srv.Close()

	task, err := attachGetTask(srv.URL, "owner-token", "task-1", map[string]string{
		"X-Relay-Password": "relay-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "task-1" || task.Output != "hello" {
		t.Fatalf("task = %#v", task)
	}
}

func TestCodeTerminalApplyPollFetchesRemoteActiveTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/task-remote" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Relay-Password"); got != "relay-secret" {
			t.Fatalf("relay password header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task": map[string]any{
				"id":         "task-remote",
				"title":      "build",
				"status":     "completed",
				"output":     "done",
				"resultText": "done",
			},
		})
	}))
	defer srv.Close()

	s := &codeTerminalSession{
		token:         "owner-token",
		baseURL:       "http://127.0.0.1:18080",
		knownTasks:    map[string]bool{"task-remote": true},
		lastOutputLen: map[string]int{"task-remote": 0},
		taskRefs: map[string]*attachTaskRef{
			"task-remote": {ID: "task-remote", BaseURL: srv.URL, Headers: map[string]string{"X-Relay-Password": "relay-secret"}},
		},
		activeTask:  "task-remote",
		sessionTask: "task-remote",
		firstDraw:   true,
	}
	s.applyPoll(nil)
	if s.lastOutputLen["task-remote"] != len("done") {
		t.Fatalf("lastOutputLen = %d", s.lastOutputLen["task-remote"])
	}
	if s.activeTask != "" {
		t.Fatalf("activeTask = %q, want cleared", s.activeTask)
	}
	if s.sessionTask != "" {
		t.Fatalf("sessionTask = %q, want cleared for remote handoff task", s.sessionTask)
	}
}
