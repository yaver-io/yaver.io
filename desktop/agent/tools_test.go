package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Webhook tests
// ---------------------------------------------------------------------------

func TestWebhookTriggerNoSecret(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// No secret configured → 503
	status, body := doRequest(t, "POST", baseURL+"/webhooks/trigger", "", `{"title":"test"}`)
	if status != 503 {
		t.Fatalf("expected 503 (no secret configured), got %d: %v", status, body)
	}
}

func TestWebhookTriggerMissingHeader(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Even with a valid body, no X-Webhook-Secret header and no config → 503
	status, _ := doRequest(t, "POST", baseURL+"/webhooks/trigger", "", `{"title":"test webhook"}`)
	if status != 503 {
		t.Fatalf("expected 503 without webhook secret configured, got %d", status)
	}
}

func TestWebhookTriggerWrongMethod(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, _ := doRequest(t, "GET", baseURL+"/webhooks/trigger", "", "")
	if status != 405 {
		t.Fatalf("expected 405 for GET on webhook trigger, got %d", status)
	}
}

func TestWebhookTriggerDefersCloudPlacementInsteadOfRunningLocal(t *testing.T) {
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

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token", WebhookSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := NewHTTPServer(0, "owner-token", "owner", "local-dev", backend.URL, "host", tm)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/trigger", bytes.NewReader([]byte(`{"title":"build apk with secret prompt","description":"ship it","runner":"codex"}`)))
	req.Header.Set("X-Webhook-Secret", "secret")
	rec := httptest.NewRecorder()
	s.handleWebhookTrigger(rec, req)

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

// ---------------------------------------------------------------------------
// Scheduler tests — requires scheduler to be nil (default startTestServer)
// ---------------------------------------------------------------------------

func TestSchedulerNotEnabled(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Scheduler is nil in basic test server → 503
	status, body := doRequest(t, "GET", baseURL+"/schedules", "tok", "")
	if status != 503 {
		t.Fatalf("expected 503 (scheduler not enabled), got %d: %v", status, body)
	}
}

// ---------------------------------------------------------------------------
// Analytics tests — requires analytics to be nil (default startTestServer)
// ---------------------------------------------------------------------------

func TestAnalyticsNotEnabled(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Analytics is nil in basic test server → 503
	status, body := doRequest(t, "GET", baseURL+"/analytics", "tok", "")
	if status != 503 {
		t.Fatalf("expected 503 (analytics not enabled), got %d: %v", status, body)
	}
}

// ---------------------------------------------------------------------------
// Notifications config tests
// ---------------------------------------------------------------------------

func TestNotificationsConfigGet(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Get config (returns default empty config)
	status, body := doRequest(t, "GET", baseURL+"/notifications/config", "tok", "")
	if status != 200 {
		t.Fatalf("get notifications config: expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body)
	}
}

func TestNotificationsConfigAuthRequired(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, _ := doRequest(t, "GET", baseURL+"/notifications/config", "", "")
	if status != 401 {
		t.Fatalf("expected 401 without token, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// Doctor endpoint tests
// ---------------------------------------------------------------------------

func TestDoctorEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/agent/doctor", "tok", "")
	if status != 200 {
		t.Fatalf("doctor: expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body)
	}
	checks, ok := body["checks"].([]interface{})
	if !ok {
		t.Fatalf("expected checks array, got %T", body["checks"])
	}
	if len(checks) == 0 {
		t.Fatal("expected at least one check result")
	}

	// Verify structure of first check
	first := checks[0].(map[string]interface{})
	if first["name"] == nil || first["status"] == nil {
		t.Fatalf("check missing name or status: %v", first)
	}
}

func TestDoctorWrongMethod(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, _ := doRequest(t, "POST", baseURL+"/agent/doctor", "tok", "")
	if status != 405 {
		t.Fatalf("expected 405 for POST on doctor, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// Tools endpoint tests
// ---------------------------------------------------------------------------

func TestToolsEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/agent/tools", "tok", "")
	if status != 200 {
		t.Fatalf("tools: expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body)
	}

	tools, ok := body["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools array, got %v", body["tools"])
	}

	// Verify at least claude is in the list
	foundClaude := false
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		if tm["id"] == "claude" {
			foundClaude = true
			if tm["name"] != "Claude Code" {
				t.Fatalf("expected claude name='Claude Code', got %v", tm["name"])
			}
			break
		}
	}
	if !foundClaude {
		t.Fatal("expected 'claude' in tools list")
	}

	// Verify support tools are present
	support, ok := body["support"].([]interface{})
	if !ok || len(support) == 0 {
		t.Fatalf("expected non-empty support array, got %v", body["support"])
	}
}

func TestToolsEndpointAuthRequired(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, _ := doRequest(t, "GET", baseURL+"/agent/tools", "", "")
	if status != 401 {
		t.Fatalf("expected 401 without token, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// tools.go function tests (unit tests for search/git/system helpers)
// ---------------------------------------------------------------------------

func TestToolsSearchFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "package main")
	writeTestFile(t, dir, "utils.go", "package main")
	writeTestFile(t, dir, "README.md", "# Test")

	result := searchFiles(dir, "*.go", 10)
	if result == "" {
		t.Fatal("expected search results")
	}
	if !strings.Contains(result, "main.go") {
		t.Fatalf("expected main.go in results: %s", result)
	}
	if !strings.Contains(result, "utils.go") {
		t.Fatalf("expected utils.go in results: %s", result)
	}
	if strings.Contains(result, "README.md") {
		t.Fatalf("should not contain README.md: %s", result)
	}
}

func TestToolsSearchFilesEmpty(t *testing.T) {
	dir := t.TempDir()
	result := searchFiles(dir, "*.xyz", 10)
	if result == "" {
		t.Fatal("expected some output even for no matches")
	}
}

func TestToolsSearchContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "package main\nfunc hello() {}\n")
	writeTestFile(t, dir, "test.go", "package main\nfunc TestHello() {}\n")

	result := searchFileContent(dir, "hello", 10)
	if strings.Contains(result, "No matches") {
		t.Fatalf("expected matches for 'hello': %s", result)
	}
}

func TestToolsGitInfo(t *testing.T) {
	// Create a temp git repo for testing
	dir := t.TempDir()
	writeTestFile(t, dir, "test.txt", "hello")

	// Initialize a git repo in the temp dir
	initGit(t, dir)

	result := gitInfo(dir, "status")
	if result == "" {
		t.Fatal("expected git status output")
	}

	result = gitInfo(dir, "log")
	if result == "" {
		t.Fatal("expected git log output")
	}
}

func TestToolsGitInfoUnknownOp(t *testing.T) {
	result := gitInfo("/tmp", "invalid-op")
	if !strings.Contains(result, "Unknown git operation") {
		t.Fatalf("expected unknown operation error, got: %s", result)
	}
}

func TestToolsSystemInfo(t *testing.T) {
	result := getSystemInfo()
	if result == "" {
		t.Fatal("expected system info")
	}
	if !strings.Contains(result, "Hostname:") {
		t.Fatalf("expected Hostname in output: %s", result)
	}
	if !strings.Contains(result, "OS:") {
		t.Fatalf("expected OS in output: %s", result)
	}
	if !strings.Contains(result, "CPUs:") {
		t.Fatalf("expected CPUs in output: %s", result)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// initGit initializes a git repo with one commit in the given directory.
func initGit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := args[0]
		out, err := execCommand(cmd, args[1:]...)
		if err != nil {
			t.Fatalf("git init step %v failed: %v\n%s", args, err, out)
		}
	}
}

func execCommand(name string, args ...string) (string, error) {
	cmd := osExec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
