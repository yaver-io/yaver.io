package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These guard the two defects that made a healthy machine look unreachable.
//
// A box that accumulated ~4000 tasks served ~8 MB from GET /tasks. The relay
// answered that with a 502 after 15s, and the phone rendered it as "the machine
// accepted the connection but never answered" — a CONNECTION error. Transport
// was fine throughout (/health was 53 bytes in 0.33s over the same relay). The
// misattribution is what made it un-debuggable: every fix attempt went at
// Tailscale, ATS, mesh and relay, none of which were broken.
//
// So the invariant under test is not "the list is convenient" — it is "the list
// endpoint can never produce a response the transport cannot carry".

func newListTasksServer(t *testing.T, n int) *HTTPServer {
	t.Helper()
	tm := &TaskManager{tasks: map[string]*Task{}}
	base := time.Now().Add(-time.Duration(n) * time.Minute)
	for i := 0; i < n; i++ {
		id := "task-" + itoaPad(i)
		tm.tasks[id] = &Task{
			ID:        id,
			Title:     id,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
			// Rows must carry REALISTIC bodies. The fixtures here used to set
			// only ID/Title/CreatedAt, so 4000 tasks serialised to a few
			// hundred KB and every assertion below passed while the real
			// endpoint shipped megabytes. That false green is why the 2026-07-20
			// recurrence shipped: the guard measured row count, and row count
			// was never the thing that broke.
			ResultText: strings.Repeat("r", 40_000),
			Output:     strings.Repeat("o", 40_000),
		}
	}
	return &HTTPServer{taskMgr: tm}
}

// TestListTasksResponseIsBoundedInBytes is the guard the previous fix lacked.
// The budget itself (listTasksMaxResponseBytes) is defined next to the handler
// that must honour it — a test-local copy would let the two drift apart, which
// is how a guard silently stops guarding.
//
// On 2026-07-20 a box with 50 tasks — inside every row-count limit — served
// 2.2 MB because ONE task had a 1.8 MB ResultText. The phone polls this list
// every 1-3s, which saturated the relay so the task-create POST timed out at
// 30s and reported the machine as unreachable. Bounding rows does not bound
// bytes; only bytes bound bytes.
func TestListTasksResponseIsBoundedInBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		url  string
	}{
		{"default limit, fat rows", 4000, "/tasks"},
		{"explicit max limit", 4000, "/tasks?limit=99999"},
		// The real-world shape: few tasks, one enormous answer.
		{"few tasks, huge bodies", 50, "/tasks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newListTasksServer(t, tc.n)
			rec := httptest.NewRecorder()
			s.listTasks(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
			if got := rec.Body.Len(); got > listTasksMaxResponseBytes {
				t.Fatalf("GET %s returned %d bytes (%.1f MB), limit %d — this is the "+
					"response the relay cannot carry, which the phone renders as "+
					"\"the machine accepted the connection but never answered\"",
					tc.url, got, float64(got)/(1<<20), listTasksMaxResponseBytes)
			}
		})
	}
}

// TestListTasksTruncatesResultText pins the specific field that caused the
// 2026-07-20 recurrence. It must be trimmed AND visibly marked, so a client
// never renders a cut-off answer as the complete one.
func TestListTasksTruncatesResultText(t *testing.T) {
	tm := &TaskManager{tasks: map[string]*Task{}}
	tm.tasks["t1"] = &Task{
		ID: "t1", Title: "t1", CreatedAt: time.Now(),
		ResultText: strings.Repeat("x", 1_800_000), // the size seen on the real box
	}
	s := &HTTPServer{taskMgr: tm}

	rec := httptest.NewRecorder()
	s.listTasks(rec, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got, _ := tasks[0].(map[string]interface{})["resultText"].(string)
	if len(got) > listTasksMaxResultText*2 {
		t.Fatalf("resultText is %d bytes — a single unbounded field rebuilds the "+
			"whole unservable response", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a truncated resultText must SAY it was truncated — a silently " +
			"cut answer looks like the complete answer")
	}
}

func itoaPad(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0000"
	}
	b := []byte("0000")
	p := 3
	for i > 0 && p >= 0 {
		b[p] = digits[i%10]
		i /= 10
		p--
	}
	return string(b)
}

func decodeList(t *testing.T, s *HTTPServer, url string) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.listTasks(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	return out
}

// TestListTasksIsBoundedWithoutAnExplicitLimit is the core guard. `?limit=` was
// already supported before this fix — and was OPT-IN, which is why it never
// helped. A cap a client can forget is not a cap.
func TestListTasksIsBoundedWithoutAnExplicitLimit(t *testing.T) {
	s := newListTasksServer(t, 4000) // the count observed on the real box

	out := decodeList(t, s, "/tasks") // NO ?limit — the client that broke
	tasks, _ := out["tasks"].([]interface{})

	if len(tasks) > listTasksDefaultLimit {
		t.Fatalf("unbounded list: returned %d tasks with no ?limit — this is the "+
			"~8 MB response the relay 502s on, surfaced to the user as a "+
			"connection failure", len(tasks))
	}
	if got := int(out["total"].(float64)); got != 4000 {
		t.Errorf("total = %d, want 4000 — a client cannot tell a truncated list "+
			"from a complete one without it", got)
	}
	if truncated, _ := out["truncated"].(bool); !truncated {
		t.Error("truncated should be true when tasks were dropped")
	}
}

