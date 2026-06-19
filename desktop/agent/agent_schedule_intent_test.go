package main

import "testing"

func TestDetectSchedulingIntent(t *testing.T) {
	positives := []string{
		"every day summarize my open PRs",
		"check the deploy every 30 minutes",
		"keep an eye on the error rate",
		"remind me to renew the cert",
		"run this daily and post to slack",
		"monitor the site and alert me",
		"from now on, lint on each push",
		"give me a report every morning",
	}
	for _, p := range positives {
		if !detectSchedulingIntent(p) {
			t.Errorf("detectSchedulingIntent(%q) = false, want true", p)
		}
	}
	negatives := []string{
		"fix the bug in auth.go",
		"add a button to the settings page",
		"explain how the relay works",
		"refactor this function",
		"",
	}
	for _, p := range negatives {
		if detectSchedulingIntent(p) {
			t.Errorf("detectSchedulingIntent(%q) = true, want false", p)
		}
	}
}

func TestScheduleFromChoice(t *testing.T) {
	if st := scheduleFromChoice(schedChoiceNo, "do x", "claude", ""); st != nil {
		t.Errorf("'No' choice should yield nil schedule, got %+v", st)
	}
	if st := scheduleFromChoice("something weird", "do x", "claude", ""); st != nil {
		t.Errorf("unrecognized free text should yield nil, got %+v", st)
	}

	daily := scheduleFromChoice(schedChoiceDaily, "summarize PRs", "glm", "glm-4.7")
	if daily == nil || daily.Cron != "0 9 * * *" {
		t.Fatalf("daily choice = %+v, want cron 0 9 * * *", daily)
	}
	if daily.Runner != "glm" || daily.Model != "glm-4.7" {
		t.Errorf("daily schedule did not carry runner/model: %+v", daily)
	}
	if daily.MaxRuns != scheduleSelfRunawayCap {
		t.Errorf("recurring fallback schedule should be capped at %d, got %d", scheduleSelfRunawayCap, daily.MaxRuns)
	}

	hourly := scheduleFromChoice(schedChoiceHourly, "x", "codex", "")
	if hourly == nil || hourly.RepeatInterval != 60 {
		t.Fatalf("hourly choice = %+v, want repeat 60", hourly)
	}

	weekly := scheduleFromChoice(schedChoiceWeekly, "x", "claude", "")
	if weekly == nil || weekly.Cron != "0 9 * * 1" {
		t.Fatalf("weekly choice = %+v, want cron 0 9 * * 1", weekly)
	}

	// Lenient free-text "Other" answers map by keyword.
	if st := scheduleFromChoice("hourly please", "x", "claude", ""); st == nil || st.RepeatInterval != 60 {
		t.Errorf("free-text 'hourly' = %+v, want repeat 60", st)
	}
}
