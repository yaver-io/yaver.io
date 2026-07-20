package main

import (
	"context"
	"testing"
	"time"
)

func TestClassifyRelayPresence(t *testing.T) {
	cases := []struct {
		name    string
		samples []bool
		want    string
	}{
		{"all up is healthy", []bool{true, true, true, true}, "stable-up"},
		{"all down is a plain outage, not a flap", []bool{false, false, false}, "stable-down"},
		// The incident shape: reachable often enough to fool a spot check, but
		// cannot hold the path. This is the one the classifier exists to catch.
		{"mixed is flapping", []bool{true, false, true, false, true}, "flapping"},
		{"one down among ups still flaps", []bool{true, true, false, true}, "flapping"},
		{"no samples reads as down", nil, "stable-down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := classifyRelayPresence(tc.samples)
			if v.Class != tc.want {
				t.Fatalf("classifyRelayPresence(%v) class = %q, want %q (detail: %s)", tc.samples, v.Class, tc.want, v.Detail)
			}
		})
	}
}

func TestClassifyRelayPresence_CountsTransitions(t *testing.T) {
	v := classifyRelayPresence([]bool{true, false, true, false})
	if v.Transitions != 3 {
		t.Errorf("expected 3 up/down transitions, got %d", v.Transitions)
	}
	if v.Up != 2 || v.Down != 2 {
		t.Errorf("expected 2 up / 2 down, got %d/%d", v.Up, v.Down)
	}
}

func TestSampleRelayPresence_UsesProbeAndClassifies(t *testing.T) {
	// Probe alternates up/down → must classify as flapping. interval tiny so the
	// test is fast; ctx not cancelled.
	seq := []bool{true, false, true}
	i := 0
	probe := func() bool { v := seq[i%len(seq)]; i++; return v }
	v := sampleRelayPresence(context.Background(), 3, time.Millisecond, probe)
	if v.Class != "flapping" {
		t.Fatalf("alternating probe should classify flapping, got %q", v.Class)
	}
	if v.Samples != 3 {
		t.Errorf("expected 3 samples, got %d", v.Samples)
	}
}

func TestSampleRelayPresence_HonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	calls := 0
	probe := func() bool { calls++; return true }
	// count=5 but cancelled: the first sample is taken before any wait, then the
	// cancel short-circuits the rest.
	v := sampleRelayPresence(ctx, 5, time.Hour, probe)
	if calls != 1 {
		t.Errorf("cancelled sampler should take exactly the first sample, took %d", calls)
	}
	if v.Samples != 1 {
		t.Errorf("expected 1 sample after cancel, got %d", v.Samples)
	}
}
