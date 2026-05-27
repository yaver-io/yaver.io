package main

// Tests for voice-launch intent detection + resolution.

import (
	"context"
	"strings"
	"testing"
)

func TestLaunchIntentMatch_Verbs(t *testing.T) {
	cases := []struct {
		in    string
		verb  string
		slug  string
		match bool
	}{
		{"launch sfmg", "launch", "sfmg", true},
		{"open carrotbet", "open", "carrotbet", true},
		{"start talos", "start", "talos", true},
		{"fire up yaver", "fire up", "yaver", true},
		{"run my-app", "run", "my-app", true},
		{"please launch sfmg on my phone", "launch", "sfmg", true},
		{"hey yaver, open the carrotbet app", "open", "the", true},
		// no launch verb at all → no match
		{"add a logout button", "", "", false},
		// "launch" not followed by a slug
		{"launch", "", "", false},
		{"please launch.", "", "", false},
	}
	for _, c := range cases {
		got := LaunchIntentMatch(c.in)
		if c.match {
			if got == nil {
				t.Errorf("%q → nil, want verb=%q slug=%q", c.in, c.verb, c.slug)
				continue
			}
			if got.Verb != c.verb || got.Slug != c.slug {
				t.Errorf("%q → verb=%q slug=%q, want verb=%q slug=%q", c.in, got.Verb, got.Slug, c.verb, c.slug)
			}
		} else if got != nil {
			t.Errorf("%q → %+v, want nil", c.in, got)
		}
	}
}

func TestHandleVoiceLaunch_NoConfig(t *testing.T) {
	res := HandleVoiceLaunch(context.Background(), &VoiceLaunchIntent{Slug: "sfmg"}, &Config{}, nil)
	if res.OK {
		t.Fatalf("expected OK=false without LaunchProjects, got: %+v", res)
	}
	if res.SpokenResponse == "" {
		t.Error("expected spoken response on misconfig")
	}
}

func TestHandleVoiceLaunch_UnknownSlug(t *testing.T) {
	cfg := &Config{Voice: &VoiceConfig{LaunchProjects: map[string]string{"talos": "/tmp/talos"}}}
	res := HandleVoiceLaunch(context.Background(), &VoiceLaunchIntent{Slug: "sfmg"}, cfg, nil)
	if res.OK {
		t.Error("expected OK=false on unknown slug")
	}
	if !strings.Contains(res.SpokenResponse, "talos") {
		t.Errorf("response should list known slugs, got: %q", res.SpokenResponse)
	}
}

func TestHandleVoiceLaunch_FuzzyMatch(t *testing.T) {
	cfg := &Config{Voice: &VoiceConfig{LaunchProjects: map[string]string{"sfmg-mobile": "/tmp/sfmg"}}}
	// LaunchIntent says "sfmg", config has "sfmg-mobile" — fuzzy match
	// should resolve. The actual smoke test will fail (no bundle at
	// /tmp/sfmg) but we should at least see we resolved the workDir.
	res := HandleVoiceLaunch(context.Background(), &VoiceLaunchIntent{Slug: "sfmg"}, cfg, nil)
	if res.WorkDir != "/tmp/sfmg" {
		t.Errorf("fuzzy match failed: WorkDir=%q want /tmp/sfmg", res.WorkDir)
	}
	// OK=false expected because the bundle doesn't exist
	if res.OK {
		t.Error("smoke test should have failed with no bundle present")
	}
}

func TestFuzzyMatchLaunchProject(t *testing.T) {
	projects := map[string]string{
		"sfmg":        "/a",
		"sfmg-mobile": "/b",
		"talos":       "/c",
	}
	if got := fuzzyMatchLaunchProject("sfmg", projects); got != "/a" {
		t.Errorf("exact match: got %q want /a", got)
	}
	if got := fuzzyMatchLaunchProject("talo", projects); got != "/c" {
		t.Errorf("prefix-of-key: got %q want /c", got)
	}
	if got := fuzzyMatchLaunchProject("nothing", projects); got != "" {
		t.Errorf("no match should return empty, got %q", got)
	}
}

