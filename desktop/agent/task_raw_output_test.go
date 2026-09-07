package main

// task_raw_output_test.go — the raw opencode console path.
//
// The agent retains the runner's RAW stdout (ANSI + TUI bytes, ungroomed)
// for the terminal view — see tasks.go emitRaw / RawOutput. This file
// guards the CONSUMERS, which is where "the webui wasn't like the console
// for opencode" died: the producer shipped with no way to reach a screen.
//
//   - GET /tasks/{id}/output?rawSince=<bytes> replays the raw tail
//     (raw_replay frame) so a terminal can be seeded and resumed.
//   - The same SSE stream carries live `raw` frames while the runner writes.
//   - GET /tasks/{id} ships the wire-capped rawOutput tail + rawOffset for
//     polling clients.
//
// Omitting `rawSince` must remain byte-for-byte the old stream (no
// raw_replay frame) — clients that predate the raw lane must not change
// behaviour.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startRawTestServer(t *testing.T) (*httptest.Server, *TaskManager) {
	t.Helper()
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	hs := NewHTTPServer(0, "test-token", "test-user", "test-device", "", "test-host", tm)
	mux := http.NewServeMux()
	mux.HandleFunc("/tasks", hs.auth(hs.handleTasks))
	mux.HandleFunc("/tasks/", hs.auth(hs.handleTaskByID))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, tm
}

// createRawFixture registers a synthetic task straight into the TaskManager
// with the raw tail pre-stamped. Deliberately NOT via POST /tasks: the real
// create path spawns a runner process (opencode is installed on dev boxes),
// whose live output would race the stamp and pollute RawOutput. A synthetic
// task is deterministic — no process, no writes.
func createRawFixture(t *testing.T, tm *TaskManager, id, raw string, status TaskStatus) string {
	t.Helper()
	task := &Task{
		ID:          id,
		Title:       "raw fixture",
		Status:      status,
		RunnerID:    "opencode",
		RawOutput:   raw,
		outputCh:    make(chan string, 512),
		rawOutputCh: make(chan taskRawFrame, 256),
		eventCh:     make(chan map[string]interface{}, 32),
		doneCh:      make(chan struct{}),
	}
	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.mu.Unlock()
	return task.ID
}

// collectSSEFrames reads an SSE response until EOF (the handler returns on
// ctx cancel, so a 5s deadline bounds a task that never reaches a terminal
// state) and returns every parsed `data:` frame in order.
func collectSSEFrames(t *testing.T, url string) []map[string]interface{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	var frames []map[string]interface{}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line[len("data: "):]), &ev); err != nil {
			t.Fatalf("bad SSE frame %q: %v", line, err)
		}
		frames = append(frames, ev)
	}
	return frames
}

func rawFrame(t *testing.T, frames []map[string]interface{}, wantType string) map[string]interface{} {
	t.Helper()
	for _, f := range frames {
		if f["type"] == wantType {
			return f
		}
	}
	t.Fatalf("no %q frame among %d frames: %v", wantType, len(frames), frames)
	return nil
}

// TestRawReplay_ExplicitSince — `?rawSince=<bytes>` replays the raw tail
// from that byte offset (rune-aligned) as a raw_replay frame with the
// authoritative offset + full flag.
func TestRawReplay_ExplicitSince(t *testing.T) {
	srv, tm := startRawTestServer(t)
	raw := "\x1b[32m✔\x1b[0m analysing repo…\n\x1b[1mworkdir:\x1b[0m /root/proj\n"
	taskID := createRawFixture(t, tm, "raw-replay-1", raw, TaskStatusFinished)

	frames := collectSSEFrames(t, srv.URL+"/tasks/"+taskID+"/output?rawSince=0")
	rf := rawFrame(t, frames, "raw_replay")
	if text, _ := rf["text"].(string); text != raw {
		t.Errorf("raw_replay text = %q, want full raw %q", text, raw)
	}
	if off, _ := rf["offset"].(float64); int(off) != len(raw) {
		t.Errorf("raw_replay offset = %v, want %d", rf["offset"], len(raw))
	}
	if full, _ := rf["full"].(bool); !full {
		t.Errorf("rawSince=0 must be a full snapshot, got full=%v", rf["full"])
	}

	// A mid-tail resume: bytes 5.. end.
	frames = collectSSEFrames(t, srv.URL+"/tasks/"+taskID+"/output?rawSince=5")
	rf = rawFrame(t, frames, "raw_replay")
	text, _ := rf["text"].(string)
	if !strings.HasPrefix(raw[5:], text) {
		t.Errorf("raw_replay text %q is not a suffix of raw[5:] %q", text, raw[5:])
	}
	if full, _ := rf["full"].(bool); full {
		t.Errorf("mid-tail resume must be an increment, got full=%v", rf["full"])
	}
}

