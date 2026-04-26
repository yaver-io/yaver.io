package main

// ops_vibe_preview.go — verb "vibe_preview": single ops surface for the
// vibe-preview pipeline so mobile-headless + AI runners can drive
// every operation through one verb instead of learning seven REST
// endpoints. Pairs the existing dedicated MCP tools (vibe_preview_*)
// — same logic, op-discriminated payload.
//
// Payload shape:
//   {op:"start",   project:"web", target_url:"http://127.0.0.1:3000",
//                  mode?:"live"|"change-only"|"summary-only", profile?}
//   {op:"stop",    project:"web"}
//   {op:"status"}
//   {op:"snapshot",project:"web"}
//   {op:"clip",    project:"web", source?:"sim-ios"|"sim-android"|"phone",
//                  duration_max_sec?:int}
//   {op:"clips",   project:"web"}
//   {op:"summaries",project:"web", limit?:int}

import (
	"encoding/json"
)

type opsVibePreviewPayload struct {
	Op             string `json:"op"`
	Project        string `json:"project,omitempty"`
	TargetURL      string `json:"target_url,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Profile        string `json:"profile,omitempty"`
	Source         string `json:"source,omitempty"`
	DurationMaxSec int    `json:"duration_max_sec,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "vibe_preview",
		Description: "Drive the vibe-preview pipeline: start/stop a session, force a snapshot, record a clip, list clips, read recent summaries. Op-discriminated payload. Same surface as the dedicated MCP tools.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":               map[string]interface{}{"type": "string", "enum": []string{"start", "stop", "status", "snapshot", "clip", "clips", "summaries"}},
				"project":          map[string]interface{}{"type": "string"},
				"target_url":       map[string]interface{}{"type": "string"},
				"mode":             map[string]interface{}{"type": "string", "enum": []string{"live", "change-only", "summary-only"}},
				"profile":          map[string]interface{}{"type": "string"},
				"source":           map[string]interface{}{"type": "string", "enum": []string{"sim-ios", "sim-android", "phone", "browser"}},
				"duration_max_sec": map[string]interface{}{"type": "integer"},
				"limit":            map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsVibePreviewHandler,
		Streaming:  false,
		AllowGuest: false, // capture/start are mutating; status reads inherit owner-auth here
	})
}

func opsVibePreviewHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsVibePreviewPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}

	mgr := ActiveVibePreviewManager()
	if mgr == nil && c.Server != nil {
		mgr = c.Server.vibePreviewMgr
	}
	if mgr == nil {
		return OpsResult{OK: false, Code: "not_found", Error: "vibe-preview manager not initialised"}
	}

	switch p.Op {
	case "start":
		sess, err := mgr.Start(VibePreviewStartOpts{
			Project:   p.Project,
			TargetURL: p.TargetURL,
			Mode:      VibePreviewMode(p.Mode),
			Profile:   p.Profile,
		})
		if err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": sess}}

	case "stop":
		if err := mgr.Stop(p.Project); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"stopped": p.Project}}

	case "status":
		return OpsResult{OK: true, Initial: map[string]interface{}{"sessions": mgr.Status()}}

	case "snapshot":
		rec, err := mgr.Snapshot(p.Project)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"seq":  rec.Seq,
			"hash": rec.Hash,
			"size": len(rec.Bytes),
		}}

	case "clip":
		rec, err := mgr.StartClip(VibeClipStartOpts{
			Project:        p.Project,
			Source:         VibeClipSource(p.Source),
			DurationMaxSec: p.DurationMaxSec,
		})
		if err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"clip": rec}}

	case "clips":
		return OpsResult{OK: true, Initial: map[string]interface{}{"clips": mgr.ListClips(p.Project)}}

	case "summaries":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"summaries": mgr.ListSummaries(p.Project, p.Limit),
		}}

	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
	}
}
