package main

// ops_workspace.go — verb "workspace": monorepo manifest over MCP.
//
// ops("local", "workspace", {op: "init"})    — wire every app
// ops("local", "workspace", {op: "list"})    — apps declared in the manifest
// ops("local", "workspace", {op: "status"})  — on-disk + env + init.md state
// ops("local", "workspace", {op: "scaffold"}) — generate a starter manifest
//
// Lets an AI coding agent onboard an N-app monorepo in a single MCP
// call instead of juggling init_project per app.

import (
	"encoding/json"
	"os"
)

type opsWorkspacePayload struct {
	// Op: "init" | "list" | "status" | "scaffold". Required.
	Op string `json:"op"`
	// Root: repo root override. Defaults to agent CWD.
	Root string `json:"root,omitempty"`
	// ManifestFile: path override (absolute or relative to Root).
	ManifestFile string `json:"manifestFile,omitempty"`
	// Init-only knobs.
	Force          bool   `json:"force,omitempty"`
	DryRun         bool   `json:"dryRun,omitempty"`
	OnlyApp        string `json:"onlyApp,omitempty"`
	AutoinitPrompt bool   `json:"autoinitPrompt,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "workspace",
		Description: "Monorepo manifest engine. op=init wires every declared app (init.md scaffolds, env check, per-app primary device). op=list returns apps, op=status returns runtime state, op=scaffold generates a starter yaver.workspace.yaml by detecting apps on disk.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":             map[string]interface{}{"type": "string", "enum": []string{"init", "list", "status", "scaffold"}},
				"root":           map[string]interface{}{"type": "string"},
				"manifestFile":   map[string]interface{}{"type": "string"},
				"force":          map[string]interface{}{"type": "boolean"},
				"dryRun":         map[string]interface{}{"type": "boolean"},
				"onlyApp":        map[string]interface{}{"type": "string"},
				"autoinitPrompt": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsWorkspaceHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsWorkspaceHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsWorkspacePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}
	root := p.Root
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		} else {
			root = "."
		}
	}
	// ManifestFile override is session-wide (package-global var) — restore on return so we don't poison
	// subsequent calls that expect the default path.
	prevOverride := WorkspaceManifestPathOverride
	if p.ManifestFile != "" {
		WorkspaceManifestPathOverride = p.ManifestFile
	}
	defer func() { WorkspaceManifestPathOverride = prevOverride }()

	switch p.Op {
	case "scaffold":
		data, m, err := ScaffoldWorkspaceManifest(root)
		if err != nil {
			return OpsResult{OK: false, Code: "scaffold_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"yaml":     string(data),
			"detected": m.Apps,
			"hint":     "write the yaml to `yaver.workspace.yaml` at the repo root, then call ops workspace { op: \"init\" }",
		}}
	case "list":
		m, err := LoadWorkspaceManifest(root)
		if err != nil {
			return OpsResult{OK: false, Code: "manifest_missing", Error: err.Error()}
		}
		order, _ := TopoSortApps(m)
		byName := map[string]WorkspaceApp{}
		for _, a := range m.Apps {
			byName[a.Name] = a
		}
		ordered := make([]WorkspaceApp, 0, len(order))
		for _, n := range order {
			ordered = append(ordered, byName[n])
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"name":   m.Name,
			"count":  len(ordered),
			"apps":   ordered,
			"shared": m.Shared,
		}}
	case "status":
		m, err := LoadWorkspaceManifest(root)
		if err != nil {
			return OpsResult{OK: false, Code: "manifest_missing", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"name":   m.Name,
			"status": CollectWorkspaceStatus(m, root),
		}}
	case "init":
		m, err := LoadWorkspaceManifest(root)
		if err != nil {
			return OpsResult{OK: false, Code: "manifest_missing", Error: err.Error()}
		}
		actions := RunWorkspaceInit(m, root, WorkspaceInitOptions{
			Force:          p.Force,
			DryRun:         p.DryRun,
			OnlyApp:        p.OnlyApp,
			AutoinitPrompt: p.AutoinitPrompt,
		})
		// Summarise by status so callers can branch without a full scan.
		counts := map[string]int{}
		for _, a := range actions {
			counts[a.Status]++
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"counts":  counts,
			"actions": actions,
		}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
	}
}
