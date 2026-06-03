package main

// company_ai_local.go — air-gap / LAN-only control-plane seam.
//
// Yaver's company AI policy normally lives in hosted Convex and is read via
// /company-ai/resolve. An air-gapped or egress-restricted tenant can't reach
// Convex, so this file lets the SAME policy live in a local file on the runtime
// and be resolved + enforced entirely offline:
//
//	policy source : $YAVER_COMPANY_AI_POLICY, else /etc/yaver/company-ai-policy.json,
//	                else <ConfigDir>/company-ai-policy.json
//	shape         : the CompanyAIOptions JSON (same file the dashboard writes),
//	                so an admin can export from Convex and drop it on the box.
//
// What this gives an air-gapped tenant TODAY:
//   - offline policy resolution (runner / provider / approvals / dataPolicy)
//     via ResolveCompanyAILocal + the /company-ai/resolve-local route
//   - offline dataPolicy enforcement: redaction is already token-scope driven;
//     retention (retentionDays) is enforced here by the local prune loop
//   - LAN device discovery already works offline via the UDP beacon
//
// Still staged (documented in docs/yaver-onprem-airgap.md): fully offline AUTH.
// The agent still validates sessions against Convex unless a pre-provisioned
// local token is used. That's the remaining piece of a true zero-Convex plane.

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LocalProviderDef mirrors the SDK/Convex ProviderDef (config only, no key).
type LocalProviderDef struct {
	ID            string   `json:"id"`
	Label         string   `json:"label,omitempty"`
	BaseURL       string   `json:"baseUrl,omitempty"`
	Models        []string `json:"models,omitempty"`
	KeyPolicy     string   `json:"keyPolicy,omitempty"`
	KeyConfigured *bool    `json:"keyConfigured,omitempty"`
}

// LocalCompanyAIPolicy is the offline mirror of CompanyAIOptions. Only the
// fields the runtime resolves/enforces are modelled; unknown JSON fields are
// ignored so the same export from Convex loads cleanly.
type LocalCompanyAIPolicy struct {
	Enabled bool `json:"enabled"`
	Runtime struct {
		Mode              string   `json:"mode"`
		DefaultProvider   string   `json:"defaultProvider"`
		DefaultDeviceID   string   `json:"defaultDeviceId"`
		FallbackDeviceIDs []string `json:"fallbackDeviceIds"`
		Region            string   `json:"region"`
	} `json:"runtime"`
	Runners struct {
		DefaultRunner        string   `json:"defaultRunner"`
		AllowedRunners       []string `json:"allowedRunners"`
		AllowUserOverride    bool     `json:"allowUserOverride"`
		CredentialMode       string   `json:"credentialMode"`
		DefaultModelByRunner []struct {
			Runner string `json:"runner"`
			Model  string `json:"model"`
		} `json:"defaultModelByRunner"`
	} `json:"runners"`
	Opencode struct {
		Providers []LocalProviderDef `json:"providers"`
	} `json:"opencode"`
	MCP struct {
		EnabledServers  []string `json:"enabledServers"`
		RequiredServers []string `json:"requiredServers"`
	} `json:"mcp"`
	WorkKinds map[string]bool `json:"workKinds"`
	Approvals struct {
		RequireApprovalForProductionWrites bool `json:"requireApprovalForProductionWrites"`
		RequireApprovalForDeploy           bool `json:"requireApprovalForDeploy"`
		RequireApprovalForRobotMotion      bool `json:"requireApprovalForRobotMotion"`
		RequireApprovalForSecretsAccess    bool `json:"requireApprovalForSecretsAccess"`
	} `json:"approvals"`
	DataPolicy struct {
		AllowCustomerDataInPrompts bool `json:"allowCustomerDataInPrompts"`
		AllowScreenshotsInPrompts  bool `json:"allowScreenshotsInPrompts"`
		AllowTelemetryInPrompts    bool `json:"allowTelemetryInPrompts"`
		RedactPII                  bool `json:"redactPII"`
		RetentionDays              int  `json:"retentionDays"`
	} `json:"dataPolicy"`
	AppProfile *struct {
		WorkKinds []struct {
			Key       string   `json:"key"`
			Enabled   *bool    `json:"enabled"`
			Approvals []string `json:"approvals"`
		} `json:"workKinds"`
		Providers []LocalProviderDef `json:"providers"`
	} `json:"appProfile"`
}

