package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFeedbackWorkWorkerEnabledRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("YAVER_FEEDBACK_WORK_WORKER", "")
	if feedbackWorkWorkerEnabled(&Config{}) {
		t.Fatal("worker should default off")
	}
	if !feedbackWorkWorkerEnabled(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{Enabled: true}}) {
		t.Fatal("config enabled should turn worker on")
	}
	t.Setenv("YAVER_FEEDBACK_WORK_WORKER", "0")
	if feedbackWorkWorkerEnabled(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{Enabled: true}}) {
		t.Fatal("env false should override config")
	}
	t.Setenv("YAVER_FEEDBACK_WORK_WORKER", "1")
	if !feedbackWorkWorkerEnabled(&Config{}) {
		t.Fatal("env true should turn worker on")
	}
}

func TestFeedbackWorkWorkerIntervalIsClamped(t *testing.T) {
	t.Setenv("YAVER_FEEDBACK_WORK_WORKER_INTERVAL", "")
	if got := feedbackWorkWorkerInterval(&Config{}); got != defaultFeedbackWorkWorkerInterval {
		t.Fatalf("default interval = %s", got)
	}
	if got := feedbackWorkWorkerInterval(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{IntervalSeconds: 1}}); got != minFeedbackWorkWorkerInterval {
		t.Fatalf("min clamp = %s", got)
	}
	if got := feedbackWorkWorkerInterval(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{IntervalSeconds: 9999}}); got != maxFeedbackWorkWorkerInterval {
		t.Fatalf("max clamp = %s", got)
	}
	t.Setenv("YAVER_FEEDBACK_WORK_WORKER_INTERVAL", "7s")
	if got := feedbackWorkWorkerInterval(&Config{}); got != 7*time.Second {
		t.Fatalf("duration env interval = %s", got)
	}
}

func TestFeedbackWorkWorkerCreateProviderIssuesRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("YAVER_FEEDBACK_WORK_CREATE_PROVIDER_ISSUES", "")
	if feedbackWorkWorkerCreateProviderIssues(&Config{}) {
		t.Fatal("provider issue creation should default off")
	}
	if !feedbackWorkWorkerCreateProviderIssues(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{CreateProviderIssues: true}}) {
		t.Fatal("config should enable provider issue creation")
	}
	t.Setenv("YAVER_FEEDBACK_WORK_CREATE_PROVIDER_ISSUES", "0")
	if feedbackWorkWorkerCreateProviderIssues(&Config{FeedbackWorkWorker: &FeedbackWorkWorkerConfig{CreateProviderIssues: true}}) {
		t.Fatal("env false should override config")
	}
	t.Setenv("YAVER_FEEDBACK_WORK_CREATE_PROVIDER_ISSUES", "1")
	if !feedbackWorkWorkerCreateProviderIssues(&Config{}) {
		t.Fatal("env true should enable provider issue creation")
	}
}

func TestFeedbackWorkWorkerTickQueuesRelaySource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var queuedBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "feedback-1",
				"projectSlug": "demo",
				"status":      "claimed",
				"target":      "branch",
			})
		case "/feedback-work-items/queue-relay-source":
			_ = json.NewDecoder(r.Body).Decode(&queuedBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"item": map[string]any{
					"id":                  "feedback-1",
					"projectSlug":         "demo",
					"status":              "claimed",
					"relaySourceIntentId": "intent-1",
				},
				"relaySourceIntent": map[string]any{
					"id":          "intent-1",
					"localTaskId": "feedback:feedback-1",
					"status":      "queued",
					"branch":      "yaver/source/feedback",
					"baseBranch":  "main",
					"projectSlug": "demo",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "device-1"}
	result, err := feedbackWorkWorkerTick(context.Background(), s, "demo", "feedback-worker-test")
	if err != nil {
		t.Fatalf("feedbackWorkWorkerTick: %v", err)
	}
	if result == nil || result.Item == nil || result.Item.ID != "feedback-1" || result.RelaySourceIntent == nil || result.RelaySourceIntent.ID != "intent-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if queuedBody["itemId"] != "feedback-1" {
		t.Fatalf("queued itemId = %v", queuedBody["itemId"])
	}
	if queuedBody["workerId"] != "feedback-worker-test" {
		t.Fatalf("queued workerId = %v", queuedBody["workerId"])
	}
	if _, leaked := queuedBody["body"]; leaked {
		t.Fatal("worker must not forward feedback body into relay-source queue request")
	}
}