// TestRawReplay_OmittedSinceIsOldBehaviour — no rawSince means no
// raw_replay frame at all, byte-for-byte the pre-raw stream.
func TestRawReplay_OmittedSinceIsOldBehaviour(t *testing.T) {
	srv, tm := startRawTestServer(t)
	taskID := createRawFixture(t, tm, "raw-replay-2", "\x1b[31mred\x1b[0m\n", TaskStatusFinished)

	frames := collectSSEFrames(t, srv.URL+"/tasks/"+taskID+"/output")
	for _, f := range frames {
		if f["type"] == "raw_replay" {
			t.Fatalf("without ?rawSince= a legacy client must never see a raw_replay frame, got %v", f)
		}
	}
}

// TestRawLiveFrames — chunks pushed into rawOutputCh surface as `raw` SSE
// frames (the terminal view's live path).
func TestRawLiveFrames(t *testing.T) {
	srv, tm := startRawTestServer(t)
	taskID := createRawFixture(t, tm, "raw-live-1", "", TaskStatusRunning)

	task, ok := tm.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s not found", taskID)
	}
	chunks := [][]byte{
		[]byte("\x1b[?25lloading"),
		[]byte(" dependencies…\x1b[0m\r\n"),
		[]byte("✓ done\x1b[?25h"),
	}
	var offset int64
	for _, c := range chunks {
		// Buffered before the handler's select drains it; 256-deep channel,
		// so no drops.
		offset += int64(len(c))
		task.rawOutputCh <- taskRawFrame{Bytes: c, Offset: offset}
	}

	frames := collectSSEFrames(t, srv.URL+"/tasks/"+taskID+"/output?rawSince=0")
	rawTypes := 0
	var joined strings.Builder
	for _, f := range frames {
		if f["type"] == "raw" {
			rawTypes++
			joined.WriteString(fmt.Sprint(f["text"]))
		}
	}
	if rawTypes != len(chunks) {
		t.Errorf("got %d raw frames, want %d (frames: %v)", rawTypes, len(chunks), frames)
	}
	for _, want := range []string{"loading", "dependencies", "done"} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("raw stream missing %q; got %q", want, joined.String())
		}
	}
}

// OpenCode 1.18.25's default formatter writes the clean assistant reply to
// stdout and its banner/tool evidence to stderr. Yaver keeps both byte streams
// in the folded raw lane, but only stdout may become the visible chat answer.
func TestOpenCodeRawReaderSeparatesAssistantReplyFromConsoleEvidence(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID: "opencode-semantic", RunnerID: "opencode",
		runner:   RunnerConfig{RunnerID: "opencode", OutputMode: "raw"},
		outputCh: make(chan string, 16), rawOutputCh: make(chan taskRawFrame, 16),
		eventCh: make(chan map[string]interface{}, 16), doneCh: make(chan struct{}),
	}
	tm.readRawOutput(
		task,
		strings.NewReader("I changed the background and the checks pass.\n"),
		strings.NewReader("\x1b[0m> build · deepseek-v4-flash\n\x1b[0m$ npm test\nPASS\n"),
	)

	if got := strings.TrimSpace(task.ResultText); got != "I changed the background and the checks pass." {
		t.Fatalf("ResultText = %q, want only OpenCode stdout assistant text", got)
	}
	if len(task.Presentation) == 0 {
		t.Fatal("semantic presentation is empty")
	}
	answer := task.Presentation[len(task.Presentation)-1]
	if answer.Kind != "message" || answer.Text != "I changed the background and the checks pass." {
		t.Fatalf("semantic presentation = %#v", task.Presentation)
	}
	for _, evidence := range []string{"deepseek-v4-flash", "$ npm test", "PASS"} {
		if !strings.Contains(task.RawOutput, evidence) {
			t.Errorf("folded raw lane lost %q: %q", evidence, task.RawOutput)
		}
		if strings.Contains(answer.Text, evidence) {
			t.Errorf("console evidence %q leaked into assistant presentation", evidence)
		}
	}
}

