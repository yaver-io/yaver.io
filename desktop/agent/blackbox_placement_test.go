package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlackBoxFatalCrashDefersWhenCloudPlacementSelected(t *testing.T) {
	const crashMessage = "fatal nil pointer in auth.ts"
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

	bb, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	s := &HTTPServer{
		token:       "owner-token",
		deviceID:    "relay-device",
		convexURL:   backend.URL,
		taskMgr:     tm,
		blackboxMgr: bb,
	}
	body := strings.NewReader(`{"type":"error","message":"` + crashMessage + `","isFatal":true,"stack":["Auth.tsx:42"]}`)
	req := httptest.NewRequest(http.MethodPost, "/blackbox/stream", body)
	req.Header.Set("X-Device-ID", "ios-1")
	req.Header.Set("X-Platform", "ios")
	req.Header.Set("X-App-Name", "DemoApp")
	rec := httptest.NewRecorder()

	s.handleBlackBoxStream(rec, req)

	out := rec.Body.String()
	if !strings.Contains(out, `"type":"cloud_workspace_required"`) {
		t.Fatalf("response missing cloud workspace event:\n%s", out)
	}
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("local tasks = %d, want 0", got)
	}
	wantSeen := []string{"/tasks/placement/preview", "/tasks/placement/record", "/tasks/placement/activate"}
	if strings.Join(seen, ",") != strings.Join(wantSeen, ",") {
		t.Fatalf("seen paths = %v, want %v", seen, wantSeen)
	}
	for _, body := range bodies {
		for _, forbidden := range []string{crashMessage, "auth.ts", "Auth.tsx"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("placement body leaked crash detail %q: %s", forbidden, body)
			}
		}
	}
}
