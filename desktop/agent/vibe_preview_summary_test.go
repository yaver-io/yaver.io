package main

// vibe_preview_summary_test.go — Phase 4 tests. Uses an injectable stub
// summarizer instead of the real claude-CLI path so the suite stays
// hermetic + deterministic. The real LLM call is gated by env var in
// production (YAVER_VIBE_SUMMARIZER=claude) and never runs in CI.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// stubSummarizer captures every Summarize call + returns canned output.
type stubSummarizer struct {
	mu     sync.Mutex
	calls  int
	text   string
	source string
	err    error
}

func (s *stubSummarizer) Summarize(ctx context.Context, before, after *vibeFrameRecord, kickCtx string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return "", s.source, s.err
	}
	if s.text == "" {
		s.text = "stub-summary: nav changed"
	}
	if s.source == "" {
		s.source = "stub"
	}
	return s.text, s.source, nil
}

func TestQueueSummary_skipsWhenLessThanTwoFrames(t *testing.T) {
	stub := &stubSummarizer{}
	SetVibePreviewSummarizer(stub)
	defer SetVibePreviewSummarizer(nil)

	mgr := NewVibePreviewManager(newFakeBrowser(genFrames(1)...))
	defer mgr.StopAll()
	mgr.SetDiskRoot(t.TempDir())

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Only the initial capture has fired (1 frame). QueueSummary must
	// short-circuit and the stub must NOT be called.
	seq := mgr.QueueSummary(context.Background(), "p", "kick-1")
	if seq != 0 {
		t.Fatalf("expected seq=0 with <2 frames, got %d", seq)
	}
	stub.mu.Lock()
	calls := stub.calls
	stub.mu.Unlock()
	if calls != 0 {
		t.Fatalf("stub should not be called with <2 frames, got %d calls", calls)
	}
}

func TestQueueSummary_shortCircuitsOnIdenticalHashes(t *testing.T) {
	stub := &stubSummarizer{}
	SetVibePreviewSummarizer(stub)
	defer SetVibePreviewSummarizer(nil)

	// Use only ONE distinct frame so the initial capture + the next
	// snapshot hash identically — the stable-frame collapse path keeps
	// the ring at length 1, but QueueSummary itself short-circuits even
	// if both ring entries had the same hash.
	mgr := NewVibePreviewManager(newFakeBrowser([]byte("only")))
	defer mgr.StopAll()
	mgr.SetDiskRoot(t.TempDir())

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < 4; i++ {
		_, _ = mgr.Snapshot("p")
	}
	// Force a second distinct entry by directly inserting the hash twice.
	// Easier: just manually populate the ring with two identical records.
	mgr.mu.Lock()
	if len(mgr.ring["p"]) > 0 {
		// Duplicate the last frame so before.hash == after.hash.
		last := mgr.ring["p"][len(mgr.ring["p"])-1]
		mgr.ring["p"] = append(mgr.ring["p"], last)
	}
	mgr.mu.Unlock()

	seq := mgr.QueueSummary(context.Background(), "p", "kick-equal-hash")
	if seq == 0 {
		t.Fatal("expected non-zero seq when ring has >=2 frames")
	}
	stub.mu.Lock()
	calls := stub.calls
	stub.mu.Unlock()
	if calls != 0 {
		t.Fatalf("stub should not be invoked when hashes match, got %d calls", calls)
	}

	// And the on-disk log should still record the no-change result.
	summaries := mgr.ListSummaries("p", 10)
	if len(summaries) == 0 {
		t.Fatal("expected at least one summary record on disk")
	}
	if summaries[0].Text != "no visible change" {
		t.Errorf("expected 'no visible change' summary, got %q", summaries[0].Text)
	}
}

func TestQueueSummary_callsSummarizerOnDistinctFrames(t *testing.T) {
	stub := &stubSummarizer{text: "a card moved"}
	SetVibePreviewSummarizer(stub)
	defer SetVibePreviewSummarizer(nil)

	mgr := NewVibePreviewManager(newFakeBrowser(genFrames(3)...))
	defer mgr.StopAll()
	mgr.SetDiskRoot(t.TempDir())

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Two distinct snapshots so ring has 3 distinct hashes.
	if _, err := mgr.Snapshot("p"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := mgr.Snapshot("p"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	ch, _, unsub := mgr.Subscribe("p")
	defer unsub()
	// Drain the started + frame events.
	drainLoop:
	for {
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}

	seq := mgr.QueueSummary(context.Background(), "p", "kick-A")
	if seq == 0 {
		t.Fatal("QueueSummary returned 0 on a distinct-hash ring")
	}

	// Wait for the goroutine to finish + emit the summary event.
	got := false
	deadline := time.After(2 * time.Second)
	collect:
	for {
		select {
		case ev := <-ch:
			if ev.Type == "summary" && ev.Message == "a card moved" {
				got = true
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if !got {
		t.Fatal("expected summary event with stub text within 2 s")
	}

	stub.mu.Lock()
	calls := stub.calls
	stub.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 summarizer call, got %d", calls)
	}

	// Persisted log should contain the summary.
	summaries := mgr.ListSummaries("p", 10)
	if len(summaries) == 0 {
		t.Fatal("expected summary persisted to disk")
	}
	if summaries[0].Text != "a card moved" {
		t.Errorf("expected persisted text, got %q", summaries[0].Text)
	}
	if summaries[0].Source != "stub" {
		t.Errorf("expected source=stub, got %q", summaries[0].Source)
	}
}

func TestQueueSummary_summarizerErrorEmitsEvent(t *testing.T) {
	stub := &stubSummarizer{err: errors.New("network down")}
	SetVibePreviewSummarizer(stub)
	defer SetVibePreviewSummarizer(nil)

	mgr := NewVibePreviewManager(newFakeBrowser(genFrames(3)...))
	defer mgr.StopAll()
	mgr.SetDiskRoot(t.TempDir())

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := mgr.Snapshot("p"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	ch, _, unsub := mgr.Subscribe("p")
	defer unsub()
	drainLoop:
	for {
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}

	if seq := mgr.QueueSummary(context.Background(), "p", "kick-X"); seq == 0 {
		t.Fatal("expected non-zero seq")
	}
	gotFailureEvent := false
	deadline := time.After(2 * time.Second)
	collect:
	for {
		select {
		case ev := <-ch:
			if ev.Type == "summary" && ev.Message != "" && ev.Message[:7] == "summary" {
				gotFailureEvent = true
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if !gotFailureEvent {
		t.Fatal("expected summary failure event within 2 s")
	}
}

func TestNoopSummarizer_neverErrors(t *testing.T) {
	s := noopSummarizer{}
	before := &vibeFrameRecord{Hash: "a", Width: 1280, Height: 720}
	after := &vibeFrameRecord{Hash: "b", Width: 1280, Height: 720}
	text, src, err := s.Summarize(context.Background(), before, after, "")
	if err != nil {
		t.Fatalf("noop summarizer should never error on valid frames, got %v", err)
	}
	if src != "noop" {
		t.Errorf("source should be 'noop', got %q", src)
	}
	if text == "" {
		t.Error("expected non-empty placeholder text")
	}
}
