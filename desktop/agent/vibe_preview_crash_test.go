package main

// vibe_preview_crash_test.go — Phase 4.5 tests. Verifies:
//   1. OnCrashDetected emits a "crash" SSE event to live subscribers.
//   2. The most-recent frame is tagged so the mobile UI can decorate it.
//   3. Identical crashes within 1 s are coalesced.
//   4. MatchVibeCrashLine identifies common framework signatures.

import (
	"context"
	"testing"
	"time"
)

func TestOnCrashDetected_emitsAndTags(t *testing.T) {
	fb := newFakeBrowser(genFrames(2)...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

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

	hash := mgr.OnCrashDetected(VibeCrashSignal{
		Project: "p",
		Source:  "flutter",
		Message: "Exception caught by widgets library",
	})
	if hash == "" {
		t.Fatal("OnCrashDetected should return tagged frame hash")
	}

	// Wait for crash event on the channel — non-blocking timeout of 500 ms.
	select {
	case ev := <-ch:
		if ev.Type != "crash" {
			t.Fatalf("expected type=crash, got %s", ev.Type)
		}
		if ev.Source != "flutter" {
			t.Fatalf("expected source=flutter, got %s", ev.Source)
		}
		if ev.Hash != hash {
			t.Fatalf("expected hash=%s, got %s", hash, ev.Hash)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no crash event arrived within 500 ms")
	}

	// The frame's crashTagged flag should now be set.
	rec := mgr.LatestFrame("p")
	if rec == nil || !rec.crashTagged {
		t.Fatalf("most-recent frame not tagged as crash: %+v", rec)
	}
}

func TestOnCrashDetected_dedupsBurst(t *testing.T) {
	fb := newFakeBrowser(genFrames(2)...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	ch, _, unsub := mgr.Subscribe("p")
	defer unsub()
	// Drain the "started" + initial "frame" replay events.
	drainLoop:
	for {
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		mgr.OnCrashDetected(VibeCrashSignal{
			Project:   "p",
			Source:    "flutter",
			Message:   "same exception",
			Timestamp: now.Add(time.Duration(i) * 100 * time.Millisecond),
		})
	}

	gotCrash := 0
	deadline := time.After(300 * time.Millisecond)
	collect:
	for {
		select {
		case ev := <-ch:
			if ev.Type == "crash" {
				gotCrash++
			}
		case <-deadline:
			break collect
		}
	}
	if gotCrash != 1 {
		t.Fatalf("expected exactly 1 crash event after dedup, got %d", gotCrash)
	}
}

func TestWaitForStability_quietWindowReturnsStable(t *testing.T) {
	fb := newFakeBrowser(genFrames(2)...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	res := mgr.WaitForStability(context.Background(), "p", 200*time.Millisecond)
	if !res.Stable {
		t.Fatalf("expected stable=true after quiet window, got %+v", res)
	}
	if res.Crash != nil {
		t.Fatalf("expected no crash on stable result, got %+v", res.Crash)
	}
}

func TestWaitForStability_crashShortcuts(t *testing.T) {
	fb := newFakeBrowser(genFrames(2)...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Fire a crash a few ms into the wait. The function should return
	// non-stable immediately, well before the 5 s window expires.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mgr.OnCrashDetected(VibeCrashSignal{
			Project: "p",
			Source:  "flutter",
			Message: "Exception caught by widgets library",
		})
	}()

	start := time.Now()
	res := mgr.WaitForStability(context.Background(), "p", 5*time.Second)
	elapsed := time.Since(start)

	if res.Stable {
		t.Fatalf("expected stable=false after a crash, got %+v", res)
	}
	if res.Crash == nil {
		t.Fatal("expected crash signal on non-stable result")
	}
	if res.Crash.Source != "flutter" {
		t.Errorf("crash.Source = %q, want flutter", res.Crash.Source)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected to short-circuit on crash, but waited %v (>2 s)", elapsed)
	}
}

func TestWaitForStability_noProjectIsTriviallyStable(t *testing.T) {
	mgr := NewVibePreviewManager(newFakeBrowser())
	res := mgr.WaitForStability(context.Background(), "", 100*time.Millisecond)
	if !res.Stable {
		t.Fatal("empty project should be trivially stable")
	}
}

func TestMatchVibeCrashLine(t *testing.T) {
	cases := []struct {
		text   string
		source string
	}{
		{"E AndroidRuntime: FATAL EXCEPTION: main\n  java.lang.NullPointerException", "android-fatal"},
		{"flutter: Exception caught by widgets library\nflutter: ══════", "flutter"},
		{"*** Terminating app due to uncaught exception 'NSInvalidArgumentException'", "ios-crash"},
		{"console.error: Unhandled JS Exception: TypeError: undefined is not a function", "rn-redbox"},
		{"normal log line nothing wrong here", ""},
	}
	for _, c := range cases {
		src, msg := MatchVibeCrashLine(c.text)
		if src != c.source {
			t.Errorf("for %q: want source=%q, got %q (msg=%q)", c.text, c.source, src, msg)
		}
		if c.source != "" && msg == "" {
			t.Errorf("for %q: matched %q but produced empty message", c.text, c.source)
		}
	}
}
