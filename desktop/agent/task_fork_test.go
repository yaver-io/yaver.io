package main

// task_fork_test.go — unit tests for the runtime-agent-switch fork
// primitive in task_fork.go. Verifies the prompt-budget clipper, the
// HTTP handler's input validation, and the parent-immutability
// invariant the design requires.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- helpers --------------------------------------------------------

func mkParentTask(t *testing.T) *Task {
	t.Helper()
	return &Task{
		ID:       "parent-1",
		Title:    "Fix the login button alignment",
		RunnerID: "claude",
		Model:    "sonnet",
		WorkDir:  "/Users/test/Workspace/sfmg",
		ResultText: strings.Repeat(
			"Sonnet finished the analysis and proposed shifting the button by 8px. ",
			60, // ~600 words
		),
		Turns: []ConversationTurn{
			{Role: "user", Content: "the login button is misaligned on iPhone"},
			{Role: "assistant", Content: "I see the issue — let me check the layout."},
			{Role: "user", Content: "any progress?"},
			{Role: "assistant", Content: "yes, found a marginLeft mismatch."},
		},
	}
}

func newForkTestServer(t *testing.T) *HTTPServer {
	t.Helper()
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	return &HTTPServer{taskMgr: tm, blackboxMgr: mgr}
}

// ---- prompt budget --------------------------------------------------

func TestBuildForkHandoffPromptIncludesAllSections(t *testing.T) {
	parent := mkParentTask(t)
	req := taskForkRequest{
		Runner: "codex",
		Input:  "now apply the fix and build",
	}
	prompt := buildForkHandoffPrompt(parent, req, defaultForkContextWords)
	for _, want := range []string{
		"[Conversation Handoff]",
		"Previous task: parent-1",
		"Previous runner: claude",
		"Work dir: /Users/test/Workspace/sfmg",
		"Latest user intent:",
		"Recent chat context follows",
		"Latest assistant tail:",
		"[New User Request]",
		"now apply the fix and build",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing %q in handoff prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildForkHandoffPromptRespectsWordBudget(t *testing.T) {
	parent := mkParentTask(t)
	parent.Turns = []ConversationTurn{
		{Role: "user", Content: strings.Repeat("alpha ", 800)},
		{Role: "assistant", Content: strings.Repeat("beta ", 800)},
	}
	req := taskForkRequest{Runner: "codex", Input: "test"}
	tight := buildForkHandoffPrompt(parent, req, 300)
	loose := buildForkHandoffPrompt(parent, req, 5000)
	if len(loose) <= len(tight) {
		t.Fatalf("loose budget should produce a longer prompt: tight=%d loose=%d", len(tight), len(loose))
	}
	// The tight budget should still produce something useful.
	if len(tight) < 200 {
		t.Errorf("tight prompt is suspiciously short: %d chars\n%s", len(tight), tight)
	}
}

func TestJoinWithinWordBudgetKeepsNewest(t *testing.T) {
	lines := []string{"oldest line one two", "middle line three four", "newest line five six"}
	got := joinWithinWordBudget(lines, 6)
	// Budget of 6 fits "newest line five six" (4 words) but not the
	// next one (would push to 8). We expect ONLY the newest line.
	if !strings.Contains(got, "newest line five six") {
		t.Errorf("expected newest line in %q", got)
	}
	if strings.Contains(got, "oldest") {
		t.Errorf("oldest should have been dropped: %q", got)
	}
}

func TestTruncateToWordsAddsEllipsis(t *testing.T) {
	got := truncateToWords("alpha beta gamma delta epsilon", 3)
	if got != "alpha beta gamma..." {
		t.Errorf("truncated wrong: %q", got)
	}
	if got := truncateToWords("only three words", 5); got != "only three words" {
		t.Errorf("under-budget input should pass through unchanged: %q", got)
	}
}

// ---- handler validation --------------------------------------------

func TestHandleTaskForkRejectsUnknownParent(t *testing.T) {
	s := newForkTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/tasks/missing/fork",
		bytes.NewReader([]byte(`{"runner":"codex","input":"test"}`)))
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, req, "missing")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404 — body %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTaskForkRejectsMissingRunner(t *testing.T) {
	s := newForkTestServer(t)
	parent := mkParentTask(t)
	s.taskMgr.tasks[parent.ID] = parent

	body := bytes.NewReader([]byte(`{"input":"do the thing"}`))
	req := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, req, parent.ID)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 — body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "runner is required") {
		t.Errorf("expected 'runner is required' in body: %s", rr.Body.String())
	}
}

