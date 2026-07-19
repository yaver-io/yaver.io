package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingCloudTaskDispatchStorePersistsOwnerOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	body := json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt"}`)
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID: "pending-cloud:one",
		Status:      "queued",
		BodyJSON:    body,
	}); err != nil {
		t.Fatal(err)
	}

	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LocalTaskID != "pending-cloud:one" {
		t.Fatalf("rows = %#v", rows)
	}
	if !bytes.Contains(rows[0].BodyJSON, []byte("secret prompt")) {
		t.Fatalf("bodyJSON = %s", rows[0].BodyJSON)
	}
	info, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".yaver", pendingCloudTaskDispatchFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("store perms = %o, want 0600", got)
	}

	if err := deletePendingCloudTaskDispatch("pending-cloud:one"); err != nil {
		t.Fatal(err)
	}
	rows, err = store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows after delete = %#v", rows)
	}
}

func TestCreateTaskOnCloudWorkspaceBlockedLeavesLocalSpoolOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var intentPayloads []map[string]any
	intentExpiresAt := time.Now().Add(30 * time.Minute).UnixMilli()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/devices/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"devices": []map[string]any{{
					"deviceId":        "cloud-dev",
					"name":            "Cloud Dev",
					"isOnline":        true,
					"publicEndpoints": []string{"http://127.0.0.1:1"},
				}},
			})
		case "/tasks/dispatch-intents", "/tasks/dispatch-intents/status":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			intentPayloads = append(intentPayloads, payload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": payload["localTaskId"],
				"status":      payload["status"],
				"expiresAt":   intentExpiresAt,
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	cloudErr := &CloudWorkspaceRequiredError{
		PendingTaskID: "pending-cloud:blocked",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-1",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Reason: "wake scheduled",
	}
	body := []byte(`{"title":"build apk","userPrompt":"secret prompt","source":"cli-remote","runner":"codex","projectName":"demo"}`)

	var progress []cloudTaskHandoffProgress
	_, _, err := createTaskOnCloudWorkspace(t.Context(), cloudErr, "Bearer owner-token", body, 1*time.Nanosecond, func(p cloudTaskHandoffProgress) {
		progress = append(progress, p)
	})
	if err == nil {
		t.Fatal("expected blocked handoff error")
	}
	if len(progress) == 0 || progress[len(progress)-1].Status != "blocked" {
		t.Fatalf("progress = %#v", progress)
	}
	for _, event := range progress {
		raw, _ := json.Marshal(event)
		for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("progress leaked %q: %s", forbidden, raw)
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
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	row := rows[0]
	if row.LocalTaskID != "pending-cloud:blocked" || row.Status != "blocked" {
		t.Fatalf("row = %#v", row)
	}
	if row.DispatchIntentID != "intent-1" || !row.ExpiresAt.Equal(time.UnixMilli(intentExpiresAt)) {
		t.Fatalf("row intent metadata = id %q expires %s", row.DispatchIntentID, row.ExpiresAt)
	}
	if !bytes.Contains(row.BodyJSON, []byte("secret prompt")) {
		t.Fatalf("local body did not preserve prompt: %s", row.BodyJSON)
	}
	var localBody map[string]any
	if err := json.Unmarshal(row.BodyJSON, &localBody); err != nil {
		t.Fatal(err)
	}
	if localBody["allowLocalFallback"] != true {
		t.Fatalf("local body missing fallback marker: %#v", localBody)
	}

	if len(intentPayloads) == 0 {
		t.Fatal("expected prompt-free backend intent payloads")
	}
	for _, payload := range intentPayloads {
		raw, _ := json.Marshal(payload)
		for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("backend intent leaked %q: %s", forbidden, raw)
			}
		}
	}
}

func TestCreateTaskOnCloudWorkspaceActivationBlockerFailsFast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var paths []string
	var statusPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/tasks/dispatch-intents":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-auth",
				"localTaskId": payload["localTaskId"],
				"status":      payload["status"],
				"expiresAt":   time.Now().Add(30 * time.Minute).UnixMilli(),
			})
		case "/tasks/dispatch-intents/status":
			if err := json.NewDecoder(r.Body).Decode(&statusPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	cloudErr := &CloudWorkspaceRequiredError{
		PendingTaskID: "pending-cloud:auth",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-auth",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Activation: map[string]any{
			"action":         "runner_auth_required",
			"targetDeviceId": "cloud-dev",
			"reason":         "Codex needs sign-in.",
		},
		Reason: "cloud workspace selected",
	}
	body := []byte(`{"title":"build apk","userPrompt":"secret prompt","source":"cli-remote","runner":"codex"}`)

	var progress []cloudTaskHandoffProgress
	_, _, err := createTaskOnCloudWorkspace(t.Context(), cloudErr, "Bearer owner-token", body, time.Minute, func(p cloudTaskHandoffProgress) {
		progress = append(progress, p)
	})
	if err == nil || !strings.Contains(err.Error(), "runner_auth_required") {
		t.Fatalf("err = %v", err)
	}
	for _, path := range paths {
		if path == "/devices/list" || path == "/config" {
			t.Fatalf("activation blocker should not enter reachability loop, paths = %#v", paths)
		}
	}
	if statusPayload["status"] != "blocked" || statusPayload["lastError"] == "" || statusPayload["blockedAction"] != "runner_auth_required" {
		t.Fatalf("status payload = %#v", statusPayload)
	}
	if len(progress) != 1 || progress[0].Status != "blocked" || !strings.Contains(progress[0].LastError, "runner_auth_required") {
		t.Fatalf("progress = %#v", progress)
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "blocked" || !strings.Contains(rows[0].LastError, "runner_auth_required") {
		t.Fatalf("rows = %#v", rows)
	}
	if !bytes.Contains(rows[0].BodyJSON, []byte("secret prompt")) {
		t.Fatalf("local body did not preserve prompt: %s", rows[0].BodyJSON)
	}
	rawStatus, _ := json.Marshal(statusPayload)
	for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
		if strings.Contains(string(rawStatus), forbidden) {
			t.Fatalf("backend intent leaked %q: %s", forbidden, rawStatus)
		}
	}
}

