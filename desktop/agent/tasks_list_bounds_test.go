package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		}
	}
	return &HTTPServer{taskMgr: tm}
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
