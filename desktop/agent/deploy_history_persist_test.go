package main

// Tests for the on-disk persistent log layer + the /deploy/runs/{id}/output
// HTTP endpoint. In-memory ring-buffer / guest-filter tests live in
// deploy_history_test.go; this file focuses on the persist-to-disk path.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployHistoryPersistentLogs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	_ = os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)

	h := NewDeployHistory(10)
	if h.LogRoot() == "" {
		t.Fatal("expected LogRoot to be set with HOME pointing at tmp")
	}
	run := h.Start(DeployRun{App: "web", Target: "cloudflare"})
	if run.LogPath == "" {
		t.Fatalf("expected LogPath after Start, got empty run=%+v", run)
	}

	// Emit more data than the in-memory tail cap — the on-disk log
	// must still contain everything.
	for i := 0; i < 200; i++ {
		h.Append(run.ID, strings.Repeat("x", 200))
	}
	h.Finish(run.ID, 0, false)

	got, _ := h.Get(run.ID, "")
	if len(got.OutputTail) > deployOutputTailCap+64 {
		t.Errorf("tail exceeds cap: %d > %d", len(got.OutputTail), deployOutputTailCap)
	}
	// On-disk file should have the full volume (201 × ~200 bytes).
	info, err := os.Stat(run.LogPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() < int64(deployOutputTailCap*2) {
		t.Errorf("on-disk log too small to have captured the full run: %d bytes", info.Size())
	}
}

func TestDeployHistoryPersistentLogsGC(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	_ = os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)

	// Tiny ring buffer so eviction kicks in predictably. Each run
	// writes some data; after maxLen+1 runs the oldest's on-disk
	// dir must be gone.
	h := NewDeployHistory(2)

	var firstPath string
	for i := 0; i < 4; i++ {
		r := h.Start(DeployRun{App: "x", Target: "y"})
		if i == 0 {
			firstPath = filepath.Dir(r.LogPath)
		}
		h.Append(r.ID, "some output line")
		h.Finish(r.ID, 0, false)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Errorf("first run's log dir should be GC'd by ring-buffer eviction: stat err=%v", err)
	}
}

func TestHandleDeployRunDetailOutput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	_ = os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)
	h := NewDeployHistory(5)
	r := h.Start(DeployRun{App: "web", Target: "cloudflare"})
	for i := 0; i < 10; i++ {
		h.Append(r.ID, "line-xyz")
	}
	h.Finish(r.ID, 0, false)
	srv := &HTTPServer{deployHistory: h}

	// Plain detail (no suffix) → JSON with elided log_path.
	req := httptest.NewRequest("GET", "/deploy/runs/"+r.ID, nil)
	w := httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), tmp) {
		t.Error("detail response must not leak the filesystem log_path to clients")
	}

	// /output suffix → text/plain full log.
	req = httptest.NewRequest("GET", "/deploy/runs/"+r.ID+"/output", nil)
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("output: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "line-xyz") {
		t.Fatalf("expected log content in output response, got: %s", w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain, got %q", ct)
	}

	// /output on missing run → 404.
	req = httptest.NewRequest("GET", "/deploy/runs/nope/output", nil)
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing id /output should be 404, got %d", w.Code)
	}

	// Unknown sub-resource → 400.
	req = httptest.NewRequest("GET", "/deploy/runs/"+r.ID+"/gibberish", nil)
	w = httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown suffix should be 400, got %d", w.Code)
	}
}

func TestHandleDeployRunDetailOutputFallbackToTail(t *testing.T) {
	// DeployHistory built without HOME pointing anywhere useful —
	// logRoot will be unset and the run has no LogPath. The output
	// endpoint must still work by streaming OutputTail instead of
	// 500ing.
	h := &DeployHistory{
		runs:   []*DeployRun{},
		byID:   map[string]*DeployRun{},
		maxLen: 5,
		// logRoot empty on purpose
	}
	r := h.Start(DeployRun{App: "a", Target: "b"})
	h.Append(r.ID, "only-in-tail")
	h.Finish(r.ID, 0, false)
	srv := &HTTPServer{deployHistory: h}

	req := httptest.NewRequest("GET", "/deploy/runs/"+r.ID+"/output", nil)
	w := httptest.NewRecorder()
	srv.handleDeployRunDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("output fallback: %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "only-in-tail") {
		t.Errorf("expected tail fallback content, got: %s", string(body))
	}
}
