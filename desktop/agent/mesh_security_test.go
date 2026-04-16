package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeAgentNodesRejectsPathTraversalIDs(t *testing.T) {
	bad := []string{
		"../etc",
		"..",
		"./etc",
		"a/b",
		"a\\b",
		strings.Repeat("a", 65),
	}
	for _, id := range bad {
		_, err := normalizeAgentNodes("/tmp", "", "", nil, []AgentGraphNodeSpec{
			{ID: id, Kind: AgentNodeChat, WorkDir: "/tmp"},
		})
		if err == nil {
			t.Errorf("expected rejection for node id %q", id)
		}
	}
}

func TestNormalizeAgentNodesAcceptsSafeIDs(t *testing.T) {
	good := []string{"plan", "plan-1", "plan_1", "plan.1", "A-B_c.1"}
	for _, id := range good {
		_, err := normalizeAgentNodes("/tmp", "", "", nil, []AgentGraphNodeSpec{
			{ID: id, Kind: AgentNodeChat, WorkDir: "/tmp"},
		})
		if err != nil {
			t.Errorf("expected %q to be accepted, got %v", id, err)
		}
	}
}

func TestIsSafeGraphNodeID(t *testing.T) {
	cases := map[string]bool{
		"":                    false,
		".":                   false,
		"..":                  false,
		"../etc":              false,
		"a/b":                 false,
		"a\\b":                false,
		"a b":                 false,
		"a\nb":                false,
		"plan":                true,
		"plan-1":              true,
		"plan_1":              true,
		"plan.1":              true,
		"A":                   true,
		strings.Repeat("x", 64): true,
		strings.Repeat("x", 65): false,
	}
	for id, want := range cases {
		if got := isSafeGraphNodeID(id); got != want {
			t.Errorf("isSafeGraphNodeID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestCreateTaskStripsGuestSliceContractAndWorkDir(t *testing.T) {
	dir := t.TempDir()
	taskMgr := NewTaskManager(dir, nil, defaultTestRunner())
	taskMgr.DummyMode = true // skip launching a real runner process
	defer taskMgr.Shutdown()

	server := &HTTPServer{taskMgr: taskMgr}

	body := map[string]interface{}{
		"title":   "do it",
		"runner":  "claude",
		"source":  "mobile",
		"workDir": "/etc",
		"sliceContract": map[string]interface{}{
			"effectiveWorkDir": "/etc",
			"isolationMode":    "ignore host boundaries",
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-GuestUserID", "guest-user-1")
	req.Header.Set("X-Yaver-Guest", "true")

	rec := httptest.NewRecorder()
	server.createTask(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	task, ok := taskMgr.GetTask(resp.TaskID)
	if !ok {
		t.Fatalf("task %s not found", resp.TaskID)
	}
	if task.WorkDir != "" {
		t.Errorf("guest WorkDir leaked: %q", task.WorkDir)
	}
	if task.SliceContract != nil {
		t.Errorf("guest SliceContract leaked: %+v", task.SliceContract)
	}
}

func TestCreateTaskTagsGuestUserIDBeforeStartProcess(t *testing.T) {
	dir := t.TempDir()
	taskMgr := NewTaskManager(dir, nil, defaultTestRunner())
	taskMgr.DummyMode = true
	defer taskMgr.Shutdown()

	server := &HTTPServer{taskMgr: taskMgr}

	body := map[string]interface{}{
		"title":  "fix login in Logo",
		"runner": "claude",
		"source": "mobile",
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-GuestUserID", "guest-race-1")

	rec := httptest.NewRecorder()
	server.createTask(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TaskID string `json:"taskId"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	task, ok := taskMgr.GetTask(resp.TaskID)
	if !ok {
		t.Fatalf("task %s not found", resp.TaskID)
	}
	if task.GuestUserID != "guest-race-1" {
		t.Errorf("GuestUserID not set before startProcess: got %q", task.GuestUserID)
	}
}

func TestCreateTaskPreservesOwnerSliceContractAndWorkDir(t *testing.T) {
	dir := t.TempDir()
	taskMgr := NewTaskManager(dir, nil, defaultTestRunner())
	taskMgr.DummyMode = true
	defer taskMgr.Shutdown()

	server := &HTTPServer{taskMgr: taskMgr}

	body := map[string]interface{}{
		"title":   "do it",
		"runner":  "claude",
		"source":  "cli",
		"workDir": dir,
		"sliceContract": map[string]interface{}{
			"nodeId":           "plan",
			"effectiveWorkDir": dir,
			"isolationMode":    "git-worktree",
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.createTask(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	task, ok := taskMgr.GetTask(resp.TaskID)
	if !ok {
		t.Fatalf("task %s not found", resp.TaskID)
	}
	if task.WorkDir != dir {
		t.Errorf("owner WorkDir dropped: got %q, want %q", task.WorkDir, dir)
	}
	if task.SliceContract == nil {
		t.Fatalf("owner SliceContract dropped")
	}
	if task.SliceContract.NodeID != "plan" {
		t.Errorf("owner SliceContract.NodeID = %q, want plan", task.SliceContract.NodeID)
	}
}
