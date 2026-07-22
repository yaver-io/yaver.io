package main

import (
	"encoding/json"
	"testing"
)

// Only the device can say the app actually came back up. These tests pin that
// `verified` is reachable ONLY from a device-emitted event.
func TestRuntimeTurnAckMarksVerifiedFromDeviceEvent(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{
		Utterance: "fix it",
		State:     runtimeQueueStateReadyToTest,
		TestTarget: &RuntimeTurnTestTarget{
			Kind: "yaver-mobile-container", State: "delivered", Listeners: 1,
		},
	})

	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{
		Type:     "state",
		Message:  "preview_worker_bundle_loaded",
		Metadata: map[string]interface{}{"turnId": item.ItemID},
	})

	got, _ := runtimeQueue.get("", item.ItemID)
	if got.TestTarget.State != "verified" {
		t.Fatalf("state = %q, want verified", got.TestTarget.State)
	}
	if got.TestTarget.DeviceID != "dev-A" {
		t.Fatalf("deviceId = %q, want dev-A", got.TestTarget.DeviceID)
	}
}

func TestRuntimeTurnAckMarksFailedWhenTheDeviceCouldNotLoad(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{
		State:      runtimeQueueStateReadyToTest,
		TestTarget: &RuntimeTurnTestTarget{State: "delivered", Listeners: 1},
	})

	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{
		Type:     "error",
		Message:  "preview_worker_bundle_load_failed: hbc version mismatch",
		Metadata: map[string]interface{}{"turnId": item.ItemID},
	})

	got, _ := runtimeQueue.get("", item.ItemID)
	if got.TestTarget.State != "failed" {
		t.Fatalf("state = %q, want failed", got.TestTarget.State)
	}
	if got.TestTarget.Detail != "hbc version mismatch" {
		t.Fatalf("detail = %q, want the device's reason", got.TestTarget.Detail)
	}
}

// A build with no bundle loader can never reload. Silence would leave the turn
// on `delivered` forever while the user waits for something impossible.
func TestRuntimeTurnAckTreatsUnsupportedLoaderAsFailure(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{TestTarget: &RuntimeTurnTestTarget{State: "delivered"}})
	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{
		Type:     "state",
		Message:  "preview_worker_bundle_load_unsupported_platform",
		Metadata: map[string]interface{}{"turnId": item.ItemID},
	})
	got, _ := runtimeQueue.get("", item.ItemID)
	if got.TestTarget.State != "failed" {
		t.Fatalf("state = %q, want failed", got.TestTarget.State)
	}
}

func TestRuntimeTurnAckIgnoresUnrelatedEvents(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{TestTarget: &RuntimeTurnTestTarget{State: "delivered"}})
	before, _ := runtimeQueue.get("", item.ItemID)

	// No correlation id at all.
	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{Type: "log", Message: "preview_worker_bundle_loaded"})
	// Correlated, but says nothing about a reload.
	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{
		Type: "log", Message: "user tapped something",
		Metadata: map[string]interface{}{"turnId": item.ItemID},
	})

	after, _ := runtimeQueue.get("", item.ItemID)
	if after.TestTarget.State != before.TestTarget.State {
		t.Fatalf("unrelated event changed state to %q", after.TestTarget.State)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("unrelated event bumped UpdatedAt")
	}
}

// A later failure on some other device must not un-verify a turn that a device
// already confirmed.
func TestRuntimeTurnAckDoesNotDowngradeAVerifiedTurn(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{TestTarget: &RuntimeTurnTestTarget{State: "delivered"}})
	meta := map[string]interface{}{"turnId": item.ItemID}

	runtimeTurnObserveDeviceEvent("dev-A", BlackBoxEvent{Message: "preview_worker_bundle_loaded", Metadata: meta})
	runtimeTurnObserveDeviceEvent("dev-B", BlackBoxEvent{Message: "preview_worker_bundle_load_failed: nope", Metadata: meta})

	got, _ := runtimeQueue.get("", item.ItemID)
	if got.TestTarget.State != "verified" {
		t.Fatalf("state = %q, want verified to survive a later failure", got.TestTarget.State)
	}
}

