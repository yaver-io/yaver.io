package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStreamTestTask(id, source string, status TaskStatus) *Task {
	return &Task{
		ID: id, Title: "stream test", Source: source, Status: status,
		RunnerID: "claude", Transport: taskTransportCLI,
		outputCh: make(chan string, 8), rawOutputCh: make(chan runnerProcessChunk, 8),
		eventCh: make(chan map[string]interface{}, 8), doneCh: make(chan struct{}),
	}
}

func TestVibingTaskOutputStreamsOnlySourceGatedTasks(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	allowed := newStreamTestTask("feedback-task", "mobile-feedback", TaskStatusFinished)
	allowed.Output = "visible runner output"
	blocked := newStreamTestTask("owner-task", "web", TaskStatusFinished)
	tm.mu.Lock()
	tm.tasks[allowed.ID] = allowed
	tm.tasks[blocked.ID] = blocked
	tm.mu.Unlock()
	s := &HTTPServer{taskMgr: tm}

	req := httptest.NewRequest(http.MethodGet, "/vibing/task/feedback-task/output?since=0&rawSince=0", nil)
	rec := httptest.NewRecorder()
	s.handleVibingTaskByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "visible runner output") || !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("missing SSE output/done frames: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/vibing/task/owner-task/output", nil)
	rec = httptest.NewRecorder()
	s.handleVibingTaskByID(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-vibing task status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRunnerProcessStreamPreservesStderrAndItsSource(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	task := newStreamTestTask("stderr-task", "mobile-feedback", TaskStatusRunning)
	tm.readRunnerProcessStream(task, "stderr", bytes.NewBufferString("login rejected\n"))

	if task.RawOutput != "login rejected\n" {
		t.Fatalf("raw output = %q", task.RawOutput)
	}
	select {
	case chunk := <-task.rawOutputCh:
		if chunk.Stream != "stderr" || string(chunk.Data) != "login rejected\n" {
			t.Fatalf("unexpected stream chunk: %#v", chunk)
		}
	default:
		t.Fatal("stderr did not reach the typed raw task lane")
	}
}

func TestTaskOutputMemoryIsBoundedBeforeLiveQueue(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	task := newStreamTestTask("bounded-task", "mobile-feedback", TaskStatusRunning)
	var output strings.Builder
	tm.emit(task, &output, strings.Repeat("x", taskOutputMaxBytes+64*1024))
	if output.Len() > taskOutputMaxBytes || len(task.Output) > taskOutputMaxBytes {
		t.Fatalf("transcript escaped cap: builder=%d task=%d cap=%d", output.Len(), len(task.Output), taskOutputMaxBytes)
	}
	if !strings.Contains(task.Output, "task transcript truncated") {
		t.Fatal("bounded transcript did not tell the user its head was dropped")
	}
	select {
	case live := <-task.outputCh:
		if len(live) > taskLiveChunkMaxBytes+128 {
			t.Fatalf("live queue retained oversized chunk: %d bytes", len(live))
		}
	default:
		t.Fatal("expected bounded live output")
	}
}

func TestTaskRenderRequestEmitsTypedIntent(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	task := newStreamTestTask("render-task", "mobile-feedback", TaskStatusRunning)
	task.WorkDir = "/project"
	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()
	s := &HTTPServer{taskMgr: tm}

	req := httptest.NewRequest(http.MethodPost, "/tasks/render-task/render-request", strings.NewReader(`{"reason":"ui-change","summary":"Settings screen is ready"}`))
	rec := httptest.NewRecorder()
	s.requestTaskRender(rec, req, task.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case event := <-task.eventCh:
		if event["type"] != "runtime_render_requested" || event["reason"] != "agent-ui-change" || event["snippet"] != "Settings screen is ready" {
			t.Fatalf("unexpected render event: %#v", event)
		}
	default:
		t.Fatal("render request did not emit an event")
	}
}

func TestRunnerCommandUsesPathExercisedByReadinessProbe(t *testing.T) {
	clearRunnerBinaryCheckCache()
	t.Cleanup(clearRunnerBinaryCheckCache)
	path := filepath.Join(t.TempDir(), "codex-outside-path")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	storeRunnerBinaryPath("codex-test", path)
	cmd, err := newRunnerCommandContext(context.Background(), "codex-test", "exec", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != path {
		t.Fatalf("command path = %q, want readiness-probed %q", cmd.Path, path)
	}
}

func TestRequestRenderToolRequiresTaskContext(t *testing.T) {
	t.Setenv("YAVER_TASK_ID", "")
	result := forwardYaverRequestRender(json.RawMessage(`{"reason":"ui-change"}`))
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "YAVER_TASK_ID not set") {
		t.Fatalf("unexpected MCP result: %s", raw)
	}
}
