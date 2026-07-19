package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpsProjectTestGrowDefersAuthorTaskWhenCloudPlacementSelected(t *testing.T) {
	const routeFile = "app/login/page.tsx"
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

	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "app", "login"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, routeFile), []byte("export default function Page(){return null}"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := NewTaskManager(projectDir, nil, defaultTestRunner())
	s := &HTTPServer{
		token:     "owner-token",
		deviceID:  "relay-device",
		convexURL: backend.URL,
		taskMgr:   tm,
	}
	payload, _ := json.Marshal(map[string]any{
		"dir":    projectDir,
		"author": true,
		"runner": "codex",
	})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "project_test_grow",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("dispatchOps failed: code=%s error=%s", res.Code, res.Error)
	}
	plan, ok := res.Initial.(*growPlan)
	if !ok || plan == nil {
		t.Fatalf("initial = %#v, want *growPlan", res.Initial)
	}
	if !strings.HasPrefix(plan.TaskID, "pending-cloud:") {
		t.Fatalf("task id = %q, want pending-cloud id", plan.TaskID)
	}
	if plan.AuthorPrompt != "" {
		t.Fatalf("author prompt should be stripped after cloud deferral")
	}
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("local tasks = %d, want 0", got)
	}
	wantSeen := []string{"/tasks/placement/preview", "/tasks/placement/record", "/tasks/placement/activate"}
	if strings.Join(seen, ",") != strings.Join(wantSeen, ",") {
		t.Fatalf("seen paths = %v, want %v", seen, wantSeen)
	}
	for i, body := range bodies {
		for _, forbidden := range []string{routeFile, "login/page.tsx", "Uncovered routes"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("placement body leaked author detail %q: %s", forbidden, body)
			}
		}
		if i < 2 && !strings.Contains(body, `"requestedRunner":"codex"`) {
			t.Fatalf("placement body missing requested runner: %s", body)
		}
	}
}
