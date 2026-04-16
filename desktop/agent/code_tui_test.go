package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatNodeLineRunningUsesCyanSpinner(t *testing.T) {
	node := &AgentGraphNodeState{
		Status: AgentNodeRunning,
		Spec:   AgentGraphNodeSpec{ID: "plan", Title: "Plan Slice"},
		Placement: &AgentNodePlacement{
			DeviceName: "local",
			Runner:     "claude-code",
		},
	}
	line := formatNodeLine(node, "⠋")
	if !strings.Contains(line, "⠋") {
		t.Fatalf("expected spinner glyph, got %q", line)
	}
	if !strings.Contains(line, "[running]") {
		t.Fatalf("expected running chip, got %q", line)
	}
	if !strings.Contains(line, "local / claude-code") {
		t.Fatalf("expected placement, got %q", line)
	}
	if !strings.Contains(line, "plan") {
		t.Fatalf("expected node id in output, got %q", line)
	}
}

func TestFormatNodeLineCompletedAndFailed(t *testing.T) {
	done := formatNodeLine(&AgentGraphNodeState{
		Status: AgentNodeCompleted,
		Spec:   AgentGraphNodeSpec{ID: "implement"},
	}, "⠋")
	if !strings.Contains(done, "[done]") || !strings.Contains(done, "✓") {
		t.Fatalf("completed chip missing: %q", done)
	}
	failed := formatNodeLine(&AgentGraphNodeState{
		Status: AgentNodeFailed,
		Spec:   AgentGraphNodeSpec{ID: "verify"},
	}, "⠋")
	if !strings.Contains(failed, "[failed]") || !strings.Contains(failed, "✗") {
		t.Fatalf("failed chip missing: %q", failed)
	}
}

func TestClipTruncatesWithEllipsis(t *testing.T) {
	if got := clip("hello", 10); got != "hello" {
		t.Fatalf("short text preserved: got %q", got)
	}
	if got := clip("hello world", 6); got != "hello…" {
		t.Fatalf("long text truncated: got %q", got)
	}
}

func TestHumanElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond: "500ms",
		2 * time.Second:        "2s",
		59 * time.Second:       "59s",
		1 * time.Minute:        "1m00s",
		2*time.Minute + 5*time.Second: "2m05s",
	}
	for d, want := range cases {
		if got := humanElapsed(d); got != want {
			t.Errorf("humanElapsed(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestTailBufferRingCapacity(t *testing.T) {
	b := newTailBuffer(3)
	for i, line := range []string{"a", "b", "c", "d"} {
		label := "n"
		if i == 3 {
			label = "m"
		}
		b.Push(label, line)
	}
	got := b.Lines()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0] != "b" || got[1] != "c" || got[2] != "d" {
		t.Fatalf("got = %v, want [b c d]", got)
	}
	if b.LastLabel() != "m" {
		t.Fatalf("LastLabel = %q, want m", b.LastLabel())
	}
}

func TestSplitLinesCleanDropsBlank(t *testing.T) {
	in := "hello\r\n\n  world  \n"
	out := splitLinesClean(in)
	if len(out) != 2 || out[0] != "hello" || out[1] != "  world" {
		t.Fatalf("unexpected split: %v", out)
	}
}
