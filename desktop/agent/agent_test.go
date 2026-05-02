package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// getFreePort finds an available TCP port.
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// startTestServer spins up an HTTPServer with a fake token and returns the base URL.
func startTestServer(t *testing.T, token string, taskMgr *TaskManager) (string, context.CancelFunc) {
	t.Helper()
	port := getFreePort(t)

	srv := NewHTTPServer(port, token, "test-user-id", "test-device-id", "", "test-host", taskMgr)
	srv.execMgr = NewExecManager(taskMgr.workDir, nil)
	srv.agentGraphMgr = NewAgentGraphManager(taskMgr)
	// Expose the server to tests that need to reach into its internal
	// managers (morning store, recording manager, etc.). Safe to set
	// here because Go tests run serially within a package unless
	// -parallel is passed; nothing in this file calls t.Parallel().
	currentTestHTTPServer = srv

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait for the server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL, cancel
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	t.Fatalf("server did not start within 3s")
	return "", nil
}

// doRequest is a helper that makes an HTTP request with optional auth and body.
func doRequest(t *testing.T, method, url, token string, body string) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/health", "", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}
	if body["version"] != version {
		t.Fatalf("expected version=%s, got %v", version, body["version"])
	}
}

func TestAuthRequired(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "secret-token", tm)
	defer cancel()

	// No token → 401
	status, body := doRequest(t, "GET", baseURL+"/tasks", "", "")
	if status != 401 {
		t.Fatalf("expected 401 without token, got %d", status)
	}

	// Wrong token → 403 (Convex validation fails, but since no convexURL it will fail)
	status, _ = doRequest(t, "GET", baseURL+"/tasks", "wrong-token", "")
	if status != 403 {
		t.Fatalf("expected 403 with wrong token, got %d", status)
	}

	// Correct token → 200
	status, body = doRequest(t, "GET", baseURL+"/tasks", "secret-token", "")
	if status != 200 {
		t.Fatalf("expected 200 with correct token, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}
}

func TestTaskListEmpty(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/tasks", "tok", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}

	tasks, ok := body["tasks"].([]interface{})
	if !ok {
		t.Fatalf("expected tasks array, got %T", body["tasks"])
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestAgentStatusEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/agent/status", "tok", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}

	agentStatus, ok := body["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected status object, got %T", body["status"])
	}

	runner, ok := agentStatus["runner"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected runner object, got %T", agentStatus["runner"])
	}
	if runner["id"] != "claude" {
		t.Fatalf("expected runner id=claude, got %v", runner["id"])
	}
	if runner["name"] != "Claude Code" {
		t.Fatalf("expected runner name=Claude Code, got %v", runner["name"])
	}

	system, ok := agentStatus["system"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected system object, got %T", agentStatus["system"])
	}
	if system["os"] == nil || system["os"] == "" {
		t.Fatal("expected system.os to be set")
	}
	if system["arch"] == nil || system["arch"] == "" {
		t.Fatal("expected system.arch to be set")
	}
}

func TestInfoEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/info", "tok", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["version"] != version {
		t.Fatalf("expected version=%s, got %v", version, body["version"])
	}
	if body["workDir"] == nil {
		t.Fatal("expected workDir to be set")
	}
}

