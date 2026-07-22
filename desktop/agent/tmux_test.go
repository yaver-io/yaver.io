package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearDiscoveryCache drops the 60s memo so a test can observe a lookup made
// under the $PATH it just set, rather than one cached by an earlier caller.
func clearDiscoveryCache(t *testing.T) {
	t.Helper()
	discoveryMu.Lock()
	discoveryCache = map[string]discoveryEntry{}
	discoveryMu.Unlock()
}

// The agent is launched with a minimal $PATH (launchd/systemd), which omits
// /opt/homebrew/bin. augmentAgentPATH() normally repairs that at startup, but
// it bails when os.UserHomeDir() fails and it never runs for code reached
// outside main(). tmux is load-bearing for every runner seat, so its resolution
// must not depend on that repair having happened.
func TestTmuxIsFoundWhenItIsInstalledOffPath(t *testing.T) {
	// Locate tmux via a prefix the daemon would NOT have on $PATH. If tmux
	// only exists on $PATH here, there is nothing to prove.
	var offPath string
	for _, prefix := range commonInstallPrefixes() {
		candidate := filepath.Join(prefix, "tmux")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			offPath = candidate
			break
		}
	}
	if offPath == "" {
		t.Skip("tmux is not installed in a known prefix; nothing to resolve")
	}

	t.Setenv("PATH", "") // exactly what LookPath cannot survive
	clearDiscoveryCache(t)
	defer clearDiscoveryCache(t)

	if _, err := exec.LookPath("tmux"); err == nil {
		t.Fatal("precondition: LookPath must fail with an empty PATH")
	}
	got := tmuxBin()
	if got == "" {
		t.Fatalf("tmux is installed at %s but tmuxBin() reported it missing", offPath)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("tmuxBin() must return an absolute path (callers exec it under the same broken PATH), got %q", got)
	}
	if !tmuxAvailable() {
		t.Fatal("tmuxAvailable() must agree with tmuxBin()")
	}
	// argv[0] must be the resolved path, never the bare name — a bare name
	// re-inherits the same $PATH the lookup just worked around.
	if name := tmuxCmdName(); name != got {
		t.Fatalf("tmuxCmdName() = %q, want the resolved %q", name, got)
	}
}

// serve calls EnsureTmuxInstalled on every startup, so the already-installed
// path must be free: no package manager, no subprocess, no log noise. A
// regression here would make every agent boot shell out to brew/apt.
func TestEnsureTmuxInstalledIsANoOpWhenPresent(t *testing.T) {
	if tmuxBin() == "" {
		t.Skip("tmux not installed here; nothing to short-circuit")
	}
	logged := 0
	ok := EnsureTmuxInstalled(context.Background(), func(string, ...interface{}) { logged++ })
	if !ok {
		t.Fatal("must report tmux usable when it is installed")
	}
	if logged != 0 {
		t.Fatalf("must not log or act on the happy path, logged %d line(s)", logged)
	}
}

// skipIfNoTmux skips the test if tmux is not installed.
func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed — skipping")
	}
}

// createTestTmuxSession creates a tmux session for testing and returns a cleanup function.
func createTestTmuxSession(t *testing.T, name string) func() {
	t.Helper()
	out, err := exec.Command("tmux", "new-session", "-d", "-s", name).CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create tmux session %q: %v: %s", name, err, string(out))
	}
	return func() {
		exec.Command("tmux", "kill-session", "-t", name).Run()
	}
}

// --- Unit tests for helper functions ---

func TestParseTmuxSessionLine(t *testing.T) {
	tests := []struct {
		line     string
		name     string
		id       string
		windows  int
		attached bool
	}{
		{"my-session|$1|3|1710000000|0", "my-session", "$1", 3, false},
		{"dev|$2|1|1710000000|1", "dev", "$2", 1, true},
		{"solo|$3|2||0", "solo", "$3", 2, false},
	}

	for _, tt := range tests {
		s := parseTmuxSessionLine(tt.line)
		if s.Name != tt.name {
			t.Errorf("parseTmuxSessionLine(%q): name=%q, want %q", tt.line, s.Name, tt.name)
		}
		if s.ID != tt.id {
			t.Errorf("parseTmuxSessionLine(%q): id=%q, want %q", tt.line, s.ID, tt.id)
		}
		if s.Windows != tt.windows {
			t.Errorf("parseTmuxSessionLine(%q): windows=%d, want %d", tt.line, s.Windows, tt.windows)
		}
		if s.Attached != tt.attached {
			t.Errorf("parseTmuxSessionLine(%q): attached=%v, want %v", tt.line, s.Attached, tt.attached)
		}
	}
}

