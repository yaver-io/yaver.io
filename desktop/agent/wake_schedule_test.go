package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Monday 2026-07-20 09:05:00 UTC — a fixed clock so weekday + window math is
// deterministic across hosts. Weekday(Monday)=1.
func wakeTestNow() time.Time { return time.Date(2026, 7, 20, 9, 5, 0, 0, time.UTC) }

func baseSchedule() WakeSchedule {
	return WakeSchedule{Machine: "dev", TimeOfDay: "09:00", TZ: "UTC", Enabled: true}
}

func TestParseWakeHHMM(t *testing.T) {
	for _, in := range []string{"09:00", "23:59", "0:5", "00:00"} {
		if _, _, err := parseWakeHHMM(in); err != nil {
			t.Errorf("parseWakeHHMM(%q) unexpected error: %v", in, err)
		}
	}
	for _, in := range []string{"", "9", "24:00", "09:60", "aa:bb", "12"} {
		if _, _, err := parseWakeHHMM(in); err == nil {
			t.Errorf("parseWakeHHMM(%q) expected error", in)
		}
	}
}

func TestWakeScheduleDueAt(t *testing.T) {
	now := wakeTestNow()
	win := wakeScheduleWindow

	cases := []struct {
		name string
		mut  func(s *WakeSchedule)
		when time.Time
		want bool
	}{
		{"due within window", nil, now, true},
		{"disabled", func(s *WakeSchedule) { s.Enabled = false }, now, false},
		{"before time", nil, time.Date(2026, 7, 20, 8, 55, 0, 0, time.UTC), false},
		{"past window", nil, time.Date(2026, 7, 20, 9, 20, 0, 0, time.UTC), false},
		{"already fired today", func(s *WakeSchedule) {
			s.LastFired = time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC).Unix()
		}, now, false},
		{"fired yesterday, due again", func(s *WakeSchedule) {
			s.LastFired = time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC).Unix()
		}, now, true},
		{"wrong weekday", func(s *WakeSchedule) { s.Days = []int{2} }, now, false}, // Tue only
		{"right weekday", func(s *WakeSchedule) { s.Days = []int{1} }, now, true},  // Mon
		{"invalid time string", func(s *WakeSchedule) { s.TimeOfDay = "nope" }, now, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := baseSchedule()
			if tc.mut != nil {
				tc.mut(&s)
			}
			if got := wakeScheduleDueAt(s, tc.when, win); got != tc.want {
				t.Errorf("wakeScheduleDueAt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWakeScheduleTimezone(t *testing.T) {
	// 09:00 Europe/Berlin (UTC+2 in July) == 07:00 UTC. At 07:05 UTC the
	// Berlin schedule should be due; a naive host-UTC reading would not fire.
	s := WakeSchedule{Machine: "dev", TimeOfDay: "09:00", TZ: "Europe/Berlin", Enabled: true}
	at := time.Date(2026, 7, 20, 7, 5, 0, 0, time.UTC)
	if !wakeScheduleDueAt(s, at, wakeScheduleWindow) {
		t.Fatal("expected Berlin 09:00 schedule to be due at 07:05 UTC")
	}
	if wakeScheduleDueAt(s, time.Date(2026, 7, 20, 9, 5, 0, 0, time.UTC), wakeScheduleWindow) {
		t.Fatal("Berlin 09:00 must NOT be due at 09:05 UTC (that is 11:05 Berlin)")
	}
}

func TestNextWakeAfter(t *testing.T) {
	now := wakeTestNow() // Mon 09:05 UTC, already past 09:00
	s := baseSchedule()
	next := nextWakeAfter(s, now)
	// Every-day schedule → next is tomorrow (Tue) 09:00.
	want := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("nextWakeAfter = %v, want %v", next, want)
	}
	// Weekday-only (Mon,Wed,Fri) at Mon 09:05 → next is Wed 09:00.
	s.Days = []int{1, 3, 5}
	if got := nextWakeAfter(s, now); !got.Equal(time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekday nextWakeAfter = %v, want Wed 09:00", got)
	}
}

func TestUpsertRemoveWakeSchedule(t *testing.T) {
	var scheds []WakeSchedule
	scheds = upsertWakeSchedule(scheds, WakeSchedule{Machine: "dev", TimeOfDay: "09:00", Enabled: true, LastFired: 123})
	scheds = upsertWakeSchedule(scheds, WakeSchedule{Machine: "ci", TimeOfDay: "07:00", Enabled: true})
	if len(scheds) != 2 {
		t.Fatalf("want 2 schedules, got %d", len(scheds))
	}
	// Re-upsert dev with a new time; LastFired must be preserved.
	scheds = upsertWakeSchedule(scheds, WakeSchedule{Machine: "DEV", TimeOfDay: "08:30", Enabled: true})
	if len(scheds) != 2 {
		t.Fatalf("upsert must replace, not append; got %d", len(scheds))
	}
	for _, s := range scheds {
		if s.Machine == "DEV" {
			if s.TimeOfDay != "08:30" {
				t.Errorf("time not updated: %s", s.TimeOfDay)
			}
			if s.LastFired != 123 {
				t.Errorf("LastFired must be preserved across edit, got %d", s.LastFired)
			}
		}
	}
	scheds, removed := removeWakeSchedule(scheds, "dev")
	if !removed || len(scheds) != 1 {
		t.Fatalf("remove failed: removed=%v len=%d", removed, len(scheds))
	}
	if _, r := removeWakeSchedule(scheds, "nope"); r {
		t.Error("removing a nonexistent machine must report removed=false")
	}
}

// TestRunWakeSchedulerOnce_FiresDueAndStamps is the closed-loop scheduler test:
// two schedules persisted to a temp HOME, only the due one fires via an injected
// invoker, LastFired is stamped + persisted, and an immediate re-tick does NOT
// re-fire the same slot (idempotency within the day).
func TestRunWakeSchedulerOnce_FiresDueAndStamps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := wakeTestNow()

	if err := saveWakeSchedules([]WakeSchedule{
		{Machine: "dev", TimeOfDay: "09:00", TZ: "UTC", Enabled: true}, // due (09:05, within window)
		{Machine: "ci", TimeOfDay: "23:00", TZ: "UTC", Enabled: true},  // not due
	}); err != nil {
		t.Fatal(err)
	}

	var woke []string
	invoke := func(m string) error { woke = append(woke, m); return nil }

	fired, err := runWakeSchedulerOnce(now, wakeScheduleWindow, invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 || fired[0] != "dev" {
		t.Fatalf("expected only 'dev' to fire, got %v", fired)
	}
	if len(woke) != 1 || woke[0] != "dev" {
		t.Fatalf("invoker should have been called for 'dev' only, got %v", woke)
	}

	// LastFired must be persisted so a restart doesn't re-fire.
	got, _ := loadWakeSchedules()
	for _, s := range got {
		if s.Machine == "dev" && s.LastFired == 0 {
			t.Error("dev LastFired not persisted")
		}
		if s.Machine == "ci" && s.LastFired != 0 {
			t.Error("ci must not have fired")
		}
	}

	// Immediate second tick at the same instant must not re-fire.
	fired2, _ := runWakeSchedulerOnce(now, wakeScheduleWindow, invoke)
	if len(fired2) != 0 {
		t.Fatalf("second tick re-fired the same slot: %v", fired2)
	}
}

// TestRunWakeSchedulerOnce_FailedInvokeRetries ensures a wake that errors does
// NOT stamp LastFired, so the next tick within the window retries it.
func TestRunWakeSchedulerOnce_FailedInvokeRetries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := wakeTestNow()
	if err := saveWakeSchedules([]WakeSchedule{{Machine: "dev", TimeOfDay: "09:00", TZ: "UTC", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	failing := func(string) error { return errWakeTest }
	fired, _ := runWakeSchedulerOnce(now, wakeScheduleWindow, failing)
	if len(fired) != 0 {
		t.Fatalf("failed invoke must not report fired, got %v", fired)
	}
	got, _ := loadWakeSchedules()
	if got[0].LastFired != 0 {
		t.Error("failed wake must not stamp LastFired (so it retries)")
	}
	// A later successful tick, still in window, fires.
	ok := func(string) error { return nil }
	fired2, _ := runWakeSchedulerOnce(time.Date(2026, 7, 20, 9, 10, 0, 0, time.UTC), wakeScheduleWindow, ok)
	if len(fired2) != 1 {
		t.Fatalf("retry within window should fire, got %v", fired2)
	}
}

var errWakeTest = &wakeTestErr{}

type wakeTestErr struct{}

func (*wakeTestErr) Error() string { return "wake test failure" }

// TestScheduledWakeRecreatesBox_ClosedLoop is the end-to-end proof that a due
// schedule actually recreates a Hetzner box: the injected invoker calls the
// same hetznerStartServer that machine_wake ultimately drives, pointed at the
// fake Hetzner API. Asserts the server was created (recreated-from-snapshot).
func TestScheduledWakeRecreatesBox_ClosedLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFakeHetzner(t)
	withFakeHetzner(t, f)
	m, err := NewCloudDeployManager(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveWakeSchedules([]WakeSchedule{{Machine: "dev", TimeOfDay: "09:00", TZ: "UTC", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	invoke := func(machine string) error {
		// Mirror machine_wake's terminal step: recreate from a snapshot id.
		_, _, e := m.hetznerStartServer("tok", machine, "starter", "eu", "1")
		return e
	}
	fired, err := runWakeSchedulerOnce(wakeTestNow(), wakeScheduleWindow, invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("schedule should have fired, got %v", fired)
	}
	f.mu.Lock()
	created := f.created
	f.mu.Unlock()
	if !created {
		t.Fatal("scheduled wake did not recreate the box (no Hetzner create call)")
	}
}

func TestWakeScheduleVerbsRegistered(t *testing.T) {
	for _, v := range []string{"machine_wake_schedule_set", "machine_wake_schedule_list", "machine_wake_schedule_clear"} {
		if _, ok := lookupOpsVerb(v); !ok {
			t.Errorf("verb %q not registered", v)
		}
	}
}

// TestWakeScheduleVerbRoundTrip drives the set → list → clear handlers with a
// temp HOME and asserts the persisted state each step.
func TestWakeScheduleVerbRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	set := opsWakeScheduleSetHandler(OpsContext{}, json.RawMessage(`{"machine":"dev","time":"08:45","days":[1,2,3,4,5],"tz":"UTC"}`))
	if !set.OK {
		t.Fatalf("set failed: %s", set.Error)
	}

	list := opsWakeScheduleListHandler(OpsContext{}, nil)
	if !list.OK {
		t.Fatalf("list failed: %s", list.Error)
	}
	listInit, _ := list.Initial.(map[string]interface{})
	if cnt, _ := listInit["count"].(int); cnt != 1 {
		t.Fatalf("expected 1 schedule, got %v", listInit["count"])
	}

	// Bad timezone must be rejected before touching disk.
	bad := opsWakeScheduleSetHandler(OpsContext{}, json.RawMessage(`{"machine":"x","time":"08:00","tz":"Nowhere/Nope"}`))
	if bad.OK {
		t.Fatal("expected bad timezone to be rejected")
	}
	// Bad day out of range rejected.
	badDay := opsWakeScheduleSetHandler(OpsContext{}, json.RawMessage(`{"machine":"x","time":"08:00","days":[9]}`))
	if badDay.OK {
		t.Fatal("expected out-of-range day to be rejected")
	}

	clr := opsWakeScheduleClearHandler(OpsContext{}, json.RawMessage(`{"machine":"dev"}`))
	if !clr.OK {
		t.Fatalf("clear failed: %s", clr.Error)
	}
	clrInit, _ := clr.Initial.(map[string]interface{})
	if removed, _ := clrInit["removed"].(bool); !removed {
		t.Error("clear should report removed=true")
	}
	after, _ := loadWakeSchedules()
	if len(after) != 0 {
		t.Fatalf("schedule not cleared, %d remain", len(after))
	}
}
