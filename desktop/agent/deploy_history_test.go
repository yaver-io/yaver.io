package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeployHistoryRingBuffer(t *testing.T) {
	// Isolate HOME: NewDeployHistory loads (and evicts, deleting log
	// dirs from) ~/.yaver/deploys. Without this a test run mutates the
	// developer's real deploy history and reads their runs as its own.
	t.Setenv("HOME", t.TempDir())
	h := NewDeployHistory(3)

	ids := []string{}
	for i := 0; i < 5; i++ {
		r := h.Start(DeployRun{App: "x", Target: "y", RequestedBy: "owner"})
		ids = append(ids, r.ID)
	}

	list := h.List(0, "")
	if len(list) != 3 {
		t.Fatalf("expected 3 entries after 5 inserts into cap=3, got %d", len(list))
	}
	// Oldest two must be evicted.
	for _, l := range list {
		if l.ID == ids[0] || l.ID == ids[1] {
			t.Errorf("evicted entry still present: %s", l.ID)
		}
	}
}

func TestDeployHistoryAppendCap(t *testing.T) {
	// Isolate HOME: NewDeployHistory loads (and evicts, deleting log
	// dirs from) ~/.yaver/deploys. Without this a test run mutates the
	// developer's real deploy history and reads their runs as its own.
	t.Setenv("HOME", t.TempDir())
	h := NewDeployHistory(5)
	r := h.Start(DeployRun{App: "x", Target: "y"})
	// Pump lots of lines — total bytes should land under the 8 KB cap.
	for i := 0; i < 2000; i++ {
		h.Append(r.ID, "this is a reasonably long line to stress the buffer")
	}
	got, ok := h.Get(r.ID, "")
	if !ok {
		t.Fatalf("Get missed entry")
	}
	if len(got.OutputTail) > deployOutputTailCap+64 {
		t.Errorf("OutputTail exceeds cap: got %d bytes (cap %d)", len(got.OutputTail), deployOutputTailCap)
	}
	// The tail should contain recent data, not the first line.
	if !strings.Contains(got.OutputTail, "reasonably long") {
		t.Error("expected recent line content in tail")
	}
}

func TestDeployHistoryFinish(t *testing.T) {
	// Isolate HOME: NewDeployHistory loads (and evicts, deleting log
	// dirs from) ~/.yaver/deploys. Without this a test run mutates the
	// developer's real deploy history and reads their runs as its own.
	t.Setenv("HOME", t.TempDir())
	h := NewDeployHistory(5)
	r := h.Start(DeployRun{App: "a", Target: "b"})
	h.Finish(r.ID, 0, false)

	got, _ := h.Get(r.ID, "")
	if got.InProgress {
		t.Error("finished entry should not be InProgress")
	}
	if !got.OK {
		t.Error("exit=0 should flag OK=true")
	}
	if got.DurationMs < 0 {
		t.Errorf("invalid duration: %d", got.DurationMs)
	}

	// Non-zero exit flags OK=false.
	r2 := h.Start(DeployRun{App: "a", Target: "b"})
	h.Finish(r2.ID, 42, false)
	got2, _ := h.Get(r2.ID, "")
	if got2.ExitCode != 42 || got2.OK {
		t.Errorf("exit=42 should NOT flag OK: %+v", got2)
	}
}

func TestDeployHistoryGuestFilter(t *testing.T) {
	// Isolate HOME: NewDeployHistory loads (and evicts, deleting log
	// dirs from) ~/.yaver/deploys. Without this a test run mutates the
	// developer's real deploy history and reads their runs as its own.
	t.Setenv("HOME", t.TempDir())
	h := NewDeployHistory(10)
	_ = h.Start(DeployRun{App: "a", Target: "b", RequestedBy: "owner"})
	guestA := h.Start(DeployRun{App: "a", Target: "b", RequestedBy: "guest-A"})
	_ = h.Start(DeployRun{App: "a", Target: "b", RequestedBy: "guest-B"})

	// Guest A only sees their own.
	list := h.List(0, "guest-A")
	if len(list) != 1 {
		t.Fatalf("expected 1 entry for guest-A, got %d", len(list))
	}
	if list[0].ID != guestA.ID {
		t.Errorf("wrong entry: %+v", list[0])
	}

	// Get respects filter.
	if _, ok := h.Get(guestA.ID, "guest-B"); ok {
		t.Error("guest-B should not be able to Get guest-A's run")
	}
	if _, ok := h.Get(guestA.ID, "guest-A"); !ok {
		t.Error("guest-A should see own run")
	}
}