func TestMatchAgentCommand(t *testing.T) {
	tests := []struct {
		cmd    string
		expect string
	}{
		{"/usr/local/bin/claude -p hello", "claude"},
		{"claude ", "claude"},
		{"codex --quiet --full-auto test", "codex"},
		{"/home/user/.local/bin/opencode run hello", "opencode"},
		{"opencode --help", "opencode"},
		{"bash", ""},
		{"python3 script.py", ""},
		{"vim", ""},
	}

	for _, tt := range tests {
		got := matchAgentCommand(tt.cmd)
		if got != tt.expect {
			t.Errorf("matchAgentCommand(%q) = %q, want %q", tt.cmd, got, tt.expect)
		}
	}
}

func TestLastNonEmptyLines(t *testing.T) {
	lines := []string{"", "hello", "", "world", "", "foo", "", ""}
	result := lastNonEmptyLines(lines, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if result[0] != "world" || result[1] != "foo" {
		t.Errorf("got %v, want [world, foo]", result)
	}
}

func TestDiffCapture(t *testing.T) {
	prev := "line1\nline2\nline3\n"
	curr := "line1\nline2\nline3\nline4\nline5\n"
	diff := diffCapture(prev, curr)
	if !strings.Contains(diff, "line4") || !strings.Contains(diff, "line5") {
		t.Errorf("diff should contain new lines, got: %q", diff)
	}
	if strings.Contains(diff, "line1") {
		t.Errorf("diff should not contain old lines, got: %q", diff)
	}
}

func TestDiffCaptureEmpty(t *testing.T) {
	// Empty prev returns the whole current
	diff := diffCapture("", "hello\nworld\n")
	if diff != "hello\nworld\n" {
		t.Errorf("expected full capture, got: %q", diff)
	}
}

// --- Integration tests (require tmux) ---

func TestTmuxSessionExists(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-exists")
	defer cleanup()

	if !tmuxSessionExists("yaver-test-exists") {
		t.Error("expected session to exist")
	}
	if tmuxSessionExists("yaver-nonexistent-session") {
		t.Error("expected nonexistent session to not exist")
	}
}

func TestTmuxManagerListSessions(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-list")
	defer cleanup()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)
	if mgr == nil {
		t.Fatal("TmuxManager should not be nil when tmux is available")
	}

	sessions, err := mgr.ListTmuxSessions()
	if err != nil {
		t.Fatalf("ListTmuxSessions: %v", err)
	}

	found := false
	for _, s := range sessions {
		if s.Name == "yaver-test-list" {
			found = true
			if s.Relationship != "unrelated" {
				t.Errorf("expected relationship=unrelated, got %q", s.Relationship)
			}
			if s.Windows < 1 {
				t.Errorf("expected at least 1 window, got %d", s.Windows)
			}
		}
	}
	if !found {
		t.Error("yaver-test-list session not found in ListTmuxSessions output")
	}
}

