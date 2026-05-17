package main

import (
	"strings"
	"testing"
)

// The whole point of this provisioner is that it NEVER spends money
// by accident. These tests assert every fail-closed gate, with no
// network and no Robot creds.
func TestRobotProvisionerRegistered(t *testing.T) {
	if _, ok := provisionerRegistry()[HostHetznerRobot]; !ok {
		t.Fatal("HostHetznerRobot must be in provisionerRegistry")
	}
}

func TestRobotBlockedWithoutCreds(t *testing.T) {
	t.Setenv("HROBOT_USER", "")
	t.Setenv("HROBOT_PASS", "")
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true", "live": "true"})
	if err != nil {
		t.Fatalf("missing creds must be a soft Manual, not error: %v", err)
	}
	if res.OK || res.Manual == "" || !strings.Contains(res.Manual, "HROBOT_USER") {
		t.Fatalf("missing creds must return actionable Manual, got %+v", res)
	}
}

func TestRobotBlockedWithoutConfirm(t *testing.T) {
	t.Setenv("HROBOT_USER", "u")
	t.Setenv("HROBOT_PASS", "p")
	res, err := provisionHetznerRobot("box", map[string]string{}) // no confirmPaidOrder
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK || !strings.Contains(res.Manual, "confirmPaidOrder=true") {
		t.Fatalf("must refuse paid order without confirm, got %+v", res)
	}
}

func TestRobotConfirmedIsDryRunByDefault(t *testing.T) {
	t.Setenv("HROBOT_USER", "u")
	t.Setenv("HROBOT_PASS", "p")
	// confirmed but NOT live → plan only, never an order/charge.
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true"})
	if err != nil {
		t.Fatalf("dry-run must not error: %v", err)
	}
	if !res.OK || res.Details["mode"] == "" || !strings.Contains(res.Details["mode"], "dry-run") {
		t.Fatalf("confirmed-but-not-live must be a dry-run plan, got %+v", res)
	}
}

func TestRobotLiveIsGuardedNotFaked(t *testing.T) {
	t.Setenv("HROBOT_USER", "u")
	t.Setenv("HROBOT_PASS", "p")
	// live=true must hard-fail (unvalidated paid path) — never a
	// silent fake success that implies a server was ordered.
	_, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true", "live": "true"})
	if err == nil || !strings.Contains(err.Error(), "not yet validated") {
		t.Fatalf("live order path must fail-loud as unvalidated, got err=%v", err)
	}
}