func TestRetryPendingCloudTaskDispatchesPostsAndDeletesQueuedRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var remotePayload map[string]any
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks" {
			t.Fatalf("unexpected remote path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&remotePayload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"taskId": "task-real",
			"status": "running",
		})
	}))
	defer remote.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/devices/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"devices": []map[string]any{{
					"deviceId":        "cloud-dev",
					"name":            "Cloud Dev",
					"isOnline":        true,
					"publicEndpoints": []string{remote.URL},
				}},
			})
		case "/tasks/dispatch-intents", "/tasks/dispatch-intents/status", "/tasks/placement/rebind":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": "pending-cloud:retry",
				"status":      "ok",
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID: "pending-cloud:retry",
		PlacementID: "placement-1",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-1",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:   "blocked",
		BodyJSON: json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt","allowLocalFallback":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	results := retryPendingCloudTaskDispatches(t.Context(), "Bearer owner-token", time.Second)
	if len(results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Err != nil || results[0].TaskID != "task-real" {
		t.Fatalf("result = %#v", results[0])
	}
	if len(results[0].ProgressLog) == 0 || results[0].ProgressLog[len(results[0].ProgressLog)-1].Status != "dispatched" {
		t.Fatalf("progress = %#v", results[0].ProgressLog)
	}
	for _, event := range results[0].ProgressLog {
		raw, _ := json.Marshal(event)
		for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("retry progress leaked %q: %s", forbidden, raw)
			}
		}
	}
	if remotePayload["userPrompt"] != "secret prompt" || remotePayload["allowLocalFallback"] != true {
		t.Fatalf("remote payload = %#v", remotePayload)
	}

	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected dispatch row deleted, got %#v", rows)
	}
}

func TestPendingCloudTaskDispatchStoreNormalizesExpiredRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-25 * time.Hour)
	if err := store.save([]pendingCloudTaskDispatch{{
		LocalTaskID: "pending-cloud:expired",
		Status:      "queued",
		BodyJSON:    json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt"}`),
		CreatedAt:   created,
		UpdatedAt:   created,
	}}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Status != "expired" {
		t.Fatalf("status = %q, want expired", rows[0].Status)
	}
	if rows[0].ExpiresAt.IsZero() || !rows[0].ExpiresAt.Equal(created.Add(pendingCloudTaskDispatchDefaultTTL)) {
		t.Fatalf("expiresAt = %s", rows[0].ExpiresAt)
	}
	if !strings.Contains(rows[0].LastError, "dispatch window expired") {
		t.Fatalf("lastError = %q", rows[0].LastError)
	}
}

func TestRetryPendingCloudTaskDispatchesSkipsExpiredRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	remoteCalled := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalled = true
		t.Fatalf("expired dispatch should not call remote path %s", r.URL.Path)
	}))
	defer remote.Close()

	var statusPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/dispatch-intents/status":
			if err := json.NewDecoder(r.Body).Decode(&statusPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          statusPayload["intentId"],
				"localTaskId": statusPayload["localTaskId"],
				"status":      statusPayload["status"],
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID:      "pending-cloud:expired",
		DispatchIntentID: "intent-expired",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-1",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:    "queued",
		BodyJSON:  json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt","allowLocalFallback":true}`),
		CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	results := retryPendingCloudTaskDispatches(t.Context(), "Bearer owner-token", time.Second)
	if len(results) != 0 {
		t.Fatalf("results = %#v", results)
	}
	if remoteCalled {
		t.Fatal("remote server was called")
	}
	if statusPayload["status"] != "expired" || statusPayload["intentId"] != "intent-expired" {
		t.Fatalf("status payload = %#v", statusPayload)
	}
}

func TestRetryPendingCloudTaskDispatchesSkipsDispatchingRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID: "pending-cloud:in-flight",
		Placement: &TaskPlacementMetadata{
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:   "dispatching",
		BodyJSON: json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if results := retryPendingCloudTaskDispatches(t.Context(), "Bearer owner-token", time.Nanosecond); len(results) != 0 {
		t.Fatalf("results = %#v", results)
	}
}