func TestTmuxAdoptAndDetach(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-adopt")
	defer cleanup()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)
	if mgr == nil {
		t.Fatal("TmuxManager should not be nil")
	}

	// Adopt
	task, err := mgr.AdoptSession("yaver-test-adopt")
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.Status != TaskStatusRunning {
		t.Errorf("expected status=running, got %s", task.Status)
	}
	if task.TmuxSession != "yaver-test-adopt" {
		t.Errorf("expected TmuxSession=yaver-test-adopt, got %s", task.TmuxSession)
	}
	if !task.IsAdopted {
		t.Error("expected IsAdopted=true")
	}
	if task.Source != "tmux-adopted" {
		t.Errorf("expected source=tmux-adopted, got %s", task.Source)
	}

	// Verify it shows as adopted in list
	sessions, _ := mgr.ListTmuxSessions()
	for _, s := range sessions {
		if s.Name == "yaver-test-adopt" {
			if s.Relationship != "adopted" {
				t.Errorf("expected adopted, got %q", s.Relationship)
			}
			if s.TaskID != task.ID {
				t.Errorf("expected taskID=%s, got %s", task.ID, s.TaskID)
			}
		}
	}

	// Double adopt should fail
	_, err = mgr.AdoptSession("yaver-test-adopt")
	if err == nil {
		t.Error("expected error on double adopt")
	}

	// Detach
	err = mgr.DetachSession(task.ID)
	if err != nil {
		t.Fatalf("DetachSession: %v", err)
	}

	// Verify task is stopped
	tm.mu.RLock()
	detachedTask := tm.tasks[task.ID]
	tm.mu.RUnlock()
	if detachedTask.Status != TaskStatusStopped {
		t.Errorf("expected status=stopped after detach, got %s", detachedTask.Status)
	}

	// Verify tmux session still exists
	if !tmuxSessionExists("yaver-test-adopt") {
		t.Error("tmux session should still exist after detach")
	}

	// Verify it's no longer adopted in list
	sessions, _ = mgr.ListTmuxSessions()
	for _, s := range sessions {
		if s.Name == "yaver-test-adopt" && s.Relationship == "adopted" {
			t.Error("session should no longer be adopted after detach")
		}
	}
}

func TestTmuxAdoptNonexistent(t *testing.T) {
	skipIfNoTmux(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)
	_, err := mgr.AdoptSession("yaver-nonexistent-session-xyz")
	if err == nil {
		t.Error("expected error when adopting nonexistent session")
	}
}

func TestTmuxDetachNonAdopted(t *testing.T) {
	skipIfNoTmux(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)
	err := mgr.DetachSession("fake-task-id")
	if err == nil {
		t.Error("expected error when detaching non-adopted task")
	}
}

func TestTmuxPollOutput(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-poll")
	defer cleanup()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)

	task, err := mgr.AdoptSession("yaver-test-poll")
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}

	// Send some text to the session
	exec.Command("tmux", "send-keys", "-t", "yaver-test-poll", "echo hello-yaver-test", "Enter").Run()

	// Wait for polling to pick it up
	time.Sleep(2 * time.Second)

	tm.mu.RLock()
	output := tm.tasks[task.ID].Output
	tm.mu.RUnlock()

	if !strings.Contains(output, "hello-yaver-test") {
		t.Errorf("expected output to contain 'hello-yaver-test', got: %q", output)
	}

	// Cleanup
	mgr.Shutdown()
}

func TestTmuxSendInput(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-input")
	defer cleanup()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)

	task, err := mgr.AdoptSession("yaver-test-input")
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}

	// allowShell: this fixture is a bare shell pane, so "echo from-mobile" is a
	// COMMAND, not a prompt. The default path refuses that on purpose — see
	// SendTmuxInputWithIntent.
	err = mgr.SendTmuxInputWithIntent(task.ID, "echo from-mobile", true)
	if err != nil {
		t.Fatalf("SendTmuxInputWithIntent: %v", err)
	}

	// Wait for the command to execute and polling to capture
	time.Sleep(2 * time.Second)

	tm.mu.RLock()
	output := tm.tasks[task.ID].Output
	turns := tm.tasks[task.ID].Turns
	tm.mu.RUnlock()

	if !strings.Contains(output, "from-mobile") {
		t.Errorf("expected output to contain 'from-mobile', got: %q", output)
	}

	// Check that a turn was recorded
	if len(turns) == 0 {
		t.Error("expected at least one conversation turn")
	} else if turns[len(turns)-1].Content != "echo from-mobile" {
		t.Errorf("expected last turn content='echo from-mobile', got %q", turns[len(turns)-1].Content)
	}

	mgr.Shutdown()
}

