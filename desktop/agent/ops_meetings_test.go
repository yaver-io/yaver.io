package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func resetMeetingStateForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	meetMu.Lock()
	eventTypes = nil
	bookings = nil
	meetMu.Unlock()
}

func TestMeetingOpsRegistered(t *testing.T) {
	want := map[string]bool{
		"meeting_next":      false,
		"meeting_join_next": false,
		"meeting_open_url":  false,
	}
	for _, v := range listOpsVerbs() {
		if _, ok := want[v.Name]; ok {
			want[v.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing ops verb %s", name)
		}
	}
}

func TestMeetingNextUsesLocalBookings(t *testing.T) {
	resetMeetingStateForTest(t)
	now := time.Now().UTC()
	bookings = []Booking{
		{
			ID:        "later",
			EventSlug: "later-sync",
			Name:      "Later",
			Email:     "later@example.com",
			StartsAt:  now.Add(3 * time.Hour),
			EndsAt:    now.Add(4 * time.Hour),
			JoinURL:   "https://meet.google.com/abc-defg-hij",
			Provider:  "google",
		},
		{
			ID:        "next",
			EventSlug: "standup",
			Name:      "Team",
			Email:     "team@example.com",
			StartsAt:  now.Add(30 * time.Minute),
			EndsAt:    now.Add(60 * time.Minute),
			JoinURL:   "https://teams.microsoft.com/l/meetup-join/x",
			Provider:  "o365",
		},
	}

	payload, _ := json.Marshal(meetingNextPayload{Provider: "auto", WithinHours: 24})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "meeting_next",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("meeting_next failed: %#v", res)
	}
	plan, ok := res.Initial.(MeetingJoinPlan)
	if !ok {
		t.Fatalf("initial type = %T, want MeetingJoinPlan", res.Initial)
	}
	if plan.MeetingID != "next" || plan.Provider != "teams" || plan.OpenURL == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestMeetingJoinNextNoOpenReturnsCarPlan(t *testing.T) {
	resetMeetingStateForTest(t)
	now := time.Now().UTC()
	bookings = []Booking{{
		ID:        "zoom1",
		EventSlug: "customer-call",
		StartsAt:  now.Add(10 * time.Minute),
		EndsAt:    now.Add(40 * time.Minute),
		JoinURL:   "https://example.zoom.us/j/123456789",
		Provider:  "zoom",
	}}

	payload, _ := json.Marshal(meetingJoinPayload{Provider: "zoom", Surface: "android-auto", Open: false})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "meeting_join_next",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("meeting_join_next failed: %#v", res)
	}
	plan := res.Initial.(MeetingJoinPlan)
	if plan.Provider != "zoom" || plan.Surface != "car" || plan.Opened {
		t.Fatalf("unexpected join plan: %#v", plan)
	}
}

func TestMeetingOpenURLClassifiesWithoutOpening(t *testing.T) {
	payload, _ := json.Marshal(meetingOpenPayload{URL: "https://teams.microsoft.com/l/meetup-join/abc", Open: false, Surface: "carplay"})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "meeting_open_url",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("meeting_open_url failed: %#v", res)
	}
	plan := res.Initial.(MeetingJoinPlan)
	if plan.Provider != "teams" || plan.Surface != "car" || plan.OpenStrategy == "" {
		t.Fatalf("unexpected open plan: %#v", plan)
	}
}

func TestMeetingOpenURLRejectsUnsafeScheme(t *testing.T) {
	payload, _ := json.Marshal(meetingOpenPayload{URL: "javascript:alert(1)"})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "meeting_open_url",
		Payload: payload,
	})
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %#v", res)
	}
}

func TestMeetingOpenModeAliasesBrowserAutomation(t *testing.T) {
	for _, mode := range []string{"browser", "automation", "selenium", "chromedp", "remote-browser"} {
		if got := normalizeMeetingOpenMode(mode); got != "browser" {
			t.Fatalf("normalizeMeetingOpenMode(%q) = %q, want browser", mode, got)
		}
	}
	if got := normalizeMeetingOpenMode(""); got != "system" {
		t.Fatalf("default open mode = %q, want system", got)
	}
}
