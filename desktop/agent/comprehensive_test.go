package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════════════════════════
// Task CRUD — full lifecycle via HTTP API
// ═══════════════════════════════════════════════════════════════════════

func TestTaskCreate(t *testing.T) {
	token := "test-token-create"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, resp := doRequest(t, "POST", baseURL+"/tasks", token,
		`{"title":"Create test task","description":"Do something"}`)
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp["ok"])
	}
	taskID, _ := resp["taskId"].(string)
	if taskID == "" {
		t.Fatal("expected non-empty taskId")
	}
	if resp["status"] != "queued" && resp["status"] != "running" {
		t.Fatalf("expected queued or running, got %v", resp["status"])
	}
}

func TestTaskCreateMissingTitle(t *testing.T) {
	token := "test-token-notitle"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "POST", baseURL+"/tasks", token, `{"description":"no title"}`)
	if code != 400 {
		t.Fatalf("expected 400, got %d", code)
	}
}

func TestTaskCreateInvalidJSON(t *testing.T) {
	token := "test-token-badjson"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "POST", baseURL+"/tasks", token, `{bad json}`)
	if code != 400 {
		t.Fatalf("expected 400, got %d", code)
	}
}

func TestTaskGetByID(t *testing.T) {
	token := "test-token-getid"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Create task
	_, resp := doRequest(t, "POST", baseURL+"/tasks", token, `{"title":"Get test"}`)
	taskID := resp["taskId"].(string)

	// Get task
	code, detail := doRequest(t, "GET", baseURL+"/tasks/"+taskID, token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	task := detail["task"].(map[string]interface{})
	if task["id"] != taskID {
		t.Fatalf("expected id=%s, got %v", taskID, task["id"])
	}
	if task["title"] != "Get test" {
		t.Fatalf("expected title 'Get test', got %v", task["title"])
	}
}

func TestTaskGetNotFound(t *testing.T) {
	token := "test-token-getnotfound"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "GET", baseURL+"/tasks/nonexistent", token, "")
	if code != 404 && code != 400 {
		t.Fatalf("expected 400/404, got %d", code)
	}
}

func TestTaskStop(t *testing.T) {
	token := "test-token-stop"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	_, resp := doRequest(t, "POST", baseURL+"/tasks", token, `{"title":"Stop test"}`)
	taskID := resp["taskId"].(string)

	// Wait for it to start
	time.Sleep(200 * time.Millisecond)

	code, stopResp := doRequest(t, "POST", baseURL+"/tasks/"+taskID+"/stop", token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if stopResp["ok"] != true {
		t.Fatalf("expected ok=true")
	}

	// Verify stopped
	time.Sleep(200 * time.Millisecond)
	_, detail := doRequest(t, "GET", baseURL+"/tasks/"+taskID, token, "")
	task := detail["task"].(map[string]interface{})
	if task["status"] != "stopped" && task["status"] != "completed" {
		t.Fatalf("expected stopped or completed, got %v", task["status"])
	}
}

func TestTaskDelete(t *testing.T) {
	token := "test-token-delete"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	_, resp := doRequest(t, "POST", baseURL+"/tasks", token, `{"title":"Delete test"}`)
	taskID := resp["taskId"].(string)

	// Wait for completion
	waitForTask(t, baseURL, token, taskID, 30*time.Second)

	// Delete
	code, _ := doRequest(t, "DELETE", baseURL+"/tasks/"+taskID, token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}

	// Verify gone
	code2, _ := doRequest(t, "GET", baseURL+"/tasks/"+taskID, token, "")
	if code2 != 404 {
		t.Fatalf("expected 404 after delete, got %d", code2)
	}
}

func TestTaskContinue(t *testing.T) {
	token := "test-token-continue"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	_, resp := doRequest(t, "POST", baseURL+"/tasks", token, `{"title":"Continue test"}`)
	taskID := resp["taskId"].(string)

	// Wait for it to start running
	time.Sleep(500 * time.Millisecond)

	code, contResp := doRequest(t, "POST", baseURL+"/tasks/"+taskID+"/continue", token,
		`{"message":"Follow up message"}`)
	// Continue may return 200 or 400 depending on task state
	if code != 200 && code != 400 {
		t.Fatalf("expected 200 or 400, got %d: %v", code, contResp)
	}
}

func TestTaskStopNonexistent(t *testing.T) {
	token := "test-token-stopnone"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "POST", baseURL+"/tasks/nonexistent/stop", token, "")
	if code != 404 && code != 400 {
		t.Fatalf("expected 400/404, got %d", code)
	}
}