// legacyWorkKindKey maps a generic work-kind to its fixed CompanyAIOptions
// boolean field (mirrors companyAIOptions.ts workKindToOptionKey).
var legacyWorkKindKey = map[string]string{
	"app-code":     "appCode",
	"erp-flow":     "erpFlow",
	"convex":       "convex",
	"web-ui":       "webUi",
	"harness-cad":  "harnessCad",
	"openscad-cad": "openScadCad",
	"robot-trial":  "robotTrial",
	"inspection":   "inspection",
}

// localCompanyAIPolicyPaths returns the candidate file locations in priority
// order. The env override wins so tests and bespoke deployments can point
// anywhere.
func localCompanyAIPolicyPaths() []string {
	var paths []string
	if env := strings.TrimSpace(os.Getenv("YAVER_COMPANY_AI_POLICY")); env != "" {
		paths = append(paths, env)
	}
	paths = append(paths, "/etc/yaver/company-ai-policy.json")
	if dir, err := ConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "company-ai-policy.json"))
	}
	return paths
}

// LoadLocalCompanyAIPolicy reads the first present policy file. Returns
// (nil, nil) when no file exists (→ the agent uses hosted Convex as usual).
func LoadLocalCompanyAIPolicy() (*LocalCompanyAIPolicy, error) {
	for _, p := range localCompanyAIPolicyPaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var pol LocalCompanyAIPolicy
		if err := json.Unmarshal(data, &pol); err != nil {
			return nil, err
		}
		return &pol, nil
	}
	return nil, nil
}

func (p *LocalCompanyAIPolicy) workKindEnabled(workKind string) bool {
	if p.AppProfile != nil {
		for _, w := range p.AppProfile.WorkKinds {
			if w.Key == workKind {
				return w.Enabled == nil || *w.Enabled
			}
		}
	}
	if key, ok := legacyWorkKindKey[workKind]; ok {
		return p.WorkKinds[key]
	}
	return false
}

func (p *LocalCompanyAIPolicy) selectRunner(requested string) (string, []string) {
	allowed := p.Runners.AllowedRunners
	if len(allowed) == 0 {
		def := p.Runners.DefaultRunner
		if def == "" {
			def = "claude"
		}
		allowed = []string{def}
	}
	contains := func(s string) bool {
		for _, a := range allowed {
			if a == s {
				return true
			}
		}
		return false
	}
	if requested != "" && p.Runners.AllowUserOverride && contains(requested) {
		return requested, allowed
	}
	if contains(p.Runners.DefaultRunner) {
		return p.Runners.DefaultRunner, allowed
	}
	return allowed[0], allowed
}

func (p *LocalCompanyAIPolicy) providerCatalog() []LocalProviderDef {
	cat := append([]LocalProviderDef{}, p.Opencode.Providers...)
	if p.AppProfile != nil {
		cat = append(cat, p.AppProfile.Providers...)
	}
	return cat
}

func (p *LocalCompanyAIPolicy) selectProvider(requested string) (*LocalProviderDef, []string) {
	cat := p.providerCatalog()
	ids := make([]string, 0, len(cat))
	for i := range cat {
		ids = append(ids, cat[i].ID)
	}
	if requested != "" && p.Runners.AllowUserOverride {
		for i := range cat {
			if cat[i].ID == requested {
				return &cat[i], ids
			}
		}
	}
	if len(cat) > 0 {
		return &cat[0], ids
	}
	return nil, ids
}

func (p *LocalCompanyAIPolicy) approvalsFor(workKind string) []string {
	req := []string{"secrets-access"}
	add := func(s string) {
		for _, x := range req {
			if x == s {
				return
			}
		}
		req = append(req, s)
	}
	if p.AppProfile != nil {
		for _, w := range p.AppProfile.WorkKinds {
			if w.Key == workKind {
				for _, a := range w.Approvals {
					add(a)
				}
			}
		}
	}
	if p.Approvals.RequireApprovalForProductionWrites && (workKind == "convex" || workKind == "erp-flow") {
		add("production-write")
	}
	if p.Approvals.RequireApprovalForDeploy && (workKind == "app-code" || workKind == "web-ui" || workKind == "convex") {
		add("deploy")
	}
	if p.Approvals.RequireApprovalForRobotMotion && workKind == "robot-trial" {
		add("robot-motion")
	}
	return req
}