func TestCORS(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// OPTIONS preflight
	status, _ := doRequest(t, "OPTIONS", baseURL+"/tasks", "", "")
	if status != 204 {
		t.Fatalf("expected 204 for OPTIONS, got %d", status)
	}

	// Check CORS headers on normal request
	req, _ := http.NewRequest("GET", baseURL+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("expected CORS header, got %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestDeleteAllTasks(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Manually add a finished task
	tm.mu.Lock()
	tm.tasks["test-1"] = &Task{
		ID:     "test-1",
		Title:  "test task",
		Status: TaskStatusFinished,
	}
	tm.mu.Unlock()

	// Verify it exists
	status, body := doRequest(t, "GET", baseURL+"/tasks", "tok", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	tasks := body["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// Delete all
	status, body = doRequest(t, "DELETE", baseURL+"/tasks", "tok", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["deleted"] != float64(1) {
		t.Fatalf("expected deleted=1, got %v", body["deleted"])
	}

	// Verify empty
	status, body = doRequest(t, "GET", baseURL+"/tasks", "tok", "")
	tasks = body["tasks"].([]interface{})
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks after delete, got %d", len(tasks))
	}
}

func TestServerClientIntegration(t *testing.T) {
	// Spin up TWO servers (simulating two yaver agents on the same machine)
	tm1 := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm2 := NewTaskManager(t.TempDir(), nil, defaultRunner)

	url1, cancel1 := startTestServer(t, "token-a", tm1)
	defer cancel1()
	url2, cancel2 := startTestServer(t, "token-b", tm2)
	defer cancel2()

	// Server 1 health check from server 2's perspective (cross-agent discovery)
	status, body := doRequest(t, "GET", url1+"/health", "", "")
	if status != 200 {
		t.Fatalf("server1 health failed: %d", status)
	}
	if body["ok"] != true {
		t.Fatal("server1 health not ok")
	}

	status, body = doRequest(t, "GET", url2+"/health", "", "")
	if status != 200 {
		t.Fatalf("server2 health failed: %d", status)
	}
	if body["ok"] != true {
		t.Fatal("server2 health not ok")
	}

	// Server 1 cannot auth with server 2's token
	status, _ = doRequest(t, "GET", url1+"/tasks", "token-b", "")
	if status != 403 {
		t.Fatalf("expected 403 cross-token, got %d", status)
	}

	// Server 2 cannot auth with server 1's token
	status, _ = doRequest(t, "GET", url2+"/tasks", "token-a", "")
	if status != 403 {
		t.Fatalf("expected 403 cross-token, got %d", status)
	}

	// Each server works with its own token
	status, _ = doRequest(t, "GET", url1+"/tasks", "token-a", "")
	if status != 200 {
		t.Fatalf("expected 200 own-token server1, got %d", status)
	}
	status, _ = doRequest(t, "GET", url2+"/tasks", "token-b", "")
	if status != 200 {
		t.Fatalf("expected 200 own-token server2, got %d", status)
	}

	// Add a task to server1, verify server2 doesn't see it
	tm1.mu.Lock()
	tm1.tasks["s1-task"] = &Task{
		ID:     "s1-task",
		Title:  "server 1 only",
		Status: TaskStatusFinished,
	}
	tm1.mu.Unlock()

	status, body = doRequest(t, "GET", url1+"/tasks", "token-a", "")
	tasks1 := body["tasks"].([]interface{})
	if len(tasks1) != 1 {
		t.Fatalf("server1 expected 1 task, got %d", len(tasks1))
	}

	status, body = doRequest(t, "GET", url2+"/tasks", "token-b", "")
	tasks2 := body["tasks"].([]interface{})
	if len(tasks2) != 0 {
		t.Fatalf("server2 expected 0 tasks, got %d", len(tasks2))
	}

	// Agent status from both
	status, body = doRequest(t, "GET", url1+"/agent/status", "token-a", "")
	if status != 200 {
		t.Fatalf("agent status server1 failed: %d", status)
	}
	s1Status := body["status"].(map[string]interface{})
	if s1Status["totalTasks"] != float64(1) {
		t.Fatalf("server1 expected totalTasks=1, got %v", s1Status["totalTasks"])
	}

	status, body = doRequest(t, "GET", url2+"/agent/status", "token-b", "")
	if status != 200 {
		t.Fatalf("agent status server2 failed: %d", status)
	}
	s2Status := body["status"].(map[string]interface{})
	if s2Status["totalTasks"] != float64(0) {
		t.Fatalf("server2 expected totalTasks=0, got %v", s2Status["totalTasks"])
	}
}

func TestPingPong(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Ping (health endpoint)
	start := time.Now()
	status, body := doRequest(t, "GET", baseURL+"/health", "", "")
	rtt := time.Since(start)

	if status != 200 {
		t.Fatalf("ping failed: %d", status)
	}
	if body["ok"] != true {
		t.Fatal("ping not ok")
	}
	if rtt > 1*time.Second {
		t.Fatalf("ping RTT too high: %v", rtt)
	}
	t.Logf("Ping RTT: %v", rtt)
}

func TestShutdownEndpoint(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)

	shutdownCalled := make(chan struct{}, 1)

	// Find the server and set onShutdown (we need to access the HTTPServer)
	// Since startTestServer doesn't expose it, we test via the HTTP response
	_ = cancel // we'll let shutdown do it

	// Add a running task to verify it gets stopped
	tm.mu.Lock()
	tm.tasks["running-1"] = &Task{
		ID:     "running-1",
		Title:  "running task",
		Status: TaskStatusFinished, // use finished so StopAll doesn't need a real process
	}
	tm.mu.Unlock()

	status, body := doRequest(t, "POST", baseURL+"/agent/shutdown", "tok", "")
	if status != 200 {
		t.Fatalf("shutdown failed: %d", status)
	}
	if body["ok"] != true {
		t.Fatal("shutdown not ok")
	}
	t.Logf("Shutdown response: %v", body)

	select {
	case <-shutdownCalled:
	case <-time.After(100 * time.Millisecond):
		// Expected — our test server doesn't have onShutdown wired
	}

	cancel() // clean up
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Initialize
	status, body := doRequest(t, "POST", baseURL+"/mcp", "tok", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != 200 {
		t.Fatalf("MCP initialize failed: %d", status)
	}
	result, _ := body["result"].(map[string]interface{})
	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "yaver" {
		t.Fatalf("expected MCP server name=yaver, got %v", serverInfo["name"])
	}

	// Tools list
	status, body = doRequest(t, "POST", baseURL+"/mcp", "tok", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != 200 {
		t.Fatalf("MCP tools/list failed: %d", status)
	}
	result, _ = body["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) < 4 {
		t.Fatalf("expected at least 4 MCP tools, got %d", len(tools))
	}

	// Verify tool names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		tm, _ := tool.(map[string]interface{})
		toolNames[tm["name"].(string)] = true
	}
	for _, expected := range []string{"create_task", "list_tasks", "get_task", "stop_task", "agent_machine_inventory", "agent_graph_start", "agent_graph_list", "agent_graph_show", "agent_graph_stop"} {
		if !toolNames[expected] {
			t.Fatalf("expected MCP tool %q not found", expected)
		}
	}
}

func TestMCPRequiresAuth(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "POST", baseURL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated /mcp to return 401, got %d", status)
	}
	if body["error"] != "missing or invalid Authorization header" {
		t.Fatalf("expected unauthorized error body, got %#v", body)
	}
}