func TestTmuxReAdoptOnStartup(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-readopt")
	defer cleanup()

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)

	// Adopt a session
	task, err := mgr.AdoptSession("yaver-test-readopt")
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}
	mgr.Shutdown()

	// Simulate restart: create new TmuxManager with the same TaskManager
	mgr2 := NewTmuxManager(tm)
	mgr2.ReAdoptOnStartup()
	defer mgr2.Shutdown()

	// The task should be re-adopted (still running)
	tm.mu.RLock()
	taskAfter := tm.tasks[task.ID]
	tm.mu.RUnlock()
	if taskAfter.Status != TaskStatusRunning {
		t.Errorf("expected status=running after re-adopt, got %s", taskAfter.Status)
	}

	// Verify it's in the adopted map. Adoption is keyed by PANE id, not by
	// session name: one session split across panes is several agents and
	// therefore several tasks, and a session key caps it at one.
	mgr2.mu.RLock()
	_, adopted := mgr2.adopted[adoptionKey(taskAfter.TmuxSession, taskAfter.TmuxPaneID)]
	mgr2.mu.RUnlock()
	if !adopted {
		t.Error("expected the task's pane to be in the adopted map after re-adopt")
	}
}

func TestTmuxSessionDisappearsMarksTaskFinished(t *testing.T) {
	skipIfNoTmux(t)

	// Create a session that we'll kill
	out, err := exec.Command("tmux", "new-session", "-d", "-s", "yaver-test-disappear").CombinedOutput()
	if err != nil {
		t.Fatalf("create session: %v: %s", err, string(out))
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	mgr := NewTmuxManager(tm)
	defer mgr.Shutdown()

	task, err := mgr.AdoptSession("yaver-test-disappear")
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}

	// Kill the session
	exec.Command("tmux", "kill-session", "-t", "yaver-test-disappear").Run()

	// Wait for poll to detect disappearance
	time.Sleep(2 * time.Second)

	tm.mu.RLock()
	status := tm.tasks[task.ID].Status
	tm.mu.RUnlock()

	if status != TaskStatusFinished {
		t.Errorf("expected status=completed after session disappears, got %s", status)
	}
}

// --- HTTP endpoint tests ---

func TestTmuxHTTPEndpoints(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-http")
	defer cleanup()

	token := "tmux-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.TmuxMgr = NewTmuxManager(tm)
	defer tm.TmuxMgr.Shutdown()

	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// GET /tmux/sessions
	status, body := doRequest(t, "GET", baseURL+"/tmux/sessions", token, "")
	if status != 200 {
		t.Fatalf("GET /tmux/sessions: expected 200, got %d", status)
	}
	sessionsRaw, ok := body["sessions"]
	if !ok {
		t.Fatal("expected 'sessions' key in response")
	}
	sessions, ok := sessionsRaw.([]interface{})
	if !ok {
		t.Fatalf("expected sessions to be array, got %T", sessionsRaw)
	}
	found := false
	for _, s := range sessions {
		sm := s.(map[string]interface{})
		if sm["name"] == "yaver-test-http" {
			found = true
			if sm["relationship"] != "unrelated" {
				t.Errorf("expected relationship=unrelated, got %v", sm["relationship"])
			}
		}
	}
	if !found {
		t.Error("session yaver-test-http not found in /tmux/sessions response")
	}

	// POST /tmux/adopt
	status, body = doRequest(t, "POST", baseURL+"/tmux/adopt", token, `{"session":"yaver-test-http"}`)
	if status != 200 {
		t.Fatalf("POST /tmux/adopt: expected 200, got %d: %v", status, body)
	}
	taskID, _ := body["taskId"].(string)
	if taskID == "" {
		t.Fatal("expected non-empty taskId")
	}

	// POST /tmux/input
	status, body = doRequest(t, "POST", baseURL+"/tmux/input", token, fmt.Sprintf(`{"taskId":%q,"input":"echo test-http-input","allowShell":true}`, taskID))
	if status != 200 {
		t.Fatalf("POST /tmux/input: expected 200, got %d: %v", status, body)
	}

	// POST /tmux/detach
	status, body = doRequest(t, "POST", baseURL+"/tmux/detach", token, fmt.Sprintf(`{"taskId":%q}`, taskID))
	if status != 200 {
		t.Fatalf("POST /tmux/detach: expected 200, got %d: %v", status, body)
	}

	// Verify session still exists
	if !tmuxSessionExists("yaver-test-http") {
		t.Error("tmux session should still exist after detach via HTTP")
	}
}

func TestTmuxHTTPAdoptNonexistent(t *testing.T) {
	skipIfNoTmux(t)

	token := "tmux-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.TmuxMgr = NewTmuxManager(tm)
	defer tm.TmuxMgr.Shutdown()

	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	status, body := doRequest(t, "POST", baseURL+"/tmux/adopt", token, `{"session":"nonexistent-session-xyz"}`)
	if status != 400 {
		t.Fatalf("expected 400 for nonexistent session, got %d: %v", status, body)
	}
}

