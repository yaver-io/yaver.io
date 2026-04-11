package testkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveHAREmptyState(t *testing.T) {
	if _, err := SaveHAR(nil, t.TempDir(), "x"); err == nil {
		t.Error("expected error on nil state")
	}
}

func TestSaveHARWritesValidJSON(t *testing.T) {
	state := &InstrumentationState{
		Requests: []NetworkEvent{
			{
				RequestID:  "1",
				Method:     "GET",
				URL:        "http://example.test/",
				Status:     200,
				StatusText: "OK",
				Type:       "Document",
				SizeBytes:  1234,
				DurationMS: 42,
				StartedAt:  time.Now(),
			},
			{
				RequestID:  "2",
				Method:     "POST",
				URL:        "http://example.test/api",
				Status:     500,
				StatusText: "Server Error",
				Type:       "Fetch",
				SizeBytes:  56,
				DurationMS: 120,
				StartedAt:  time.Now(),
			},
		},
	}
	dir := t.TempDir()
	path, err := SaveHAR(state, dir, "run")
	if err != nil {
		t.Fatalf("SaveHAR: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	log, _ := parsed["log"].(map[string]interface{})
	if log == nil {
		t.Fatal("HAR missing log")
	}
	entries, _ := log["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("entries = %d, want 2", len(entries))
	}
	if !filepathEndsWith(path, ".har") {
		t.Errorf("path does not end with .har: %s", path)
	}
}

func TestInstrumentationStateSummary(t *testing.T) {
	s := &InstrumentationState{
		Console: []ConsoleEvent{
			{Level: "error", Message: "boom"},
			{Level: "warning", Message: "deprecated"},
		},
		Requests: []NetworkEvent{
			{Status: 200},
			{Status: 404},
		},
		Perf: PerformanceMetrics{LargestContentfulPaintMS: 420},
	}
	line := s.SummaryLine()
	if line == "" {
		t.Fatal("SummaryLine is empty")
	}
	if s.ConsoleErrorCount() != 1 {
		t.Errorf("ConsoleErrorCount = %d, want 1", s.ConsoleErrorCount())
	}
}

// filepathEndsWith is a tiny local helper so we don't pull in
// strings just for this one check.
func filepathEndsWith(p, suffix string) bool {
	return len(p) >= len(suffix) && p[len(p)-len(suffix):] == suffix
}

// Ensure WriteInstrumentation produces a real file on disk.
func TestWriteInstrumentation(t *testing.T) {
	dir := t.TempDir()
	s := &InstrumentationState{
		Console: []ConsoleEvent{{Level: "error", Message: "hi"}},
	}
	p, err := WriteInstrumentation(s, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file not created: %v", err)
	}
	expected := filepath.Join(dir, "instrumentation.json")
	if p != expected {
		t.Errorf("path = %s, want %s", p, expected)
	}
}