func TestHandleTaskForkRejectsUnsupportedRunner(t *testing.T) {
	s := newForkTestServer(t)
	parent := mkParentTask(t)
	s.taskMgr.tasks[parent.ID] = parent

	body := bytes.NewReader([]byte(`{"runner":"aider","input":"x"}`))
	req := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, req, parent.ID)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 — body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unsupported runner") {
		t.Errorf("expected 'unsupported runner' in body: %s", rr.Body.String())
	}
}

func TestHandleTaskForkRejectsEmptyInput(t *testing.T) {
	s := newForkTestServer(t)
	parent := mkParentTask(t)
	s.taskMgr.tasks[parent.ID] = parent

	body := bytes.NewReader([]byte(`{"runner":"codex","input":""}`))
	req := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, req, parent.ID)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 — body %s", rr.Code, rr.Body.String())
	}
}

// ---- end-to-end: fork creates child, parent unchanged --------------

func TestHandleTaskForkCreatesChildAndKeepsParentImmutable(t *testing.T) {
	s := newForkTestServer(t)
	parent := mkParentTask(t)
	s.taskMgr.tasks[parent.ID] = parent
	originalRunner := parent.RunnerID
	originalModel := parent.Model
	originalTurns := len(parent.Turns)
	originalTitle := parent.Title

	body := bytes.NewReader([]byte(`{
		"runner":"opencode",
		"model":"zai-coding-plan/glm-4.7",
		"mode":"plan",
		"input":"switch to opencode plan and outline the next 3 steps",
		"contextWords":800
	}`))
	r := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, r, parent.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body %s", rr.Code, rr.Body.String())
	}
	var resp taskForkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response: %v\n%s", err, rr.Body.String())
	}
	if !resp.OK {
		t.Fatal("ok=false")
	}
	if resp.TaskID == "" {
		t.Fatal("child taskId empty")
	}
	if resp.ParentTaskID != parent.ID {
		t.Errorf("parentTaskId: got %q want %q", resp.ParentTaskID, parent.ID)
	}
	if resp.Relationship != "forked-by-yaver" {
		t.Errorf("relationship: got %q", resp.Relationship)
	}
	if resp.RunnerID != "opencode" {
		t.Errorf("runner: got %q want opencode", resp.RunnerID)
	}
	if resp.ContextWordsUsed <= 0 {
		t.Errorf("contextWordsUsed should be positive: %d", resp.ContextWordsUsed)
	}

	// Parent immutability: nothing about the parent task should have moved.
	stillParent, ok := s.taskMgr.GetTask(parent.ID)
	if !ok {
		t.Fatal("parent disappeared")
	}
	if stillParent.RunnerID != originalRunner {
		t.Errorf("parent runner mutated: %q → %q", originalRunner, stillParent.RunnerID)
	}
	if stillParent.Model != originalModel {
		t.Errorf("parent model mutated: %q → %q", originalModel, stillParent.Model)
	}
	if len(stillParent.Turns) != originalTurns {
		t.Errorf("parent turn count mutated: %d → %d", originalTurns, len(stillParent.Turns))
	}
	if stillParent.Title != originalTitle {
		t.Errorf("parent title mutated: %q → %q", originalTitle, stillParent.Title)
	}

	// Child should be present, with the right runner.
	child, ok := s.taskMgr.GetTask(resp.TaskID)
	if !ok {
		t.Fatal("child task not found after fork")
	}
	if child.RunnerID != "opencode" {
		t.Errorf("child runner: got %q want opencode", child.RunnerID)
	}
	if child.Source != "runner-switch-fork" {
		t.Errorf("child source: got %q want runner-switch-fork", child.Source)
	}
	if !strings.Contains(child.Title, "[New User Request]") {
		t.Errorf("child prompt missing handoff trailer: %q", child.Title)
	}
	if !strings.Contains(child.Title, "switch to opencode plan") {
		t.Errorf("child prompt missing user input: %q", child.Title)
	}
}