func TestTmuxHTTPNoTmux(t *testing.T) {
	// Test endpoints when TmuxMgr is nil
	token := "tmux-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	// Don't set TmuxMgr — it stays nil

	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// GET /tmux/sessions should return empty array, not error
	status, body := doRequest(t, "GET", baseURL+"/tmux/sessions", token, "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	sessions := body["sessions"].([]interface{})
	if len(sessions) != 0 {
		t.Errorf("expected empty sessions, got %d", len(sessions))
	}

	// POST /tmux/adopt should return 503
	status, _ = doRequest(t, "POST", baseURL+"/tmux/adopt", token, `{"session":"foo"}`)
	if status != 503 {
		t.Fatalf("expected 503, got %d", status)
	}
}

// --- MCP tool tests ---

func TestMCPTmuxToolsInToolsList(t *testing.T) {
	token := "tmux-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Call MCP initialize
	mcpReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	doRequest(t, "POST", baseURL+"/mcp", token, mcpReq)

	// Call tools/list
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	status, body := doRequest(t, "POST", baseURL+"/mcp", token, toolsReq)
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}

	result, ok := body["result"].(map[string]interface{})
	if !ok {
		t.Fatal("expected result in response")
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array in result")
	}

	tmuxTools := map[string]bool{
		"tmux_list_sessions":  false,
		"tmux_adopt_session":  false,
		"tmux_detach_session": false,
		"tmux_send_input":     false,
	}

	for _, tool := range tools {
		tm := tool.(map[string]interface{})
		name, _ := tm["name"].(string)
		if _, exists := tmuxTools[name]; exists {
			tmuxTools[name] = true
		}
	}

	for name, found := range tmuxTools {
		if !found {
			t.Errorf("MCP tool %q not found in tools/list", name)
		}
	}
}

func TestMCPTmuxListSessions(t *testing.T) {
	skipIfNoTmux(t)
	cleanup := createTestTmuxSession(t, "yaver-test-mcp-list")
	defer cleanup()

	token := "tmux-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.TmuxMgr = NewTmuxManager(tm)
	defer tm.TmuxMgr.Shutdown()

	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// Initialize MCP
	doRequest(t, "POST", baseURL+"/mcp", token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)

	// Call tmux_list_sessions
	callReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tmux_list_sessions","arguments":{}}}`
	status, body := doRequest(t, "POST", baseURL+"/mcp", token, callReq)
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}

	result, _ := body["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("expected content in MCP response")
	}
	text, _ := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "yaver-test-mcp-list") {
		t.Errorf("expected output to contain session name, got: %s", text)
	}
}

// --- Store persistence tests ---

func TestStoreAdoptedTaskPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := newTaskStoreAt(dir)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	now := time.Now()
	tasks := map[string]*Task{
		"t1": {
			ID:          "t1",
			Title:       "tmux: my-session",
			Status:      TaskStatusRunning,
			Source:      "tmux-adopted",
			TmuxSession: "my-session",
			IsAdopted:   true,
			CreatedAt:   now,
			StartedAt:   &now,
		},
		"t2": {
			ID:        "t2",
			Title:     "normal task",
			Status:    TaskStatusRunning,
			Source:    "mobile",
			CreatedAt: now,
			StartedAt: &now,
		},
	}

	store.Save(tasks)

	// Load back
	loaded := store.Load()

	// Adopted task should still be running (not auto-stopped)
	if loaded["t1"].Status != TaskStatusRunning {
		t.Errorf("adopted task: expected status=running, got %s", loaded["t1"].Status)
	}
	if !loaded["t1"].IsAdopted {
		t.Error("adopted task: expected IsAdopted=true")
	}
	if loaded["t1"].TmuxSession != "my-session" {
		t.Errorf("adopted task: expected TmuxSession=my-session, got %s", loaded["t1"].TmuxSession)
	}

	// Normal running task should be stopped on load
	if loaded["t2"].Status != TaskStatusStopped {
		t.Errorf("normal task: expected status=stopped, got %s", loaded["t2"].Status)
	}
}