func TestFeedbackWorkWorkerTickCreatesOwnerTaskForTaskTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	statusBodies := []map[string]any{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "feedback-1",
				"projectSlug": "demo",
				"status":      "claimed",
				"target":      "task",
				"title":       "Login button fails",
				"body":        "When I tap Login nothing happens.",
				"kind":        "bug",
				"priority":    "high",
				"platform":    "ios",
			})
		case "/feedback-work-items/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			statusBodies = append(statusBodies, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": body["status"],
				"taskId": body["taskId"],
			})
		case "/feedback-work-items/queue-relay-source":
			t.Fatal("task target should not queue relay-source work")
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	tm := NewTaskManager(t.TempDir(), nil, RunnerConfig{RunnerID: "dummy", Name: "Dummy", Command: "dummy", OutputMode: "raw"})
	tm.DummyMode = true
	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "device-1", taskMgr: tm}
	result, err := feedbackWorkWorkerTick(context.Background(), s, "", "feedback-worker-test")
	if err != nil {
		t.Fatalf("feedbackWorkWorkerTick: %v", err)
	}
	if result == nil || result.TaskID == "" || result.Item == nil || result.Item.ID != "feedback-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	task, ok := tm.GetTask(result.TaskID)
	if !ok {
		t.Fatalf("created task %q not found", result.TaskID)
	}
	if task.Source != "feedback-work" {
		t.Fatalf("task source = %q", task.Source)
	}
	if !strings.Contains(task.Description, "When I tap Login nothing happens.") {
		t.Fatalf("task description did not include feedback body: %q", task.Description)
	}
	if len(statusBodies) != 1 || statusBodies[0]["status"] != "task_created" || statusBodies[0]["taskId"] != result.TaskID {
		t.Fatalf("status bodies = %+v", statusBodies)
	}
	if _, leaked := statusBodies[0]["body"]; leaked {
		t.Fatal("backend status update must not send feedback body")
	}
}

func TestFeedbackWorkWorkerBlocksTaskTargetWhenCloudPlacementSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var seen []string
	var statusBodies []map[string]any
	var metadataPayloads []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "feedback-1",
				"projectSlug": "demo",
				"status":      "claimed",
				"target":      "task",
				"title":       "Login button fails",
				"body":        "When I tap Login nothing happens.",
				"kind":        "bug",
				"priority":    "high",
				"platform":    "ios",
			})
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before feedback work can run.",
			})
		case "/feedback-work-items/status":
			statusBodies = append(statusBodies, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": body["status"],
				"taskId": body["taskId"],
			})
		case "/feedback-work-items/queue-relay-source":
			t.Fatal("task target should not queue relay-source work")
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	tm := NewTaskManager(t.TempDir(), nil, RunnerConfig{RunnerID: "dummy", Name: "Dummy", Command: "dummy", OutputMode: "raw"})
	tm.DummyMode = true
	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "local-dev", taskMgr: tm}

	result, err := feedbackWorkWorkerTick(context.Background(), s, "", "feedback-worker-test")
	if err == nil {
		t.Fatal("expected cloud placement blocker error")
	}
	var placementErr *feedbackWorkCloudPlacementBlockedError
	if !errors.As(err, &placementErr) {
		t.Fatalf("err = %T %v, want feedbackWorkCloudPlacementBlockedError", err, err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil", result)
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local feedback-work task, got %d", len(tasks))
	}
	if len(statusBodies) != 1 || statusBodies[0]["status"] != "blocked" {
		t.Fatalf("status bodies = %#v", statusBodies)
	}
	if taskID, _ := statusBodies[0]["taskId"].(string); !strings.HasPrefix(taskID, "pending-cloud:") {
		t.Fatalf("status taskId = %#v", statusBodies[0]["taskId"])
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "body"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	wantPaths := []string{
		"/feedback-work-items/claim",
		"/tasks/placement/preview",
		"/tasks/placement/record",
		"/tasks/placement/activate",
		"/feedback-work-items/status",
	}
	if len(seen) != len(wantPaths) {
		t.Fatalf("paths = %#v", seen)
	}
	for i := range wantPaths {
		if seen[i] != wantPaths[i] {
			t.Fatalf("paths = %#v", seen)
		}
	}
}

func TestFeedbackWorkWorkerTickCreatesPrivateIssueDraftForIssueTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	statusBodies := []map[string]any{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "feedback-1",
				"status":     "claimed",
				"target":     "issue",
				"title":      "Checkout freezes",
				"body":       "Guest tapped pay and the screen stayed disabled.",
				"kind":       "bug",
				"priority":   "high",
				"component":  "checkout",
				"appVersion": "1.2.3",
				"platform":   "ios",
			})
		case "/feedback-work-items/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			statusBodies = append(statusBodies, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": body["status"],
				"reason": body["reason"],
			})
		case "/feedback-work-items/queue-relay-source":
			t.Fatal("issue target should not queue relay-source work")
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "device-1"}
	result, err := feedbackWorkWorkerTick(context.Background(), s, "", "feedback-worker-test")
	if err != nil {
		t.Fatalf("feedbackWorkWorkerTick: %v", err)
	}
	if result == nil || result.Item == nil || result.Item.ID != "feedback-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(statusBodies) != 1 || statusBodies[0]["status"] != "issue_draft_created" {
		t.Fatalf("status bodies = %+v", statusBodies)
	}
	if _, leaked := statusBodies[0]["body"]; leaked {
		t.Fatal("backend status update must not send feedback body")
	}
	if _, leaked := statusBodies[0]["path"]; leaked {
		t.Fatal("backend status update must not send local draft path")
	}
	draftPath := filepath.Join(home, ".yaver", "feedback-issue-drafts", "unscoped", "feedback-1.md")
	data, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	text := string(data)
	for _, want := range []string{"# Checkout freezes", "Kind: bug", "Priority: high", "Guest tapped pay"} {
		if !strings.Contains(text, want) {
			t.Fatalf("draft missing %q:\n%s", want, text)
		}
	}
}

