package main

// package_ops.go — owner-only MCP/ops verbs for Yaver Task Packages: author,
// publish, allocate to a runner, run once (incl. MCP-over-MCP), and inspect.
// Domain-agnostic; verticals (yaver-bet, fintech, …) are use cases.
// See docs/yaver-task-packages.md.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "package_publish",
		Description: "Validate and store a Task Package (yaver/v1 manifest). Payload IS the manifest " +
			"{apiVersion,kind,metadata:{name,...},spec:{task:{kind,sources?,steps?,mcp?,goal?},...}}. " +
			"Returns the stored package (version auto-bumps on re-publish). Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"metadata": map[string]interface{}{"type": "object", "description": "{name (required), description?, version?}"},
			"spec":     map[string]interface{}{"type": "object", "description": "{task:{kind, sources?, steps?, mcp?, goal?}, runtimes?, vantage?, schedule?, output?, consent?, guard?}"},
		}, "spec"),
		Handler:    packagePublishHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "package_list",
		Description: "List published Task Packages (name, kind, version, engines, vantage requirement). Owner-only.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     packageListHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "package_get",
		Description: "Get one Task Package manifest + its allocations + recent runs. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		}, "name"),
		Handler:    packageGetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "package_run",
		Description: "Run a Task Package ONCE on this runtime and return the result. Executes declarative " +
			"fetch sources and MCP-over-MCP bindings (call another MCP server — e.g. your yaver-bet MCP — or a " +
			"local Yaver verb). Results are stored vantage-tagged in the collection store. ACTING-tier packages " +
			"(operate/agent) require confirm=true. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"name":    map[string]interface{}{"type": "string"},
			"confirm": map[string]interface{}{"type": "boolean", "description": "Required to run an ACTING-tier (operate/agent/write) package."},
		}, "name"),
		Handler:    packageRunHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "package_delete",
		Description: "Delete a published Task Package by name. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		}, "name"),
		Handler:    packageDeleteHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "package_allocate",
		Description: "Bind a Task Package to a runner device + target (mobile|agent|docker|worker), under consent. " +
			"This is the cross-user allocation: which person/device runs which package. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"packageName":  map[string]interface{}{"type": "string"},
			"device":       map[string]interface{}{"type": "string", "description": "Runner device id."},
			"target":       map[string]interface{}{"type": "string", "description": "mobile | agent | docker | worker"},
			"wifiOnly":     map[string]interface{}{"type": "boolean"},
			"chargingOnly": map[string]interface{}{"type": "boolean"},
			"force":        map[string]interface{}{"type": "boolean", "description": "Share even if the preflight (package_check) failed or was never run."},
		}, "packageName", "device"),
		Handler:    packageAllocateHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "package_status",
		Description: "Status of a package's allocations (runner roster) + recent run counters. Owner-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		}, "name"),
		Handler:    packageStatusHandler,
		AllowGuest: false,
	})
}

func packagePublishHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p TaskPackage
	if len(payload) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "manifest payload required"}
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Metadata.Owner == "" {
		p.Metadata.Owner = c.ActorUserID
	}
	if err := validatePackage(&p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	out := pkgStore.upsertPackage(p)
	// Best-effort: publish privacy-safe bookkeeping to Convex so the package can
	// be shared cross-user. Never blocks the local publish.
	go func() {
		defer func() { _ = recover() }()
		if cfg, _ := LoadConfig(); cfg != nil {
			syncTaskPackage(cfg.DeviceID, out)
		}
	}()
	return OpsResult{OK: true, Initial: map[string]interface{}{"package": out, "tier": out.effectiveTier()}}
}

func packageListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	pkgs := pkgStore.listPackages()
	rows := make([]map[string]interface{}, 0, len(pkgs))
	for _, p := range pkgs {
		rows = append(rows, map[string]interface{}{
			"name":     p.Metadata.Name,
			"kind":     p.Spec.Task.Kind,
			"version":  p.Metadata.Version,
			"engines":  p.Spec.Task.Engines,
			"runtimes": p.Spec.Runtimes,
			"vantage":  p.Spec.Vantage,
			"tier":     p.effectiveTier(),
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"packages": rows, "count": len(rows)}}
}

func packageGetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(payload, &args)
	if strings.TrimSpace(args.Name) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
	}
	p, ok := pkgStore.getPackage(args.Name)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "no such package"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"package":     p,
		"tier":        p.effectiveTier(),
		"allocations": pkgStore.listAllocations(args.Name),
		"recentRuns":  pkgStore.recentRuns(args.Name, 20),
	}}
}

func packageRunHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
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
	res := runPackageOnce(c, p, args.Confirm)
	return OpsResult{OK: true, Initial: map[string]interface{}{"run": res}}
}

func packageDeleteHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(payload, &args)
	if !pkgStore.deletePackage(strings.TrimSpace(args.Name)) {
		return OpsResult{OK: false, Code: "not_found", Error: "no such package"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"deleted": args.Name}}
}

func packageAllocateHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		PackageName  string `json:"packageName"`
		Device       string `json:"device"`
		Target       string `json:"target"`
		WifiOnly     bool   `json:"wifiOnly"`
		ChargingOnly bool   `json:"chargingOnly"`
		Force        bool   `json:"force"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	if strings.TrimSpace(args.PackageName) == "" || strings.TrimSpace(args.Device) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "packageName and device required"}
	}
	p, ok := pkgStore.getPackage(args.PackageName)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "no such package"}
	}
	// Sanity gate: don't share a package that hasn't passed preflight.
	if !args.Force {
		chk, checked := pkgStore.getCheck(args.PackageName)
		if !checked {
			return OpsResult{OK: false, Code: "check_required",
				Error: "run package_check before sharing (or pass force=true)"}
		}
		if chk.Status == "fail" {
			return OpsResult{OK: false, Code: "check_failed",
				Error: "preflight failed; fix the package or pass force=true. Run package_check to see why"}
		}
	}
	target := args.Target
	if target == "" {
		target = "mobile"
	}
	// Eligibility: engines needing a real browser/emulator can't run on mobile.
	for _, e := range p.Spec.Task.Engines {
		if (e == "playwright" || e == "redroid") && target == "mobile" {
			return OpsResult{OK: false, Code: "bad_payload",
				Error: "package needs engine '" + e + "' which the mobile target can't run; allocate target=agent|docker|worker"}
		}
	}
	out := pkgStore.upsertAllocation(PackageAllocation{
		PackageName:    args.PackageName,
		RunnerDeviceID: args.Device,
		Target:         target,
		Status:         "proposed",
		WifiOnly:       args.WifiOnly,
		ChargingOnly:   args.ChargingOnly,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{"allocation": out}}
}

func packageStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(payload, &args)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"allocations": pkgStore.listAllocations(args.Name),
		"recentRuns":  pkgStore.recentRuns(args.Name, 20),
	}}
}
