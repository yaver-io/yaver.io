package main

import (
	"testing"
	"time"
)

// A user stop during crash backoff is terminal. Before this guard, StopTask
// cancelled attempt N while it still looked Running; the monitor classified
// that expected exit as another crash and quietly launched attempt N+1.
func TestStopTaskDuringCrashBackoffDoesNotResurrectRunner(t *testing.T) {
	t.Setenv("YAVER_TASK_TMUX", "0")
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	t.Cleanup(tm.Shutdown)

	task, err := tm.CreateTask("crash once", "", "", "test", "", "exit 1", nil)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		tm.mu.RLock()
		status, retries := task.Status, task.retryCount
		tm.mu.RUnlock()
		if status == TaskStatusQueued && retries == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never entered crash backoff: status=%s retries=%d", status, retries)
		}
		time.Sleep(10 * time.Millisecond)
	}

	started := time.Now()
	if err := tm.StopTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stop waited through retry backoff: %s", elapsed)
	}

	// The first retry delay is two seconds. Wait beyond it and prove no new
	// process was launched and no mutable channel was swapped underneath stop.
	time.Sleep(2200 * time.Millisecond)
	tm.mu.RLock()
	status, retries := task.Status, task.retryCount
	tm.mu.RUnlock()
	if status != TaskStatusStopped || retries != 1 {
		t.Fatalf("stopped task resurrected: status=%s retries=%d", status, retries)
	}
}
