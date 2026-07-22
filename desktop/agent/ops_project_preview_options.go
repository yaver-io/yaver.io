package main

// The surface-facing door to the capability detection layer.
//
// One verb, every surface. Mobile, web, tvOS and glass all ask the agent what a
// project supports instead of each maintaining its own framework conditionals.
// The agent can see the project on disk; the surfaces cannot.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "project_preview_options",
		Description: "Detect what preview/run options a project actually supports. Accepts {workDir|projectName, framework?, hasPairedDevice?}. " +
			"Returns the detected framework plus the option list a surface should render. Hermes is only ever offered for react-native/expo.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir":         map[string]interface{}{"type": "string"},
				"projectName":     map[string]interface{}{"type": "string"},
				"projectPath":     map[string]interface{}{"type": "string"},
				"framework":       map[string]interface{}{"type": "string"},
				"hasPairedDevice": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsProjectPreviewOptionsHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsProjectPreviewOptionsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		WorkDir         string `json:"workDir"`
		ProjectName     string `json:"projectName"`
		ProjectPath     string `json:"projectPath"`
		Framework       string `json:"framework"`
		HasPairedDevice bool   `json:"hasPairedDevice"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}

	workDir := firstNonEmptyStr(
		strings.TrimSpace(req.WorkDir),
		strings.TrimSpace(req.ProjectPath),
	)
	// A name is resolvable to a path via the same lookup /dev/build-native uses,
	// so a caller that only knows the project name still gets real detection
	// rather than falling back to its own guess.
	if workDir == "" && strings.TrimSpace(req.ProjectName) != "" {
		if ref, err := resolveProjectRef(req.ProjectName, ""); err == nil {
			workDir = ref.Path
		}
	}

	caps := DetectProjectPreviewCapabilities(workDir, req.Framework, req.HasPairedDevice)
	return OpsResult{OK: true, Initial: caps}
}
