package main

// remote_runtime_lease_test.go — P5 concurrency arbitration tests.
// Pure — the lease is process-local; every case is deterministic.

import (
	"strings"
	"testing"
	"time"
)

func TestControlLease_TakeWhenFree(t *testing.T) {
	l := &ControlLease{}
	snap, err := l.TakeControl("phone-1", "Phone", false, time.Now())
	if err != nil {
		t.Fatalf("take on free lease errored: %v", err)
	}
	if !snap.Held || snap.HolderID != "phone-1" {
		t.Fatalf("snapshot = %+v, want held+phone-1", snap)
	}
}

func TestControlLease_RejectStrangerWhenHeld(t *testing.T) {
	l := &ControlLease{idleTimeout: time.Minute}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	_, err := l.TakeControl("tv-1", "TV", false, time.Now())
	if err == nil {
		t.Fatal("stranger take must fail while lease is held")
	}
	if !strings.Contains(err.Error(), "force=true") {
		t.Fatalf("error should hint force=true, got %v", err)
	}
}

func TestControlLease_ForceOverride(t *testing.T) {
	l := &ControlLease{idleTimeout: time.Minute}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	snap, err := l.TakeControl("tv-1", "TV", true, time.Now())
	if err != nil {
		t.Fatalf("force take errored: %v", err)
	}
	if snap.HolderID != "tv-1" {
		t.Fatalf("force did not switch holder, snap=%+v", snap)
	}
}

func TestControlLease_StealAfterIdleTimeout(t *testing.T) {
	l := &ControlLease{idleTimeout: 50 * time.Millisecond}
	base := time.Now()
	l.TakeControl("phone-1", "Phone", false, base)
	// Well past the idle timeout.
	snap, err := l.TakeControl("tv-1", "TV", false, base.Add(time.Second))
	if err != nil {
		t.Fatalf("steal after idle timeout errored: %v", err)
	}
	if snap.HolderID != "tv-1" {
		t.Fatalf("holder = %q, want tv-1 after idle steal", snap.HolderID)
	}
}

func TestControlLease_CheckAndRefreshRejectsStranger(t *testing.T) {
	l := &ControlLease{idleTimeout: time.Minute}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	err := l.CheckAndRefresh("tv-1", time.Now())
	if err == nil {
		t.Fatal("gate should reject a stranger while the lease is held")
	}
}

func TestControlLease_CheckAndRefreshAllowsHolder(t *testing.T) {
	l := &ControlLease{idleTimeout: time.Minute}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	if err := l.CheckAndRefresh("phone-1", time.Now()); err != nil {
		t.Fatalf("gate should allow the current holder: %v", err)
	}
}

func TestControlLease_CheckAndRefreshAdoptsAnonWhenFree(t *testing.T) {
	// Legacy web viewer: no clientId. When lease is free the call
	// succeeds AND the anon caller is NOT stamped as holder (so
	// nothing steals from a real client that shows up later).
	l := &ControlLease{}
	if err := l.CheckAndRefresh("", time.Now()); err != nil {
		t.Fatalf("anon on free lease errored: %v", err)
	}
	if snap := l.Snapshot(); snap.Held {
		t.Fatalf("anon call should not seize the lease, snap=%+v", snap)
	}
}

func TestControlLease_ReleaseByHolder(t *testing.T) {
	l := &ControlLease{}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	snap := l.ReleaseControl("phone-1", false)
	if snap.Held {
		t.Fatalf("release should clear the holder, snap=%+v", snap)
	}
}

func TestControlLease_ReleaseByStrangerIsNoop(t *testing.T) {
	l := &ControlLease{}
	l.TakeControl("phone-1", "Phone", false, time.Now())
	snap := l.ReleaseControl("tv-1", false)
	if !snap.Held || snap.HolderID != "phone-1" {
		t.Fatalf("stranger release must not clear holder, snap=%+v", snap)
	}
}

func TestExecuteControl_LeaseGate(t *testing.T) {
	// End-to-end: ExecuteControl on a session held by phone-1 rejects
	// a control POST that carries clientId=tv-1.
	mgr := NewRemoteRuntimeManager()
	sess := newTestRemoteRuntimeSession("rr_lease_e2e", "ios-simulator", "SIM-UDID")
	mgr.sessions[sess.ID] = sess
	live := &remoteRuntimeLiveState{
		sessionID: sess.ID, targetID: sess.TargetID, platform: "ios", deviceID: sess.DeviceID,
	}
	mgr.live[sess.ID] = live

	if _, err := live.ensureLease().TakeControl("phone-1", "Phone", false, time.Now()); err != nil {
		t.Fatalf("seed take: %v", err)
	}
	_, err := mgr.ExecuteControl(sess.ID, remoteRuntimeControlRequest{
		Action: "tap", X: 100, Y: 200, ClientID: "tv-1", ClientLabel: "TV",
	})
	if err == nil {
		t.Fatal("ExecuteControl must reject tv-1 while phone-1 holds the lease")
	}
	if !strings.Contains(err.Error(), "take control first") {
		t.Fatalf("gate error should mention take control, got %v", err)
	}
}
