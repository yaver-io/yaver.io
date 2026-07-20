package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInferPlacementTaskKind(t *testing.T) {
	cases := []struct {
		name   string
		input  taskPlacementRequestInput
		expect string
	}{
		{
			name:   "explicit valid hint wins",
			input:  taskPlacementRequestInput{KindHint: "build", Title: "just chat"},
			expect: "build",
		},
		{
			name:   "deploy wording",
			input:  taskPlacementRequestInput{Title: "ship this release"},
			expect: "deploy",
		},
		{
			name:   "native artifact wording",
			input:  taskPlacementRequestInput{Title: "make an apk"},
			expect: "build",
		},
		{
			name:   "unknown stays private",
			input:  taskPlacementRequestInput{Title: "please improve it"},
			expect: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferPlacementTaskKind(
				tc.input.KindHint,
				tc.input.Title,
				tc.input.Description,
				tc.input.CustomCommand,
				tc.input.Source,
			)
			if got != tc.expect {
				t.Fatalf("kind = %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestShouldDeferLocalTaskForPlacement(t *testing.T) {
	cases := []struct {
		name      string
		placement *TaskPlacementMetadata
		localID   string
		expect    bool
	}{
		{
			name:      "relay runs locally",
			placement: &TaskPlacementMetadata{Lane: "relay_source"},
			localID:   "dev-1",
			expect:    false,
		},
		{
			name:      "cloud same device runs",
			placement: &TaskPlacementMetadata{Lane: "cloud_standard", TargetDeviceID: "dev-1"},
			localID:   "dev-1",
			expect:    false,
		},
		{
			name:      "cloud other device defers",
			placement: &TaskPlacementMetadata{Lane: "cloud_build", TargetDeviceID: "dev-2"},
			localID:   "dev-1",
			expect:    true,
		},
		{
			name:      "cloud no target waking defers",
			placement: &TaskPlacementMetadata{Lane: "cloud_heavy", WakeRequired: true},
			localID:   "dev-1",
			expect:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDeferLocalTaskForPlacement(tc.placement, tc.localID); got != tc.expect {
				t.Fatalf("defer = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestDecodeCloudWorkspaceRequiredError(t *testing.T) {
	raw := []byte(`{
		"action":"cloud_workspace_required",
		"pendingTaskId":"pending-cloud:abc",
		"reason":"workspace is waking",
		"placement":{"lane":"cloud_build","targetDeviceId":"cloud-dev","resourceClass":"build"},
		"activation":{"action":"wake_scheduled"}
	}`)
	err := decodeCloudWorkspaceRequiredError(http.StatusConflict, raw)
	cerr, ok := err.(*CloudWorkspaceRequiredError)
	if !ok {
		t.Fatalf("error = %#v, want CloudWorkspaceRequiredError", err)
	}
	if cerr.PendingTaskID != "pending-cloud:abc" {
		t.Fatalf("pendingTaskId = %q", cerr.PendingTaskID)
	}
	if cerr.Placement == nil || cerr.Placement.Lane != "cloud_build" || cerr.Placement.TargetDeviceID != "cloud-dev" {
		t.Fatalf("placement = %#v", cerr.Placement)
	}
	if !strings.Contains(cerr.Error(), "workspace is waking") {
		t.Fatalf("error string missing reason: %q", cerr.Error())
	}
}

func TestDecodeCloudWorkspaceRequiredIgnoresOtherConflicts(t *testing.T) {
	if err := decodeCloudWorkspaceRequiredError(http.StatusConflict, []byte(`{"error":"busy"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestActivateTaskPlacementPreservesStructuredBlocker(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/placement/activate" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             false,
			"action":         "runner_auth_required",
			"targetDeviceId": "cloud-dev",
			"reason":         "Cloud Workspace is awake but Codex needs sign-in before tasks can run.",
		})
	}))
	defer backend.Close()

	client, err := newTaskPlacementBackendClient(backend.URL, "owner-token")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.activateTaskPlacement(context.Background(), "placement-1", "pending-cloud:abc")
	if err == nil || result != nil {
		t.Fatalf("result=%#v err=%v, want structured activation error", result, err)
	}
	var activationErr *taskPlacementActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("err = %T %v, want taskPlacementActivationError", err, err)
	}
	if activationErr.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d", activationErr.StatusCode)
	}
	activation := activationMapFromError(err)
	if activation["action"] != "runner_auth_required" || activation["targetDeviceId"] != "cloud-dev" {
		t.Fatalf("activation = %#v", activation)
	}
	if !strings.Contains(err.Error(), "runner_auth_required") || !strings.Contains(err.Error(), "Codex needs sign-in") {
		t.Fatalf("error text = %q", err.Error())
	}
}

func TestPreviewTaskPlacementTimesOutFast(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/placement/preview" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		time.Sleep(taskPlacementHTTPTimeout + 250*time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer backend.Close()

	s := &HTTPServer{
		convexURL: backend.URL,
		token:     "owner-token",
	}
	start := time.Now()
	_, err := s.previewTaskPlacement(context.Background(), taskPlacementRecordRequest{Kind: "unknown"})
	if err == nil {
		t.Fatal("previewTaskPlacement unexpectedly succeeded against a slow backend")
	}
	if elapsed := time.Since(start); elapsed > taskPlacementPreviewHTTPTimeout+300*time.Millisecond {
		t.Fatalf("previewTaskPlacement took %v, want <= %v", elapsed, taskPlacementPreviewHTTPTimeout+300*time.Millisecond)
	}
}

func TestTaskBodyWithLocalFallbackPreservesPromptForTargetOnly(t *testing.T) {
	raw, err := taskBodyWithLocalFallback([]byte(`{"title":"build apk","description":"build apk","userPrompt":"secret local prompt","runner":"codex"}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["allowLocalFallback"] != true {
		t.Fatalf("allowLocalFallback = %#v", body["allowLocalFallback"])
	}
	if body["userPrompt"] != "secret local prompt" {
		t.Fatalf("userPrompt dropped: %#v", body)
	}
}

func TestRebindCloudTaskPlacementSendsOnlyIdsAndStatus(t *testing.T) {
	var captured map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/placement/rebind" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "config-token"}); err != nil {
		t.Fatal(err)
	}
	if err := rebindCloudTaskPlacement(context.Background(), "placement-1", "real-task-1", "running", "Bearer owner-token"); err != nil {
		t.Fatal(err)
	}
	if captured["placementId"] != "placement-1" || captured["taskId"] != "real-task-1" || captured["status"] != "running" {
		t.Fatalf("unexpected rebind payload: %#v", captured)
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := captured[forbidden]; ok {
			t.Fatalf("rebind payload leaked %q: %#v", forbidden, captured)
		}
	}
}

func TestMarkCloudTaskPlacementStatusSendsOnlyIdAndStatus(t *testing.T) {
	var captured map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/placement/status" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer config-token" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "config-token"}); err != nil {
		t.Fatal(err)
	}
	if err := markCloudTaskPlacementStatus(context.Background(), "placement-1", "completed", ""); err != nil {
		t.Fatal(err)
	}
	if captured["placementId"] != "placement-1" || captured["status"] != "completed" {
		t.Fatalf("unexpected status payload: %#v", captured)
	}
	for _, forbidden := range []string{"taskId", "title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := captured[forbidden]; ok {
			t.Fatalf("status payload leaked %q: %#v", forbidden, captured)
		}
	}
}

func TestFinalTaskPlacementStatus(t *testing.T) {
	cases := []struct {
		status TaskStatus
		expect string
	}{
		{TaskStatusFinished, "completed"},
		{TaskStatusFailed, "failed"},
		{TaskStatusStopped, "failed"},
		{TaskStatusRunning, ""},
		{TaskStatusQueued, ""},
		{TaskStatusReview, ""},
	}
	for _, tc := range cases {
		if got := finalTaskPlacementStatus(tc.status); got != tc.expect {
			t.Fatalf("finalTaskPlacementStatus(%q) = %q, want %q", tc.status, got, tc.expect)
		}
	}
}

func TestCloudTaskDispatchIntentPayloadsArePromptFree(t *testing.T) {
	var payloads []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, body)
		switch r.URL.Path {
		case "/tasks/dispatch-intents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      "queued",
			})
		case "/tasks/dispatch-intents/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "pending-cloud:abc",
				"taskId":      body["taskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	cloudErr := &CloudWorkspaceRequiredError{
		PendingTaskID: "pending-cloud:abc",
		Reason:        "workspace waking",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-1",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
	}
	intent, err := createCloudTaskDispatchIntent(context.Background(), cloudErr, terminalRemoteTaskSource, "codex", "project", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updateCloudTaskDispatchIntent(context.Background(), intent, cloudErr.PendingTaskID, "dispatched", "real-task", "cloud-dev", "", "accepted", "", "", false, false); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d", len(payloads))
	}
	if payloads[0]["sourceSurface"] != terminalRemoteTaskSource || payloads[0]["requestedRunner"] != "codex" {
		t.Fatalf("unexpected create payload: %#v", payloads[0])
	}
	if payloads[1]["status"] != "dispatched" || payloads[1]["taskId"] != "real-task" {
		t.Fatalf("unexpected update payload: %#v", payloads[1])
	}
	for i, payload := range payloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
}

func TestPromptFreeConvexMetadataPayloadRejectsSensitiveKeys(t *testing.T) {
	for _, key := range []string{"title", "description", "prompt", "userPrompt", "user-prompt", "bodyJson", "workDir", "gitRemote", "customCommand"} {
		t.Run(key, func(t *testing.T) {
			err := ensurePromptFreeConvexMetadataPayload(map[string]any{
				"localTaskId": "pending-cloud:test",
				key:           "secret task content",
			})
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, want rejection for %q", err, key)
			}
		})
	}
	if err := ensurePromptFreeConvexMetadataPayload(map[string]any{
		"localTaskId":    "pending-cloud:test",
		"placementId":    "placement-1",
		"targetDeviceId": "cloud-dev",
		"status":         "queued",
		"reason":         "workspace waking",
	}); err != nil {
		t.Fatalf("safe metadata rejected: %v", err)
	}
}

func TestPostTaskDispatchIntentRefusesSensitivePayloadBeforeHTTP(t *testing.T) {
	called := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("backend should not receive sensitive dispatch metadata")
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	_, err := postTaskDispatchIntent(context.Background(), "/tasks/dispatch-intents", map[string]any{
		"localTaskId": "pending-cloud:test",
		"userPrompt":  "secret prompt",
	}, "Bearer owner-token")
	if err == nil || !strings.Contains(err.Error(), "userPrompt") {
		t.Fatalf("err = %v, want sensitive-field rejection", err)
	}
	if called {
		t.Fatal("backend was called")
	}
}

func TestClaimRelaySourceIntentPayloadIsPromptFree(t *testing.T) {
	var captured map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/relay-source-intents/claim" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "relay-intent-1",
			"localTaskId": "pending-relay:abc",
			"status":      "claimed",
			"branch":      "yaver/source/demo",
			"baseBranch":  "main",
			"projectSlug": "demo",
		})
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "config-token"}); err != nil {
		t.Fatal(err)
	}
	intent, err := claimRelaySourceIntent(context.Background(), "Bearer owner-token", "demo", "relay-1")
	if err != nil {
		t.Fatal(err)
	}
	if intent == nil || intent.ID != "relay-intent-1" || intent.Branch != "yaver/source/demo" {
		t.Fatalf("unexpected intent: %#v", intent)
	}
	if captured["projectSlug"] != "demo" || captured["relayId"] != "relay-1" {
		t.Fatalf("unexpected claim payload: %#v", captured)
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := captured[forbidden]; ok {
			t.Fatalf("claim payload leaked %q: %#v", forbidden, captured)
		}
	}
}

