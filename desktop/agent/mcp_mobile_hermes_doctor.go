package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type mobileHermesDoctorInput struct {
	// DeviceID routes the diagnosis to the box that HOLDS the project. Hermes
	// doctoring inspects a checkout — which native modules it needs, and whether
	// the Yaver container can host them — so the honest answer comes from the
	// machine with the code, not the one asking. See REMOTE_WORKER.md §A.
	DeviceID             string            `json:"device_id,omitempty"`
	Directory            string            `json:"directory"`
	AvailableModules     []string          `json:"availableModules"`
	AvailableModuleMap   map[string]string `json:"availableModuleMap"`
	SupportedRuntimeSets []RuntimeFamily   `json:"supportedRuntimeFamilies"`
}

func mobileHermesDoctor(req mobileHermesDoctorInput) map[string]interface{} {
	start := strings.TrimSpace(req.Directory)
	if start == "" {
		if cwd, err := os.Getwd(); err == nil {
			start = cwd
		}
	}
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}

	projectDir, framework := resolveMobileProject(start)
	out := map[string]interface{}{
		"ok":                 false,
		"startDir":           start,
		"projectDir":         projectDir,
		"framework":          framework,
		"target":             "mobile-hermes",
		"readyToBuildHermes": false,
		"readyToReloadPhone": false,
		"blockers":           []string{},
		"warnings":           []string{},
		"nextActions":        []map[string]string{},
	}

	addBlocker := func(msg string) {
		out["blockers"] = append(out["blockers"].([]string), msg)
	}
	addWarning := func(msg string) {
		out["warnings"] = append(out["warnings"].([]string), msg)
	}
	addAction := func(tool, reason string) {
		out["nextActions"] = append(out["nextActions"].([]map[string]string), map[string]string{
			"tool":   tool,
			"reason": reason,
		})
	}

	if projectDir == "" {
		addBlocker("No React Native or Expo project was found at the selected directory or common monorepo locations such as apps/* and mobile/.")
		addAction("project_self_host_create", "Create a self-hosted starter monorepo with apps/mobile, apps/web, Convex, and Cloudflare wiring.")
		return out
	}
	out["projectDir"] = projectDir
	out["framework"] = framework

	if framework != "expo" && framework != "react-native" {
		addBlocker(fmt.Sprintf("Yaver phone reload uses the Hermes path for Expo and React Native projects; detected %q.", framework))
		return out
	}

	status := mobileProjectStatus(projectDir)
	out["projectStatus"] = status
	out["machineCapability"] = capabilityForMobileHermes()

	if errText, _ := status["error"].(string); errText != "" {
		addBlocker(errText)
		return out
	}

	missingTools := stringSliceFromInterface(status["missingTools"])
	if len(missingTools) > 0 {
		addBlocker("Missing local build tools: " + strings.Join(missingTools, ", ") + ".")
		addAction("diagnose", "Run Yaver diagnostics to confirm host runtime and install guidance.")
	}

	depsInstalled, _ := status["dependenciesInstalled"].(bool)
	needsInstall, _ := status["needsDependencyInstall"].(bool)
	canInstall, _ := status["canAutoInstallDependencies"].(bool)
	if needsInstall || !depsInstalled {
		if canInstall {
			addWarning("Project dependencies are not installed yet; MCP can install them with the detected package manager.")
			addAction("mobile_project_prepare", "Install project or workspace dependencies before building the Hermes bundle.")
		} else {
			pm, _ := status["packageManager"].(string)
			addBlocker(fmt.Sprintf("Dependencies are not installed and Yaver cannot auto-install them with %s on this machine.", pm))
		}
	}

	hermesCompiler, _ := status["hermesCompiler"].(string)
	hermesErr, _ := status["hermesCompilerError"].(string)
	if hermesCompiler == "" || hermesCompiler == "missing" {
		if hermesErr == "" {
			hermesErr = "Hermes compiler is not available yet."
		}
		addBlocker(hermesErr)
	}

	if compat, err := BuildNativeModuleCompatReportWith(projectDir, req.SupportedRuntimeSets, nativeModuleOverlay(req)); err == nil {
		out["nativeCompatibility"] = compat
		// This MUST mirror the build gate in devserver_http.go
		// (handleBuildNativeBundle), or the doctor becomes a false green in the
		// opposite direction: until 2026-07-20 it BLOCKED on missing modules and
		// merely WARNED on version drift — exactly backwards from what the build
		// path enforces. A doctor that says "blocked" for a load the build path
		// now allows (a guarded require of an absent module renders a fallback)
		// sends the agent to fix the wrong thing. Whatever the gate blocks on, the
		// doctor blocks on; whatever the gate warns on, the doctor warns on.
		//
		// Missing module = WARNING: it throws only if the guest calls it unguarded.
		if len(compat.Incompatible) > 0 {
			addWarning("Native modules declared by the project are not in the Yaver phone runtime: " +
				strings.Join(compat.Incompatible, ", ") +
				". They throw only if called unguarded — a require() wrapped in try/catch renders a fallback and loads fine.")
		}
		// Version / framework drift = BLOCKER: it corrupts the JSI/TurboModule
		// contract for a module the host DOES register, which no guest guard fixes.
		if len(compat.VersionMismatches) > 0 {
			addBlocker(fmt.Sprintf("%d native module version mismatch(es) at a likely-breaking boundary.", len(compat.VersionMismatches)))
		}
		if compat.ReactVersionMismatch != nil {
			addBlocker("React version differs from the selected Yaver phone runtime family.")
		}
		if compat.ExpoVersionMismatch != nil {
			addBlocker("Expo version differs from the selected Yaver phone runtime family.")
		}
		if compat.RNVersionMismatch != nil {
			addBlocker("React Native version differs from the selected Yaver phone runtime family.")
		}
	} else {
		addWarning("Native-module compatibility could not be checked: " + err.Error())
	}

	blockers := out["blockers"].([]string)
	buildState, _ := status["buildState"].(string)
	readyToBuild := len(blockers) == 0
	out["readyToBuildHermes"] = readyToBuild
	out["readyToReloadPhone"] = readyToBuild && buildState == "ready"
	out["ok"] = out["readyToReloadPhone"]

	if readyToBuild && buildState != "ready" {
		addAction("mobile_project_build", "Compile the Hermes bundle Yaver mobile loads for phone reload.")
	}
	if readyToBuild && buildState == "ready" {
		out["guidance"] = "Hermes bundle is compiled. Open Yaver mobile and reload this project; rebuild with mobile_project_build after source changes."
	} else {
		out["guidance"] = "Resolve blockers and run the listed MCP tools in order, then reload from Yaver mobile."
	}

	return out
}

func nativeModuleOverlay(req mobileHermesDoctorInput) map[string]string {
	overlay := map[string]string{}
	for name, version := range req.AvailableModuleMap {
		name = strings.TrimSpace(name)
		if name != "" {
			overlay[name] = strings.TrimSpace(version)
		}
	}
	for _, name := range req.AvailableModules {
		name = strings.TrimSpace(name)
		if name != "" {
			if _, ok := overlay[name]; !ok {
				overlay[name] = ""
			}
		}
	}
	if len(overlay) == 0 {
		return nil
	}
	return overlay
}

func stringSliceFromInterface(v interface{}) []string {
	switch in := v.(type) {
	case []string:
		return append([]string(nil), in...)
	case []interface{}:
		out := make([]string, 0, len(in))
		for _, item := range in {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}
