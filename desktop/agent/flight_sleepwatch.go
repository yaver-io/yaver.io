package main

// flight_sleepwatch.go — detect that the machine was suspended, and record it.
//
// WHY THIS IS NOT JUST "LISTEN FOR A SLEEP NOTIFICATION"
//
// macOS sleep is the single most likely reason a remote box goes silent, and
// until this file existed the recorder could not tell it apart from a power cut:
// both look identical from outside (the box simply stops answering). But a
// suspended agent gets no signal — the OS freezes the process without asking, so
// there is nothing to handle. The only honest way to learn about sleep from pure
// Go is to notice, on resume, that time passed while we were not running.
//
// THE CLOCK SUBTLETY THAT DECIDES THE IMPLEMENTATION
//
// Go's time.Sub uses the MONOTONIC reading when both operands carry one, and on
// Darwin the monotonic clock (mach_absolute_time) does NOT advance while the
// machine is asleep. So the obvious `time.Now().Sub(last)` reports ~0 across a
// three-hour sleep and detects nothing. Detection therefore compares WALL clocks
// via Round(0), which strips the monotonic reading.
//
// The cost of using the wall clock is that a large NTP correction looks like a
// sleep. We do not pretend otherwise: the threshold is wide enough that routine
// drift never trips it, and the recorded detail says "suspended or a large clock
// correction" rather than asserting a sleep we cannot prove. A wrong-but-stated
// cause is recoverable; a confidently wrong one is what cost six hours already.

import (
	"context"
	"fmt"
	"time"
)

const (
	// flightSleepTick is how often we check. Frequent enough that `sleep` is
	// stamped close to when the machine actually suspended, cheap enough to be
	// irrelevant (one clock read).
	flightSleepTick = 30 * time.Second
	// flightSleepThreshold is the wall-clock gap that means "we were not
	// running". Generous relative to the tick: a loaded box can miss a tick by
	// seconds, and NTP steps are typically well under a minute. Only a real
	// suspend (or a gross clock correction) crosses this.
	flightSleepThreshold = 3 * time.Minute
)

// watchFlightSleepWake records suspend/resume for as long as ctx lives.
//
// It emits a PAIR per gap:
//   - `sleep` stamped at the last moment we know the machine was awake, which is
//     the most precise honest claim available — we cannot observe the suspend
//     itself.
//   - `wake` stamped now, carrying the gap.
//
// Cheap by construction: one clock read per tick, and it records only when a gap
// is found, so a box that never sleeps never writes an event.
func watchFlightSleepWake(ctx context.Context) {
	r := getFlightRecorder()
	if r == nil {
		return
	}
	// Wall clock deliberately (see the file comment): the monotonic reading
	// pauses across sleep on Darwin and would make every gap invisible.
	last := time.Now().Round(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(flightSleepTick):
		}
		now := time.Now().Round(0)
		gap := now.Sub(last)
		if gap >= flightSleepThreshold {
			recordFlightSuspendGap(r, last, now, gap)
		}
		last = now
	}
}

// recordFlightSuspendGap writes the sleep/wake pair for one observed gap.
//
// Split out from the loop so the decision (what a gap means, and how honestly to
// describe it) is testable without waiting on a ticker.
func recordFlightSuspendGap(r *flightRecorder, last, now time.Time, gap time.Duration) {
	// A backwards gap means the clock was stepped back; that is not a sleep and
	// claiming one would be a fabrication.
	if gap <= 0 {
		return
	}
	_ = r.recordAt(flightKindSleep, "last moment the machine was observed awake", last)
	_ = r.recordAt(flightKindWake, fmt.Sprintf(
		"resumed after a %s gap — the machine was suspended, or its clock was corrected by that much",
		roundFlightAge(gap),
	), now)
}