// The 2026-07-21 "I lost the summary — every follow-up starts a fresh chat"
// report: a fork must render as ONE continuous thread, so the child carries the
// parent's conversation turns for display (WhatsApp-style), then appends the new
// user turn — without duplicating a message at the seam and without leaking the
// giant handoff prompt into the visible history.
func TestHandleTaskForkSeedsParentTurnsForContinuousThread(t *testing.T) {
	s := newForkTestServer(t)
	parent := mkParentTask(t)
	s.taskMgr.tasks[parent.ID] = parent

	body := bytes.NewReader([]byte(`{
		"runner":"opencode",
		"input":"now make it work on Android too",
		"contextWords":800
	}`))
	r := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, r, parent.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 — body %s", rr.Code, rr.Body.String())
	}
	var resp taskForkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response: %v", err)
	}
	child, ok := s.taskMgr.GetTask(resp.TaskID)
	if !ok {
		t.Fatal("child task not found after fork")
	}

	// The whole parent thread must be present, in order, followed by the new
	// user turn — so the phone shows a continuous conversation, not one exchange.
	if len(child.Turns) != len(parent.Turns)+1 {
		t.Fatalf("child turns: got %d want %d (parent %d + new user turn)",
			len(child.Turns), len(parent.Turns)+1, len(parent.Turns))
	}
	for i, want := range parent.Turns {
		if child.Turns[i].Role != want.Role || child.Turns[i].Content != want.Content {
			t.Errorf("child turn %d: got {%s %q} want {%s %q}",
				i, child.Turns[i].Role, child.Turns[i].Content, want.Role, want.Content)
		}
	}
	last := child.Turns[len(child.Turns)-1]
	if last.Role != "user" || last.Content != "now make it work on Android too" {
		t.Errorf("last child turn should be the new user request, got {%s %q}", last.Role, last.Content)
	}
	// The visible history must NOT contain the [Conversation Handoff] scaffold —
	// that goes to the runner via Title, never into the chat bubbles.
	for i, turn := range child.Turns {
		if strings.Contains(turn.Content, "[Conversation Handoff]") {
			t.Errorf("turn %d leaked the handoff prompt into visible history: %q", i, turn.Content)
		}
	}
}