// Deploy preflight must never ship. It reports and prints.
func TestRuntimeDeployPreflightBlocksUntestedWork(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{State: runtimeQueueStateReadyToTest})

	res := opsRuntimeTurnDeployPreflightHandler(OpsContext{}, json.RawMessage(`{"turnId":"`+item.ItemID+`","target":"testflight"}`))
	pf, ok := res.Initial.(runtimeDeployPreflight)
	if !ok {
		t.Fatalf("unexpected result type %T", res.Initial)
	}
	if pf.Ready {
		t.Fatalf("untested work reported ready to ship")
	}
	if pf.Command != "" {
		t.Fatalf("blocked preflight still handed out a deploy command: %q", pf.Command)
	}
	if len(pf.Blockers) == 0 {
		t.Fatalf("no blockers explained")
	}
}

func TestRuntimeDeployPreflightPassesOnceADeviceVerified(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{
		State:      runtimeQueueStateReadyToTest,
		TestTarget: &RuntimeTurnTestTarget{State: "verified"},
	})

	res := opsRuntimeTurnDeployPreflightHandler(OpsContext{}, json.RawMessage(`{"turnId":"`+item.ItemID+`"}`))
	pf := res.Initial.(runtimeDeployPreflight)
	if !pf.Ready {
		t.Fatalf("verified work blocked: %+v", pf.Blockers)
	}
	if pf.Command != "./scripts/deploy-testflight.sh" {
		t.Fatalf("command = %q", pf.Command)
	}
	got, _ := runtimeQueue.get("", item.ItemID)
	if got.State != runtimeQueueStateReadyToDeploy {
		t.Fatalf("state = %q, want ready_to_deploy", got.State)
	}
}

// Every blocker at once — fixing one and rediscovering the next burns the
// daily upload budget.
func TestRuntimeDeployPreflightReportsEveryBlockerAtOnce(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{State: runtimeQueueStateCaptured})
	res := opsRuntimeTurnDeployPreflightHandler(OpsContext{}, json.RawMessage(`{"turnId":"`+item.ItemID+`"}`))
	pf := res.Initial.(runtimeDeployPreflight)
	if len(pf.Blockers) < 2 {
		t.Fatalf("expected both the unstarted-work and untested blockers, got %+v", pf.Blockers)
	}
}

func TestRuntimeTurnEvidenceAttachesToAnExistingTurn(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{OwnerUserID: "u1", State: runtimeQueueStateRunning})

	res := opsRuntimeTurnEvidenceHandler(
		OpsContext{ActorUserID: "u1"},
		json.RawMessage(`{"turnId":"`+item.ItemID+`","evidence":[{"kind":"screenshot","ref":"bb://shot/1","screen":"Login"}]}`),
	)
	if !res.OK {
		t.Fatalf("evidence attach failed: %+v", res)
	}
	got, _ := runtimeQueue.get("u1", item.ItemID)
	if len(got.Evidence) != 1 || got.Evidence[0].Ref != "bb://shot/1" {
		t.Fatalf("evidence not attached: %+v", got.Evidence)
	}
}

func TestRuntimeTurnEvidenceRefusesAnotherOwnersTurn(t *testing.T) {
	withIsolatedRuntimeQueue(t)
	item := runtimeQueue.add(&RuntimeTurnQueueItem{OwnerUserID: "u1"})
	res := opsRuntimeTurnEvidenceHandler(
		OpsContext{ActorUserID: "u2"},
		json.RawMessage(`{"turnId":"`+item.ItemID+`","evidence":[{"kind":"screenshot","ref":"x"}]}`),
	)
	if res.OK {
		t.Fatalf("u2 attached evidence to u1's turn")
	}
}

func TestRuntimeViewportCoversSpatialAndTV(t *testing.T) {
	spatial := runtimeViewportFromSurface(RuntimeTurnSurface{Class: "glass"})
	if spatial.Surface != "spatial" || spatial.VisualBudget != "panel" || !spatial.Voice {
		t.Fatalf("spatial viewport = %+v", spatial)
	}
	tv := runtimeViewportFromSurface(RuntimeTurnSurface{Class: "tvos"})
	if tv.Surface != "shared-tv" || tv.RiskPolicy != "shared-tv" || tv.Interaction != "dpad" {
		t.Fatalf("tv viewport = %+v", tv)
	}
}