func TestFeedbackWorkWorkerTickCreatesProviderIssueOnlyWhenExplicitlyEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	statusBodies := []map[string]any{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "feedback-2",
				"projectSlug": "demo",
				"status":      "claimed",
				"target":      "issue",
				"title":       "Checkout freezes",
				"body":        "Guest tapped pay and the screen stayed disabled.",
				"kind":        "bug",
			})
		case "/feedback-work-items/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			statusBodies = append(statusBodies, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       "feedback-2",
				"status":   body["status"],
				"issueUrl": body["issueUrl"],
				"reason":   body["reason"],
			})
		case "/feedback-work-items/queue-relay-source":
			t.Fatal("issue target should not queue relay-source work")
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	called := false
	old := createFeedbackProviderIssue
	createFeedbackProviderIssue = func(item *feedbackWorkItem, draftPath string) (string, error) {
		called = true
		if item == nil || item.ID != "feedback-2" {
			t.Fatalf("unexpected item: %+v", item)
		}
		data, err := os.ReadFile(draftPath)
		if err != nil {
			t.Fatalf("read draft in provider hook: %v", err)
		}
		if !strings.Contains(string(data), "Guest tapped pay") {
			t.Fatalf("draft did not include local feedback body:\n%s", string(data))
		}
		return "https://github.com/example/repo/issues/123", nil
	}
	defer func() { createFeedbackProviderIssue = old }()

	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "device-1"}
	result, err := feedbackWorkWorkerTick(context.Background(), s, "", "feedback-worker-test", true)
	if err != nil {
		t.Fatalf("feedbackWorkWorkerTick: %v", err)
	}
	if !called {
		t.Fatal("provider issue hook was not called")
	}
	if result == nil || result.Item == nil || result.Item.ID != "feedback-2" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(statusBodies) != 1 || statusBodies[0]["status"] != "issue_created" {
		t.Fatalf("status bodies = %+v", statusBodies)
	}
	if statusBodies[0]["issueUrl"] != "https://github.com/example/repo/issues/123" {
		t.Fatalf("issueUrl = %v", statusBodies[0]["issueUrl"])
	}
	if _, leaked := statusBodies[0]["body"]; leaked {
		t.Fatal("backend status update must not send feedback body")
	}
	if _, leaked := statusBodies[0]["path"]; leaked {
		t.Fatal("backend status update must not send local draft path")
	}
}

func TestFeedbackWorkWorkerTickBlocksClaimWhenQueueFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	statuses := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feedback-work-items/claim":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": "claimed",
			})
		case "/feedback-work-items/queue-relay-source":
			http.Error(w, `{"error":"no repo"}`, http.StatusBadRequest)
		case "/feedback-work-items/status":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if status, _ := body["status"].(string); status != "" {
				statuses = append(statuses, status)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "feedback-1",
				"status": body["status"],
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	if err := SaveConfig(&Config{AuthToken: "tok", ConvexSiteURL: backend.URL}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{token: "tok", convexURL: backend.URL, deviceID: "device-1"}
	if _, err := feedbackWorkWorkerTick(context.Background(), s, "", "feedback-worker-test"); err == nil {
		t.Fatal("expected queue failure")
	}
	if len(statuses) != 1 || statuses[0] != "blocked" {
		t.Fatalf("statuses = %+v", statuses)
	}
}
