package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The rotation tests matter more than the formatting ones. This logger runs on
// boxes that are disk-tight (the machine it was written for had 11 GB free of
// 228 GB), and it defaults to DEBUG — so an unbounded or mis-rotating log would
// not be a cosmetic bug, it would take the machine down. "Add rotation later"
// is exactly how that happens, so it is tested here, now.

func newTestLogger(t *testing.T, maxBytes int) *diagLogger {
	t.Helper()
	return &diagLogger{path: filepath.Join(t.TempDir(), "agent.log"), min: diagDebug}
}

func TestDiagLogWritesTaggedLevelledLines(t *testing.T) {
	d := newTestLogger(t, diagMaxBytes)
	d.logf(diagInfo, tagConnect, "GET /health -> %d", 200)
	d.logf(diagError, tagRelay, "relay refused")

	raw, err := os.ReadFile(d.path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"[connect]", "INFO", "GET /health -> 200", "[relay]", "ERROR"} {
		if !strings.Contains(body, want) {
			t.Errorf("log missing %q\n%s", want, body)
		}
	}
}

// A level filter that does not filter is worse than none: it implies the quiet
// levels were checked and found empty.
func TestDiagLogRespectsMinimumLevel(t *testing.T) {
	d := newTestLogger(t, diagMaxBytes)
	d.min = diagWarn
	d.logf(diagDebug, tagAgent, "debug-line-should-not-appear")
	d.logf(diagInfo, tagAgent, "info-line-should-not-appear")
	d.logf(diagWarn, tagAgent, "warn-line-should-appear")

	raw, _ := os.ReadFile(d.path)
	body := string(raw)
	if strings.Contains(body, "should-not-appear") {
		t.Errorf("lines below the minimum level were written:\n%s", body)
	}
	if !strings.Contains(body, "warn-line-should-appear") {
		t.Errorf("line at the minimum level was dropped:\n%s", body)
	}
}

// Default MUST be debug: the failures this exists for are intermittent, so a
// log that defaults to quiet records nothing on the one run that mattered.
func TestDiagLevelParsingAndDefault(t *testing.T) {
	for in, want := range map[string]diagLevel{
		"debug": diagDebug, "INFO": diagInfo, "warn": diagWarn,
		"warning": diagWarn, "Error": diagError,
	} {
		got, ok := parseDiagLevel(in)
		if !ok || got != want {
			t.Errorf("parseDiagLevel(%q) = %v,%v; want %v,true", in, got, ok, want)
		}
	}
	if got, ok := parseDiagLevel("nonsense"); ok || got != diagDebug {
		t.Errorf("unknown level should fall back to debug, got %v,%v", got, ok)
	}
}

// THE size guard. Without it the log grows until the disk does not.
func TestDiagLogRotatesAndBoundsTotalSize(t *testing.T) {
	dir := t.TempDir()
	d := &diagLogger{path: filepath.Join(dir, "agent.log"), min: diagDebug}

	// Force rotation quickly by writing well past the threshold.
	line := strings.Repeat("x", 4096)
	writes := (diagMaxBytes / 4096) * (diagMaxGenerations + 3)
	for i := 0; i < writes; i++ {
		d.logf(diagDebug, tagConnect, "%s", line)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var total int64
	var logs int
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.HasPrefix(e.Name(), "agent.log") {
			logs++
			total += info.Size()
		}
	}

	// live file + at most diagMaxGenerations rotated
	if logs > diagMaxGenerations+1 {
		t.Errorf("kept %d log files, want at most %d — generations are not being dropped",
			logs, diagMaxGenerations+1)
	}
	ceiling := int64(diagMaxBytes) * int64(diagMaxGenerations+1)
	// Allow one line of slop: rotation triggers just after crossing the mark.
	if total > ceiling+8192 {
		t.Errorf("total log bytes = %d, exceeds the %d ceiling — this is the "+
			"unbounded growth that would fill a disk-tight box", total, ceiling)
	}
	if total == 0 {
		t.Error("no log output at all — the cap must bound the log, not disable it")
	}
}

// A full/read-only disk must degrade to "no logging", never take the agent down.
func TestDiagLogDegradesWhenUnwritable(t *testing.T) {
	d := &diagLogger{path: "/nonexistent-dir-for-yaver-test/agent.log", min: diagDebug}
	d.logf(diagError, tagAgent, "this must not panic")
	if !d.disabled {
		t.Error("logger should disable itself after an unrecoverable open failure")
	}
	d.logf(diagError, tagAgent, "second call must be a no-op")
}

// withRequestLog must record the pair that identifies an oversized response —
// status AND byte count — because that is precisely what distinguished "the
// request never arrived" from "it arrived and the response was 8 MB".
func TestRequestLogRecordsStatusAndSize(t *testing.T) {
	dir := t.TempDir()
	prev := diagInst
	diagInst = &diagLogger{path: filepath.Join(dir, "agent.log"), min: diagDebug}
	diagOnce.Do(func() {}) // mark done so diag() returns our instance
	defer func() { diagInst = prev }()

	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, strings.Repeat("y", 1234))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks?token=SECRET-MUST-NOT-BE-LOGGED", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	h.ServeHTTP(rec, req)

	raw, _ := os.ReadFile(diagInst.path)
	body := string(raw)
	for _, want := range []string{"GET", "/tasks", "502", "1234B", "203.0.113.9", "[connect]"} {
		if !strings.Contains(body, want) {
			t.Errorf("request log missing %q\n%s", want, body)
		}
	}
	// Privacy: a bearer token can ride in ?token=. Logging the raw URI would
	// write a credential to disk.
	if strings.Contains(body, "SECRET-MUST-NOT-BE-LOGGED") {
		t.Error("query string was logged — this writes ?token= credentials to disk")
	}
	// 5xx must be visible at ERROR, not buried at debug.
	if !strings.Contains(body, "ERROR") {
		t.Error("a 502 should be logged at ERROR level")
	}
}

// Wrapping the mux must not cost streaming: PTY and WebSocket endpoints need
// Hijack, SSE needs Flush. A logging change that breaks the shell is a bad
// trade at any level of detail.
func TestRequestLogPreservesHijackAndFlush(t *testing.T) {
	var canHijack, canFlush bool
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, canHijack = w.(http.Hijacker)
		_, canFlush = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/shell", nil))
	if !canHijack {
		t.Error("wrapped ResponseWriter lost http.Hijacker — WebSocket/PTY upgrades would break")
	}
	if !canFlush {
		t.Error("wrapped ResponseWriter lost http.Flusher — SSE/streaming would stall")
	}
}

// Rotated generations older than the retention window are removed, so a quiet
// box does not carry history forever.
func TestDiagLogPrunesAgedGenerations(t *testing.T) {
	dir := t.TempDir()
	d := &diagLogger{path: filepath.Join(dir, "agent.log"), min: diagDebug}

	old := d.path + ".3"
	if err := os.WriteFile(old, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-diagMaxAge - time.Hour)
	if err := os.Chtimes(old, aged, aged); err != nil {
		t.Fatal(err)
	}
	fresh := d.path + ".1"
	if err := os.WriteFile(fresh, []byte("recent"), 0o600); err != nil {
		t.Fatal(err)
	}

	d.pruneAged()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a generation older than the retention window should have been pruned")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent generation must NOT be pruned")
	}
}