func TestTaskDeleteNonexistent(t *testing.T) {
	token := "test-token-delnone"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "DELETE", baseURL+"/tasks/nonexistent", token, "")
	if code != 404 && code != 400 {
		t.Fatalf("expected 400/404, got %d", code)
	}
}

func TestTaskCreateWithModel(t *testing.T) {
	token := "test-token-model"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, resp := doRequest(t, "POST", baseURL+"/tasks", token,
		`{"title":"Model test","model":"opus"}`)
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true")
	}
}

func TestTaskCreateWithRunner(t *testing.T) {
	token := "test-token-runner"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, resp := doRequest(t, "POST", baseURL+"/tasks", token,
		`{"title":"Runner test","runner":"claude"}`)
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Multiple tasks & concurrency
// ═══════════════════════════════════════════════════════════════════════

func TestMultipleTasks(t *testing.T) {
	token := "test-token-multi"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Create 5 tasks
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		_, resp := doRequest(t, "POST", baseURL+"/tasks", token,
			fmt.Sprintf(`{"title":"Task %d"}`, i))
		ids[i] = resp["taskId"].(string)
	}

	// List should show all 5
	_, listResp := doRequest(t, "GET", baseURL+"/tasks", token, "")
	tasks := listResp["tasks"].([]interface{})
	if len(tasks) < 5 {
		t.Fatalf("expected at least 5 tasks, got %d", len(tasks))
	}

	// Delete all
	code, _ := doRequest(t, "DELETE", baseURL+"/tasks/delete-all", token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200 for delete all, got %d", code)
	}
}