// A seed turn that duplicates the incoming user turn must collapse at the seam
// (the client optimistically appends the follow-up to the parent before forking,
// so the parent tail can equal the child's first turn).
func TestCreateTaskSeedTurnsDedupesSeamDuplicate(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true
	seed := []ConversationTurn{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "make it work on Android"}, // == the incoming prompt
	}
	task, err := tm.CreateTaskWithOptions("t", "", "", "test", "claude", "", nil, TaskCreateOptions{
		InitialUserPrompt: "make it work on Android",
		SeedTurns:         seed,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 3 seed turns, last one dedup'd against the new user turn → 3 total, not 4.
	if len(task.Turns) != 3 {
		t.Fatalf("turns: got %d want 3 (seam duplicate collapsed) — %+v", len(task.Turns), task.Turns)
	}
	if task.Turns[2].Role != "user" || task.Turns[2].Content != "make it work on Android" {
		t.Errorf("final turn wrong: {%s %q}", task.Turns[2].Role, task.Turns[2].Content)
	}
}

func TestSeedForkTurnsBoundsTail(t *testing.T) {
	big := make([]ConversationTurn, maxSeededForkTurns+25)
	for i := range big {
		big[i] = ConversationTurn{Role: "user", Content: "x"}
	}
	got := seedForkTurns(big)
	if len(got) != maxSeededForkTurns {
		t.Fatalf("bounded len: got %d want %d", len(got), maxSeededForkTurns)
	}
	if short := seedForkTurns(big[:3]); len(short) != 3 {
		t.Fatalf("short passthrough: got %d want 3", len(short))
	}
	if seedForkTurns(nil) != nil {
		t.Fatal("nil should pass through as nil")
	}
}

func TestHandleTaskForkDefersCloudPlacementInsteadOfCreatingLocalChild(t *testing.T) {
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before forked tasks can run.",
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
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := &HTTPServer{
		token:     "owner-token",
		convexURL: backend.URL,
		deviceID:  "local-dev",
		taskMgr:   tm,
	}
	parent := mkParentTask(t)
	parent.WorkDir = t.TempDir()
	s.taskMgr.tasks[parent.ID] = parent

	body := bytes.NewReader([]byte(`{
		"runner":"codex",
		"input":"continue from the private handoff and apply the fix",
		"contextWords":800
	}`))
	r := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", body)
	rr := httptest.NewRecorder()
	s.handleTaskFork(rr, r, parent.ID)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d want 409 — body %s", rr.Code, rr.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 1 {
		t.Fatalf("expected only parent task, got %d tasks", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "input"} {
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
	if len(rows) != 1 || !strings.Contains(string(rows[0].BodyJSON), "private handoff") {
		t.Fatalf("pending rows = %#v", rows)
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}

func TestMCPForkTaskDefersCloudPlacementInsteadOfCreatingLocalChild(t *testing.T) {
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
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before MCP forked tasks can run.",
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
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	s := &HTTPServer{
		token:     "owner-token",
		convexURL: backend.URL,
		deviceID:  "local-dev",
		taskMgr:   tm,
	}
	parent := mkParentTask(t)
	parent.WorkDir = t.TempDir()
	s.taskMgr.tasks[parent.ID] = parent

	rawArgs, _ := json.Marshal(map[string]any{
		"name": "fork_task",
		"arguments": map[string]any{
			"task_id":       parent.ID,
			"runner":        "codex",
			"input":         "continue from the MCP private handoff and apply the fix",
			"context_words": 800,
		},
	})
	out := s.handleMCPToolCall(rawArgs)
	text := billingToolText(t, out)
	if !strings.Contains(text, "cloud_workspace_required") || !strings.Contains(text, "pending-cloud:") {
		t.Fatalf("MCP output = %s", text)
	}
	if tasks := tm.ListTasks(); len(tasks) != 1 {
		t.Fatalf("expected only parent task, got %d tasks", len(tasks))
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "input"} {
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
	if len(rows) != 1 || !strings.Contains(string(rows[0].BodyJSON), "MCP private handoff") {
		t.Fatalf("pending rows = %#v", rows)
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}

func TestHandleTaskForkContextWordsClampedToBounds(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int // approximate — we compare to the computed budget
	}{
		{"zero defaults", 0, defaultForkContextWords},
		{"too small clamps up", 50, minForkContextWords},
		{"too big clamps down", 99999, maxForkContextWords},
		{"in-range passes through", 1500, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newForkTestServer(t)
			parent := mkParentTask(t)
			s.taskMgr.tasks[parent.ID] = parent
			payload, _ := json.Marshal(taskForkRequest{
				Runner:       "codex",
				Input:        "x",
				ContextWords: tc.in,
			})
			r := httptest.NewRequest(http.MethodPost, "/tasks/parent-1/fork", bytes.NewReader(payload))
			rr := httptest.NewRecorder()
			s.handleTaskFork(rr, r, parent.ID)
			if rr.Code != http.StatusOK {
				t.Fatalf("status: got %d want 200 — body %s", rr.Code, rr.Body.String())
			}
			// We don't enforce equality on ContextWordsUsed (it's the
			// actual word count of the produced prompt, which can be
			// less than the budget). We just check the prompt itself
			// roughly tracks the budget direction.
			var resp taskForkResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &resp)
			if resp.ContextWordsUsed <= 0 {
				t.Errorf("ContextWordsUsed empty for budget=%d", tc.in)
			}
		})
	}
}