func TestClaimRelaySourceIntentNoWorkReturnsNil(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "intent": nil})
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	intent, err := claimRelaySourceIntent(context.Background(), "", "", "relay-1")
	if err != nil {
		t.Fatal(err)
	}
	if intent != nil {
		t.Fatalf("intent = %#v, want nil", intent)
	}
}

func TestPreviewTaskPlacementDoesNotSendPromptText(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/placement/preview" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "placement-1",
			"lane":           "cloud_build",
			"resourceClass":  "build",
			"targetDeviceId": "cloud-dev",
			"wakeRequired":   true,
			"creditEstimate": map[string]any{
				"unit": "usd_cents",
			},
		})
	}))
	defer srv.Close()

	s := &HTTPServer{convexURL: srv.URL, token: "owner-token"}
	placement, err := s.previewTaskPlacement(context.Background(), taskPlacementRecordRequest{
		Kind:            "build",
		SourceSurface:   "web",
		ProjectSlug:     "project",
		RequestedRunner: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if placement == nil || placement.Lane != "cloud_build" || placement.TargetDeviceID != "cloud-dev" {
		t.Fatalf("unexpected placement: %#v", placement)
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := captured[forbidden]; ok {
			t.Fatalf("preview payload leaked %q: %#v", forbidden, captured)
		}
	}
}

func TestCreateTaskDefersCloudPlacementBeforeLocalLaunch(t *testing.T) {
	var seen []string
	var recordPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   false,
			})
		case "/tasks/placement/record":
			if err := json.NewDecoder(r.Body).Decode(&recordPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   false,
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
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	body := []byte(`{"title":"build apk","description":"build apk","source":"web","runner":"codex","placementKind":"build"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.createTask(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" {
		t.Fatalf("action = %#v", resp["action"])
	}
	if resp["pendingTaskId"] == "" {
		t.Fatal("expected pendingTaskId")
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

func TestCreateTaskDefersCloudPlacementWithStructuredActivationBlocker(t *testing.T) {
	var seen []string
	var recordPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   false,
			})
		case "/tasks/placement/record":
			if err := json.NewDecoder(r.Body).Decode(&recordPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   false,
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
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	body := []byte(`{"title":"build apk","description":"build apk","userPrompt":"secret local prompt","source":"web","runner":"codex","placementKind":"build"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.createTask(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	activation, _ := resp["activation"].(map[string]any)
	if activation["action"] != "runner_auth_required" {
		t.Fatalf("activation = %#v", resp["activation"])
	}
	if activation["targetDeviceId"] != "cloud-dev" {
		t.Fatalf("activation target = %#v", activation["targetDeviceId"])
	}
	if len(seen) != 3 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
	for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "workDir", "gitBranch", "gitRemote"} {
		if _, ok := recordPayload[forbidden]; ok {
			t.Fatalf("record payload leaked %q: %#v", forbidden, recordPayload)
		}
	}
}

func TestDeployDefersCloudPlacementInsteadOfRunningLocal(t *testing.T) {
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
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before tasks can run.",
			})
		case "/tasks/dispatch-intents":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      "queued",
			})
		case "/tasks/dispatch-intents/status":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	workDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workDir, "convex"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	req := httptest.NewRequest(http.MethodPost, "/deploy", bytes.NewReader([]byte(`{"target":"convex"}`)))
	rec := httptest.NewRecorder()
	s.handleDeploy(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}

