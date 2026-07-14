package main

// ops_storage.go — storage reclaim and process control as ops verbs, so a
// runner (Claude / Codex / opencode) can do the same thing the phone does.
//
// The runner flow this enables, in the words it will actually happen in:
//
//	build fails: "no space left on device"
//	runner: storage_scan            -> 41 GB reclaimable, 22 GB of it is
//	                                   DerivedData for three old projects
//	runner: storage_reclaim {ids}   -> {ok:false, code:"confirm_required",
//	                                    initial:<the plan>}
//	runner: shows the plan to the human, gets a yes
//	runner: storage_reclaim {ids, confirm:true} -> freed 22 GB
//	build retried, passes
//
// The confirm gate is the same one every destructive verb in this codebase
// uses (ops.go invariant 4): the unconfirmed call is a dry run that returns
// the plan, and it is not a failure — it is the plan. A runner that treats
// confirm_required as an error and gives up is behaving correctly-ish; a
// runner that surfaces the plan and asks is behaving as designed.

import (
	"context"
	"encoding/json"
	"fmt"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "storage_scan",
		Description: "Scan this machine for reclaimable build caches (Xcode DerivedData, Gradle, " +
			"CocoaPods, npm/yarn, Expo, Go build cache, Docker, Yaver task history), grouped by " +
			"project. Read-only. Returns filesystem usage plus a plan of what could be freed. " +
			"Every target is rebuildable — worst case for reclaiming one is a slower next build.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"refresh": map[string]interface{}{
					"type":        "boolean",
					"description": "Force a fresh scan instead of reusing the 60s-cached one.",
				},
			},
		},
		Handler: opsStorageScan,
	})

	registerOpsVerb(opsVerbSpec{
		Name: "storage_reclaim",
		Description: "Delete approved reclaimable caches by target id (ids come from storage_scan). " +
			"DESTRUCTIVE: requires confirm:true. Without confirm it returns code=confirm_required " +
			"and the dry-run plan — show that plan to the human and get an explicit yes before " +
			"re-calling with confirm:true.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Target ids from storage_scan.",
				},
				"confirm": map[string]interface{}{
					"type":        "boolean",
					"description": "Must be true to actually delete. Omit for a dry run.",
				},
			},
			"required": []string{"ids"},
		},
		Handler: opsStorageReclaim,
	})

	registerOpsVerb(opsVerbSpec{
		Name: "proc_top",
		Description: "Live process table sorted by CPU or memory — the structured equivalent of htop. " +
			"Returns typed rows (pid, name, cmd, cpuPct, rssMb, user, status), not ps text.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"sort":  map[string]interface{}{"type": "string", "enum": []string{"cpu", "mem", "pid", "name"}},
				"limit": map[string]interface{}{"type": "integer", "description": "Rows to return (default 20, max 500)."},
			},
		},
		Handler: opsProcTop,
	})

	registerOpsVerb(opsVerbSpec{
		Name: "proc_kill",
		Description: "Kill a process by pid. DESTRUCTIVE: requires confirm:true. Refuses to kill the " +
			"Yaver agent itself, its parent, or init — killing the agent from a remote surface would " +
			"sever the connection being used to kill it.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pid":     map[string]interface{}{"type": "integer"},
				"force":   map[string]interface{}{"type": "boolean", "description": "SIGKILL instead of SIGTERM."},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"required": []string{"pid"},
		},
		Handler: opsProcKill,
	})
}

func opsStorageScan(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Refresh bool `json:"refresh"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	scan := scanStorage(p.Refresh)
	return OpsResult{OK: true, Initial: scan}
}

func opsStorageReclaim(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		IDs     []string `json:"ids"`
		Confirm bool     `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	if len(p.IDs) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "ids is required; call storage_scan first"}
	}

	if !p.Confirm {
		// The dry run IS the plan. Hand it back with the refusal so the
		// caller has everything it needs to ask the human.
		plan := performStorageReclaim(p.IDs, true)
		return OpsResult{
			OK:      false,
			Code:    "confirm_required",
			Error:   fmt.Sprintf("storage_reclaim would free %s across %d target(s); re-call with confirm:true to proceed", plan.Freed, len(plan.Outcomes)),
			Initial: plan,
		}
	}

	res := performStorageReclaim(p.IDs, false)
	return OpsResult{OK: true, Initial: res}
}

func opsProcTop(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Sort  string `json:"sort"`
		Limit int    `json:"limit"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	table, err := sampleProcesses(context.Background(), p.Sort, p.Limit)
	if err != nil {
		return OpsResult{OK: false, Code: "internal", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: table}
}

func opsProcKill(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		PID     int32 `json:"pid"`
		Force   bool  `json:"force"`
		Confirm bool  `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	if p.PID <= 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "pid is required"}
	}
	if !p.Confirm {
		name := processNameFor(p.PID)
		return OpsResult{
			OK:      false,
			Code:    "confirm_required",
			Error:   fmt.Sprintf("proc_kill would terminate pid %d (%s); re-call with confirm:true to proceed", p.PID, name),
			Initial: map[string]interface{}{"pid": p.PID, "name": name, "force": p.Force},
		}
	}
	if err := killProcess(p.PID, p.Force); err != nil {
		return OpsResult{OK: false, Code: "refused", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"pid": p.PID, "killed": true}}
}
