package main

// Evidence attachment + deploy preflight for runtime turns.
//
// DEPLOY IS NEVER AUTOMATIC, and this file does not deploy. It runs the checks
// and hands back the exact command to run. That is deliberate:
//
//   - A voice surface cannot meaningfully consent to a store submission. "Ship
//     it" spoken at a steering wheel must not become a TestFlight upload.
//   - TestFlight allows ~15-20 uploads/app/day and has NO rollback — a bad
//     build can only be superseded. An accidental upload costs a whole slot and
//     cannot be undone.
//   - Every deploy is metered somewhere. A verb that deploys on a voice command
//     is a verb that sprays deploys.
//
// So the contract is: preflight reports readiness, the turn moves to
// `ready_to_deploy`, and a human runs the printed command. If you ever add
// execution here, it must require a typed confirmation from a full-visual
// surface, never a spoken one.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn_evidence",
		Description: "Attach evidence (screenshot, clip, console log, route) to an existing runtime turn. Accepts {turnId, evidence:[{kind,ref,screen,sourceSurface,durationMs}]}. Refs only — blobs stay on the device/box.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"itemId":   map[string]interface{}{"type": "string"},
				"turnId":   map[string]interface{}{"type": "string"},
				"evidence": map[string]interface{}{"type": "array"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnEvidenceHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "runtime_turn_deploy_preflight",
		Description: "Check whether a runtime turn is safe to ship and return the exact deploy command. NEVER deploys — deploy stays a human action. Moves the turn to ready_to_deploy when the checks pass.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"itemId": map[string]interface{}{"type": "string"},
				"turnId": map[string]interface{}{"type": "string"},
				"target": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRuntimeTurnDeployPreflightHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsRuntimeTurnEvidenceHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		ItemID   string                `json:"itemId"`
		TurnID   string                `json:"turnId"`
		Evidence []RuntimeTurnEvidence `json:"evidence"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	id := firstNonEmptyStr(strings.TrimSpace(req.ItemID), strings.TrimSpace(req.TurnID))
	if id == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "itemId or turnId is required"}
	}
	if len(req.Evidence) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "evidence is required"}
	}
	if _, ok := runtimeQueue.get(c.ActorUserID, id); !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "runtime turn not found"}
	}
	item, _ := runtimeQueue.update(id, func(i *RuntimeTurnQueueItem) {
		i.Evidence = append(i.Evidence, req.Evidence...)
	})
	return OpsResult{OK: true, Initial: runtimeTurnResponseFromItem(item, item.Spoken, true)}
}

// runtimeDeployPreflight is the result of asking "is this shippable?" without
// shipping it.
type runtimeDeployPreflight struct {
	OK       bool     `json:"ok"`
	Target   string   `json:"target"`
	Ready    bool     `json:"ready"`
	Blockers []string `json:"blockers,omitempty"`
	Command  string   `json:"command,omitempty"`
	Note     string   `json:"note"`
	Spoken   string   `json:"spoken,omitempty"`
	State    string   `json:"state"`
	TurnID   string   `json:"turnId,omitempty"`
}

// runtimeDeployCommandFor returns the command a human runs for a target. These
// mirror the deploy table in CLAUDE.md; they are printed, never executed.
func runtimeDeployCommandFor(target string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "testflight", "ios", "apple":
		return "./scripts/deploy-testflight.sh", true
	case "play", "playstore", "android", "google":
		return "JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh", true
	case "npm", "cli":
		return "gh workflow run release-cli.yml --ref main   # then publish npm from a Mac", true
	case "web", "cloudflare":
		return "./scripts/deploy-web.sh", true
	case "convex", "backend":
		return "cd backend && npx convex deploy --yes", true
	default:
		return "", false
	}
}

// runtimeTurnDeployBlockers lists every reason this turn is not shippable.
// Returning the FULL list matters: fixing one blocker and rediscovering the
// next one at the next preflight is how a 15-upload daily budget gets burned.
func runtimeTurnDeployBlockers(item RuntimeTurnQueueItem) []string {
	var blockers []string
	switch item.State {
	case runtimeQueueStateReadyToTest, runtimeQueueStateReadyToDeploy, runtimeQueueStateDone:
		// fine
	case runtimeQueueStateFailed:
		blockers = append(blockers, "the work failed; nothing to ship")
	case runtimeQueueStateCaptured:
		blockers = append(blockers, "this is still an unstarted idea")
	case runtimeQueueStateNeedsInput:
		blockers = append(blockers, "the runner is waiting on your answer")
	default:
		blockers = append(blockers, "the work is still running")
	}
	// The whole point of the verify chain: do not ship something no device has
	// ever successfully run.
	if item.TestTarget == nil || item.TestTarget.State == "" || item.TestTarget.State == "unverified" {
		blockers = append(blockers, "not tested on a device yet — run runtime_turn_verify first")
	} else if item.TestTarget.State == "unreachable" {
		blockers = append(blockers, "the last reload reached no device")
	} else if item.TestTarget.State == "failed" {
		blockers = append(blockers, "the last device reload failed: "+item.TestTarget.Detail)
	}
	return blockers
}

func opsRuntimeTurnDeployPreflightHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		ItemID string `json:"itemId"`
		TurnID string `json:"turnId"`
		Target string `json:"target"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	id := firstNonEmptyStr(strings.TrimSpace(req.ItemID), strings.TrimSpace(req.TurnID))
	if id == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "itemId or turnId is required"}
	}
	item, ok := runtimeQueue.get(c.ActorUserID, id)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "runtime turn not found"}
	}

	target := strings.TrimSpace(req.Target)
	cmd, known := runtimeDeployCommandFor(target)
	if !known {
		return OpsResult{OK: false, Code: "unknown_target", Error: "unknown deploy target: " + target}
	}
	if target == "" {
		target = "testflight"
	}

	blockers := runtimeTurnDeployBlockers(item)
	ready := len(blockers) == 0

	out := runtimeDeployPreflight{
		OK:       true,
		Target:   target,
		Ready:    ready,
		Blockers: blockers,
		TurnID:   item.ItemID,
		Note:     "Yaver does not deploy for you. Run the command yourself when you're ready.",
	}
	if ready {
		out.Command = cmd
		out.Spoken = "Ready to ship. I've put the command on your phone."
		out.State = runtimeQueueStateReadyToDeploy
		runtimeQueue.update(item.ItemID, func(i *RuntimeTurnQueueItem) {
			i.State = runtimeQueueStateReadyToDeploy
			i.Spoken = out.Spoken
		})
	} else {
		out.Spoken = "Not ready: " + blockers[0]
		out.State = item.State
	}
	return OpsResult{OK: true, Initial: out}
}