func (p *LocalCompanyAIPolicy) modelForRunner(runner, requested string) string {
	if requested != "" && p.Runners.AllowUserOverride {
		return requested
	}
	for _, m := range p.Runners.DefaultModelByRunner {
		if m.Runner == runner {
			return m.Model
		}
	}
	return ""
}

// ResolveCompanyAILocal resolves a unit of work against a locally-stored policy,
// producing the same response shape as the Convex resolver (minus secrets). The
// air-gap counterpart to /company-ai/resolve.
func ResolveCompanyAILocal(p *LocalCompanyAIPolicy, workKind, requestedRunner, requestedModel, requestedProvider, requestedDeviceID string) map[string]interface{} {
	if p == nil {
		return map[string]interface{}{"ok": false, "error": "no local company AI policy configured"}
	}
	workKindEnabled := p.workKindEnabled(workKind)
	runner, allowedRunners := p.selectRunner(requestedRunner)
	model := p.modelForRunner(runner, requestedModel)
	provider, allowedProviders := p.selectProvider(requestedProvider)
	deviceID := requestedDeviceID
	if deviceID == "" {
		deviceID = p.Runtime.DefaultDeviceID
	}
	runtimeReady := p.Enabled && workKindEnabled && deviceID != ""

	providerKeyMissing := provider != nil && provider.KeyPolicy != "" && provider.KeyPolicy != "none" &&
		provider.KeyConfigured != nil && !*provider.KeyConfigured

	var providerOut map[string]interface{}
	if provider != nil {
		providerOut = map[string]interface{}{
			"id": provider.ID, "label": provider.Label, "baseUrl": provider.BaseURL,
			"keyPolicy": provider.KeyPolicy, "allowedProviders": allowedProviders,
		}
	} else {
		providerOut = map[string]interface{}{"id": nil, "allowedProviders": allowedProviders}
	}

	return map[string]interface{}{
		"ok":              true,
		"source":          "local-airgap",
		"workKind":        workKind,
		"enabled":         p.Enabled,
		"workKindEnabled": workKindEnabled,
		"runtimeReady":    runtimeReady,
		"runtime": map[string]interface{}{
			"mode": p.Runtime.Mode, "provider": p.Runtime.DefaultProvider, "region": p.Runtime.Region,
			"deviceId": nullableString(deviceID), "fallbackDeviceIds": p.Runtime.FallbackDeviceIDs,
		},
		"runner": map[string]interface{}{
			"id": runner, "model": model, "allowedRunners": allowedRunners,
			"credentialMode": p.Runners.CredentialMode, "allowUserOverride": p.Runners.AllowUserOverride,
		},
		"provider": providerOut,
		"mcp": map[string]interface{}{
			"enabledServers": p.MCP.EnabledServers, "requiredServers": p.MCP.RequiredServers,
		},
		"approvals": map[string]interface{}{
			"required":                           p.approvalsFor(workKind),
			"requireApprovalForProductionWrites": p.Approvals.RequireApprovalForProductionWrites,
			"requireApprovalForDeploy":           p.Approvals.RequireApprovalForDeploy,
			"requireApprovalForRobotMotion":      p.Approvals.RequireApprovalForRobotMotion,
			"requireApprovalForSecretsAccess":    p.Approvals.RequireApprovalForSecretsAccess,
		},
		"dataPolicy": map[string]interface{}{
			"allowCustomerDataInPrompts": p.DataPolicy.AllowCustomerDataInPrompts,
			"allowScreenshotsInPrompts":  p.DataPolicy.AllowScreenshotsInPrompts,
			"allowTelemetryInPrompts":    p.DataPolicy.AllowTelemetryInPrompts,
			"redactPII":                  p.DataPolicy.RedactPII,
			"retentionDays":              p.DataPolicy.RetentionDays,
		},
		"nextActions": map[string]interface{}{
			"configureCompanyAI":     !p.Enabled,
			"configureRuntimeDevice": deviceID == "",
			"enableWorkKind":         !workKindEnabled,
			"reauthRunner":           p.Runners.CredentialMode == "user-auth-on-runtime",
			"configureProviderKey":   providerKeyMissing,
		},
	}
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// LocalRuntimeDataPolicy returns the DataPolicy from the local policy file, or
// the zero value when no file is present. Used by the retention loop.
func LocalRuntimeDataPolicy() DataPolicy {
	pol, err := LoadLocalCompanyAIPolicy()
	if err != nil || pol == nil {
		return DataPolicy{}
	}
	return DataPolicy{RedactPII: pol.DataPolicy.RedactPII, RetentionDays: pol.DataPolicy.RetentionDays}
}

// PruneExpiredTasks drops finished tasks older than retentionDays from the
// in-memory task store, enforcing dataPolicy.retentionDays on the runtime.
// Returns the number pruned. retentionDays <= 0 is a no-op. Reuses the tested
// tasksToPrune selector so running/pending tasks are never removed.
func (tm *TaskManager) PruneExpiredTasks(retentionDays int, now time.Time) int {
	if retentionDays <= 0 {
		return 0
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	ids := make([]string, 0, len(tm.tasks))
	slice := make([]*Task, 0, len(tm.tasks))
	for id, t := range tm.tasks {
		ids = append(ids, id)
		slice = append(slice, t)
	}
	pruneIdx := tasksToPrune(slice, retentionDays, now)
	for _, i := range pruneIdx {
		delete(tm.tasks, ids[i])
	}
	return len(pruneIdx)
}

// resolveCompanyAIWithFallback resolves a unit of work via hosted Convex
// (resolveCompanyAIRuntime), and transparently falls back to the local
// air-gap policy file when the Convex call fails AND a local policy is present
// on this runtime. This makes air-gap automatic: an egress-restricted box that
// has had a company-ai-policy.json dropped on it keeps resolving even with
// Convex unreachable, using the SAME entry point — no separate route needed.
//
// A successful Convex resolution always wins (online + authoritative). The
// fallback fires only when (a) Convex errored — network down, no URL, or a
// server error — and (b) a local policy file exists; its presence is the
// admin's signal that this box may operate offline. With no local file the
// original Convex error is surfaced unchanged.
func resolveCompanyAIWithFallback(convexURL, token string, payload map[string]interface{}) (json.RawMessage, error) {
	data, err := resolveCompanyAIRuntime(convexURL, token, payload)
	if err == nil {
		return data, nil
	}
	pol, lerr := LoadLocalCompanyAIPolicy()
	if lerr != nil || pol == nil {
		return nil, err // no offline policy → surface the original Convex error
	}
	str := func(k string) string { v, _ := payload[k].(string); return v }
	local := ResolveCompanyAILocal(pol, str("workKind"), str("requestedRunner"), str("requestedModel"), str("requestedProvider"), str("requestedDeviceId"))
	local["fallback"] = "convex-unreachable"
	local["fallbackError"] = err.Error()
	b, merr := json.Marshal(local)
	if merr != nil {
		return nil, err
	}
	log.Printf("[company-ai] Convex resolve failed (%v); served from local air-gap policy", err)
	return json.RawMessage(b), nil
}

var retentionLoopOnce sync.Once

// StartLocalRetentionLoop launches a background loop that prunes the task store
// to the local policy's retentionDays every hour. No-op when there is no local
// policy or retention is disabled. Idempotent (starts at most once).
func StartLocalRetentionLoop(tm *TaskManager) {
	if tm == nil {
		return
	}
	retentionLoopOnce.Do(func() {
		go func() {
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			for range t.C {
				days := LocalRuntimeDataPolicy().RetentionDays
				if days <= 0 {
					continue
				}
				if n := tm.PruneExpiredTasks(days, time.Now()); n > 0 {
					log.Printf("[data-policy] retention: pruned %d task(s) older than %d day(s)", n, days)
				}
			}
		}()
	})
}
