package main

// schedules_http_integration_test.go — end-to-end /schedules/* CRUD +
// pause/resume + run-now. Proves the scheduler endpoints actually
// wire up to the in-memory scheduler and that run-now fires an
// immediate task.

import (
	"testing"
	"time"
)

func TestSchedulesHTTPFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	currentTestHTTPServer.scheduler = NewScheduler(tm)

	// Create a cron schedule. Cron is on purpose in the far future
	// so natural firing never happens during the test.
	status, body := doRequest(t, "POST", baseURL+"/schedules", "owner-tok",
		`{"title":"nightly","cron":"0 4 1 1 *","runner":"claude-code"}`)
	if status != 201 {
		t.Fatalf("create: got %d, body=%v", status, body)
	}
	sched, _ := body["schedule"].(map[string]interface{})
	id, _ := sched["id"].(string)
	if id == "" {
		t.Fatalf("no id returned: %v", body)
	}

	// List picks it up.
	status, body = doRequest(t, "GET", baseURL+"/schedules", "owner-tok", "")
	if status != 200 {
		t.Fatalf("list: got %d", status)
	}
	items, _ := body["schedules"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(items))
	}

	// Pause.
	status, _ = doRequest(t, "POST", baseURL+"/schedules/"+id+"/pause", "owner-tok", "")
	if status != 200 {
		t.Fatalf("pause: got %d", status)
	}

	// Resume.
	status, _ = doRequest(t, "POST", baseURL+"/schedules/"+id+"/resume", "owner-tok", "")
	if status != 200 {
		t.Fatalf("resume: got %d", status)
	}

	// Run now — fires in a goroutine, so we wait a moment for the
	// task manager to pick up the new task.
	status, _ = doRequest(t, "POST", baseURL+"/schedules/"+id+"/run-now", "owner-tok", "")
	if status != 202 {
		t.Fatalf("run-now: expected 202, got %d", status)
	}
	// Scheduler.executeScheduled runs in a goroutine, so we wait
	// for LastTaskID to populate. 5s because remote machines
	// (Hetzner arm64) have slower disk IO and `taskMgr.CreateTask`
	// persists a file — a 2s budget has flaked in practice.
	deadline := time.Now().Add(5 * time.Second)
	var st *ScheduledTask
	for time.Now().Before(deadline) {
		var ok bool
		st, ok = currentTestHTTPServer.scheduler.GetSchedule(id)
		if ok && st.LastTaskID != "" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if st == nil || st.LastTaskID == "" {
		t.Fatalf("run-now didn't spawn a task — LastTaskID is empty")
	}

	// Unknown action is 404.
	status, _ = doRequest(t, "POST", baseURL+"/schedules/"+id+"/blow-up", "owner-tok", "")
	if status != 404 {
		t.Errorf("unknown action: expected 404, got %d", status)
	}

	// Delete — endpoint returns 202 (removal is async).
	status, _ = doRequest(t, "DELETE", baseURL+"/schedules/"+id, "owner-tok", "")
	if status != 202 {
		t.Fatalf("delete: got %d", status)
	}

	// Eventually gone.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, ok := currentTestHTTPServer.scheduler.GetSchedule(id)
		if !ok {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("schedule was still present 2s after DELETE")
}

func TestScheduleRunNowRequiresPOST(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	currentTestHTTPServer.scheduler = NewScheduler(tm)

	// Create a schedule to have a real id.
	_, body := doRequest(t, "POST", baseURL+"/schedules", "owner-tok",
		`{"title":"X","cron":"0 4 1 1 *"}`)
	sched, _ := body["schedule"].(map[string]interface{})
	id, _ := sched["id"].(string)

	status, _ := doRequest(t, "GET", baseURL+"/schedules/"+id+"/run-now", "owner-tok", "")
	if status != 405 {
		t.Errorf("GET on run-now: expected 405, got %d", status)
	}
}
