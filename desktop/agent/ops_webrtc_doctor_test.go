package main

import (
	"strings"
	"testing"
)

// firstBootedUDID must pull a real UDID out of `simctl list devices booted`.
func TestFirstBootedUDID(t *testing.T) {
	out := "-- iOS 26.4 --\n    iPhone (3D94A65E-8E01-4CC0-9C2D-74DA6E0533D2) (Booted)\n"
	if got := firstBootedUDID(out); got != "3D94A65E-8E01-4CC0-9C2D-74DA6E0533D2" {
		t.Fatalf("firstBootedUDID = %q", got)
	}
	if firstBootedUDID("no devices\n") != "" {
		t.Error("expected empty for no booted device")
	}
}

// streamingReady must be false while any CRITICAL dep is missing, true only when
// all criticals are satisfied — so a weak model gets an honest gate.
func TestStreamingReadyGatesOnCriticals(t *testing.T) {
	degraded := []depCheck{
		{Name: "watchman", Present: false, Critical: false},
		{Name: "simctl-capture", Present: false, Critical: true},
	}
	if streamingReady(degraded) {
		t.Error("a degraded critical simctl must make streamingReady false")
	}
	healthy := []depCheck{
		{Name: "watchman", Present: false, Critical: false}, // non-critical missing is OK
		{Name: "simctl-capture", Present: true, Critical: true},
		{Name: "xcodebuild", Present: true, Critical: true},
	}
	if !streamingReady(healthy) {
		t.Error("all criticals present → streamingReady true even with a non-critical gap")
	}
}

// remediationSummary must label critical gaps as BLOCKER and name the fix — the
// self-heal breadcrumb a weak model follows.
func TestRemediationSummaryLabelsBlockers(t *testing.T) {
	checks := []depCheck{
		{Name: "simctl-capture", Present: false, Critical: true, Detail: "degraded", Fix: "reboot the box"},
		{Name: "watchman", Present: false, Critical: false, Detail: "slow polling", Fix: "brew install watchman"},
	}
	sum := strings.Join(remediationSummary(checks), "\n")
	if !strings.Contains(sum, "[BLOCKER] simctl-capture") {
		t.Errorf("critical gap must be a BLOCKER: %s", sum)
	}
	if !strings.Contains(sum, "brew install watchman") {
		t.Errorf("must name the fix command: %s", sum)
	}
}
