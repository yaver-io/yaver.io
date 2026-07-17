package main

import (
	"strings"
	"testing"
)

func TestResolveShipPromptNamesAndAdHoc(t *testing.T) {
	if got := resolveShipPrompt("toparla"); got != shipPromptToparla {
		t.Fatal("toparla must resolve from the library")
	}
	if got := resolveShipPrompt("TOPARLA"); got != shipPromptToparla {
		t.Fatal("library lookup must be case-insensitive; this is driven by voice")
	}
	if got := resolveShipPrompt("devam"); got != shipPromptDevam {
		t.Fatal("devam must resolve from the library")
	}
	// An unknown name is an ad-hoc prompt, not an error — the surface this is
	// driven from is often a spoken utterance.
	if got := resolveShipPrompt("son bir test"); got != "son bir test" {
		t.Fatalf("ad-hoc prompt = %q, want it used verbatim", got)
	}
	if got := resolveShipPrompt("   "); got != "" {
		t.Fatalf("blank prompt = %q, want empty", got)
	}
}

// The wording of toparla is load-bearing. The target is baseline-OK, NOT done:
// a prompt that reads as "finish up" invites a runner to rush a half-built
// feature green under time pressure, which is the one thing that must never
// reach a deploy.
func TestToparlaAsksForABuildNotACompletedTask(t *testing.T) {
	p := shipPromptToparla
	for _, must := range []string{
		"Do NOT try to finish",   // explicitly forbids completion
		"revert or stash",        // licenses dropping unready work
		"Half-done is fine",      // says the quiet part
		"Build-breaking is not.", // the actual line being drawn
		"devam",                  // promises the continuation, so stopping is cheap
	} {
		if !strings.Contains(p, must) {
			t.Errorf("toparla must contain %q — without it the prompt reads as 'hurry up and finish', which is the failure mode", must)
		}
	}
}

// devam must tell the runner what changed while it was held. A resumed runner
// otherwise wakes into a repo it does not recognize.
func TestDevamTellsTheRunnerMainMoved(t *testing.T) {
	for _, must := range []string{"pull", "unchanged", "stashed"} {
		if !strings.Contains(shipPromptDevam, must) {
			t.Errorf("devam must mention %q", must)
		}
	}
}

func TestShipPromptNamesAreStable(t *testing.T) {
	got := shipPromptNames()
	if len(got) != 2 || got[0] != "devam" || got[1] != "toparla" {
		t.Fatalf("prompt names = %v, want [devam toparla]", got)
	}
}

// A nil keeper (notifications/keeper unavailable) must degrade to "no
// deliveries", never panic — the gate still does the real work.
func TestBroadcastShipPromptNilKeeperIsSafe(t *testing.T) {
	res := broadcastShipPrompt(t.Context(), nil, "toparla", "test")
	if res.Delivered != 0 || len(res.Deliveries) != 0 {
		t.Fatal("a nil keeper must yield no deliveries")
	}
	if res.summary() != "no live runner panes" {
		t.Fatalf("summary = %q", res.summary())
	}
}