func TestRetryPendingCloudTaskDispatchesSkipsUserActionBlockedRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	remoteCalled := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalled = true
		t.Fatalf("user-action blocked dispatch should not call remote path %s", r.URL.Path)
	}))
	defer remote.Close()

	backendCalls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		if r.URL.Path != "/tasks/dispatch-intents" || r.Method != http.MethodGet {
			t.Fatalf("unexpected backend path %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id":            "intent-auth",
			"localTaskId":   "pending-cloud:auth-blocked",
			"status":        "blocked",
			"blockedAction": "runner_auth_required",
			"reason":        "Codex needs sign-in.",
			"expiresAt":     time.Now().Add(30 * time.Minute).UnixMilli(),
		}})
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID: "pending-cloud:auth-blocked",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-auth",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:        "blocked",
		BlockedAction: "runner_auth_required",
		LastError:     "runner_auth_required: Codex needs sign-in.",
		BodyJSON:      json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt","allowLocalFallback":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	results := retryPendingCloudTaskDispatches(t.Context(), "Bearer owner-token", time.Second)
	if len(results) != 0 {
		t.Fatalf("results = %#v", results)
	}
	if remoteCalled {
		t.Fatal("blocked dispatch attempted network work")
	}
	if backendCalls != 1 {
		t.Fatalf("backend refresh calls = %d, want 1", backendCalls)
	}
}

func TestRetryPendingCloudTaskDispatchesRefreshesClearedBlocker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var remotePayload map[string]any
	var sawDispatchingClear bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks" {
			t.Fatalf("unexpected remote path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&remotePayload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"taskId": "task-cleared",
			"status": "running",
		})
	}))
	defer remote.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/dispatch-intents":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]map[string]any{{
					"id":             "intent-cleared",
					"localTaskId":    "pending-cloud:cleared",
					"status":         "queued",
					"targetDeviceId": "cloud-dev",
					"expiresAt":      time.Now().Add(30 * time.Minute).UnixMilli(),
				}})
				return
			}
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected dispatch-intents method %s", r.Method)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(payload)
			for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("backend intent leaked %q: %s", forbidden, raw)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "intent-cleared",
				"localTaskId":    payload["localTaskId"],
				"status":         "queued",
				"targetDeviceId": "cloud-dev",
				"expiresAt":      time.Now().Add(30 * time.Minute).UnixMilli(),
			})
		case "/config":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/devices/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"devices": []map[string]any{{
					"deviceId":        "cloud-dev",
					"name":            "Cloud Dev",
					"isOnline":        true,
					"publicEndpoints": []string{remote.URL},
				}},
			})
		case "/tasks/dispatch-intents/status":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(payload)
			for _, forbidden := range []string{"secret prompt", "userPrompt", "title", "description", "bodyJson"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("backend status leaked %q: %s", forbidden, raw)
				}
			}
			if payload["status"] == "dispatching" {
				if payload["clearBlockedAction"] != true {
					t.Fatalf("dispatching payload missing clearBlockedAction: %#v", payload)
				}
				sawDispatchingClear = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/tasks/placement/rebind":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID:      "pending-cloud:cleared",
		DispatchIntentID: "intent-cleared",
		Placement: &TaskPlacementMetadata{
			PlacementID:    "placement-cleared",
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:        "blocked",
		BlockedAction: "runner_auth_required",
		LastError:     "runner_auth_required: Codex needs sign-in.",
		BodyJSON:      json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt","allowLocalFallback":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	results := retryPendingCloudTaskDispatches(t.Context(), "Bearer owner-token", time.Second)
	if len(results) != 1 || results[0].Err != nil || results[0].TaskID != "task-cleared" {
		t.Fatalf("results = %#v", results)
	}
	if !sawDispatchingClear {
		t.Fatal("did not observe dispatching clearBlockedAction update")
	}
	if remotePayload["userPrompt"] != "secret prompt" || remotePayload["allowLocalFallback"] != true {
		t.Fatalf("remote payload = %#v", remotePayload)
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected cleared dispatch row deleted, got %#v", rows)
	}
}

func TestRenderPendingCloudTaskDispatchStatusIsPromptFree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if err := savePendingCloudTaskDispatch(pendingCloudTaskDispatch{
		LocalTaskID: "pending-cloud:status",
		Placement: &TaskPlacementMetadata{
			Lane:           "cloud_build",
			TargetDeviceID: "cloud-dev",
		},
		Status:        "blocked",
		BodyJSON:      json.RawMessage(`{"title":"build apk","userPrompt":"secret prompt"}`),
		UpdatedAt:     now.Add(-2 * time.Minute),
		Attempts:      2,
		LastError:     "dial failed",
		BlockedAction: "runner_auth_required",
	}); err != nil {
		t.Fatal(err)
	}

	out := renderPendingCloudTaskDispatchStatus(now)
	for _, want := range []string{"Pending Cloud Workspace tasks", "pending-cloud:status", "blocked", "cloud_build", "target=cloud-dev", "attempts=2", "dial failed", "action: runner_auth_required"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"secret prompt", "userPrompt", "build apk"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("status leaked %q:\n%s", forbidden, out)
		}
	}
}
