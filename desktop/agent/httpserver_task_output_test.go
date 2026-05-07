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
