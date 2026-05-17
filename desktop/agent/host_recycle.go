package main

// host_recycle.go — Phase B of docs/managed-cloud-host-lifecycle.md.
//
// "Recycle" = bring up a fresh BYO Hetzner box, verify it, then
// snapshot+delete the old one. Zero-downtime ordering: the old box
// keeps serving until the new box is proven healthy, so a failure
// rolls back to exactly the old state with nothing destroyed.
//
// The state machine is pure logic over recycleBackend so the SAFETY
// GUARDS are unit-tested without touching real infra:
//   1. never destroy the device this agent runs on (self-destruct)
//   2. dry-run unless confirm=true (destructive)
//   3. new box must pass health BEFORE the old box is touched; if it
//      fails, the half-up new box is deleted (no paid orphan) and the
//      old box is left exactly as-is
//   4. the old box is NEVER deleted without a successful snapshot
//      first (CLAUDE.md hard rule — recover-safety)
//
// BYO only (user's vault Hetzner token). The managed-cloud lifecycle
// is the LemonSqueezy-gated Convex path and is wholly separate.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type recycleBackend interface {
	SelfDeviceID() string
	CreateServer(name, plan, region string) (ip, id string, err error)
	HealthOK(ip string) bool
	Snapshot(id, label string) error
	DeleteServer(id string) error
}

type recycleRequest struct {
	TargetDeviceID string `json:"targetDeviceId"`
	// OldServerID is the Hetzner numeric id of the box being retired.
	// Required + explicit on purpose: a delete target must never be
	// fuzzy-resolved.
	OldServerID string `json:"oldServerId"`
	NewName     string `json:"newName"`
	Plan        string `json:"plan,omitempty"`
	Region      string `json:"region,omitempty"`
	Confirm     bool   `json:"confirm"`
}

type recycleResult struct {
	OK         bool     `json:"ok"`
	DryRun     bool     `json:"dryRun,omitempty"`
	NewIP      string   `json:"newIp,omitempty"`
	NewID      string   `json:"newServerId,omitempty"`
	OldDeleted bool     `json:"oldDeleted,omitempty"`
	Steps      []string `json:"steps"`
	Error      string   `json:"error,omitempty"`
}

func recycleHost(be recycleBackend, req recycleRequest) recycleResult {
	r := recycleResult{}
	add := func(s string) { r.Steps = append(r.Steps, s) }

	plan := strings.TrimSpace(req.Plan)
	if plan == "" {
		plan = "starter"
	}
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "eu"
	}

	// Guard 1 — never decommission the box this agent runs on.
	self := strings.TrimSpace(be.SelfDeviceID())
	if self != "" && self == strings.TrimSpace(req.TargetDeviceID) {
		r.Error = "refusing to recycle the device this agent runs on (self-destruct guard) — run recycle from a different owned device"
		return r
	}
	if strings.TrimSpace(req.OldServerID) == "" {
		r.Error = "oldServerId is required — the delete target must be explicit, never fuzzy-matched"
		return r
	}
	if strings.TrimSpace(req.NewName) == "" {
		r.Error = "newName is required"
		return r
	}

	// Guard 2 — destructive; dry-run unless explicitly confirmed.
	if !req.Confirm {
		r.DryRun = true
		r.OK = true
		add(fmt.Sprintf("PLAN: create %q (%s/%s) → health-check → snapshot old #%s → delete old #%s. Re-run with confirm=true to execute.",
			req.NewName, plan, region, req.OldServerID, req.OldServerID))
		return r
	}

	// Step 1 — create the new box. The old box keeps serving.
	ip, id, err := be.CreateServer(req.NewName, plan, region)
	if err != nil {
		r.Error = "create new box failed (old box untouched): " + err.Error()
		return r
	}
	r.NewIP, r.NewID = ip, id
	add("created new box #" + id + " (" + ip + ")")

	// Step 2 — verify health BEFORE touching the old box.
	if !be.HealthOK(ip) {
		if derr := be.DeleteServer(id); derr != nil {
			add("WARNING: new box #" + id + " unhealthy AND its cleanup-delete failed: " + derr.Error() + " — orphan, reap manually")
		} else {
			add("new box unhealthy → deleted it (no paid orphan); old box kept")
		}
		r.Error = "new box failed health check — old box left running (rollback, nothing destroyed)"
		return r
	}
	add("new box healthy")

	// Step 3 — snapshot the old box. Never delete un-snapshotted.
	label := fmt.Sprintf("yaver-recycle-%s-%d", req.OldServerID, time.Now().Unix())
	if serr := be.Snapshot(req.OldServerID, label); serr != nil {
		r.Error = "old-box snapshot failed — NOT deleting old box (recover-safety). New box is up at " + ip + "; old box also still up. Fix snapshot, then delete old #" + req.OldServerID + " manually."
		return r
	}
	add("snapshotted old box #" + req.OldServerID)

	// Step 4 — delete the old box.
	if derr := be.DeleteServer(req.OldServerID); derr != nil {
		r.Error = "old box snapshot OK but delete failed: " + derr.Error() + " — delete it manually (the snapshot exists)"
		return r
	}
	add("deleted old box #" + req.OldServerID)
	r.OldDeleted = true
	r.OK = true
	return r
}

// liveRecycleBackend wires the state machine to the Phase A Hetzner
// primitives + a real agent health probe. Token is the user's
// vault-backed Hetzner account token (BYO), never a payload field.
type liveRecycleBackend struct{ token string }

func (liveRecycleBackend) SelfDeviceID() string { return localDeviceID() }

func (b liveRecycleBackend) CreateServer(name, plan, region string) (string, string, error) {
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return "", "", err
	}
	return m.hetznerCreateServer(b.token, name, plan, region)
}

func (liveRecycleBackend) HealthOK(ip string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 18; i++ { // ~3 min: cloud-init + agent start
		resp, err := client.Get("http://" + ip + ":18080/info")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(10 * time.Second)
	}
	return false
}

func (b liveRecycleBackend) Snapshot(id, label string) error {
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return err
	}
	return m.hetznerSnapshotServer(b.token, id, label)
}

func (b liveRecycleBackend) DeleteServer(id string) error {
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return err
	}
	return m.hetznerDeleteServer(b.token, id)
}
