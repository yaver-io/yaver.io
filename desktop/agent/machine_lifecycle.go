package main

// machine_lifecycle.go — first-class BYO "own machine" lifecycle verbs
// behind `yaver machine create|up|down|rm`. These are the production,
// single-owner counterparts to the managed cloud_stop/cloud_start verbs:
//
//   - BYO vault token only (accountField(ProviderHetzner)) — the user pays
//     their own Hetzner. Real spend is still fail-closed behind the same
//     confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1 double gate used by the older
//     BYO cloud_stop/cloud_start plane, so a stray UI/API call never creates or
//     wakes a billable box.
//   - down = snapshot (recover-safe) THEN delete — scale-to-zero to cut cost.
//     ALWAYS snapshot first; a failed snapshot ABORTS the delete (CLAUDE.md).
//   - up   = recreate the box from the stop snapshot (~minutes).
//   - Every transition is mirrored to Convex byoMachines (bookkeeping only —
//     the token never leaves the vault) so `yaver machine list` and the
//     runner-path auto-wake can see a box's power state.
//
// Kept in its own file (leaves the gated managed cloud_stop/start + their
// tests untouched) per feedback_other_sessions_prune_untested.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// machineLifecycleReady resolves the BYO Hetzner token + a deploy manager,
// shared by every verb. Returns a typed OpsResult on failure (OK=false).
func machineLifecycleReady() (token string, m *CloudDeployManager, fail *OpsResult) {
	token = accountField(ProviderHetzner, "token")
	if strings.TrimSpace(token) == "" {
		return "", nil, &OpsResult{OK: false, Code: "no_account",
			Error: "Hetzner not connected — run `yaver vault add HETZNER_TOKEN` / `yaver accounts connect hetzner` first (BYO token, stays in the vault)"}
	}
	mgr, err := NewCloudDeployManager(".")
	if err != nil {
		return "", nil, &OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	return token, mgr, nil
}

func machineConfirmPlan(confirm bool, plan string) *OpsResult {
	if confirm && cloudStopStartLive() {
		return nil
	}
	return &OpsResult{OK: true, Initial: map[string]interface{}{
		"dryRun": true, "plan": plan, "why": planGateReason(confirm),
	}}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_create",
		Description: "Create a NEW BYO cloud box on your own Hetzner account and record it as <name>. Requires name + confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1. plan=starter|pro|scale, region=eu|us. Uses your vault Hetzner token; you pay Hetzner directly.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"name", "confirm"},
			"properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "Machine name/alias (stable handle across stop/start)"},
				"plan":    map[string]interface{}{"type": "string", "description": "starter|pro|scale (default starter)"},
				"region":  map[string]interface{}{"type": "string", "description": "eu|us (default eu)"},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMachineCreateHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_down",
		Description: "Scale a BYO box to zero: snapshot (recover-safe) THEN delete the server so Hetzner billing stops (a powered-off box still bills full price — only delete stops it). Requires serverId + confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1. Returns the snapshot id used by machine_up.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"serverId", "confirm"},
			"properties": map[string]interface{}{
				"serverId": map[string]interface{}{"type": "string", "description": "Hetzner numeric server id (explicit — never fuzzy-matched)"},
				"name":     map[string]interface{}{"type": "string", "description": "Machine name to keep on the bookkeeping row"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMachineDownHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_up",
		Description: "Bring a stopped BYO box back: recreate the server from its stop snapshot (~minutes). Requires snapshotImageId + name + confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1. plan/region optional.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"snapshotImageId", "name", "confirm"},
			"properties": map[string]interface{}{
				"snapshotImageId": map[string]interface{}{"type": "string", "description": "Snapshot image id from machine_down"},
				"name":            map[string]interface{}{"type": "string", "description": "Machine name/alias"},
				"plan":            map[string]interface{}{"type": "string", "description": "starter|pro|scale (default starter)"},
				"region":          map[string]interface{}{"type": "string", "description": "eu|us (default eu)"},
				"confirm":         map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMachineUpHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_rm",
		Description: "Permanently remove a BYO box: snapshot (recover-safe, always) THEN delete the server and mark it deleted. Requires serverId + confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1. The final snapshot is retained unless purge=true.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"serverId", "confirm"},
			"properties": map[string]interface{}{
				"serverId": map[string]interface{}{"type": "string", "description": "Hetzner numeric server id"},
				"name":     map[string]interface{}{"type": "string"},
				"purge":    map[string]interface{}{"type": "boolean", "description": "Also delete the final snapshot image (irreversible)"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMachineRmHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_wake",
		Description: "Wake a scaled-to-zero BYO box BY NAME: look up its stopped byoMachines row and recreate the server from its stop snapshot. Unlike machine_up you don't pass a snapshot id — the row supplies it, so a voice/car surface can just say the box name. Idempotent: a box already active returns immediately. Requires confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1 (recreating a box starts Hetzner billing). Call this before runner_turn when the target box may be asleep.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"name", "confirm"},
			"properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "Machine name/alias to wake"},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsMachineWakeHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// machineWakeDecision decides what to do with a byoMachines row BEFORE any
// billable provisioning. ready=true means the row is a stopped box with a
// snapshot, safe to recreate; a non-nil early result is the terminal answer
// (not found / already up / permanently deleted / no snapshot / wrong state).
// Pure so the state machine is unit-tested without Convex or a live box.
func machineWakeDecision(row *byoMachineRow) (ready bool, early *OpsResult) {
	if row == nil {
		return false, &OpsResult{OK: false, Code: "not_found", Error: "no such BYO machine"}
	}
	switch row.State {
	case "active":
		return false, &OpsResult{OK: true, Initial: map[string]interface{}{
			"name": row.Name, "already": true, "state": "active",
			"ip": row.ServerIP, "serverId": row.ServerID, "deviceId": row.DeviceID,
		}}
	case "deleted":
		return false, &OpsResult{OK: false, Code: "deleted",
			Error: fmt.Sprintf("machine %q was permanently removed (machine_rm) — create a new one", row.Name)}
	case "stopped":
		if strings.TrimSpace(row.SnapshotImageID) == "" {
			return false, &OpsResult{OK: false, Code: "no_snapshot",
				Error: fmt.Sprintf("machine %q has no stop snapshot to recreate from", row.Name)}
		}
		return true, nil
	default:
		return false, &OpsResult{OK: false, Code: "bad_state",
			Error: fmt.Sprintf("machine %q is in state %q — only a stopped box can be woken", row.Name, row.State)}
	}
}

func opsMachineWakeHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return OpsResult{OK: false, Code: "internal", Error: "cannot load config to look up machines"}
	}
	rows, err := fetchByoMachines(cfg)
	if err != nil {
		return OpsResult{OK: false, Code: "lookup_failed", Error: err.Error()}
	}
	row := latestByName(rows, name, "")
	ready, early := machineWakeDecision(row)
	if !ready {
		return *early
	}
	// Recreating a box is billable → same confirm gate as the other mutations.
	if gate := machineConfirmPlan(p.Confirm, fmt.Sprintf("would recreate box %q from snapshot %s (starts Hetzner billing)", row.Name, row.SnapshotImageID)); gate != nil {
		return *gate
	}
	token, m, fail := machineLifecycleReady()
	if fail != nil {
		return *fail
	}
	plan := firstNonEmpty(strings.TrimSpace(row.Plan), "starter")
	region := firstNonEmpty(strings.TrimSpace(row.Region), "eu")
	ip, id, err := m.hetznerStartServer(token, row.Name, plan, region, row.SnapshotImageID)
	if err != nil {
		return OpsResult{OK: false, Code: "wake_failed", Error: err.Error()}
	}
	syncByoMachine("hetzner", id, "active", map[string]interface{}{
		"name": row.Name, "serverIp": ip, "region": region, "plan": plan, "snapshotImageId": row.SnapshotImageID,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"woke": id, "name": row.Name, "ip": ip, "fromSnapshot": row.SnapshotImageID,
		// The recreated agent re-registers over the relay a minute or two later;
		// a caller should poll runner_sessions / status before the first turn.
		"note": "box is booting — its agent re-registers in ~1-2 min before runner_turn can reach it",
	}}
}

func opsMachineCreateHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Name    string `json:"name"`
		Plan    string `json:"plan"`
		Region  string `json:"region"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	plan := firstNonEmpty(strings.TrimSpace(p.Plan), "starter")
	region := firstNonEmpty(strings.TrimSpace(p.Region), "eu")
	if gate := machineConfirmPlan(p.Confirm, fmt.Sprintf("would create box %q (%s/%s) on your Hetzner account", name, plan, region)); gate != nil {
		return *gate
	}
	token, m, fail := machineLifecycleReady()
	if fail != nil {
		return *fail
	}
	ip, id, err := m.hetznerCreateServer(token, name, plan, region)
	if err != nil {
		return OpsResult{OK: false, Code: "create_failed", Error: err.Error()}
	}
	syncByoMachine("hetzner", id, "active", map[string]interface{}{
		"name": name, "serverIp": ip, "region": region, "plan": plan,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"created": id, "name": name, "ip": ip, "plan": plan, "region": region,
	}}
}

func opsMachineDownHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ServerID string `json:"serverId"`
		Name     string `json:"name"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	serverID := strings.TrimSpace(p.ServerID)
	if serverID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "serverId required"}
	}
	if gate := machineConfirmPlan(p.Confirm, fmt.Sprintf("would snapshot server %s then delete it (scale to zero)", serverID)); gate != nil {
		return *gate
	}
	token, m, fail := machineLifecycleReady()
	if fail != nil {
		return *fail
	}
	label := "yaver-machine-down-" + serverID
	snapID, err := m.hetznerStopServer(token, serverID, label)
	if err != nil {
		code := "down_failed"
		if snapID != "" {
			code = "delete_failed_snapshot_ok"
		}
		return OpsResult{OK: false, Code: code, Error: err.Error(),
			Initial: map[string]interface{}{"snapshotImageId": snapID}}
	}
	extra := map[string]interface{}{"snapshotImageId": snapID}
	if n := strings.TrimSpace(p.Name); n != "" {
		extra["name"] = n
	}
	syncByoMachine("hetzner", serverID, "stopped", extra)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"stopped": serverID, "snapshotImageId": snapID,
	}}
}

func opsMachineUpHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		SnapshotImageID string `json:"snapshotImageId"`
		Name            string `json:"name"`
		Plan            string `json:"plan"`
		Region          string `json:"region"`
		Confirm         bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	snap := strings.TrimSpace(p.SnapshotImageID)
	name := strings.TrimSpace(p.Name)
	if snap == "" || name == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "snapshotImageId and name required"}
	}
	plan := firstNonEmpty(strings.TrimSpace(p.Plan), "starter")
	region := firstNonEmpty(strings.TrimSpace(p.Region), "eu")
	if gate := machineConfirmPlan(p.Confirm, fmt.Sprintf("would recreate box %q (%s/%s) from snapshot %s", name, plan, region, snap)); gate != nil {
		return *gate
	}
	token, m, fail := machineLifecycleReady()
	if fail != nil {
		return *fail
	}
	ip, id, err := m.hetznerStartServer(token, name, plan, region, snap)
	if err != nil {
		return OpsResult{OK: false, Code: "up_failed", Error: err.Error()}
	}
	syncByoMachine("hetzner", id, "active", map[string]interface{}{
		"name": name, "serverIp": ip, "region": region, "plan": plan, "snapshotImageId": snap,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"started": id, "name": name, "ip": ip, "fromSnapshot": snap,
	}}
}

func opsMachineRmHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ServerID string `json:"serverId"`
		Name     string `json:"name"`
		Purge    bool   `json:"purge"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	serverID := strings.TrimSpace(p.ServerID)
	if serverID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "serverId required"}
	}
	if gate := machineConfirmPlan(p.Confirm, fmt.Sprintf("would snapshot server %s then delete + mark removed", serverID)); gate != nil {
		return *gate
	}
	token, m, fail := machineLifecycleReady()
	if fail != nil {
		return *fail
	}
	// Always snapshot before delete (recover-safe), even for rm.
	snapID, err := m.hetznerStopServer(token, serverID, "yaver-machine-rm-"+serverID)
	if err != nil {
		code := "rm_failed"
		if snapID != "" {
			code = "delete_failed_snapshot_ok"
		}
		return OpsResult{OK: false, Code: code, Error: err.Error(),
			Initial: map[string]interface{}{"snapshotImageId": snapID}}
	}
	purged := false
	if p.Purge && snapID != "" {
		if derr := m.hetznerDeleteImage(token, snapID); derr == nil {
			purged = true
		}
	}
	extra := map[string]interface{}{"snapshotImageId": snapID}
	if n := strings.TrimSpace(p.Name); n != "" {
		extra["name"] = n
	}
	syncByoMachine("hetzner", serverID, "deleted", extra)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"removed": serverID, "snapshotImageId": snapID, "snapshotPurged": purged,
	}}
}
