package main

import "testing"

// A Mac mini was found with 6,023 failed tasks out of 6,035. Five schedules
// pointed at loops whose working directory lived in /tmp (which macOS purges),
// so every fire failed instantly and was retried ~60s later — for two months.
// The box looked broken over the relay; the relay was fine. These pin the
// breaker that makes that impossible.

func TestSchedulePausesAfterConsecutiveFailures(t *testing.T) {
	s := &Scheduler{}
	st := &ScheduledTask{ID: "sched-x", Title: "yaver-loop:sfmg-autofix", Status: "scheduled", NextRunAt: "later"}

	for i := 1; i < maxConsecutiveFailures; i++ {
		s.noteRunOutcomeLocked(st, TaskStatusFailed)
		if st.Status == "paused" {
			t.Fatalf("paused early, after %d failures (limit %d)", i, maxConsecutiveFailures)
		}
	}
	// The one that trips it.
	s.noteRunOutcomeLocked(st, TaskStatusFailed)

	if st.Status != "paused" {
		t.Fatalf("status = %q after %d consecutive failures, want paused — this is the 6000-failure crash loop",
			st.Status, maxConsecutiveFailures)
	}
	if st.NextRunAt != "" {
		t.Errorf("NextRunAt = %q, want empty — a paused schedule must not be scheduled to fire again", st.NextRunAt)
	}
	if st.PausedReason == "" {
		t.Error("PausedReason empty — an auto-paused schedule must explain itself, or it is just a mystery")
	}
}

// One success must clear the streak. A flaky schedule (fails 9, succeeds, fails 9)
// must never be paused — the breaker is for permanently broken schedules only.
func TestSuccessResetsTheFailureStreak(t *testing.T) {
	s := &Scheduler{}
	st := &ScheduledTask{ID: "sched-y", Status: "scheduled"}

	for i := 0; i < maxConsecutiveFailures-1; i++ {
		s.noteRunOutcomeLocked(st, TaskStatusFailed)
	}
	s.noteRunOutcomeLocked(st, TaskStatusFinished)
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d after a success, want 0", st.ConsecutiveFailures)
	}
	for i := 0; i < maxConsecutiveFailures-1; i++ {
		s.noteRunOutcomeLocked(st, TaskStatusFailed)
	}
	if st.Status == "paused" {
		t.Error("paused a flaky-but-working schedule — the streak must reset on every success")
	}
}

// A human stopping a task is a decision, not a fault. It must not count toward
// the breaker, or cancelling your own runs would eventually disable the schedule.
func TestStoppedRunsDoNotTripTheBreaker(t *testing.T) {
	s := &Scheduler{}
	st := &ScheduledTask{ID: "sched-z", Status: "scheduled"}
	for i := 0; i < maxConsecutiveFailures*2; i++ {
		s.noteRunOutcomeLocked(st, TaskStatusStopped)
	}
	if st.Status == "paused" {
		t.Error("stopped runs tripped the breaker — a human cancelling a run is not a failure")
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d from stopped runs, want 0", st.ConsecutiveFailures)
	}
}

// The scheduler already skips paused schedules — verify the breaker's output is
// actually the state that stops it firing, not a cosmetic field.
func TestPausedIsTheStateCheckAndRunSkips(t *testing.T) {
	st := &ScheduledTask{ID: "sched-w", Status: "paused"}
	if st.Status != "paused" {
		t.Skip()
	}
	// checkAndRun's guard is `if st.Status == "paused" { continue }` (scheduler.go).
	// This asserts the breaker sets exactly that literal, so the two stay in sync.
	s := &Scheduler{}
	fresh := &ScheduledTask{ID: "sched-v", Status: "scheduled", NextRunAt: "later"}
	for i := 0; i < maxConsecutiveFailures; i++ {
		s.noteRunOutcomeLocked(fresh, TaskStatusFailed)
	}
	if fresh.Status != "paused" {
		t.Fatalf("breaker set status=%q; checkAndRun only skips the literal \"paused\"", fresh.Status)
	}
}