func TestConcurrentTaskCreation(t *testing.T) {
	token := "test-token-concurrent"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			code, resp := doRequest(t, "POST", baseURL+"/tasks", token,
				fmt.Sprintf(`{"title":"Concurrent task %d"}`, n))
			if code != 200 && code != 201 {
				errors <- fmt.Errorf("task %d: expected 200/201, got %d", n, code)
				return
			}
			if resp["ok"] != true {
				errors <- fmt.Errorf("task %d: expected ok=true", n)
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Task status transitions (dummy mode)
// ═══════════════════════════════════════════════════════════════════════

func TestTaskStatusTransition(t *testing.T) {
	token := "test-token-status"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	_, resp := doRequest(t, "POST", baseURL+"/tasks", token, `{"title":"Status test"}`)
	taskID := resp["taskId"].(string)

	// Should transition through queued/running → completed
	status := waitForTask(t, baseURL, token, taskID, 30*time.Second)
	if status != "completed" {
		t.Fatalf("expected completed, got %s", status)
	}

	// Verify output is non-empty
	_, detail := doRequest(t, "GET", baseURL+"/tasks/"+taskID, token, "")
	task := detail["task"].(map[string]interface{})
	output, _ := task["output"].(string)
	if len(output) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestStopAllTasks(t *testing.T) {
	token := "test-token-stopall"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Create a few tasks
	for i := 0; i < 3; i++ {
		doRequest(t, "POST", baseURL+"/tasks", token, fmt.Sprintf(`{"title":"StopAll %d"}`, i))
	}

	time.Sleep(200 * time.Millisecond)

	// Stop all
	code, resp := doRequest(t, "POST", baseURL+"/tasks/stop-all", token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Auth edge cases
// ═══════════════════════════════════════════════════════════════════════

func TestAuthNoHeader(t *testing.T) {
	token := "test-token-noheader"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "GET", baseURL+"/info", "", "")
	if code != 401 && code != 403 {
		t.Fatalf("expected 401/403, got %d", code)
	}
}

func TestAuthWrongToken(t *testing.T) {
	token := "test-token-wrongtoken"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, _ := doRequest(t, "GET", baseURL+"/info", "wrong-token", "")
	if code != 401 && code != 403 {
		t.Fatalf("expected 401/403, got %d", code)
	}
}

func TestAuthEmptyBearer(t *testing.T) {
	token := "test-token-emptybearer"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	req, _ := http.NewRequest("GET", baseURL+"/info", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 && resp.StatusCode != 403 {
		t.Fatalf("expected 401/403, got %d", resp.StatusCode)
	}
}

func TestHealthNoAuth(t *testing.T) {
	token := "test-token-healthnoauth"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Health should work without auth
	code, resp := doRequest(t, "GET", baseURL+"/health", "", "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// MCP — tool calls
// ═══════════════════════════════════════════════════════════════════════

func TestMCPCreateTask(t *testing.T) {
	token := "test-token-mcpcreate"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Initialize
	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	// Create task
	resp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"create_task","arguments":{"prompt":"MCP test task"}
	}}`)
	task := mcpResultJSON(t, resp)["task"].(map[string]interface{})
	if got := task["title"]; got != "MCP test task" {
		t.Fatalf("expected created task title, got %v", got)
	}
	if got := task["source"]; got != "mcp" {
		t.Fatalf("expected source=mcp, got %v", got)
	}
}

func TestMCPCreateTaskWithVerbosity(t *testing.T) {
	token := "test-token-mcpverb"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	resp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"create_task","arguments":{"prompt":"Verbosity test","verbosity":2}
	}}`)
	task := mcpResultJSON(t, resp)["task"].(map[string]interface{})
	if got := task["title"]; got != "Verbosity test" {
		t.Fatalf("expected created task title, got %v", got)
	}
}

func TestMCPListTasks(t *testing.T) {
	token := "test-token-mcplist"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	// Create a task first
	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"create_task","arguments":{"prompt":"List test"}
	}}`)

	// List
	resp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
		"name":"list_tasks","arguments":{}
	}}`)
	result := resp["result"].(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "List test") {
		t.Fatalf("expected task title in list, got: %s", text)
	}
}

func TestMCPGetTask(t *testing.T) {
	token := "test-token-mcpget"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	// Create
	createResp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"create_task","arguments":{"prompt":"Get test"}
	}}`)
	taskID, _ := mcpResultJSON(t, createResp)["task"].(map[string]interface{})["id"].(string)
	if taskID == "" {
		t.Fatalf("could not extract task ID from create_task response: %#v", createResp)
	}

	// Get task
	resp := doMCPRequest(t, baseURL, token, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
		"name":"get_task","arguments":{"task_id":"%s"}
	}}`, taskID))
	result := resp["result"].(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, taskID) {
		t.Fatalf("expected task ID in result, got: %s", text)
	}
}