func TestChainDefersCloudPlacementBeforeCreatingLocalTasks(t *testing.T) {
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
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before chains can run.",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	req := httptest.NewRequest(http.MethodPost, "/chain", bytes.NewReader([]byte(`{
		"tasks":[
			{"title":"build apk for demo","description":"compile the native app"},
			{"title":"run smoke tests"}
		],
		"runner":"codex"
	}`)))
	rec := httptest.NewRecorder()
	s.handleChainCreate(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local chained tasks, got %d", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "tasks"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	if len(seen) != 3 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}

func TestMCPCreateTaskDefersCloudPlacementInsteadOfRunningLocal(t *testing.T) {
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
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before tasks can run.",
			})
		case "/tasks/dispatch-intents":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "intent-1",
				"localTaskId":    body["localTaskId"],
				"status":         "queued",
				"targetDeviceId": "cloud-dev",
			})
		case "/tasks/dispatch-intents/status":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{
		token:     "owner-token",
		convexURL: backend.URL,
		deviceID:  "local-dev",
		taskMgr:   tm,
	}
	rawArgs, _ := json.Marshal(map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"prompt": "build apk with secret prompt",
			"runner": "codex",
		},
	})
	out := server.handleMCPToolCall(rawArgs)
	text := billingToolText(t, out)
	if !strings.Contains(text, "cloud_workspace_required") || !strings.Contains(text, "pending-cloud:") {
		t.Fatalf("MCP output = %s", text)
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"prompt", "userPrompt", "title", "description", "bodyJson", "workDir"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.Contains(string(rows[0].BodyJSON), "build apk with secret prompt") {
		t.Fatalf("pending rows = %#v", rows)
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("paths = %#v", seen)
	}
}

func TestMCPAskDefersCloudPlacementInsteadOfRunningLocal(t *testing.T) {
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before ask tasks can run.",
			})
		case "/tasks/dispatch-intents":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "intent-1",
				"localTaskId":    body["localTaskId"],
				"status":         "queued",
				"targetDeviceId": "cloud-dev",
			})
		case "/tasks/dispatch-intents/status":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{
		token:     "owner-token",
		convexURL: backend.URL,
		deviceID:  "local-dev",
		taskMgr:   tm,
	}
	rawArgs, _ := json.Marshal(map[string]any{
		"name": "yaver_ask",
		"arguments": map[string]any{
			"question": "how should I test this private code path?",
			"runner":   "codex",
			"depth":    "single",
		},
	})
	out := server.handleMCPToolCall(rawArgs)
	text := billingToolText(t, out)
	if !strings.Contains(text, "cloud_workspace_required") || !strings.Contains(text, "pending-cloud:") {
		t.Fatalf("MCP output = %s", text)
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"prompt", "userPrompt", "title", "description", "bodyJson", "workDir", "question"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.Contains(string(rows[0].BodyJSON), "private code path") {
		t.Fatalf("pending rows = %#v", rows)
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("paths = %#v", seen)
	}
}

func TestRecoverDefersCloudPlacementWithoutMutatingGlobalWorkDir(t *testing.T) {
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
				"lane":           "cloud_build",
				"resourceClass":  "build",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_build",
				"resourceClass":  "build",
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before recovery tasks can run.",
			})
		case "/tasks/dispatch-intents":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      "queued",
			})
		case "/tasks/dispatch-intents/status":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	originalWorkDir := t.TempDir()
	recoveryWorkDir := t.TempDir()
	tm := NewTaskManager(originalWorkDir, nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	req := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewReader([]byte(`{
		"kind":"hermes-build-failed",
		"project":"demo",
		"workDir":`+strconv.Quote(recoveryWorkDir)+`,
		"error":"private hermes build stack"
	}`)))
	rec := httptest.NewRecorder()
	s.handleRecover(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	if tm.workDir != originalWorkDir {
		t.Fatalf("task manager workDir mutated to %q, want %q", tm.workDir, originalWorkDir)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "error"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.Contains(string(rows[0].BodyJSON), "private hermes build stack") {
		t.Fatalf("pending rows = %#v", rows)
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}

func TestVibingExecuteDefersCloudPlacementWithoutMutatingGlobalWorkDir(t *testing.T) {
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before vibing tasks can run.",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	originalWorkDir := t.TempDir()
	vibeWorkDir := t.TempDir()
	tm := NewTaskManager(originalWorkDir, nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	body := []byte(`{
		"prompt":"add a private checkout screen",
		"projectName":"demo",
		"projectPath":` + strconv.Quote(vibeWorkDir) + `
	}`)
	req := httptest.NewRequest(http.MethodPost, "/vibing/execute", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleVibingExecute(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	if tm.workDir != originalWorkDir {
		t.Fatalf("task manager workDir mutated to %q, want %q", tm.workDir, originalWorkDir)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
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

func TestTaskPlacementRequestUsesCoarseRepoMetadata(t *testing.T) {
	dir := t.TempDir()
	touch := func(rel string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	touch("package.json")
	touch("android/settings.gradle")
	touch("Dockerfile")

	req := taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		Title:          "build app",
		Source:         "web",
		Runner:         "codex",
		WorkDir:        dir,
		TargetDeviceID: "device-1",
	})
	if req.Kind != "build" {
		t.Fatalf("kind = %q, want build", req.Kind)
	}
	if req.ProjectSlug != filepath.Base(dir) {
		t.Fatalf("projectSlug = %q, want basename only", req.ProjectSlug)
	}
	if !req.HasNativeMobile {
		t.Fatal("expected native mobile signal")
	}
	if !req.HasDocker {
		t.Fatal("expected docker signal")
	}
	if req.FileCount == 0 {
		t.Fatal("expected bounded file count")
	}
}