// newTaskStoreAt creates a TaskStore at a specific directory for testing.
func newTaskStoreAt(dir string) (*TaskStore, error) {
	return &TaskStore{
		path: dir + "/tasks.json",
	}, nil
}

// --- Full E2E integration test (emulates mobile → server → tmux) ---

// TestTmuxE2EFullFlow is the comprehensive end-to-end test that verifies:
// 1. Server starts with TmuxManager
// 2. Tmux sessions are discovered via HTTP
// 3. A session can be adopted via HTTP (mobile emulation)
// 4. Input sent via HTTP reaches the tmux pane
// 5. Output from tmux is captured and appears in task output
// 6. Conversation turns are recorded
// 7. Task appears in task list with isAdopted=true
// 8. Session can be detached — tmux session survives, task marked stopped
// 9. MCP tools work for tmux operations
// 10. A second session can be adopted concurrently
func TestTmuxE2EFullFlow(t *testing.T) {
	skipIfNoTmux(t)

	// Create two tmux sessions
	cleanup1 := createTestTmuxSession(t, "yaver-e2e-session1")
	defer cleanup1()
	cleanup2 := createTestTmuxSession(t, "yaver-e2e-session2")
	defer cleanup2()

	// Seed session1 with some initial output
	exec.Command("tmux", "send-keys", "-t", "yaver-e2e-session1", "echo 'initial-output-session1'", "Enter").Run()
	time.Sleep(500 * time.Millisecond)

	// Start server
	token := "e2e-test-token"
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.TmuxMgr = NewTmuxManager(tm)
	defer tm.TmuxMgr.Shutdown()

	baseURL, cancel := startTestServer(t, token, tm)
	defer cancel()

	// ── Step 1: List sessions — both should appear as "unrelated" ──
	status, body := doRequest(t, "GET", baseURL+"/tmux/sessions", token, "")
	if status != 200 {
		t.Fatalf("list sessions: expected 200, got %d", status)
	}
	sessions := body["sessions"].([]interface{})
	found1, found2 := false, false
	for _, s := range sessions {
		sm := s.(map[string]interface{})
		name, _ := sm["name"].(string)
		rel, _ := sm["relationship"].(string)
		if name == "yaver-e2e-session1" {
			found1 = true
			if rel != "unrelated" {
				t.Errorf("session1: expected unrelated, got %s", rel)
			}
		}
		if name == "yaver-e2e-session2" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("expected both sessions in list (found1=%v, found2=%v)", found1, found2)
	}

	// ── Step 2: Adopt session1 ──
	status, body = doRequest(t, "POST", baseURL+"/tmux/adopt", token,
		`{"session":"yaver-e2e-session1"}`)
	if status != 200 {
		t.Fatalf("adopt: expected 200, got %d: %v", status, body)
	}
	taskID1, _ := body["taskId"].(string)
	if taskID1 == "" {
		t.Fatal("expected taskId after adopt")
	}

	// ── Step 3: Verify task in task list with tmux fields ──
	status, body = doRequest(t, "GET", baseURL+"/tasks", token, "")
	if status != 200 {
		t.Fatalf("list tasks: expected 200, got %d", status)
	}
	tasks := body["tasks"].([]interface{})
	foundTask := false
	for _, task := range tasks {
		taskMap := task.(map[string]interface{})
		if taskMap["id"] == taskID1 {
			foundTask = true
			if taskMap["isAdopted"] != true {
				t.Errorf("expected isAdopted=true, got %v", taskMap["isAdopted"])
			}
			if taskMap["tmuxSession"] != "yaver-e2e-session1" {
				t.Errorf("expected tmuxSession=yaver-e2e-session1, got %v", taskMap["tmuxSession"])
			}
			if taskMap["status"] != "running" {
				t.Errorf("expected status=running, got %v", taskMap["status"])
			}
			if taskMap["source"] != "tmux-adopted" {
				t.Errorf("expected source=tmux-adopted, got %v", taskMap["source"])
			}
		}
	}
	if !foundTask {
		t.Fatal("adopted task not found in task list")
	}

	// ── Step 4: Send input to adopted session ──
	status, _ = doRequest(t, "POST", baseURL+"/tmux/input", token,
		fmt.Sprintf(`{"taskId":%q,"input":"echo mobile-sent-this-12345","allowShell":true}`, taskID1))
	if status != 200 {
		t.Fatalf("input: expected 200, got %d", status)
	}

	// Wait for polling to capture output
	time.Sleep(2 * time.Second)

	// ── Step 5: Verify output captured ──
	status, body = doRequest(t, "GET", baseURL+"/tasks/"+taskID1, token, "")
	if status != 200 {
		t.Fatalf("get task: expected 200, got %d", status)
	}
	taskDetail := body["task"].(map[string]interface{})
	output, _ := taskDetail["output"].(string)
	if !strings.Contains(output, "mobile-sent-this-12345") {
		t.Errorf("expected output to contain input result, got: %s", output[:min(len(output), 300)])
	}

	// ── Step 6: Verify conversation turns ──
	turns, _ := taskDetail["turns"].([]interface{})
	if len(turns) == 0 {
		t.Error("expected at least one conversation turn")
	} else {
		lastTurn := turns[len(turns)-1].(map[string]interface{})
		if lastTurn["content"] != "echo mobile-sent-this-12345" {
			t.Errorf("expected last turn to be the input, got: %v", lastTurn["content"])
		}
		if lastTurn["role"] != "user" {
			t.Errorf("expected turn role=user, got %v", lastTurn["role"])
		}
	}

	// ── Step 7: Adopt session2 concurrently ──
	status, body = doRequest(t, "POST", baseURL+"/tmux/adopt", token,
		`{"session":"yaver-e2e-session2"}`)
	if status != 200 {
		t.Fatalf("adopt session2: expected 200, got %d", status)
	}
	taskID2, _ := body["taskId"].(string)

	// Send input to session2
	doRequest(t, "POST", baseURL+"/tmux/input", token,
		fmt.Sprintf(`{"taskId":%q,"input":"echo session2-test-input","allowShell":true}`, taskID2))
	time.Sleep(2 * time.Second)

	// Verify session2 output
	status, body = doRequest(t, "GET", baseURL+"/tasks/"+taskID2, token, "")
	task2Detail := body["task"].(map[string]interface{})
	output2, _ := task2Detail["output"].(string)
	if !strings.Contains(output2, "session2-test-input") {
		t.Errorf("session2: expected output to contain input, got: %s", output2[:min(len(output2), 300)])
	}

	// ── Step 8: Verify sessions show correct adoption status ──
	status, body = doRequest(t, "GET", baseURL+"/tmux/sessions", token, "")
	sessions = body["sessions"].([]interface{})
	for _, s := range sessions {
		sm := s.(map[string]interface{})
		name, _ := sm["name"].(string)
		rel, _ := sm["relationship"].(string)
		tid, _ := sm["taskId"].(string)
		if name == "yaver-e2e-session1" {
			if rel != "adopted" {
				t.Errorf("session1: expected adopted, got %s", rel)
			}
			if tid != taskID1 {
				t.Errorf("session1: expected taskId=%s, got %s", taskID1, tid)
			}
		}
		if name == "yaver-e2e-session2" {
			if rel != "adopted" {
				t.Errorf("session2: expected adopted, got %s", rel)
			}
			if tid != taskID2 {
				t.Errorf("session2: expected taskId=%s, got %s", taskID2, tid)
			}
		}
	}

	// ── Step 9: Detach session1 ──
	status, _ = doRequest(t, "POST", baseURL+"/tmux/detach", token,
		fmt.Sprintf(`{"taskId":%q}`, taskID1))
	if status != 200 {
		t.Fatalf("detach: expected 200, got %d", status)
	}

	// Verify tmux session still alive
	if !tmuxSessionExists("yaver-e2e-session1") {
		t.Fatal("session1 should still exist after detach")
	}

	// Verify task status = stopped
	status, body = doRequest(t, "GET", baseURL+"/tasks/"+taskID1, token, "")
	taskDetail = body["task"].(map[string]interface{})
	if taskDetail["status"] != "stopped" {
		t.Errorf("expected status=stopped after detach, got %v", taskDetail["status"])
	}

	// Session2 should still be running
	status, body = doRequest(t, "GET", baseURL+"/tasks/"+taskID2, token, "")
	task2Detail = body["task"].(map[string]interface{})
	if task2Detail["status"] != "running" {
		t.Errorf("session2 task should still be running, got %v", task2Detail["status"])
	}

	// ── Step 10: MCP tmux_list_sessions ──
	doRequest(t, "POST", baseURL+"/mcp", token,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)

	status, body = doRequest(t, "POST", baseURL+"/mcp", token,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tmux_list_sessions","arguments":{}}}`)
	if status != 200 {
		t.Fatalf("MCP tmux_list: expected 200, got %d", status)
	}
	result := body["result"].(map[string]interface{})
	content := result["content"].([]interface{})
	mcpText, _ := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(mcpText, "yaver-e2e-session1") || !strings.Contains(mcpText, "yaver-e2e-session2") {
		t.Errorf("MCP list should contain both sessions, got: %s", mcpText)
	}

	// ── Step 11: MCP tmux_adopt + tmux_send_input (re-adopt session1) ──
	// Session1 was detached, re-adopt via MCP
	status, body = doRequest(t, "POST", baseURL+"/mcp", token,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tmux_adopt_session","arguments":{"session_name":"yaver-e2e-session1"}}}`)
	if status != 200 {
		t.Fatalf("MCP adopt: expected 200, got %d", status)
	}
	mcpResult := body["result"].(map[string]interface{})
	mcpContent := mcpResult["content"].([]interface{})
	adoptText, _ := mcpContent[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(adoptText, "Adopted") {
		t.Errorf("MCP adopt should say Adopted, got: %s", adoptText)
	}

	// Send input via MCP
	status, body = doRequest(t, "POST", baseURL+"/mcp", token,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tmux_send_input","arguments":{"task_id":%q,"input":"echo mcp-input-test","allow_shell":true}}}`, taskID2))
	if status != 200 {
		t.Fatalf("MCP send_input: expected 200, got %d", status)
	}

	time.Sleep(2 * time.Second)

	// Verify MCP input arrived in session2 output
	status, body = doRequest(t, "GET", baseURL+"/tasks/"+taskID2, token, "")
	task2Detail = body["task"].(map[string]interface{})
	output2, _ = task2Detail["output"].(string)
	if !strings.Contains(output2, "mcp-input-test") {
		t.Errorf("MCP input should appear in output, got: %s", output2[:min(len(output2), 300)])
	}

	t.Log("E2E full flow passed: list → adopt → input → output → turns → concurrent → detach → MCP")
}

// min() lived here historically; the same function now lives in
// console_docker_helpers.go, which ships in every build. Removing the
// duplicate so `go test ./...` can link without a `min redeclared` error.

// Helper to make the context import not unused
var _ = context.Background
var _ = json.Marshal

// A screenless surface (watch, car) cannot see a modal. The agent must refuse
// on its behalf: a prompt sent while codex showed "› 1. Update now" had its
// appended Enter select that option, codex ran `npm install`, exited, and the
// tmux session died with it.
func TestTmuxMenuDetection(t *testing.T) {
	menu := []string{
		"› 1. Update now (runs `npm install -g @openai/codex`)",
		"  2. Skip",
		"❯ 1. Yes, I trust this folder",
		"   2. No, exit",
		"1) Claude account with subscription",
	}
	for _, line := range menu {
		if !tmuxMenuOptionPattern.MatchString(line) {
			t.Errorf("menu row not recognised: %q", line)
		}
	}
	// Ordinary agent output must never read as a menu row.
	for _, line := range []string{
		"the answer is 42",
		"› reply with exactly HELLO",
		"  gpt-5.6-sol default · ~",
		"Step1. do the thing", // no space after the dot
	} {
		if tmuxMenuOptionPattern.MatchString(line) {
			t.Errorf("ordinary output misread as a menu row: %q", line)
		}
	}
}

// A bare number is how a caller answers a menu; anything else is a prompt.
func TestTmuxChoiceAnswer(t *testing.T) {
	for _, ok := range []string{"1", "2", " 3 ", "10"} {
		if !isTmuxChoiceAnswer(ok) {
			t.Errorf("%q should be accepted as a menu answer", ok)
		}
	}
	for _, no := range []string{"", "2 files changed", "fix the bug", "1.5", "y"} {
		if isTmuxChoiceAnswer(no) {
			t.Errorf("%q must not be treated as a menu answer", no)
		}
	}
}
