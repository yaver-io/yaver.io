package main

import (
	"testing"
	"time"
)

func TestMatchCronPart_StepsAndRanges(t *testing.T) {
	cases := []struct {
		part   string
		value  int
		lo, hi int
		want   bool
	}{
		{"*", 0, 0, 59, true},
		{"*", 37, 0, 59, true},
		{"5", 5, 0, 59, true},
		{"5", 6, 0, 59, false},
		{"1-5", 3, 0, 59, true},
		{"1-5", 6, 0, 59, false},
		{"*/15", 0, 0, 59, true},
		{"*/15", 15, 0, 59, true},
		{"*/15", 30, 0, 59, true},
		{"*/15", 45, 0, 59, true},
		{"*/15", 7, 0, 59, false},
		{"*/30", 30, 0, 59, true},
		{"*/30", 31, 0, 59, false},
		{"10-30/10", 10, 0, 59, true},
		{"10-30/10", 20, 0, 59, true},
		{"10-30/10", 25, 0, 59, false},
		{"10-30/10", 40, 0, 59, false},
		{"5/10", 5, 0, 59, true},
		{"5/10", 15, 0, 59, true},
		{"5/10", 6, 0, 59, false},
		{"bad", 1, 0, 59, false},
		{"*/0", 1, 0, 59, false}, // zero step is invalid
	}
	for _, c := range cases {
		if got := matchCronPart(c.part, c.value, c.lo, c.hi); got != c.want {
			t.Errorf("matchCronPart(%q, %d, %d, %d) = %v, want %v", c.part, c.value, c.lo, c.hi, got, c.want)
		}
	}
}

func TestExpandCronMacro(t *testing.T) {
	cases := map[string]string{
		"@daily":    "0 0 * * *",
		"@midnight": "0 0 * * *",
		"@hourly":   "0 * * * *",
		"@weekly":   "0 0 * * 0",
		"@monthly":  "0 0 1 * *",
		"@yearly":   "0 0 1 1 *",
		"@annually": "0 0 1 1 *",
		"0 9 * * *": "0 9 * * *", // passthrough
		"@bogus":    "@bogus",    // passthrough
	}
	for in, want := range cases {
		if got := expandCronMacro(in); got != want {
			t.Errorf("expandCronMacro(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextCronRun_EveryThirtyMinutes(t *testing.T) {
	// The case the old hand-rolled parser silently failed: */30 never matched.
	next := nextCronRun("*/30 * * * *")
	if next.IsZero() {
		t.Fatal("nextCronRun(*/30 * * * *) returned zero — step syntax not parsed")
	}
	if m := next.Minute(); m != 0 && m != 30 {
		t.Fatalf("next minute = %d, want 0 or 30", m)
	}
	if next.Before(time.Now()) {
		t.Fatalf("next run %v is in the past", next)
	}
}

func TestNextCronRun_DailyMacro(t *testing.T) {
	next := nextCronRun("@daily")
	if next.IsZero() {
		t.Fatal("nextCronRun(@daily) returned zero")
	}
	if next.Minute() != 0 || next.Hour() != 0 {
		t.Fatalf("@daily next = %02d:%02d, want 00:00", next.Hour(), next.Minute())
	}
}
