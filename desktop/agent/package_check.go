package main

// package_check.go — preflight sanity check that runs BEFORE a package is
// shared. It lints the manifest statically and dry-runs it once on the owner's
// box, returning a pass/warn/fail verdict with reasons. package_allocate refuses
// to share a package whose latest check failed (or was never run) unless force.
//
// Philosophy: don't ship a broken or silently-blocked package to a friend. A
// geo-block from the OWNER's vantage is a WARN, not a FAIL — it's often expected
// (the runner's vantage differs). A broken MCP binding or invalid manifest is a
// FAIL.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PackageCheckFinding is one lint/dry-run observation.
type PackageCheckFinding struct {
	Level   string `json:"level"` // info | warn | fail
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PackageCheckResult is the verdict, stored per package and read by the share gate.
type PackageCheckResult struct {
	Package  string                `json:"package"`
	Status   string                `json:"status"` // pass | warn | fail
	At       int64                 `json:"at"`
	Findings []PackageCheckFinding `json:"findings"`
	DryRun   *PackageRunResult     `json:"dryRun,omitempty"`
}

func (r *PackageCheckResult) add(level, code, msg string) {
	r.Findings = append(r.Findings, PackageCheckFinding{Level: level, Code: code, Message: msg})
}

// checkPackage runs the static lint + dry-run and returns the verdict.
func checkPackage(c OpsContext, p *TaskPackage) *PackageCheckResult {
	r := &PackageCheckResult{Package: p.Metadata.Name, Findings: []PackageCheckFinding{}}

	// --- static lint ---
	if err := validatePackage(p); err != nil {
		r.add("fail", "invalid_manifest", err.Error())
	}
	if strings.TrimSpace(p.Spec.Consent.Summary) == "" {
		r.add("warn", "no_consent_summary",
			"no consent.summary — the runner will see a generic message; add one so they know what their device does")
	}
	for _, b := range p.Spec.Task.MCP {
		if b.transport() == "" {
			r.add("fail", "bad_mcp_binding",
				fmt.Sprintf("mcp binding %q has neither url (http) nor verb (local)", b.Name))
		}
	}
	browserEngine := ""
	for _, e := range p.Spec.Task.Engines {
		if e == "playwright" || e == "redroid" {
			browserEngine = e
		}
	}
	mobileRuntime := false
	for _, rt := range p.Spec.Runtimes {
		if rt == "mobile" {
			mobileRuntime = true
		}
	}
	if browserEngine != "" && mobileRuntime {
		r.add("fail", "mobile_engine_conflict",
			fmt.Sprintf("engine %q can't run on the mobile target; remove mobile from runtimes or change the engine", browserEngine))
	}
	if p.effectiveTier() == "acting" {
		r.add("warn", "acting_tier",
			"ACTING tier: this package can take actions on the runner's device — they will be asked to confirm; make the consent explicit")
	}
	hasExtract := false
	for _, s := range p.Spec.Task.Sources {
		if len(s.Extract) > 0 {
			hasExtract = true
		}
	}
	if len(p.Spec.Task.Sources) > 0 && !hasExtract && len(p.Spec.Task.MCP) == 0 {
		r.add("warn", "no_extraction",
			"sources have no extract rules — only a liveness count is recorded; add extract{} or an mcp binding")
	}

	// --- dynamic dry-run (read-only; acting packages return needs_confirmation) ---
	dry := runPackageOnce(c, p, false)
	r.DryRun = &dry
	switch {
	case dry.Status == "ok":
		r.add("info", "dry_run_ok", fmt.Sprintf("dry-run ok: %d fields extracted from this vantage", len(dry.Fields)))
	case dry.Status == "needs_confirmation":
		r.add("info", "dry_run_acting", "dry-run gated (acting tier) — not executed; consent/confirm path verified")
	case strings.HasPrefix(dry.Status, "blocked"):
		r.add("warn", "dry_run_blocked",
			fmt.Sprintf("blocked (%s) from THIS box's vantage — expected if the source is geo/IP-gated; the runner's vantage may differ", dry.Status))
	default:
		r.add("fail", "dry_run_error", fmt.Sprintf("dry-run failed (%s); see notes", dry.Status))
	}
	for _, call := range dry.MCPCalls {
		if call["ok"] != true {
			r.add("fail", "mcp_unreachable",
				fmt.Sprintf("mcp binding %v did not answer: %v", call["name"], call["error"]))
		}
	}

	// --- aggregate verdict ---
	status := "pass"
	for _, f := range r.Findings {
		if f.Level == "fail" {
			status = "fail"
			break
		}
		if f.Level == "warn" {
			status = "warn"
		}
	}
	r.Status = status
	return r
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "package_check",
		Description: "Preflight a Task Package BEFORE sharing: lint the manifest + dry-run it once on this box, " +
			"returning pass/warn/fail with reasons. A geo/IP block from this box's vantage is a WARN (the runner's " +
			"vantage may differ); a broken manifest or unreachable MCP binding is a FAIL. package_allocate refuses " +
			"to share a package that failed (or was never checked) unless force=true. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		}, "name"),
		Handler:    packageCheckHandler,
		AllowGuest: false,
	})
}

func packageCheckHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Name string `json:"name"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(args.Name) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	p, ok := pkgStore.getPackage(args.Name)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "no such package"}
	}
	result := checkPackage(c, p)
	pkgStore.setCheck(args.Name, result)
	return OpsResult{OK: true, Initial: map[string]interface{}{"check": result}}
}
