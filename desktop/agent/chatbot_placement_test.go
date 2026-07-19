package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatBotDefersTelegramTaskWhenCloudPlacementSelected(t *testing.T) {
	const prompt = "fix the login bug in auth.ts"
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
	cb := NewChatBot(tm, nil, nil, nil, ChatBotPlacementConfig{
		ConvexURL:     backend.URL,
		Token:         "owner-token",
		LocalDeviceID: "relay-device",
		WorkDir:       t.TempDir(),
	})

	reply := cb.createTask(prompt)
	if !strings.Contains(reply, "Cloud Workspace is selected") {
		t.Fatalf("reply = %q, want Cloud Workspace handoff", reply)
	}
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("local tasks = %d, want 0", got)
	}
	wantSeen := []string{"/tasks/placement/preview", "/tasks/placement/record", "/tasks/placement/activate"}
	if strings.Join(seen, ",") != strings.Join(wantSeen, ",") {
		t.Fatalf("seen paths = %v, want %v", seen, wantSeen)
	}
	for _, body := range bodies {
		if strings.Contains(body, prompt) || strings.Contains(body, "auth.ts") || strings.Contains(body, "login bug") {
			t.Fatalf("placement body leaked prompt: %s", body)
		}
	}
}
