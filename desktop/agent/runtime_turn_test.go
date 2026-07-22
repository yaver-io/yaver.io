package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRuntimeTurnCapturesIdeaWithoutRunningByDefault(t *testing.T) {
	resp := executeRuntimeTurn(OpsContext{}, RuntimeTurnRequest{
		Utterance: "idea: make the disconnected screen show the failed probe",
		Surface:   RuntimeTurnSurface{Class: "watch"},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.State != runtimeQueueStateCaptured {
		t.Fatalf("state = %q, want captured", resp.State)
	}
	if resp.Queue == nil || resp.Queue.ItemID == "" {
		t.Fatalf("missing queue item: %+v", resp)
	}
	if resp.Queue.IntentClass != "idea-capture" {
		t.Fatalf("intent = %q, want idea-capture", resp.Queue.IntentClass)
	}
}

func TestRuntimeTurnCreatesQueuedTaskWhenAskedToRun(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "fix the startup flicker",
		Surface:   RuntimeTurnSurface{Class: "watch"},
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue-or-run"},
		},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	if resp.State != runtimeQueueStateRunning {
		t.Fatalf("state = %q, want running", resp.State)
	}
	if _, ok := tm.GetTask(resp.Queue.TaskID); !ok {
		t.Fatalf("task %q not found", resp.Queue.TaskID)
	}
}

func TestRuntimeTurnEnqueuesIdeaAsTaskWhenRequested(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "idea: make the disconnected screen show the failed probe",
		Surface:   RuntimeTurnSurface{Class: "watch"},
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue"},
		},
	})
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	if resp.Queue.IntentClass != "idea-capture" {
		t.Fatalf("intent = %q, want idea-capture", resp.Queue.IntentClass)
	}
	task, ok := tm.GetTask(resp.Queue.TaskID)
	if !ok {
		t.Fatalf("task %q not found", resp.Queue.TaskID)
	}
	if !strings.Contains(task.Description, "Treat this as idea capture first") {
		t.Fatalf("task prompt did not preserve idea-capture mode:\n%s", task.Description)
	}
}

func TestRuntimeTurnStatusMapsCompletedTaskToReadyToTest(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, RuntimeTurnRequest{
		Utterance: "fix the startup flicker",
		Development: RuntimeTurnDevelopment{
			Queue: RuntimeTurnQueuePrefs{Mode: "enqueue-or-run"},
		},
	})
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task-backed queue item: %+v", resp)
	}
	task, ok := tm.GetTask(resp.Queue.TaskID)
	if !ok {
		t.Fatalf("task not found")
	}
	task.Status = TaskStatusFinished
	task.ResultText = "Done. Tests pass."

	status := runtimeTurnStatus(OpsContext{Server: server}, resp.Queue.ItemID)
	if !status.OK {
		t.Fatalf("status failed: %+v", status)
	}
	if status.State != runtimeQueueStateReadyToTest {
		t.Fatalf("state = %q, want ready_to_test", status.State)
	}
	if status.TestTarget == nil || status.TestTarget.Kind != "yaver-mobile-container" {
		t.Fatalf("missing test target: %+v", status.TestTarget)
	}
}

func TestRuntimeTurnRejectsEmptyInput(t *testing.T) {
	resp := executeRuntimeTurn(OpsContext{}, RuntimeTurnRequest{Utterance: "   "})
	if resp.OK {
		t.Fatalf("empty input unexpectedly succeeded: %+v", resp)
	}
	if resp.Code != "bad_payload" {
		t.Fatalf("code = %q, want bad_payload", resp.Code)
	}
}

func TestRuntimeTurnNormalizesSimplePayloadAliases(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	server := &HTTPServer{taskMgr: tm}
	req := RuntimeTurnRequest{
		Text:    "fix the login button",
		Run:     true,
		Surface: RuntimeTurnSurface{ID: "watch"},
	}
	normalizeRuntimeTurnRequest(&req)
	resp := executeRuntimeTurn(OpsContext{Ctx: context.Background(), Server: server}, req)
	if !resp.OK {
		t.Fatalf("runtime turn failed: %+v", resp)
	}
	if resp.Queue == nil || resp.Queue.TaskID == "" {
		t.Fatalf("expected task from text/run aliases: %+v", resp.Queue)
	}
	if resp.Queue.Utterance != "fix the login button" {
		t.Fatalf("utterance = %q", resp.Queue.Utterance)
	}
	if resp.Queue.Surface.Class != "watch" {
		t.Fatalf("surface class = %q, want watch", resp.Queue.Surface.Class)
	}
}

func TestRuntimeTurnsListsRecentItems(t *testing.T) {
	runtimeQueue = &runtimeQueueStore{items: make(map[string]*RuntimeTurnQueueItem)}
	defer func() { runtimeQueue = &runtimeQueueStore{items: make(map[string]*RuntimeTurnQueueItem)} }()
	a := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "first", State: runtimeQueueStateCaptured})
	time.Sleep(time.Nanosecond)
	b := runtimeQueue.add(&RuntimeTurnQueueItem{Utterance: "second", State: runtimeQueueStateRunning})

	items := runtimeQueue.list(10)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].ItemID != b.ItemID || items[1].ItemID != a.ItemID {
		t.Fatalf("items not newest-first: %+v", items)
	}
}
