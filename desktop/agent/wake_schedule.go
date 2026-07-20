package main

// wake_schedule.go — "time of wakeup": scheduled, time-of-day auto-wake for a
// parked (scaled-to-zero) managed/BYO box. This is the counterpart to the
// idle → scale-to-zero park loop (park_check.go / machine_activity.go): park
// spends nothing while you sleep, and a wake schedule brings the box back up
// on its own a few minutes before you start work, so the first turn of the day
// doesn't pay the ~1-2 min cold-recreate latency.
//
// WHERE THIS RUNS: on an always-on device (your primary daemon / the control
// plane), NEVER on the ephemeral box itself — a parked box is DELETED, so it
// cannot run its own alarm clock. The tick loads the schedules, decides which
// are due (pure wakeScheduleDueAt), and drives the existing machine_wake verb
// (which is idempotent: a box already active returns immediately, so a double
// fire is harmless).
//
// ISOLATED FILE on purpose (prefer-new-files): own init() registering the
// machine_wake_schedule_* verbs, own JSON state file, injectable tick so the
// closed-loop test drives a real wake against the fake Hetzner without a live
// box. Zero edits to machine_lifecycle.go's wake handler — it is reused as-is.
//
// Storage: ~/.yaver/wake-schedules.json (local; never Convex — it is per-box
// operator config, not identity/discovery data).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// wakeScheduleWindow is how long after the scheduled minute a wake may still
// fire. Two jobs: (1) tolerate a ticker that missed a minute or two, (2) stop a
// schedule created LATER in the day for an already-past time from firing
// immediately ("wake at 09:00" set at 15:00 must NOT wake the box now).
const wakeScheduleWindow = 15 * time.Minute

// WakeSchedule is one per-machine time-of-day auto-wake rule.
type WakeSchedule struct {
	Machine   string `json:"machine"`             // byoMachines row name (the machine_wake handle)
	TimeOfDay string `json:"timeOfDay"`           // "HH:MM", 24-hour, in TZ
	Days      []int  `json:"days,omitempty"`      // 0=Sun..6=Sat; empty = every day
	TZ        string `json:"tz,omitempty"`        // IANA location (e.g. "Europe/Berlin"); empty = host local
	Enabled   bool   `json:"enabled"`             // false = kept but dormant
	LastFired int64  `json:"lastFiredUnix,omitempty"` // unix seconds of last successful wake
}

var wakeScheduleMu sync.Mutex

func wakeSchedulesPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wake-schedules.json"), nil
}

// loadWakeSchedules reads the schedule list. A missing file is not an error —
// it means "no schedules yet".
func loadWakeSchedules() ([]WakeSchedule, error) {
	p, err := wakeSchedulesPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read wake schedules: %w", err)
	}
	var out []WakeSchedule
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse wake schedules: %w", err)
	}
	return out, nil
}

