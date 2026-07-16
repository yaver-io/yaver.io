package main

import (
	"strings"
	"testing"
	"time"
)

// A suspend must produce a sleep stamped when the machine was LAST AWAKE, not
// when we noticed on resume — otherwise the timeline is inverted and the
// recorder misreports the very thing it exists to get right.
func TestSuspendGapStampsSleepAtLastAwake(t *testing.T) {
	r := testRecorder(t, "s1")
	last := time.Now().Add(-3 * time.Hour)
	now := time.Now()
	recordFlightSuspendGap(r, last, now, now.Sub(last))

	events, err := r.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected a sleep/wake pair, got %d: %+v", len(events), events)
	}
	if events[0].Kind != flightKindSleep || events[1].Kind != flightKindWake {
		t.Fatalf("expected sleep then wake, got %s then %s", events[0].Kind, events[1].Kind)
	}
	sleepAt, err := time.Parse(time.RFC3339, events[0].At)
	if err != nil {
		t.Fatalf("parse sleep stamp: %v", err)
	}
	// Must be back at `last`, not ~now.
	if drift := sleepAt.Sub(last.UTC()); drift > time.Second || drift < -time.Second {
		t.Errorf("sleep stamped %v from the last-awake moment; it must not be stamped at resume time", drift)
	}
}

// The gap is measured on the WALL clock, so a large NTP correction is
// indistinguishable from a sleep. The detail must say so rather than assert a
// suspend we cannot prove.
func TestSuspendGapDetailDoesNotOverclaim(t *testing.T) {
	r := testRecorder(t, "s1")
	last := time.Now().Add(-2 * time.Hour)
	now := time.Now()
	recordFlightSuspendGap(r, last, now, now.Sub(last))

	events, _ := r.read()
	wake := events[1].Detail
	if !strings.Contains(wake, "2h") {
		t.Errorf("wake must carry the gap size, got %q", wake)
	}
	if !strings.Contains(strings.ToLower(wake), "clock") {
		t.Errorf("wake must admit a clock correction is indistinguishable from a suspend, got %q", wake)
	}
}

// A clock stepped BACKWARDS is not a sleep; recording one would be fabrication.
func TestSuspendGapIgnoresBackwardsClock(t *testing.T) {
	r := testRecorder(t, "s1")
	now := time.Now()
	last := now.Add(2 * time.Hour) // "last awake" in the future = clock went back
	recordFlightSuspendGap(r, last, now, now.Sub(last))

	events, _ := r.read()
	if len(events) != 0 {
		t.Errorf("a backwards clock must record nothing, got %+v", events)
	}
}

// The threshold must sit well above the tick, or a merely-loaded box that misses
// a tick gets reported as having slept.
func TestSleepThresholdIsWellAboveTick(t *testing.T) {
	if flightSleepThreshold <= flightSleepTick*2 {
		t.Errorf("threshold %v is too close to tick %v — a slow tick would look like a suspend",
			flightSleepThreshold, flightSleepTick)
	}
}
