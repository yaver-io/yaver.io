package main

import (
	"runtime"
	"testing"
)

// TestDeployCapability_PlatformLockBlocksMismatchedHost: TestFlight is
// declared darwin-only via xcodebuild's Platforms. Calling on a Linux
// agent must produce CanDeploy=false with a platform-lock reason —
// the entire point of this endpoint is to give the UI a yes/no before
// xcodebuild silently fails.
func TestDeployCapability_PlatformLockBlocksMismatchedHost(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("only meaningful when GOOS != darwin")
	}
	cap := ComputeDeployCapability("testflight", "", nil)
	if cap.CanDeploy {
		t.Fatalf("CanDeploy must be false on %s for testflight, got cap=%+v", runtime.GOOS, cap)
	}
	if cap.PlatformLock == "" {
		t.Fatal("expected PlatformLock=darwin, got empty")
	}
	if cap.Reason == "" {
		t.Fatal("expected reason filled when platform-locked")
	}
	if cap.CIAlternative == "" {
		t.Fatal("CI fallback must be surfaced when local can't deploy")
	}
}

// TestDeployCapability_PlatformNeutralTargetIgnoresHost: Cloudflare /
// Convex have no Platforms restriction, so the platform-lock path
// should never short-circuit them. Required tools may still be
// missing (likely are in the test env) but the failure reason must
// blame tools/secrets, not platform.
func TestDeployCapability_PlatformNeutralTargetIgnoresHost(t *testing.T) {
	cap := ComputeDeployCapability("cloudflare", "", nil)
	if cap.PlatformLock != "" {
		t.Fatalf("cloudflare should have no platform lock, got %q", cap.PlatformLock)
	}
}

// TestDeployCapability_UnknownTarget: defensive — UI sends a stale
// target name → endpoint should report CanDeploy=false with a clear
// reason, not panic or 500.
func TestDeployCapability_UnknownTarget(t *testing.T) {
	cap := ComputeDeployCapability("does-not-exist", "", nil)
	if cap.CanDeploy {
		t.Fatal("unknown target must not report CanDeploy=true")
	}
	if cap.Reason == "" {
		t.Fatal("expected reason on unknown target")
	}
}

// TestBuildDeployCapabilitiesReport_AllTargetsByDefault: with empty
// targets list the report must enumerate every catalogue entry — the
// UI relies on this to render a "what's possible from this device"
// matrix without having to enumerate target names client-side.
func TestBuildDeployCapabilitiesReport_AllTargetsByDefault(t *testing.T) {
	rep := BuildDeployCapabilitiesReport(nil, "", "test-device", nil)
	if rep.DeviceID != "test-device" {
		t.Fatalf("DeviceID = %q, want test-device", rep.DeviceID)
	}
	if rep.Platform != runtime.GOOS {
		t.Fatalf("Platform = %q, want %q", rep.Platform, runtime.GOOS)
	}
	want := len(BuildTargetNames())
	if len(rep.Targets) != want {
		t.Fatalf("len(Targets) = %d, want %d", len(rep.Targets), want)
	}
	// Sorted by target name — the UI relies on stable order so it can
	// memoise the layout without keying off insertion order.
	for i := 1; i < len(rep.Targets); i++ {
		if rep.Targets[i-1].Target > rep.Targets[i].Target {
			t.Fatalf("targets not sorted: %s > %s", rep.Targets[i-1].Target, rep.Targets[i].Target)
		}
	}
}

// TestBuildDeployCapabilitiesReport_SubsetFilter: callers can scope
// to one target — verifies the path the per-button mobile UI takes
// (one HTTP call, one verdict, no extra targets in the response).
func TestBuildDeployCapabilitiesReport_SubsetFilter(t *testing.T) {
	rep := BuildDeployCapabilitiesReport([]string{"convex"}, "", "test-device", nil)
	if len(rep.Targets) != 1 || rep.Targets[0].Target != "convex" {
		t.Fatalf("expected single convex result, got %+v", rep.Targets)
	}
}

// TestTargetCIWorkflow_AllTargetsCovered: every catalogued target has
// a CI fallback declared. If we add a new target without a CI path
// the UI loses the "trigger via CI instead" fallback button — failing
// the test on add is cheaper than discovering it post-ship.
func TestTargetCIWorkflow_AllTargetsCovered(t *testing.T) {
	for _, name := range BuildTargetNames() {
		if _, ok := targetCIWorkflow[name]; !ok {
			t.Errorf("target %q has no targetCIWorkflow entry — add one or document why it's truly local-only", name)
		}
	}
}
