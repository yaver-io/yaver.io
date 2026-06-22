package main

import (
	"strings"
	"testing"
)

func planContains(steps []PlanStep, substr string) bool {
	for _, s := range steps {
		if strings.Contains(s.Cmd, substr) || strings.Contains(s.Title, substr) {
			return true
		}
	}
	return false
}

func TestNextStepsOrdersRemediation(t *testing.T) {
	r := PublishReadiness{
		Checks: []CheckStatus{
			{Name: "permissions", OK: false, Blocker: true},
			{Name: "listing-identity", OK: false, Blocker: true},
			{Name: "assets", OK: false, Blocker: true},
			{Name: "store-auth-apple", OK: false},
			{Name: "store-auth-google", OK: false},
		},
		Blockers: []string{"permissions", "listing-identity", "assets"},
	}
	steps := nextSteps(r, "myapp")

	// All gaps produce a step.
	if !planContains(steps, "caps generate") {
		t.Error("missing permissions ⇒ caps generate step")
	}
	if !planContains(steps, "bundleIdentifier") {
		t.Error("missing identity ⇒ app.json edit step")
	}
	if !planContains(steps, "assets capture") {
		t.Error("missing assets ⇒ capture step")
	}
	if !planContains(steps, "apple-asc-key") {
		t.Error("missing apple auth ⇒ asc-key step")
	}
	if !planContains(steps, "--project myapp") {
		t.Error("project should be threaded into commands")
	}
	// Identity comes before push; push is always last.
	idIdx, pushIdx := -1, -1
	for i, s := range steps {
		if strings.Contains(s.Title, "bundle/package") {
			idIdx = i
		}
		if strings.Contains(s.Cmd, "listing push") {
			pushIdx = i
		}
	}
	if idIdx == -1 || pushIdx == -1 || idIdx >= pushIdx {
		t.Errorf("identity (%d) must precede push (%d)", idIdx, pushIdx)
	}
	if pushIdx != len(steps)-1 {
		t.Error("push must be the final step")
	}
}

func TestNextStepsReadyStillShowsCopyAndPush(t *testing.T) {
	r := PublishReadiness{Ready: true} // all green, no failing checks
	steps := nextSteps(r, "")
	if !planContains(steps, "listing draft") || !planContains(steps, "listing push") {
		t.Error("even when ready, the final draft + push steps should show")
	}
	// No remediation steps when nothing failed → just the 2 final steps.
	if len(steps) != 2 {
		t.Errorf("ready ⇒ only draft+push, got %d steps", len(steps))
	}
}