// TestListTasksReturnsNewestFirst pins ordering. ListTasks ranges over a map,
// so before the sort the order was randomised per call: the pre-existing
// ?limit=N returned an ARBITRARY N tasks, and a client could see one task twice
// while never seeing another.
func TestListTasksReturnsNewestFirst(t *testing.T) {
	s := newListTasksServer(t, 200)
	out := decodeList(t, s, "/tasks?limit=5")
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) != 5 {
		t.Fatalf("returned %d tasks, want 5", len(tasks))
	}

	var prev time.Time
	for i, raw := range tasks {
		m := raw.(map[string]interface{})
		ts, err := time.Parse(time.RFC3339Nano, m["createdAt"].(string))
		if err != nil {
			t.Fatalf("bad createdAt: %v", err)
		}
		if i > 0 && ts.After(prev) {
			t.Fatalf("task %d is NEWER than the one before it — list is not "+
				"newest-first, so ?limit returns an arbitrary subset", i)
		}
		prev = ts
	}

	// The newest task must be present: a "recent tasks" view that omits the most
	// recent one is worse than an empty list, because it looks correct.
	first := tasks[0].(map[string]interface{})
	if first["id"] != "task-0199" {
		t.Errorf("newest task = %v, want task-0199", first["id"])
	}
}

// TestListTasksCapsAnOverlyLargeExplicitLimit — a client asking for everything
// must still not be able to reproduce the 8 MB response.
func TestListTasksCapsAnOverlyLargeExplicitLimit(t *testing.T) {
	s := newListTasksServer(t, 4000)
	out := decodeList(t, s, "/tasks?limit=99999")
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) > listTasksMaxLimit {
		t.Fatalf("returned %d tasks for ?limit=99999 — the hard cap (%d) is not "+
			"enforced, so any client can still wedge the relay",
			len(tasks), listTasksMaxLimit)
	}
}

// TestListTasksHonoursASmallExplicitLimit — the cap must not break the feature
// it is protecting.
func TestListTasksHonoursASmallExplicitLimit(t *testing.T) {
	s := newListTasksServer(t, 100)
	out := decodeList(t, s, "/tasks?limit=3")
	if tasks, _ := out["tasks"].([]interface{}); len(tasks) != 3 {
		t.Fatalf("returned %d tasks for ?limit=3, want 3", len(tasks))
	}
	// Fewer tasks than the limit must NOT report truncation.
	small := newListTasksServer(t, 2)
	out2 := decodeList(t, small, "/tasks")
	if truncated, _ := out2["truncated"].(bool); truncated {
		t.Error("truncated should be false when every task was returned")
	}
}

// TestListTasksOmitsTranscriptButKeepsCount pins the second half of the payload
// fix. Bounding the ROW COUNT is not enough if each row carries its full
// conversation: measured at ~12 KB/task on a real box, the 500-row ceiling
// still permitted a ~6 MB response — most of the original 8 MB bug, just
// harder to trigger.
//
// The detail endpoint is unaffected (getTask calls GetTask directly), so
// nothing is lost — only the LIST stops shipping transcripts.
func TestListTasksOmitsTranscriptButKeepsCount(t *testing.T) {
	tm := &TaskManager{tasks: map[string]*Task{}}
	tm.tasks["t1"] = &Task{
		ID: "t1", Title: "t1", CreatedAt: time.Now(),
		Turns: []ConversationTurn{{}, {}, {}},
	}
	s := &HTTPServer{taskMgr: tm}

	rec := httptest.NewRecorder()
	s.listTasks(rec, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	row := tasks[0].(map[string]interface{})

	if _, present := row["turns"]; present {
		t.Error("the LIST must not carry `turns` — that is the ~12 KB/row leak " +
			"that lets a bounded list still return megabytes")
	}
	if got, _ := row["turnCount"].(float64); int(got) != 3 {
		t.Errorf("turnCount = %v, want 3 — a client must still be able to show "+
			"the turn count without downloading the transcript", got)
	}
}

type blockingTaskStore struct {
	saveDelay time.Duration
	saved     chan struct{}
}

func (s *blockingTaskStore) Load() map[string]*Task { return map[string]*Task{} }
func (s *blockingTaskStore) Save(tasks map[string]*Task) {}
func (s *blockingTaskStore) SaveRecords(records []persistedTask) {
	time.Sleep(s.saveDelay)
	select {
	case s.saved <- struct{}{}:
	default:
	}
}

func TestCreateTaskDoesNotWaitForSlowTaskStore(t *testing.T) {
	store := &blockingTaskStore{
		saveDelay: 750 * time.Millisecond,
		saved:     make(chan struct{}, 1),
	}
	tm := NewTaskManager(t.TempDir(), store, defaultRunner)
	tm.DummyMode = true

	start := time.Now()
	task, err := tm.CreateTask("Hello hello", "Reply with exactly BEACH_OK", "", "mobile", "", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task == nil {
		t.Fatal("CreateTask returned nil task")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("CreateTask took %v with a slow task store — task history rewrites are blocking POST /tasks again", elapsed)
	}
	select {
	case <-store.saved:
	case <-time.After(2 * time.Second):
		t.Fatal("background task-store save never completed")
	}
}
