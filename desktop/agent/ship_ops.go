package main

import (
	"encoding/json"
	"strings"
	"time"
)

// The ship verb. Registered as ops so every surface — phone, watch, web, voice —
// gets the barrier for free. That is the actual point: the utterance at the top
// of ship.go is the product, and the CLI is just its typed form.

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "ship",
		Description: "Freeze every autorun, converge main, deploy once, then resume the fleet. " +
			"Sends `toparla` to every live runner first (wrap up to a build-OK state — not finished, just not build-breaking), " +
			"freezes the loops so none can start a new iteration, waits up to toparlaTimeout (default 10m) for them to park, " +
			"pins main to a SHA, optionally repairs it until it compiles, deploys ONLY the targets the diff since the last ship touched, " +
			"then thaws and sends `devam` so the runners know main moved. " +
			"Freezing is instant; draining is not — a runner mid-kick may take up to 30m, so a toparla timeout deploys the pinned SHA and lets that work land on the next ship. " +
			"Deploy always runs on THIS machine (TestFlight cannot upload from anywhere else); freezeMachines names the machines running the autoruns. " +
			"On any failure the fleet is thawed and a notification is sent — a frozen fleet is worse than a failed deploy.",
		Schema:  shipSchema(),
		Handler: opsShipHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ship_status",
		Description: "Inspect a running or finished ship: its phases, the pinned SHA, which targets were detected and why, the drain state, and the deploy result. Empty id lists all.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "additionalProperties": false},
		Handler:     opsShipStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ship_prompts",
		Description: "List the named barrier prompts (toparla, devam) and their text. Any other string passed as ship's prompt is used verbatim as an ad-hoc prompt.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false},
		Handler:     opsShipPromptsHandler,
	})
}

func shipSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"freezeMachines": map[string]interface{}{
			"type": "array", "items": map[string]interface{}{"type": "string"},
			"description": "Machines running autoruns to freeze (deviceId|alias|primary), e.g. [\"mini\"]. This machine is ALWAYS frozen too — it is where the deploy runs. An unreachable machine aborts the ship rather than deploying while it keeps pushing.",
		},
		"toparlaTimeout": map[string]interface{}{"type": "string", "default": "10m", "description": "How long to let runners reach a build-OK state before deploying anyway. Expiry is not an error: the deploy pins a SHA, so in-flight work simply lands on the next ship."},
		"prompt":         map[string]interface{}{"type": "string", "description": "Wrap-up prompt: a library name (toparla) or ad-hoc text. Default toparla."},
		"noPrompt":       map[string]interface{}{"type": "boolean", "description": "Skip toparla/devam entirely; rely on the gate alone. Correct but slow — the drain then takes as long as the longest in-flight kick."},
		"repair":         map[string]interface{}{"type": "boolean", "default": true, "description": "If main does not build, run a bounded autorun to fix it before deploying. Requires a tasks/ship-repair.md task file. Refuses to deploy a red main either way."},
		"repairMaxIters": map[string]interface{}{"type": "integer", "minimum": 1, "default": 3},
		"targets":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Override target detection. Names: convex, web-cloudflare, cli-npm, testflight-ios, playstore-android. Needed for the FIRST ship, which refuses to infer targets with no ship/last marker."},
		"dryRun":         map[string]interface{}{"type": "boolean", "description": "Freeze, drain, detect and report — but do not deploy. Still thaws."},
		"workDir":        map[string]interface{}{"type": "string"},
	}, "additionalProperties": false}
}

type shipPayload struct {
	FreezeMachines []string `json:"freezeMachines"`
	ToparlaTimeout string   `json:"toparlaTimeout"`
	Prompt         string   `json:"prompt"`
	NoPrompt       bool     `json:"noPrompt"`
	Repair         *bool    `json:"repair"`
	RepairMaxIters int      `json:"repairMaxIters"`
	Targets        []string `json:"targets"`
	DryRun         bool     `json:"dryRun"`
	WorkDir        string   `json:"workDir"`
}

func opsShipHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p shipPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	opts := shipOptions{
		FreezeMachines: p.FreezeMachines,
		Prompt:         p.Prompt,
		NoPrompt:       p.NoPrompt,
		Repair:         true,
		RepairMaxIters: p.RepairMaxIters,
		Targets:        p.Targets,
		DryRun:         p.DryRun,
		WorkDir:        p.WorkDir,
	}
	if p.Repair != nil {
		opts.Repair = *p.Repair
	}
	if t := strings.TrimSpace(p.ToparlaTimeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid toparlaTimeout: " + err.Error()}
		}
		opts.ToparlaTimeout = d
	}

	sess, err := shipSessions.start(c.Ctx, c.Server, opts)
	if err != nil {
		return OpsResult{OK: false, Code: "ship_busy", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"id":     sess.ID,
		"status": sess.Status,
		"note":   "Ship started. It runs detached — poll ship_status, or wait for the notification. The fleet thaws on every exit path, including failure.",
	}}
}

func opsShipStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	sessions, err := shipSessions.status(strings.TrimSpace(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"ships": sessions}}
}

func opsShipPromptsHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	prompts := map[string]string{}
	for _, n := range shipPromptNames() {
		prompts[n] = shipPromptLibrary[n]
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"names":   shipPromptNames(),
		"prompts": prompts,
		"note":    "Any string that is not a library name is used verbatim as an ad-hoc prompt.",
	}}
}