func TestDeployLimiter(t *testing.T) {
	l := newDeployLimiter()

	// Owner: 2 allowed, 3rd rejected.
	if !l.tryAcquire("owner", 2) {
		t.Fatal("first acquire failed")
	}
	if !l.tryAcquire("owner", 2) {
		t.Fatal("second acquire failed")
	}
	if l.tryAcquire("owner", 2) {
		t.Fatal("third acquire should have been rejected")
	}

	// Guest key is independent.
	if !l.tryAcquire("guest:X", 1) {
		t.Fatal("guest:X first acquire failed")
	}
	if l.tryAcquire("guest:X", 1) {
		t.Fatal("guest:X second acquire should have been rejected")
	}

	// Release restores slots.
	l.release("owner")
	if !l.tryAcquire("owner", 2) {
		t.Fatal("owner acquire after release failed")
	}

	// Over-release doesn't panic.
	l.release("nobody")
	l.release("nobody")
}

func TestDeployRunsEndpoints(t *testing.T) {
	// Isolate HOME: NewDeployHistory loads (and evicts, deleting log
	// dirs from) ~/.yaver/deploys. Without this a test run mutates the
	// developer's real deploy history and reads their runs as its own.
	t.Setenv("HOME", t.TempDir())
	h := NewDeployHistory(10)
	// Pre-populate with one owner run + one guest run.
	_ = h.Start(DeployRun{App: "web", Target: "cloudflare", RequestedBy: "owner", IsGuest: false})
	guestRun := h.Start(DeployRun{App: "web", Target: "cloudflare", RequestedBy: "guest-1", IsGuest: true})
	h.Append(guestRun.ID, "building...")
	h.Finish(guestRun.ID, 0, false)

	srv := &HTTPServer{deployHistory: h}

	// Owner sees both.
	req := httptest.NewRequest("GET", "/deploy/runs", nil)
	w := httptest.NewRecorder()
	srv.handleDeployRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner list: %d", w.Code)
	}
	var list struct {
		Runs []DeployRun `json:"runs"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Runs) != 2 {
		t.Fatalf("owner should see 2 runs, got %d", len(list.Runs))
	}
	// OutputTail must be elided from the list (the detail endpoint
	// carries the tail).
	for _, r := range list.Runs {
		if r.OutputTail != "" {
			t.Error("list response should not carry OutputTail")
		}
	}

	// Guest only sees their own.
	req = httptest.NewRequest("GET", "/deploy/runs", nil)
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "guest-1")
	w = httptest.NewRecorder()
	srv.handleDeployRuns(w, req)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Runs) != 1 || list.Runs[0].ID != guestRun.ID {
		t.Fatalf("guest list wrong: %+v", list.Runs)
	}

	// Guest detail with correct ID, correct filter: OK + includes OutputTail.
	req = httptest.NewRequest("GET", "/deploy/runs/"+guestRun.ID, nil)
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "guest-1")
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("guest detail: %d", w.Code)
	}
	var detail DeployRun
	json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.ID != guestRun.ID || !strings.Contains(detail.OutputTail, "building...") {
		t.Fatalf("detail wrong: %+v", detail)
	}

	// Guest detail with ID they don't own → 404 (info hiding).
	otherGuest := h.Start(DeployRun{App: "x", Target: "y", RequestedBy: "guest-2", IsGuest: true})
	req = httptest.NewRequest("GET", "/deploy/runs/"+otherGuest.ID, nil)
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "guest-1")
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("guest cross-access should be 404, got %d", w.Code)
	}

	// Missing ID → 400.
	req = httptest.NewRequest("GET", "/deploy/runs/", nil)
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing id should 400, got %d", w.Code)
	}
}

func TestDeployShipConcurrencyCap(t *testing.T) {
	// Two guest deploys should be allowed (cap=2); a third must be
	// rejected with 429.
	srv := &HTTPServer{
		deployLimiter: newDeployLimiter(),
	}
	// Pre-fill the guest's slots.
	srv.deployLimiter.tryAcquire("guest:G", deployShipLimits.Guest)
	srv.deployLimiter.tryAcquire("guest:G", deployShipLimits.Guest)

	body := `{"app":"web","target":"cloudflare"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "G")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 once guest cap exhausted, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "concurrency cap") {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}
