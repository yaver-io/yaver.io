package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The failure being prevented is not a dropped message but a CORRUPTED one:
// sendTmuxLine is three tmux calls with a 250ms beat in the middle, so a
// second sender's text can land between the first sender's text and its Enter.
// The result is one prompt fused from two people's words, submitted to an
// agent with shell access, with both senders told "sent".
//
// These tests model a target as a composer buffer: text appends, Enter
// submits and clears. Without serialization the submitted lines contain both
// senders' words; with it, every submitted line is exactly one sender's.

type fakeComposer struct {
	mu        sync.Mutex
	buf       strings.Builder
	submitted []string
}

func (c *fakeComposer) typeText(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.WriteString(s)
}

func (c *fakeComposer) enter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submitted = append(c.submitted, c.buf.String())
	c.buf.Reset()
}

func (c *fakeComposer) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.submitted...)
}

// sendLine mirrors sendTmuxLine's shape: type, beat, Enter — the beat is what
// opens the interleaving window.
func (c *fakeComposer) sendLine(text string) error {
	c.typeText(text)
	time.Sleep(2 * time.Millisecond) // stands in for tmuxSubmitDelay
	c.enter()
	return nil
}

func TestTmuxInputQueueKeepsConcurrentSendersFromFusingPrompts(t *testing.T) {
	comp := &fakeComposer{}
	senders := []string{
		"deploy the staging build",
		"no wait revert",
		"run the tests first",
		"ship it",
	}

	var wg sync.WaitGroup
	for _, msg := range senders {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			if err := submitTmuxInput("%pane-fuse", func() error { return comp.sendLine(m) }); err != nil {
				t.Errorf("submit %q: %v", m, err)
			}
		}(msg)
	}
	wg.Wait()

	got := comp.lines()
	if len(got) != len(senders) {
		t.Fatalf("expected %d submitted lines, got %d: %q", len(senders), len(got), got)
	}
	// Every submitted line must be exactly one sender's message — never a
	// concatenation, never empty.
	valid := map[string]bool{}
	for _, m := range senders {
		valid[m] = true
	}
	for _, line := range got {
		if !valid[line] {
			t.Fatalf("submitted line %q is not any single sender's message — words were fused across senders (all: %q)", line, got)
		}
	}
	// And each exactly once.
	seen := map[string]int{}
	for _, line := range got {
		seen[line]++
	}
	for _, m := range senders {
		if seen[m] != 1 {
			t.Fatalf("message %q submitted %d times, want exactly 1 (all: %q)", m, seen[m], got)
		}
	}
}

func TestTmuxInputQueuePreservesFIFOOrderPerTarget(t *testing.T) {
	// Two messages typed in order must arrive in order. A plain sync.Mutex
	// would give atomicity but Go guarantees no fairness, and out-of-order
	// delivery reads to a user as the agent losing the thread.
	comp := &fakeComposer{}
	const n = 12
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf("msg-%02d", i)
		if err := submitTmuxInput("%pane-fifo", func() error { return comp.sendLine(msg) }); err != nil {
			t.Fatalf("submit %s: %v", msg, err)
		}
	}
	got := comp.lines()
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("msg-%02d", i)
		if got[i] != want {
			t.Fatalf("position %d = %q, want %q (all: %q)", i, got[i], want, got)
		}
	}
}

func TestTmuxInputQueueIsPerTargetNotGlobal(t *testing.T) {
	// A slow pane must not stall every other session on the box. Two targets
	// each take ~60ms; serialized globally that is ~120ms, in parallel ~60ms.
	start := time.Now()
	var wg sync.WaitGroup
	for _, target := range []string{"%pane-par-a", "%pane-par-b"} {
		wg.Add(1)
		go func(tg string) {
			defer wg.Done()
			_ = submitTmuxInput(tg, func() error {
				time.Sleep(60 * time.Millisecond)
				return nil
			})
		}(target)
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 110*time.Millisecond {
		t.Fatalf("two targets took %v — they are sharing one queue instead of running in parallel", elapsed)
	}
}

func TestTmuxInputQueueRefusesUnkeyedWrites(t *testing.T) {
	// An empty target has no serialization domain. Running it unserialized is
	// exactly the corruption case, so it must refuse rather than proceed.
	err := submitTmuxInput("", func() error { return nil })
	if err == nil {
		t.Fatal("expected a refusal for an empty target")
	}
	if !strings.Contains(err.Error(), "serialize") {
		t.Fatalf("refusal should explain why, got %q", err)
	}
}

func TestTmuxInputQueueSurfacesTheCallersError(t *testing.T) {
	// The queue must be transparent: a caller's failure is its own, not a
	// generic queue error. SendTmuxInput's return is what tells a phone the
	// message landed.
	want := fmt.Errorf("tmux send-keys exploded")
	got := submitTmuxInput("%pane-err", func() error { return want })
	if got != want {
		t.Fatalf("error = %v, want the caller's own error %v", got, want)
	}
}

func TestTmuxInputQueueFailsLoudlyWhenFull(t *testing.T) {
	// A full queue must never silently drop: the sender would be told nothing
	// and the words would never arrive — the same class of bug as the
	// interleave.
	target := "%pane-full"
	release := make(chan struct{})
	var wg sync.WaitGroup

	// Occupy the worker so nothing drains.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = submitTmuxInput(target, func() error { <-release; return nil })
	}()
	time.Sleep(20 * time.Millisecond)

	// Fill the buffer.
	for i := 0; i < tmuxInputQueueDepth; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = submitTmuxInput(target, func() error { return nil })
		}()
	}
	time.Sleep(50 * time.Millisecond)

	err := submitTmuxInput(target, func() error { return nil })
	close(release)
	wg.Wait()

	if err == nil {
		t.Fatal("expected an explicit queue-full error, got nil — a silent drop")
	}
	if !strings.Contains(err.Error(), "full") {
		t.Fatalf("queue-full error should say so, got %q", err)
	}
}
