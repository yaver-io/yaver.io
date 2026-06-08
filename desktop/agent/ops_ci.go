package main

// ops_ci.go — control surface for the Model 1 self-hosted CI runner adapter
// (ci_selfhosted_runner.go). Self-registering ops verbs so the web/mobile/CLI
// drive it via callOps without any central-router edit (mirrors ops_git.go).
//
//   ci_runner_register {provider, target, scope?, host?, labels?, isolation?, where?, maxConcurrent?}
//   ci_runner_list     {}
//   ci_runner_remove   {key}            // key = "github:owner/repo"
//   ci_runner_status   {}               // registrations + live flag + local savings ledger
//
// Owner-only. The registration is HOST-LOCAL (never Convex); the forge token is
// minted just-in-time from the box's own git creds and never persisted.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_register",
		Description: "Register THIS box as a GitHub/GitLab self-hosted CI runner for a repo/org so the user's existing workflows (runs-on: [self-hosted, yaver]) run here and GitHub bills $0 for the minutes. Ephemeral per-job, container-isolated by default, private-repos-only. Token minted just-in-time from local git creds, never persisted/Convex. See docs/yaver-managed-cloud-ci-absorption.md.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"provider", "target"},
			"properties": map[string]interface{}{
				"provider":      map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}},
				"target":        map[string]interface{}{"type": "string", "description": "owner/repo (repo scope), org name (org scope), or gitlab numeric project id"},
				"scope":         map[string]interface{}{"type": "string", "enum": []string{"repo", "org"}, "description": "default repo"},
				"host":          map[string]interface{}{"type": "string", "description": "GHES / self-managed GitLab host; default github.com / gitlab.com"},
				"labels":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "extra runs-on labels on top of self-hosted,yaver,os:*,arch:*"},
				"isolation":     map[string]interface{}{"type": "string", "enum": []string{"container", "host"}, "description": "default container (needs docker). host = trusted private box only."},
				"where":         map[string]interface{}{"type": "string", "enum": []string{"self-hosted", "operator-fleet", "yaver-cloud"}, "description": "hardware class for metering. default self-hosted (free)."},
				"maxConcurrent": map[string]interface{}{"type": "integer", "description": "default 1"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCIRunnerRegisterHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_list",
		Description: "List the self-hosted CI runner registrations on this box (provider/target/labels/isolation + whether the supervisor is live).",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:    opsCIRunnerListHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_remove",
		Description: "Stop + forget one self-hosted CI runner registration by key (e.g. \"github:owner/repo\"). The ephemeral runner auto-deregisters from the forge after its current job.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"key"},
			"properties": map[string]interface{}{
				"key": map[string]interface{}{"type": "string", "description": "registration key, e.g. github:owner/repo"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCIRunnerRemoveHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_workflow_scaffold",
		Description: "Generate a GitHub Actions workflow pinned to runs-on:[self-hosted,yaver] for a deploy target (test|npm|testflight|play-internal) so the user's existing pipelines run on THEIR hardware for $0 (TestFlight on your own Mac = the big macOS-minutes win). Returns the YAML + the GitHub Actions secrets to set; pass write:true (+workDir) to write .github/workflows/<file>. See docs/yaver-managed-cloud-ci-absorption.md.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target"},
			"properties": map[string]interface{}{
				"target":    map[string]interface{}{"type": "string", "enum": []string{"test", "npm", "testflight", "play-internal"}},
				"workDir":   map[string]interface{}{"type": "string", "description": "project dir to write into (required when write:true)"},
				"write":     map[string]interface{}{"type": "boolean", "description": "default false = preview only"},
				"overwrite": map[string]interface{}{"type": "boolean", "description": "replace an existing workflow file"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCIWorkflowScaffoldHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_workflow_targets",
		Description: "List the scaffoldable self-hosted-runner workflow targets (test/npm/testflight/play-internal) with their runs-on labels + required GitHub Actions secrets — for the config UI dropdown.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:    opsCIWorkflowTargetsHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_status",
		Description: "CI runner status for this box: registrations + live supervisors + the local SAVINGS LEDGER (runs, charged ¢, what GitHub Actions would have billed, ¢ saved).",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:    opsCIRunnerStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func ciManagerFor(c OpsContext) (*CIManager, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "internal", Error: "ci_runner verb needs an HTTPServer context"}
	}
	return ensureCIManager(c.Server.ensureRunnerStore()), nil
}

func opsCIRunnerRegisterHandler(c OpsContext, payload json.RawMessage) OpsResult {
	mgr, errRes := ciManagerFor(c)
	if errRes != nil {
		return *errRes
	}
	var p struct {
		Provider      string   `json:"provider"`
		Target        string   `json:"target"`
		Scope         string   `json:"scope"`
		Host          string   `json:"host"`
		Labels        []string `json:"labels"`
		Isolation     string   `json:"isolation"`
		Where         string   `json:"where"`
		MaxConcurrent int      `json:"maxConcurrent"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	provider := CIProvider(strings.ToLower(strings.TrimSpace(p.Provider)))
	if provider != CIGitHub && provider != CIGitLab {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider must be github or gitlab"}
	}
	if strings.TrimSpace(p.Target) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "target required (owner/repo, org, or project id)"}
	}
	stored, err := mgr.Register(CIRunnerRegistration{
		Provider:      provider,
		Target:        strings.TrimSpace(p.Target),
		Scope:         strings.TrimSpace(p.Scope),
		Host:          strings.TrimSpace(p.Host),
		Labels:        p.Labels,
		Isolation:     CIIsolation(strings.TrimSpace(p.Isolation)),
		Where:         CIRunWhere(strings.TrimSpace(p.Where)),
		MaxConcurrent: p.MaxConcurrent,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "register_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"key":      stored.key(),
		"labels":   stored.runnerLabels(),
		"runsOn":   stored.runnerLabels(),
		"forgeUrl": stored.forgeURL(),
		"hint":     "Set `runs-on: [self-hosted, yaver]` in your workflow. Token is minted per-job from this box's git creds (run git_connect first if missing).",
	}}
}

func opsCIRunnerListHandler(c OpsContext, _ json.RawMessage) OpsResult {
	mgr, errRes := ciManagerFor(c)
	if errRes != nil {
		return *errRes
	}
	regs := mgr.regs.List()
	rows := make([]map[string]interface{}, 0, len(regs))
	for _, r := range regs {
		rows = append(rows, map[string]interface{}{
			"key":           r.key(),
			"provider":      string(r.Provider),
			"target":        r.Target,
			"scope":         r.Scope,
			"labels":        r.runnerLabels(),
			"isolation":     string(r.Isolation),
			"where":         string(r.Where),
			"maxConcurrent": r.MaxConcurrent,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"registrations": rows, "count": len(rows)}}
}

func opsCIRunnerRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	mgr, errRes := ciManagerFor(c)
	if errRes != nil {
		return *errRes
	}
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Key) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "key required (e.g. github:owner/repo)"}
	}
	if err := mgr.Unregister(strings.TrimSpace(p.Key)); err != nil {
		return OpsResult{OK: false, Code: "remove_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": p.Key}}
}

func opsCIRunnerStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	mgr, errRes := ciManagerFor(c)
	if errRes != nil {
		return *errRes
	}
	return OpsResult{OK: true, Initial: mgr.Status()}
}

func opsCIWorkflowScaffoldHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Target    string `json:"target"`
		WorkDir   string `json:"workDir"`
		Write     bool   `json:"write"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	relPath, content, secrets, err := scaffoldCIWorkflow(p.Target, p.WorkDir, p.Write, p.Overwrite)
	if err != nil {
		return OpsResult{OK: false, Code: "scaffold_failed", Error: err.Error(), Initial: map[string]interface{}{
			"path": relPath, "content": content, "secrets": secrets,
		}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"path":    relPath,
		"content": content,
		"secrets": secrets,
		"written": p.Write,
		"hint":    "Commit this workflow, set the listed secrets in your repo (Settings → Secrets → Actions), register a runner with ci_runner_register, then push the tag — the build runs on your box.",
	}}
}

func opsCIWorkflowTargetsHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	tpls := ciWorkflowTemplates()
	rows := make([]map[string]interface{}, 0, len(tpls))
	for _, k := range ciWorkflowTargets() {
		tpl := tpls[k]
		rows = append(rows, map[string]interface{}{
			"target":      tpl.Target,
			"file":        tpl.File,
			"runsOn":      tpl.RunsOn,
			"secrets":     tpl.Secrets,
			"description": tpl.Description,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"targets": rows}}
}