// TestSFMGBackgroundTaskStreamContract is the headless, device-independent
// version of a real vibing request: "change SFMG's background color". A
// surface must receive two deliberately different lanes from one subscription:
// a quiet, primary explanation for the user and the unmodified ANSI/diff
// evidence that its expandable terminal renderer can colour. No mobile build,
// LLM process, or application mutation is involved, so this remains a fast
// regression test for every client surface.
func TestSFMGBackgroundTaskStreamContract(t *testing.T) {
	srv, tm := startRawTestServer(t)
	raw := "\x1b[1m$ node -e 'update Expo background color'\x1b[0m\n" +
		"diff --git a/app.json b/app.json\n--- a/app.json\n+++ b/app.json\n" +
		"-      \"backgroundColor\": \"#1B5E20\"\n+      \"backgroundColor\": \"#123456\"\n"
	taskID := createRawFixture(t, tm, "sfmg-background-contract", raw, TaskStatusFinished)
	task, ok := tm.GetTask(taskID)
	if !ok {
		t.Fatal("fixture task missing")
	}
	task.ProjectName = "sfmg"
	tm.present(task, taskPresentationInput{
		ID: task.ID + "-activity", Kind: "status", Phase: "coding", State: "running",
		Text: "Updating the app background.",
	})
	tm.present(task, taskPresentationInput{
		ID: task.ID + "-answer", Kind: "message", Role: "assistant",
		Text: "The SFMG app background is now #123456.\n\n**$ node -e 'update Expo background color'**\n\ndiff --git a/app.json b/app.json\n\nThe configuration change is ready to review.",
	})

	frames := collectSSEFrames(t, srv.URL+"/tasks/"+taskID+"/output?rawSince=0")
	rawReplay := rawFrame(t, frames, "raw_replay")
	if got, _ := rawReplay["text"].(string); got != raw {
		t.Fatalf("raw console bytes changed in transit:\n got %q\nwant %q", got, raw)
	}
	snapshot, err := json.Marshal(rawFrame(t, frames, "presentation_snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	friendly := string(snapshot)
	for _, want := range []string{
		"Completed on sfmg.",
		"The SFMG app background is now #123456.",
		"The configuration change is ready to review.",
	} {
		if !strings.Contains(friendly, want) {
			t.Errorf("friendly presentation missing %q: %s", want, friendly)
		}
	}
	for _, terminalOnly := range []string{"node -e", "diff --git", "app.json"} {
		if strings.Contains(friendly, terminalOnly) {
			t.Errorf("terminal evidence leaked into primary presentation (%q): %s", terminalOnly, friendly)
		}
	}
}

func TestRemotelessRawReaderUsesOpenCodeSemanticBoundary(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID: "remoteless-semantic", RunnerID: "remoteless",
		runner:   RunnerConfig{RunnerID: "remoteless", Command: "opencode", OutputMode: "raw"},
		outputCh: make(chan string, 16), rawOutputCh: make(chan taskRawFrame, 16),
		eventCh: make(chan map[string]interface{}, 16), doneCh: make(chan struct{}),
	}
	tm.readRawOutput(
		task,
		strings.NewReader("The requested change is ready.\n"),
		strings.NewReader("> build · deepseek-v4-flash\n$ npm test\nPASS\n"),
	)

	if got := strings.TrimSpace(task.ResultText); got != "The requested change is ready." {
		t.Fatalf("ResultText = %q, want only the OpenCode-backed assistant reply", got)
	}
	if len(task.Presentation) == 0 || task.Presentation[len(task.Presentation)-1].Text != "The requested change is ready." {
		t.Fatalf("semantic presentation = %#v", task.Presentation)
	}
}

// TestRawCap_TruncationMarker — emitRaw must never hold more than
// rawOutputMaxBytes in memory: past the cap the head is dropped and a
// readable marker is prepended so a client knows the earliest bytes went.
func TestRawCap_TruncationMarker(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{RunnerID: "opencode", rawOutputCh: make(chan taskRawFrame, 128)}
	chunk := strings.Repeat("x", 8*1024)
	// Feed ~1.1 MB through the cap path (600 KB > 512 KB cap).
	for i := 0; i < 80; i++ {
		tm.emitRaw(task, []byte(chunk))
	}
	wantLen := rawOutputMaxBytes + len(rawOutputTruncatedMarker)
	if len(task.RawOutput) != wantLen {
		t.Errorf("RawOutput len = %d, want %d (cap + marker)", len(task.RawOutput), wantLen)
	}
	if !strings.HasPrefix(task.RawOutput, rawOutputTruncatedMarker) {
		t.Errorf("RawOutput must start with the truncation marker; got %q", task.RawOutput[:80])
	}
	if task.RawOutputOffset != int64(80*len(chunk)) {
		t.Errorf("RawOutputOffset = %d, want monotonic source length %d", task.RawOutputOffset, 80*len(chunk))
	}
	if task.RawOutputBase != task.RawOutputOffset-rawOutputMaxBytes {
		t.Errorf("RawOutputBase = %d, want retained-tail base %d", task.RawOutputBase, task.RawOutputOffset-rawOutputMaxBytes)
	}
}

func TestRawLiveFrameCursorStaysBoundToItsChunk(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{RunnerID: "opencode", rawOutputCh: make(chan taskRawFrame, 2)}
	tm.emitRaw(task, []byte("first"))
	tm.emitRaw(task, []byte("second"))

	first := <-task.rawOutputCh
	second := <-task.rawOutputCh
	if string(first.Bytes) != "first" || first.Offset != 5 {
		t.Fatalf("first frame = %#v, want bytes=first offset=5", first)
	}
	if string(second.Bytes) != "second" || second.Offset != 11 {
		t.Fatalf("second frame = %#v, want bytes=second offset=11", second)
	}
}

// TestGetTask_RawWireCap — GET /tasks/{id} ships at most
// taskWireRawOutputCap bytes of raw tail plus the FULL byte offset, so a
// polling client can seed a terminal and resume via ?rawSince= without
// re-fetching the rest.
func TestGetTask_RawWireCap(t *testing.T) {
	srv, tm := startRawTestServer(t)
	big := strings.Repeat("y", 200*1024) // > 64 KB wire cap, < 512 KB retain cap
	taskID := createRawFixture(t, tm, "raw-wire-1", big, TaskStatusFinished)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/tasks/"+taskID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET task: %v", err)
	}
	defer res.Body.Close()
	var parsed struct {
		Task struct {
			RawOutput string `json:"rawOutput"`
			RawOffset int    `json:"rawOffset"`
		} `json:"task"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if len(parsed.Task.RawOutput) != taskWireRawOutputCap {
		t.Errorf("rawOutput wire len = %d, want cap %d", len(parsed.Task.RawOutput), taskWireRawOutputCap)
	}
	if parsed.Task.RawOffset != len(big) {
		t.Errorf("rawOffset = %d, want full tail length %d", parsed.Task.RawOffset, len(big))
	}
	if !strings.HasSuffix(big, parsed.Task.RawOutput) {
		t.Errorf("wire rawOutput must be the TAIL of the retained bytes")
	}
}