func TestMCPToolNotFound(t *testing.T) {
	token := "test-token-mcpnotfound"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	resp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"nonexistent_tool","arguments":{}
	}}`)
	// Should have error in result
	result := resp["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Fatalf("expected isError=true for nonexistent tool")
	}
}

func TestMCPAgentGraphStartAndList(t *testing.T) {
	token := "test-token-mcp-agent-graph"
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}
	}}`)

	startResp := doMCPRequest(t, baseURL, token, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"agent_graph_start","arguments":{
			"work_dir":%q,
			"prompt":"Ship onboarding and keep mobile releases green",
			"template":"ship",
			"allowed_devices":["local"]
		}
	}}`, workDir))
	startResult := startResp["result"].(map[string]interface{})
	startContent := startResult["content"].([]interface{})
	startText := startContent[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(startText, "Agent graph started.") {
		t.Fatalf("expected graph start confirmation, got: %s", startText)
	}
	if !strings.Contains(startText, "Machine pool: local") {
		t.Fatalf("expected machine pool in result, got: %s", startText)
	}

	listResp := doMCPRequest(t, baseURL, token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
		"name":"agent_graph_list","arguments":{}
	}}`)
	listResult := listResp["result"].(map[string]interface{})
	listContent := listResult["content"].([]interface{})
	listText := listContent[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(listText, "nodes=2") {
		t.Fatalf("expected ship template node count, got: %s", listText)
	}
	if !strings.Contains(listText, "@ ") {
		t.Fatalf("expected node placement output, got: %s", listText)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Config
// ═══════════════════════════════════════════════════════════════════════

func TestConfigLoadSaveRoundtrip(t *testing.T) {
	// Override home dir for test
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	cfg := &Config{
		AuthToken:     "test-token",
		DeviceID:      "test-device",
		ConvexSiteURL: "https://test.convex.site",
		RelayPassword: "test-pass",
		RelayServers: []RelayServerConfig{
			{ID: "r1", QuicAddr: "1.2.3.4:4433", HttpURL: "https://relay.example.com", Password: "pw"},
		},
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.AuthToken != cfg.AuthToken {
		t.Fatalf("expected AuthToken=%s, got %s", cfg.AuthToken, loaded.AuthToken)
	}
	if loaded.DeviceID != cfg.DeviceID {
		t.Fatalf("expected DeviceID=%s, got %s", cfg.DeviceID, loaded.DeviceID)
	}
	if len(loaded.RelayServers) != 1 {
		t.Fatalf("expected 1 relay server, got %d", len(loaded.RelayServers))
	}
}

func TestConfigLoadEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on empty should not fail: %v", err)
	}
	if cfg.AuthToken != "" {
		t.Fatalf("expected empty AuthToken, got %s", cfg.AuthToken)
	}
}

func TestConfigLoadCorrupted(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	configDir := filepath.Join(tmpHome, ".yaver")
	os.MkdirAll(configDir, 0700)
	os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{corrupted"), 0600)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for corrupted config")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Task persistence (store)
// ═══════════════════════════════════════════════════════════════════════

func TestTaskStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	store, err := NewTaskStore()
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	tm := NewTaskManager(dir, store, defaultTestRunner())
	tm.DummyMode = true

	task, err := tm.CreateTask("Persist test", "", "", "test", "", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Wait for completion
	time.Sleep(5 * time.Second)

	// Reload store
	store2, _ := NewTaskStore()
	loaded := store2.Load()
	if _, ok := loaded[task.ID]; !ok {
		t.Fatalf("task %s not found in persisted store", task.ID)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Runners endpoint
// ═══════════════════════════════════════════════════════════════════════

func TestRunnersEndpoint(t *testing.T) {
	token := "test-token-runners"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	code, resp := doRequest(t, "GET", baseURL+"/agent/status", token, "")
	if code != 200 && code != 201 {
		t.Fatalf("expected 200/201, got %d", code)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true")
	}
}

// ═══════════════════════════════════════════════════════════════════════
// CORS
// ═══════════════════════════════════════════════════════════════════════

func TestCORSPreflight(t *testing.T) {
	token := "test-token-cors"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	req, _ := http.NewRequest("OPTIONS", baseURL+"/tasks", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		t.Fatalf("expected 200 or 204, got %d", resp.StatusCode)
	}
	if h := resp.Header.Get("Access-Control-Allow-Origin"); h == "" {
		t.Fatal("expected Access-Control-Allow-Origin header")
	}
}

func TestCORSOnResponse(t *testing.T) {
	token := "test-token-corsresp"
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	req, _ := http.NewRequest("GET", baseURL+"/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if h := resp.Header.Get("Access-Control-Allow-Origin"); h != "*" {
		t.Fatalf("expected CORS header *, got %s", h)
	}
}

// ═══════════════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════════════

func defaultTestRunner() RunnerConfig {
	return RunnerConfig{
		RunnerID:   "claude",
		Name:       "Claude",
		Command:    "claude",
		Args:       []string{"--print", "--output-format", "stream-json"},
		OutputMode: "stream-json",
	}
}

func waitForTask(t *testing.T, baseURL, token, taskID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, detail := doRequest(t, "GET", baseURL+"/tasks/"+taskID, token, "")
		task, ok := detail["task"].(map[string]interface{})
		if !ok {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		status, _ := task["status"].(string)
		if status == "completed" || status == "failed" || status == "stopped" {
			return status
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish within %v", taskID, timeout)
	return ""
}

func doMCPRequest(t *testing.T, baseURL, token, body string) map[string]interface{} {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create MCP request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("MCP request failed: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode MCP response: %v", err)
	}
	return result
}

func mcpResultJSON(t *testing.T, resp map[string]interface{}) map[string]interface{} {
	t.Helper()
	result, _ := resp["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("expected MCP content in response: %#v", resp)
	}
	text, _ := content[0].(map[string]interface{})["text"].(string)
	if text == "" {
		t.Fatalf("expected MCP text payload in response: %#v", resp)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected JSON text payload, got %q: %v", text, err)
	}
	return parsed
}