// saveWakeSchedules writes the list atomically (tmp + rename), matching the
// config.go durability posture — a torn schedule file would silently drop a
// user's alarm.
func saveWakeSchedules(scheds []WakeSchedule) error {
	p, err := wakeSchedulesPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(scheds, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	return nil
}

// parseWakeHHMM parses "HH:MM" 24-hour into hour, minute. Rejects out-of-range.
func parseWakeHHMM(s string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("time %q must be HH:MM (24-hour)", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("time %q: hour must be 0-23", s)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("time %q: minute must be 0-59", s)
	}
	return h, m, nil
}

// scheduleLocation resolves the schedule's timezone, falling back to the host's
// local zone when unset or unparseable (never fails the decision on a bad TZ).
func scheduleLocation(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.Local
}

// dayMatches reports whether now's weekday is allowed. Empty Days = every day.
func dayMatches(days []int, weekday time.Weekday) bool {
	if len(days) == 0 {
		return true
	}
	for _, d := range days {
		if d == int(weekday) {
			return true
		}
	}
	return false
}

// wakeScheduleDueAt is the pure decision: is this schedule due to fire at `now`?
// Due iff enabled, today is an allowed weekday, now is within
// [scheduledToday, scheduledToday+window), and it hasn't already fired since
// scheduledToday. All time math is done in the schedule's own timezone so a
// "09:00" alarm means 09:00 wherever the user is, not on the host.
func wakeScheduleDueAt(s WakeSchedule, now time.Time, window time.Duration) bool {
	if !s.Enabled {
		return false
	}
	h, m, err := parseWakeHHMM(s.TimeOfDay)
	if err != nil {
		return false
	}
	loc := scheduleLocation(s.TZ)
	nowLoc := now.In(loc)
	if !dayMatches(s.Days, nowLoc.Weekday()) {
		return false
	}
	scheduled := time.Date(nowLoc.Year(), nowLoc.Month(), nowLoc.Day(), h, m, 0, 0, loc)
	if nowLoc.Before(scheduled) {
		return false // not time yet today
	}
	if nowLoc.Sub(scheduled) >= window {
		return false // missed the catch-up window
	}
	if s.LastFired > 0 {
		last := time.Unix(s.LastFired, 0).In(loc)
		if !last.Before(scheduled) {
			return false // already fired for today's slot
		}
	}
	return true
}

// nextWakeAfter returns the next wall-clock time this schedule would fire at or
// after `now` (for display in the list verb). Zero time if disabled/invalid.
func nextWakeAfter(s WakeSchedule, now time.Time) time.Time {
	if !s.Enabled {
		return time.Time{}
	}
	h, m, err := parseWakeHHMM(s.TimeOfDay)
	if err != nil {
		return time.Time{}
	}
	loc := scheduleLocation(s.TZ)
	nowLoc := now.In(loc)
	// Scan up to 8 days forward to find the next allowed weekday at HH:MM.
	for i := 0; i < 8; i++ {
		day := nowLoc.AddDate(0, 0, i)
		if !dayMatches(s.Days, day.Weekday()) {
			continue
		}
		cand := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
		if !cand.Before(nowLoc) {
			return cand
		}
	}
	return time.Time{}
}

// upsertWakeSchedule replaces the schedule for a machine (by name,
// case-insensitive) or appends it. Pure list transform — no I/O — so it is unit
// tested directly.
func upsertWakeSchedule(scheds []WakeSchedule, s WakeSchedule) []WakeSchedule {
	name := strings.ToLower(strings.TrimSpace(s.Machine))
	for i := range scheds {
		if strings.ToLower(strings.TrimSpace(scheds[i].Machine)) == name {
			// Preserve LastFired across an edit so re-saving a schedule the same
			// day doesn't re-arm an already-fired slot.
			s.LastFired = scheds[i].LastFired
			scheds[i] = s
			return scheds
		}
	}
	return append(scheds, s)
}

// removeWakeSchedule drops the schedule for a machine; returns the new list and
// whether anything was removed.
func removeWakeSchedule(scheds []WakeSchedule, machine string) ([]WakeSchedule, bool) {
	name := strings.ToLower(strings.TrimSpace(machine))
	out := scheds[:0:0]
	removed := false
	for _, s := range scheds {
		if strings.ToLower(strings.TrimSpace(s.Machine)) == name {
			removed = true
			continue
		}
		out = append(out, s)
	}
	return out, removed
}

// wakeInvoker performs the actual wake of a machine by name. Injected so the
// tick is testable against the fake Hetzner (or a pure fake) without wiring the
// full ops stack. Returns an error if the wake failed.
type wakeInvoker func(machine string) error

// runWakeSchedulerOnce is the injectable tick: load schedules, fire the due
// ones via invoke, stamp LastFired on success, persist. Returns the machines
// woken this tick. A wake failure is reported but does NOT stamp LastFired, so
// the next tick retries within the window. Safe to call concurrently — guarded
// by wakeScheduleMu around the load/save.
func runWakeSchedulerOnce(now time.Time, window time.Duration, invoke wakeInvoker) (fired []string, err error) {
	wakeScheduleMu.Lock()
	defer wakeScheduleMu.Unlock()
	scheds, err := loadWakeSchedules()
	if err != nil {
		return nil, err
	}
	changed := false
	for i := range scheds {
		if !wakeScheduleDueAt(scheds[i], now, window) {
			continue
		}
		if invErr := invoke(scheds[i].Machine); invErr != nil {
			// Leave LastFired untouched → retried next tick while still in window.
			continue
		}
		scheds[i].LastFired = now.Unix()
		changed = true
		fired = append(fired, scheds[i].Machine)
	}
	if changed {
		if serr := saveWakeSchedules(scheds); serr != nil {
			return fired, serr
		}
	}
	return fired, nil
}

// invokeMachineWakeVerb is the production wakeInvoker — it drives the existing
// machine_wake ops handler with confirm=true. Kept tiny so the scheduler owns
// no Hetzner logic of its own.
func invokeMachineWakeVerb(machine string) error {
	payload, _ := json.Marshal(map[string]interface{}{"name": machine, "confirm": true})
	res := opsMachineWakeHandler(OpsContext{}, payload)
	if !res.OK {
		return fmt.Errorf("machine_wake %q failed: %s", machine, res.Error)
	}
	return nil
}

// startWakeScheduler runs the tick every minute on an always-on daemon. No-op
// when there are no schedules, so a normal desktop agent pays nothing. Guarded
// by a stop channel for clean shutdown / tests.
func startWakeScheduler(stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(1 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				scheds, _ := loadWakeSchedules()
				if len(scheds) == 0 {
					continue
				}
				if woke, err := runWakeSchedulerOnce(time.Now(), wakeScheduleWindow, invokeMachineWakeVerb); err == nil && len(woke) > 0 {
					logInfo("wake-schedule", "woke %s on schedule", strings.Join(woke, ", "))
				}
			}
		}
	}()
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_wake_schedule_set",
		Description: "Set a time-of-day auto-wake for a parked box: bring machine <machine> back up at <time> (HH:MM, 24h) on the given days. Runs on your always-on daemon, never the box. Idempotent per machine (re-setting replaces). Pair with idle auto-park (machine_park_check) so the box sleeps overnight and is warm by the time you start. Example: {machine:'dev', time:'08:45', days:[1,2,3,4,5], tz:'Europe/Berlin'}.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"machine", "time"},
			"properties": map[string]interface{}{
				"machine": map[string]interface{}{"type": "string", "description": "Machine name/alias (the machine_wake handle)"},
				"time":    map[string]interface{}{"type": "string", "description": "HH:MM 24-hour local-to-tz wake time"},
				"days":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "Weekdays 0=Sun..6=Sat; omit = every day"},
				"tz":      map[string]interface{}{"type": "string", "description": "IANA timezone (e.g. Europe/Berlin); omit = host local"},
				"enabled": map[string]interface{}{"type": "boolean", "description": "Default true; false keeps the rule but dormant"},
			},
			"additionalProperties": false,
		},
		Handler:    opsWakeScheduleSetHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_wake_schedule_list",
		Description: "List all time-of-day auto-wake schedules and each one's next fire time. Read-only.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsWakeScheduleListHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_wake_schedule_clear",
		Description: "Remove the time-of-day auto-wake schedule for a machine. Requires machine. Returns whether a rule was removed.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"machine"},
			"properties": map[string]interface{}{
				"machine": map[string]interface{}{"type": "string", "description": "Machine name/alias"},
			},
			"additionalProperties": false,
		},
		Handler:    opsWakeScheduleClearHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsWakeScheduleSetHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Machine string `json:"machine"`
		Time    string `json:"time"`
		Days    []int  `json:"days"`
		TZ      string `json:"tz"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	machine := strings.TrimSpace(p.Machine)
	if machine == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "machine required"}
	}
	if _, _, err := parseWakeHHMM(p.Time); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	for _, d := range p.Days {
		if d < 0 || d > 6 {
			return OpsResult{OK: false, Code: "bad_payload", Error: "days must be 0-6 (0=Sun..6=Sat)"}
		}
	}
	if tz := strings.TrimSpace(p.TZ); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("unknown timezone %q", tz)}
		}
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	sched := WakeSchedule{
		Machine:   machine,
		TimeOfDay: strings.TrimSpace(p.Time),
		Days:      p.Days,
		TZ:        strings.TrimSpace(p.TZ),
		Enabled:   enabled,
	}
	wakeScheduleMu.Lock()
	defer wakeScheduleMu.Unlock()
	scheds, err := loadWakeSchedules()
	if err != nil {
		return OpsResult{OK: false, Code: "load_failed", Error: err.Error()}
	}
	scheds = upsertWakeSchedule(scheds, sched)
	if err := saveWakeSchedules(scheds); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	next := nextWakeAfter(sched, time.Now())
	out := map[string]interface{}{
		"machine": machine, "time": sched.TimeOfDay, "enabled": enabled,
	}
	if !next.IsZero() {
		out["nextWake"] = next.Format(time.RFC3339)
	}
	return OpsResult{OK: true, Initial: out}
}

func opsWakeScheduleListHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	scheds, err := loadWakeSchedules()
	if err != nil {
		return OpsResult{OK: false, Code: "load_failed", Error: err.Error()}
	}
	sort.SliceStable(scheds, func(i, j int) bool {
		return strings.ToLower(scheds[i].Machine) < strings.ToLower(scheds[j].Machine)
	})
	now := time.Now()
	list := make([]map[string]interface{}, 0, len(scheds))
	for _, s := range scheds {
		item := map[string]interface{}{
			"machine": s.Machine, "time": s.TimeOfDay, "days": s.Days,
			"tz": s.TZ, "enabled": s.Enabled,
		}
		if next := nextWakeAfter(s, now); !next.IsZero() {
			item["nextWake"] = next.Format(time.RFC3339)
		}
		if s.LastFired > 0 {
			item["lastWoke"] = time.Unix(s.LastFired, 0).Format(time.RFC3339)
		}
		list = append(list, item)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"schedules": list, "count": len(list)}}
}

func opsWakeScheduleClearHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	machine := strings.TrimSpace(p.Machine)
	if machine == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "machine required"}
	}
	wakeScheduleMu.Lock()
	defer wakeScheduleMu.Unlock()
	scheds, err := loadWakeSchedules()
	if err != nil {
		return OpsResult{OK: false, Code: "load_failed", Error: err.Error()}
	}
	scheds, removed := removeWakeSchedule(scheds, machine)
	if err := saveWakeSchedules(scheds); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"machine": machine, "removed": removed}}
}
