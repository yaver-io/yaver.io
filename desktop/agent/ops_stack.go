package main

import "encoding/json"

type opsStackPayload struct {
	WorkDir string `json:"workDir,omitempty"`
}

type opsStackRunnableTarget struct {
	Target    DetectedTarget `json:"target"`
	OpsTarget string         `json:"opsTarget,omitempty"`
	Runnable  bool           `json:"runnable"`
	Reason    string         `json:"reason,omitempty"`
}

type opsStackInitial struct {
	Detection       *StackDetection          `json:"detection"`
	Changed         bool                     `json:"changed"`
	Warnings        []string                 `json:"warnings,omitempty"`
	RunnableTargets []opsStackRunnableTarget `json:"runnableTargets,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "stack_detect",
		Description: "Detect the canonical project stack for workDir (default .), returning the sanitized detection tree and machine-runnable target verdicts.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsStackHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsStackHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsStackPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}
	det, changed := stackDetectCached(workDir)
	safe := sanitizeStackDetection(det)
	return OpsResult{
		OK: true,
		Initial: opsStackInitial{
			Detection:       safe,
			Changed:         changed,
			Warnings:        append([]string(nil), safe.Warnings...),
			RunnableTargets: buildRunnableTargets(det),
		},
	}
}

func sanitizeStackDetection(in *StackDetection) *StackDetection {
	if in == nil {
		return nil
	}
	out := *in
	out.Root = ""
	if len(in.Packages) > 0 {
		out.Packages = make([]*StackDetection, 0, len(in.Packages))
		for _, pkg := range in.Packages {
			out.Packages = append(out.Packages, sanitizeStackDetection(pkg))
		}
	}
	return &out
}

func buildRunnableTargets(det *StackDetection) []opsStackRunnableTarget {
	if det == nil {
		return nil
	}
	var out []opsStackRunnableTarget
	for _, target := range det.Targets {
		opsTarget := firstOpsTarget(target)
		row := opsStackRunnableTarget{Target: target, OpsTarget: opsTarget}
		if opsTarget == "" {
			row.Reason = "no deploy action for this target"
			out = append(out, row)
			continue
		}
		cap := ComputeDeployCapability(opsTarget, "", nil)
		row.Runnable = cap.CanDeploy
		row.Reason = cap.Reason
		out = append(out, row)
	}
	return out
}

func firstOpsTarget(t DetectedTarget) string {
	for _, action := range t.Actions {
		if action.OpsTarget != "" {
			return action.OpsTarget
		}
	}
	return ""
}
