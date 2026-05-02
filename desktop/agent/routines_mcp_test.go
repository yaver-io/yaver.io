package main

// routines_mcp_test.go — end-to-end coverage of the MCP-only routine
// surface. Routines are the Verb-mode wrapper on the existing
// Scheduler; this test proves the full lifecycle (create → run-now
// → get → list → pause → resume → update → delete) with a stub ops
// dispatcher so we can assert the scheduler invoked the requested
// verb on the requested machine with the requested payload, without
// actually wiring up the real ops registry.

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingDispatcher captures every dispatchOps call so the test can
// assert the scheduler routed the verb correctly. Concurrent fires
// (e.g. routine_run_now's goroutine) need the mutex.
type recordingDispatcher struct {
	mu    sync.Mutex
	calls []OpsRequest
	count int32
	// result is what every fire returns. Default is OK so successive
	// fires don't accumulate "failed" history entries; a test that
	// wants a failure flips this before the fire.
	result OpsResult
}

func (r *recordingDispatcher) dispatch(req OpsRequest) OpsResult {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	atomic.AddInt32(&r.count, 1)
	return r.result
}

func (r *recordingDispatcher) snapshot() []OpsRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestRoutineMCPLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	currentTestHTTPServer.scheduler = NewScheduler(tm)
	rec := &recordingDispatcher{result: OpsResult{OK: true, Initial: "stub-ok"}}
	currentTestHTTPServer.scheduler.SetOpsDispatcher(rec.dispatch)

	// 1. Create a one-shot routine far in the future so the scheduler
	//    tick won't fire it; we only fire via routine_run_now in this
	//    test for determinism.
	createBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
		"name":"routine_create","arguments":{
			"name":"smoke",
			"verb":"run",
			"machine":"primary",
			"payload":{"cmd":"echo hi"},
			"run_at":"2099-01-01T00:00:00Z"
		}}}`
	resp := doMCPRequest(t, baseURL, "owner-tok", createBody)
	id := mcpExtractField(t, resp, "id")
	if id == "" {
		t.Fatalf("create: no id in response: %v", resp)
	}
	if !strings.HasPrefix(id, "sched-") {
		t.Fatalf("create: id should start with sched- (matches generateTaskID), got %q", id)
	}

	// 2. routine_list returns this routine and excludes any non-routine
	//    schedule the test fixture might have created.
	listResp := doMCPRequest(t, baseURL, "owner-tok", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
		"name":"routine_list","arguments":{}}}`)
	listText := mcpFirstText(t, listResp)
	if !strings.Contains(listText, id) {
		t.Fatalf("list: missing routine id %q in: %s", id, listText)
	}
	if !strings.Contains(listText, `"verb": "run"`) {
		t.Fatalf("list: missing verb=run in: %s", listText)
	}

	// 3. routine_run_now fires synchronously through the scheduler's
	//    goroutine. Wait briefly for the dispatcher to be hit.
	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(3, "routine_run_now", map[string]interface{}{"id": id}))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&rec.count) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&rec.count) == 0 {
		t.Fatalf("routine_run_now did not invoke the dispatcher within 2s")
	}
	calls := rec.snapshot()
	if got := calls[0].Verb; got != "run" {
		t.Fatalf("dispatched verb = %q, want %q", got, "run")
	}
	if got := calls[0].Machine; got != "primary" {
		t.Fatalf("dispatched machine = %q, want %q", got, "primary")
	}
	if !strings.Contains(string(calls[0].Payload), `"cmd":"echo hi"`) {
		t.Fatalf("dispatched payload didn't carry cmd: %s", string(calls[0].Payload))
	}

	// 4. routine_get reflects the run in history.
	getText := mcpFirstText(t, doMCPRequest(t, baseURL, "owner-tok",
		mcpToolCallBody(4, "routine_get", map[string]interface{}{"id": id})))
	if !strings.Contains(getText, `"verb": "run"`) || !strings.Contains(getText, `"status": "ok"`) {
		t.Fatalf("get: history missing successful run: %s", getText)
	}

	// 5. pause + resume.
	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(5, "routine_pause", map[string]interface{}{"id": id}))
	if st, _ := currentTestHTTPServer.scheduler.GetSchedule(id); st.Status != "paused" {
		t.Fatalf("pause: status=%q, want paused", st.Status)
	}
	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(6, "routine_resume", map[string]interface{}{"id": id}))
	if st, _ := currentTestHTTPServer.scheduler.GetSchedule(id); st.Status == "paused" {
		t.Fatalf("resume: status still paused")
	}

	// 6. routine_update: change the schedule mode from one-shot to a
	//    cron and the machine from primary → local. Confirm both stuck.
	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(7, "routine_update", map[string]interface{}{
		"id":      id,
		"machine": "local",
		"cron":    "0 9 * * 1-5",
	}))
	st, ok := currentTestHTTPServer.scheduler.GetSchedule(id)
	if !ok {
		t.Fatalf("update: routine vanished")
	}
	if st.Machine != "local" {
		t.Fatalf("update: machine=%q, want local", st.Machine)
	}
	if st.Cron != "0 9 * * 1-5" {
		t.Fatalf("update: cron=%q, want '0 9 * * 1-5'", st.Cron)
	}
	if st.RunAt != "" {
		t.Fatalf("update: switching to cron should have cleared run_at, got %q", st.RunAt)
	}

	// 7. delete leaves nothing behind.
	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(8, "routine_delete", map[string]interface{}{"id": id}))
	if _, ok := currentTestHTTPServer.scheduler.GetSchedule(id); ok {
		t.Fatalf("delete: routine still present")
	}
}

