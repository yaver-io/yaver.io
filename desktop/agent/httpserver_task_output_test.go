package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskOutputSSEWaitsForTerminalStatusAfterOutputChannelCloses(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID:       "t1",
		Title:    "Run ls",
		Status:   TaskStatusRunning,
		Output:   "partial output\n",
		outputCh: make(chan string, 1),
		doneCh:   make(chan struct{}),
	}

	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	srv := &HTTPServer{taskMgr: tm}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.streamOutput(w, r, task.ID)
	}))
	defer ts.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(task.outputCh)
		time.Sleep(50 * time.Millisecond)
		tm.mu.Lock()
		task.Status = TaskStatusFinished
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	req, err := http.NewRequest(http.MethodPost, ts.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("missing done event:\n%s", text)
	}
	if !strings.Contains(text, `"status":"completed"`) {
		t.Fatalf("done event used non-terminal status:\n%s", text)
	}
	if strings.Contains(text, `"status":"running"`) {
		t.Fatalf("done event leaked running status:\n%s", text)
	}
}

func TestTaskOutputSSESurvivesOutputChannelReplacementBeforeTaskDone(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	firstOut := make(chan string, 1)
	firstRaw := make(chan taskRawFrame, 1)
	firstEvents := make(chan map[string]interface{}, 1)
	firstDone := make(chan struct{})
	task := &Task{
		ID:          "t-restart",
		Title:       "Run ls",
		Status:      TaskStatusRunning,
		Output:      "first generation\n",
		outputCh:    firstOut,
		rawOutputCh: firstRaw,
		eventCh:     firstEvents,
		doneCh:      firstDone,
	}

	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	srv := &HTTPServer{taskMgr: tm}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.streamOutput(w, r, task.ID)
	}))
	defer ts.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(firstOut)

		time.Sleep(350 * time.Millisecond)
		secondOut := make(chan string, 1)
		secondRaw := make(chan taskRawFrame, 1)
		secondEvents := make(chan map[string]interface{}, 1)
		secondDone := make(chan struct{})

		tm.mu.Lock()
		task.outputCh = secondOut
		task.rawOutputCh = secondRaw
		task.eventCh = secondEvents
		task.doneCh = secondDone
		tm.mu.Unlock()

		secondOut <- "second generation output\n"
		tm.mu.Lock()
		task.Output += "second generation output\n"
		tm.mu.Unlock()

		time.Sleep(50 * time.Millisecond)
		tm.mu.Lock()
		task.Status = TaskStatusFinished
		tm.mu.Unlock()
		close(secondDone)
	}()

	req, err := http.NewRequest(http.MethodPost, ts.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, "second generation output") {
		t.Fatalf("replacement output missing from SSE:\n%s", text)
	}
	if !strings.Contains(text, `"type":"done"`) || !strings.Contains(text, `"status":"completed"`) {
		t.Fatalf("terminal done frame missing or wrong:\n%s", text)
	}
	if strings.Contains(text, `"status":"running"`) {
		t.Fatalf("stream terminated early with running status:\n%s", text)
	}
}

func TestTaskOutputSSEReplaysSemanticPresentationSnapshot(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID: "semantic-1", Status: TaskStatusFinished,
		Presentation: []TaskPresentationMessage{{
			ID: "answer", Kind: "message", Role: "assistant", Text: "The build is ready.",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
		outputCh: make(chan string), doneCh: make(chan struct{}),
	}
	close(task.outputCh)
	close(task.doneCh)
	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	srv := &HTTPServer{taskMgr: tm}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.streamOutput(w, r, task.ID)
	}))
	defer ts.Close()
	resp, err := http.Post(ts.URL, "text/event-stream", nil)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, `"type":"presentation_snapshot"`) || !strings.Contains(text, `"The build is ready."`) {
		t.Fatalf("semantic snapshot missing from SSE:\n%s", text)
	}
}

func TestTaskOutputSSERepairsDroppedPresentationDeltaWithBoundedSnapshot(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID: "semantic-repair", Status: TaskStatusRunning,
		outputCh: make(chan string), doneCh: make(chan struct{}),
		// A nil event channel represents a saturated/disconnected live event
		// lane. present still records the authoritative state, which the SSE
		// ticker must repair without blocking the runner or a reconnect.
		eventCh: nil,
	}
	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	srv := &HTTPServer{taskMgr: tm}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.streamOutput(w, r, task.ID)
	}))
	defer ts.Close()

	go func() {
		time.Sleep(40 * time.Millisecond)
		tm.present(task, taskPresentationInput{
			ID: "activity", Kind: "status", Text: "Checking the work.", State: "running",
		})
		// The repair interval is 500 ms. Keep this comfortably beyond it so
		// the test proves the snapshot path, not a timing race.
		time.Sleep(650 * time.Millisecond)
		tm.mu.Lock()
		task.Status = TaskStatusFinished
		tm.mu.Unlock()
		close(task.doneCh)
	}()

	resp, err := http.Post(ts.URL, "text/event-stream", nil)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, `"type":"presentation_snapshot"`) || !strings.Contains(text, `"Checking the work."`) {
		t.Fatalf("dropped presentation delta was not repaired by snapshot:\n%s", text)
	}
	if !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("stream did not finish after repaired snapshot:\n%s", text)
	}
}
