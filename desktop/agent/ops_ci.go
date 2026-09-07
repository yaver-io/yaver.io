package main

// ops_ci.go — control surface for the Model 1 self-hosted CI runner adapter
// (ci_selfhosted_runner.go). Self-registering ops verbs so the web/mobile/CLI
// drive it via callOps without any central-router edit (mirrors ops_git.go).
//
//   ci_runner_register {provider, target, scope?, host?, labels?, isolation?, where?}
//   ci_runner_preflight {same payload}       // no runner is created
//   ci_runner_list     {}
//   ci_runner_remove   {key}            // key = "github:owner/repo"
//   ci_runner_status   {}               // registrations + live flag + local savings ledger
//
// Owner-only. The registration is HOST-LOCAL (never Convex); the forge token is
// minted just-in-time from the box's own git creds and never persisted.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:           "ci_runner_preflight",
		Description:    "Probe a proposed GitHub/GitLab runner registration without creating or persisting it. Verifies local isolation, forge credentials, exact project resolution, and private visibility; returns the enforced labels/policy or a typed refusal.",
		Schema:         ciRunnerRegistrationSchema(),
		Handler:        opsCIRunnerPreflightHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:           "ci_runner_register",
		Description:    "Register THIS box as a project-scoped GitHub/GitLab self-hosted CI runner. Registration succeeds only after a real private-project and runner-token probe. GitLab runners are locked, tagged, protected-ref-only, and removed after their one job. Container-isolated by default; host mode is for trusted private projects on dedicated boxes.",
		Schema:         ciRunnerRegistrationSchema(),
		Handler:        opsCIRunnerRegisterHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_list",
		Description: "List self-hosted CI runner registrations on this box. Runtime presence is supervisorActive; state/lastError report whether the forge-facing operation is actually healthy.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIRunnerListHandler,
		Streaming:      false,
		AllowCompanion: false,
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
		Handler:        opsCIRunnerRemoveHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_workflow_scaffold",
		Description: "Generate a GitHub Actions workflow pinned to runs-on:[self-hosted,yaver] for a deploy target (test|npm|testflight|play-internal) so the user's existing pipelines run on THEIR hardware for $0 (TestFlight on your own Mac = the big macOS-minutes win). Returns the YAML + the GitHub Actions secrets to set; pass write:true (+workDir) to write .github/workflows/<file>. See docs/yaver-managed-cloud-ci-absorption.md.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"target"},
			"properties": map[string]interface{}{
				"provider":  map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}, "description": "default github"},
				"target":    map[string]interface{}{"type": "string", "enum": []string{"test", "npm", "testflight", "play-internal"}},
				"workDir":   map[string]interface{}{"type": "string", "description": "project dir to write into (required when write:true)"},
				"write":     map[string]interface{}{"type": "boolean", "description": "default false = preview only"},
				"overwrite": map[string]interface{}{"type": "boolean", "description": "replace an existing workflow file"},
			},
			"additionalProperties": false,
		},
		Handler:        opsCIWorkflowScaffoldHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_workflow_targets",
		Description: "List the scaffoldable self-hosted-runner workflow targets (test/npm/testflight/play-internal) with their runs-on labels + required GitHub Actions secrets — for the config UI dropdown.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIWorkflowTargetsHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_jail_setup",
		Description: "Operator-fleet: create the CI network jail on THIS box — a dedicated docker bridge + DOCKER-USER iptables rules that block jailed CI jobs from reaching the LAN (RFC1918/link-local/CGNAT) while still allowing the public internet (npm/github). Linux-only firewall; container CI runs then auto-join it. See docs/yaver-public-compute-operator-fleet.md.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIJailSetupHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_jail_status",
		Description: "Report the CI network jail state on this box: whether the jail docker network exists + whether the egress firewall rules are active (linux).",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIJailStatusHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_jail_teardown",
		Description: "Remove the CI network jail (firewall rules + docker network + marker) from this box.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIJailTeardownHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ci_runner_status",
		Description: "CI runner status for this box: registrations + honest supervisor state/last error + the local savings ledger. supervisorActive means only that the loop exists; state and lastError carry operational health.",
		Schema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false,
		},
		Handler:        opsCIRunnerStatusHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
}

func ciRunnerRegistrationSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":     "object",
		"required": []string{"provider", "target"},
		"properties": map[string]interface{}{
			"provider":      map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}},
			"target":        map[string]interface{}{"type": "string", "description": "GitHub owner/repo or GitLab namespace/project path (numeric GitLab id also accepted)"},
			"scope":         map[string]interface{}{"type": "string", "enum": []string{"repo", "org"}, "description": "default repo; org is refused while strict private-project enforcement is active"},
			"host":          map[string]interface{}{"type": "string", "description": "GHES / self-managed GitLab host; default github.com / gitlab.com"},
			"labels":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "extra runs-on/tags labels on top of self-hosted,yaver,os:*,arch:*"},
			"isolation":     map[string]interface{}{"type": "string", "enum": []string{"container", "host"}, "description": "default container (needs a working Docker daemon). host = trusted private project on a dedicated box only"},
			"where":         map[string]interface{}{"type": "string", "enum": []string{"self-hosted", "operator-fleet", "yaver-cloud"}, "description": "hardware class for metering; default self-hosted"},
			"maxConcurrent": map[string]interface{}{"type": "integer", "enum": []int{1}, "description": "currently exactly 1; use separate registrations for separate projects"},
		},
		"additionalProperties": false,
	}
}

func ciManagerFor(c OpsContext) (*CIManager, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "internal", Error: "ci_runner verb needs an HTTPServer context"}
	}
	return ensureCIManager(c.Server.ensureRunnerStore()), nil
}