// TestRoutineCreateValidation locks in the create-time argument
// guards: verb required, exactly one schedule field, run_at must
// parse as RFC3339.
func TestRoutineCreateValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	currentTestHTTPServer.scheduler = NewScheduler(tm)
	currentTestHTTPServer.scheduler.SetOpsDispatcher(func(OpsRequest) OpsResult {
		return OpsResult{OK: true}
	})

	cases := []struct {
		name string
		args map[string]interface{}
		// substr that should appear in the returned error text.
		errSubstr string
	}{
		{"missing verb", map[string]interface{}{"run_at": "2099-01-01T00:00:00Z"}, "verb is required"},
		{"no schedule", map[string]interface{}{"verb": "run"}, "one of run_at, cron, or repeat_interval"},
		{"two schedule fields", map[string]interface{}{
			"verb": "run", "run_at": "2099-01-01T00:00:00Z", "cron": "0 0 * * *",
		}, "exactly one"},
		{"bad run_at", map[string]interface{}{
			"verb": "run", "run_at": "not-a-timestamp",
		}, "RFC3339"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(int64(100+i), "routine_create", c.args))
			text := mcpFirstText(t, resp)
			if !strings.Contains(text, c.errSubstr) {
				t.Fatalf("expected error to contain %q, got: %s", c.errSubstr, text)
			}
		})
	}
}

// TestRoutineFireFailureRecorded — when the dispatcher returns
// !OK, the routine's history captures the code + error. This is the
// signal the user reads via routine_get to debug why their cron
// silently isn't doing anything.
func TestRoutineFireFailureRecorded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()
	currentTestHTTPServer.scheduler = NewScheduler(tm)
	rec := &recordingDispatcher{result: OpsResult{
		OK: false, Code: "remote_failed", Error: "peer unreachable",
	}}
	currentTestHTTPServer.scheduler.SetOpsDispatcher(rec.dispatch)

	resp := doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(1, "routine_create", map[string]interface{}{
		"verb": "run", "machine": "some-peer", "run_at": "2099-01-01T00:00:00Z",
	}))
	id := mcpExtractField(t, resp, "id")

	doMCPRequest(t, baseURL, "owner-tok", mcpToolCallBody(2, "routine_run_now", map[string]interface{}{"id": id}))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&rec.count) == 0 {
		time.Sleep(20 * time.Millisecond)
	}

	getText := mcpFirstText(t, doMCPRequest(t, baseURL, "owner-tok",
		mcpToolCallBody(3, "routine_get", map[string]interface{}{"id": id})))
	if !strings.Contains(getText, `"opsCode": "remote_failed"`) {
		t.Fatalf("expected opsCode=remote_failed in history, got: %s", getText)
	}
	if !strings.Contains(getText, "peer unreachable") {
		t.Fatalf("expected error message in history, got: %s", getText)
	}
}

// mcpToolCallBody serializes a tools/call JSON-RPC envelope. Avoids
// scattering escape-quoting throughout the test cases.
func mcpToolCallBody(id int64, tool string, args map[string]interface{}) string {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		panic("mcpToolCallBody: marshal args: " + err.Error())
	}
	envelope := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      tool,
			"arguments": json.RawMessage(argsJSON),
		},
	}
	body, _ := json.Marshal(envelope)
	return string(body)
}

// mcpFirstText returns the first text content from an MCP tool
// response. Returns "" if absent rather than failing — tests use
// substring matching, so an empty string fails the assertion with a
// clear "expected … got: " message.
func mcpFirstText(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	result, _ := resp["result"].(map[string]interface{})
	contents, _ := result["content"].([]interface{})
	for _, c := range contents {
		m, _ := c.(map[string]interface{})
		if txt, ok := m["text"].(string); ok {
			return txt
		}
	}
	return ""
}

// mcpExtractField pulls a top-level JSON field out of the first text
// content. Used for grabbing the routine id after a create call —
// the response body is a pretty-printed JSON object inside the text
// content, not the JSON-RPC result map directly.
func mcpExtractField(t *testing.T, resp map[string]interface{}, field string) string {
	t.Helper()
	text := mcpFirstText(t, resp)
	if text == "" {
		return ""
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("mcpExtractField: text was not JSON: %s", text)
	}
	v, _ := parsed[field].(string)
	return v
}
