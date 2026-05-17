package main

import (
	"fmt"
	"strings"
	"testing"
)

// fakeRecycleBackend records calls + lets a test program failures.
// The point of these tests is the SAFETY GUARDS — that a destructive
// orchestration never deletes the wrong/own box, never deletes
// without a snapshot, and rolls back cleanly.
type fakeRecycleBackend struct {
	self        string
	createErr   error
	createID    string
	healthy     bool
	snapErr     error
	deleteErr   error
	created     []string
	snapshotted []string
	deleted     []string
}

func (f *fakeRecycleBackend) SelfDeviceID() string { return f.self }
func (f *fakeRecycleBackend) CreateServer(name, plan, region string) (string, string, error) {
	if f.createErr != nil {
		return "", "", f.createErr
	}
	id := f.createID
	if id == "" {
		id = "new-1"
	}
	f.created = append(f.created, id)
	return "10.0.0.9", id, nil
}
func (f *fakeRecycleBackend) HealthOK(ip string) bool { return f.healthy }
func (f *fakeRecycleBackend) Snapshot(id, label string) error {
	if f.snapErr != nil {
		return f.snapErr
	}
	f.snapshotted = append(f.snapshotted, id)
	return nil
}
func (f *fakeRecycleBackend) DeleteServer(id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func baseReq() recycleRequest {
	return recycleRequest{TargetDeviceID: "dev-OLD", OldServerID: "555", NewName: "box2", Confirm: true}
}

// Guard 1: never recycle the device the agent runs on.
func TestRecycle_RefusesSelfDestruct(t *testing.T) {
	be := &fakeRecycleBackend{self: "dev-OLD", healthy: true}
	res := recycleHost(be, baseReq()) // target == self
	if res.OK || !strings.Contains(res.Error, "self-destruct") {
		t.Fatalf("must refuse self-destruct, got %+v", res)
	}
	if len(be.created) != 0 || len(be.deleted) != 0 {
		t.Fatalf("nothing must be created/deleted on self-destruct guard: %+v", be)
	}
}

// Guard 2: destructive → dry-run unless confirm=true.
func TestRecycle_DryRunWithoutConfirm(t *testing.T) {
	be := &fakeRecycleBackend{self: "dev-CTRL", healthy: true}
	req := baseReq()
	req.Confirm = false
	res := recycleHost(be, req)
	if !res.OK || !res.DryRun {
		t.Fatalf("no-confirm must be a successful dry-run, got %+v", res)
	}
	if len(be.created)+len(be.deleted)+len(be.snapshotted) != 0 {
		t.Fatalf("dry-run must touch nothing: %+v", be)
	}
}

// Guard 3: new box unhealthy → delete the new box, KEEP the old, error.
func TestRecycle_RollsBackWhenNewUnhealthy(t *testing.T) {
	be := &fakeRecycleBackend{self: "dev-CTRL", healthy: false, createID: "new-9"}
	res := recycleHost(be, baseReq())
	if res.OK || !strings.Contains(res.Error, "health") {
		t.Fatalf("unhealthy new box must fail, got %+v", res)
	}
	if len(be.snapshotted) != 0 {
		t.Fatalf("old box must NOT be snapshotted when new is unhealthy: %+v", be.snapshotted)
	}
	if strSliceHas(be.deleted, "555") {
		t.Fatalf("OLD box must never be deleted on rollback; deleted=%v", be.deleted)
	}
	if !strSliceHas(be.deleted, "new-9") {
		t.Fatalf("the unhealthy NEW box must be cleaned up (no paid orphan); deleted=%v", be.deleted)
	}
}

// Guard 4: snapshot failure aborts the delete — old box survives.
func TestRecycle_NeverDeletesUnsnapshotted(t *testing.T) {
	be := &fakeRecycleBackend{self: "dev-CTRL", healthy: true, snapErr: fmt.Errorf("hetzner 500")}
	res := recycleHost(be, baseReq())
	if res.OK || !strings.Contains(res.Error, "snapshot failed") {
		t.Fatalf("snapshot failure must abort, got %+v", res)
	}
	if strSliceHas(be.deleted, "555") {
		t.Fatalf("old box deleted despite snapshot failure — recover-safety violated: %v", be.deleted)
	}
}

// Happy path: create → health → snapshot(old) → delete(old), ordered.
func TestRecycle_HappyPathOrder(t *testing.T) {
	be := &fakeRecycleBackend{self: "dev-CTRL", healthy: true, createID: "new-7"}
	res := recycleHost(be, baseReq())
	if !res.OK || !res.OldDeleted || res.NewID != "new-7" {
		t.Fatalf("happy path failed: %+v", res)
	}
	if !strSliceHas(be.created, "new-7") || !strSliceHas(be.snapshotted, "555") || !strSliceHas(be.deleted, "555") {
		t.Fatalf("expected create new-7 + snapshot 555 + delete 555; got %+v", be)
	}
	if strSliceHas(be.deleted, "new-7") {
		t.Fatalf("the healthy new box must NOT be deleted: %v", be.deleted)
	}
}

func strSliceHas(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