type opsCIRunnerRegistrationPayload struct {
	Provider      string   `json:"provider"`
	Target        string   `json:"target"`
	Scope         string   `json:"scope"`
	Host          string   `json:"host"`
	Labels        []string `json:"labels"`
	Isolation     string   `json:"isolation"`
	Where         string   `json:"where"`
	MaxConcurrent int      `json:"maxConcurrent"`
}

func decodeCIRunnerRegistration(payload json.RawMessage) (CIRunnerRegistration, error) {
	var p opsCIRunnerRegistrationPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return CIRunnerRegistration{}, err
	}
	return normalizeCIRegistration(CIRunnerRegistration{
		Provider:      CIProvider(strings.ToLower(strings.TrimSpace(p.Provider))),
		Target:        strings.TrimSpace(p.Target),
		Scope:         strings.TrimSpace(p.Scope),
		Host:          strings.TrimSpace(p.Host),
		Labels:        p.Labels,
		Isolation:     CIIsolation(strings.TrimSpace(p.Isolation)),
		Where:         CIRunWhere(strings.TrimSpace(p.Where)),
		MaxConcurrent: p.MaxConcurrent,
	})
}

func opsCIRunnerPreflightHandler(c OpsContext, payload json.RawMessage) OpsResult {
	reg, err := decodeCIRunnerRegistration(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	prepared, err := prepareCIRegistration(ctx, reg)
	if err != nil {
		return ciRegistrationFailureResult("ci_preflight_failed", err)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"ready":         true,
		"provider":      prepared.Provider,
		"target":        prepared.Target,
		"projectId":     prepared.ProjectID,
		"labels":        prepared.runnerLabels(),
		"isolation":     prepared.Isolation,
		"privateOnly":   true,
		"protectedOnly": prepared.Provider == CIGitLab,
	}}
}

func opsCIRunnerRegisterHandler(c OpsContext, payload json.RawMessage) OpsResult {
	mgr, errRes := ciManagerFor(c)
	if errRes != nil {
		return *errRes
	}
	reg, err := decodeCIRunnerRegistration(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	stored, err := mgr.Register(ctx, reg)
	if err != nil {
		return ciRegistrationFailureResult("register_failed", err)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"key":       stored.key(),
		"projectId": stored.ProjectID,
		"labels":    stored.runnerLabels(),
		"runsOn":    stored.runnerLabels(),
		"forgeUrl":  stored.forgeURL(),
		"hint":      ciRunnerWorkflowHint(stored),
	}}
}

func ciRegistrationFailureResult(fallbackCode string, err error) OpsResult {
	result := OpsResult{OK: false, Code: fallbackCode, Error: err.Error()}
	var failure *ciPreflightFailure
	if errors.As(err, &failure) {
		result.Code = failure.Code
		result.Initial = failure.Initial
	}
	return result
}

func ciRunnerWorkflowHint(r CIRunnerRegistration) string {
	if r.Provider == CIGitLab {
		return "Use matching `tags:` in .gitlab-ci.yml and protect the release branch/tag. This runner refuses untagged and unprotected jobs."
	}
	return "Set `runs-on: [self-hosted, yaver]` in the GitHub workflow. Only this verified private repository is registered."
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
			"privateOnly":   r.PrivateOnly,
			"projectId":     r.ProjectID,
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
		Provider  string `json:"provider"`
		Target    string `json:"target"`
		WorkDir   string `json:"workDir"`
		Write     bool   `json:"write"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	provider := CIProvider(strings.ToLower(strings.TrimSpace(p.Provider)))
	if provider == "" {
		provider = CIGitHub
	}
	relPath, content, secrets, err := scaffoldCIWorkflowFor(provider, p.Target, p.WorkDir, p.Write, p.Overwrite)
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
		"hint":    ciWorkflowInstallHint(provider, relPath),
	}}
}

func ciWorkflowInstallHint(provider CIProvider, relPath string) string {
	if provider == CIGitLab {
		return "Add `include: [{ local: '" + filepath.ToSlash(relPath) + "' }]` to .gitlab-ci.yml, protect the release branch/tag, set any listed CI/CD variables, and register the tagged runner. Store deploy jobs are manual by design."
	}
	return "Commit this workflow, set the listed Actions secrets, register the repository runner, then invoke the manual release workflow."
}

func opsCIJailSetupHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	res, err := setupCIJail(context.Background())
	if err != nil {
		return OpsResult{OK: false, Code: "jail_setup_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: res}
}

func opsCIJailStatusHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	ctx := context.Background()
	subnet, _, err := ensureCIJailNetwork(ctx)
	present := err == nil && subnet != ""
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"network":        ciJailNetworkName,
		"networkPresent": present,
		"subnet":         subnet,
		"firewallActive": present && ciJailFirewallActive(ctx, subnet),
		"activeForRuns":  ciJailNetwork(),
	}}
}

func opsCIJailTeardownHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	if err := teardownCIJail(context.Background()); err != nil {
		return OpsResult{OK: false, Code: "jail_teardown_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": ciJailNetworkName}}
}

func opsCIWorkflowTargetsHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	tpls := ciWorkflowTemplates()
	rows := make([]map[string]interface{}, 0, len(tpls))
	for _, k := range ciWorkflowTargets() {
		tpl := tpls[k]
		rows = append(rows, map[string]interface{}{
			"target":      tpl.Target,
			"file":        tpl.File,
			"gitlabFile":  filepath.Join(".gitlab", tpl.GitLabFile),
			"runsOn":      tpl.RunsOn,
			"secrets":     tpl.Secrets,
			"description": tpl.Description,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"targets": rows}}
}
