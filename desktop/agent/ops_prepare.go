package main

// ops_prepare.go — verb "prepare": discovery + plan for getting a
// project from "fresh git clone" to "dev server / Hermes bundle ready".
//
// The agent already auto-installs missing Node, auto-installs workspace
// deps, and auto-builds native bundles inside /dev/start and
// /dev/build-native. What was missing was a *preview* of what's about
// to happen — a plan that web UI / mobile app / coding-agent MCP
// callers can render BEFORE pulling the trigger, and a diagnosis that
// names the broken bits when something can't auto-fix (missing file:
// tarball, integrity hash drift, peer-dep conflict, etc.).
//
// This verb is read-only by default. It runs the same discovery the
// dev-server preflight does — `which node/npm/npx/expo/...` via
// commandExists, find-style probing via DetectMonorepo and
// detectDevServer, lockfile + workspace-symlink checks via
// detectProjectPreparation — and returns a structured plan with
// per-step status and reason. The web dashboard and mobile Hot Reload
// card both call it before `/dev/start` so the user sees "we'll auto-
// install workspace deps + start vite on :5173" instead of staring at
// a generic "Starting…" spinner.
//
// AI coding agents call it via MCP (tool name = ops/prepare) when they
// want to understand a project before issuing build/run commands — the
// returned plan tells them whether the project is already serve-ready
// (skip), needs a one-shot install (do it then start), or has a real
// blocker (surface to user).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "prepare",
		Description: "Discover a project's state and return a plan to make it serve-able. " +
			"Read-only by default. Reports framework, package manager, missing tools/deps, " +
			"workspace layout, lockfile health, Hermes compiler availability, and an ordered " +
			"list of remediation steps. Web UI / mobile / coding-agent MCP callers use this " +
			"to preview what /dev/start or /dev/build-native will do before pulling the trigger.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir": map[string]interface{}{
					"type":        "string",
					"description": "Absolute path to the project root or a workspace sub-app. Required.",
				},
				"intent": map[string]interface{}{
					"type": "string",
					"description": "What the caller plans to do next. \"webview\" (default) — render a web preview in the dashboard's Webview tab via the dev server; routes through /dev/start. " +
						"\"hermes\" — compile a Hermes bundle and push it to the paired mobile container via /dev/build-native + /insert. " +
						"\"both\" — Expo projects can serve a web preview AND a Hermes bundle for the same source.",
					"enum": []string{"webview", "hermes", "both", "serve"},
				},
			},
			"required":             []string{"workDir"},
			"additionalProperties": false,
		},
		Handler:    opsPrepareHandler,
		Streaming:  false,
		AllowGuest: true,
	})
}

type prepareReq struct {
	WorkDir string `json:"workDir"`
	Intent  string `json:"intent"`
}

type prepareStep struct {
	Action  string `json:"action"`            // "install_workspace_deps", "build_native_bundle", "start_dev_server", etc.
	Done    bool   `json:"done"`              // true = already satisfied
	Reason  string `json:"reason"`            // why this step is in the plan (or why it's already done)
	Command string `json:"command,omitempty"` // illustrative shell form; not necessarily what the agent actually runs
	Cwd     string `json:"cwd,omitempty"`     // working directory the agent would run it in
	Blocker string `json:"blocker,omitempty"` // when set, this step can't auto-execute — names the human action required
}

type prepareIssue struct {
	Severity string `json:"severity"` // "info", "warning", "error"
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"` // action the user / coding agent should take
}

func opsPrepareHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req prepareReq
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("invalid prepare payload: %v", err)}
		}
	}
	req.WorkDir = strings.TrimSpace(req.WorkDir)
	if req.WorkDir == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "workDir is required"}
	}
	if !filepath.IsAbs(req.WorkDir) {
		return OpsResult{OK: false, Code: "bad_payload", Error: "workDir must be absolute"}
	}
	if st, err := os.Stat(req.WorkDir); err != nil || !st.IsDir() {
		return OpsResult{OK: false, Code: "not_found", Error: fmt.Sprintf("workDir does not exist or is not a directory: %s", req.WorkDir)}
	}
	intent := strings.TrimSpace(req.Intent)
	if intent == "" || intent == "serve" {
		// "serve" is the legacy alias for "webview" — both produce
		// the dev-server-start plan. Keep both accepted so older
		// callers (CLI scripts, the autodev loop) don't break.
		intent = "webview"
	}

	// 1. Resolve the actual workDir the dev/build paths would use.
	//    For monorepos without a marker at the root, walk into the
	//    sub-app the picker would land on (with our improved
	//    name/depth tiebreakers). Surface that explicitly so the UI
	//    can show "we picked apps/web for you" instead of silently
	//    re-routing.
	resolvedWorkDir := req.WorkDir
	pickerNote := ""
	if ds := detectDevServer(req.WorkDir); ds == nil {
		if _, sub, err := monorepoFallbackDevServer(req.WorkDir); err == nil && sub != "" {
			resolvedWorkDir = sub
			pickerNote = fmt.Sprintf("monorepo: chose %s as the active sub-app", strings.TrimPrefix(sub, req.WorkDir+string(filepath.Separator)))
		}
	}

	// 2. Read package.json + run preparedness probe.
	manifest, manifestErr := readProjectPackageManifest(resolvedWorkDir)
	prep := detectProjectPreparation(resolvedWorkDir, manifest)

	// 3. Detect framework so the plan's commands are concrete.
	framework := ""
	if ds := detectDevServer(resolvedWorkDir); ds != nil {
		framework = ds.Name()
	}

	// 4. Lockfile health: integrity entries that can't be satisfied
	//    are the single most common cold-clone blocker (carrotbet's
	//    yaver-feedback-web tarball drift hit us today). We don't
	//    parse package-lock.json deeply here — just flag its presence
	//    and detect file: deps in package.json that point at missing
	//    paths, since those produce ENOENT during install.
	issues := []prepareIssue{}
	if manifestErr != nil {
		issues = append(issues, prepareIssue{
			Severity: "error",
			Message:  fmt.Sprintf("package.json missing or invalid in %s", resolvedWorkDir),
			Fix:      "Verify the workDir points at a project root with a package.json, or specify a sub-app workDir explicitly.",
		})
	}
	if manifest != nil {
		// projectPackageManifest exposes the three npm dep buckets
		// directly; we just want the union for file: scanning. (No
		// optionalDependencies in our schema — they'd join here too.)
		buckets := []map[string]string{manifest.Dependencies, manifest.PeerDependencies, manifest.DevDependencies}
		for _, bucket := range buckets {
			for name, ver := range bucket {
				if !strings.HasPrefix(ver, "file:") {
					continue
				}
				rel := strings.TrimPrefix(ver, "file:")
				abs := rel
				if !filepath.IsAbs(rel) {
					abs = filepath.Clean(filepath.Join(resolvedWorkDir, rel))
				}
				if _, err := os.Stat(abs); err != nil {
					issues = append(issues, prepareIssue{
						Severity: "error",
						Message:  fmt.Sprintf("file: dependency %s points at %s which does not exist", name, abs),
						Fix:      fmt.Sprintf("pack the source SDK (`cd <sdk> && npm pack`) so the .tgz exists, OR change %s to the published npm version.", name),
					})
				}
			}
		}
	}

	// 5. Build the plan. Each step has a `Done` flag computed from the
	//    discovery state so the UI can render "step 2 of 4: install
	//    workspace deps — pending" without re-running probes itself.
	plan := []prepareStep{}

	// Step: missing toolchain — only auto-installable for Node;
	// surfaces as a blocker for everything else (yarn / pnpm / bun /
	// hermesc with no embedded fallback).
	for _, tool := range prep.MissingTools {
		step := prepareStep{
			Action:  "install_tool",
			Reason:  fmt.Sprintf("%s is missing on this machine", tool),
			Command: "POST /install/runtime payload={tool: \"" + tool + "\"}",
			Cwd:     resolvedWorkDir,
		}
		if isOnlyNodeMissing([]string{tool}) {
			step.Reason = "Node missing — agent will auto-install LTS into ~/.yaver/runtimes/node"
		} else if !canInstallMissingTool([]string{tool}) {
			step.Blocker = fmt.Sprintf("%s isn't auto-installable here; install it on the host or pick a different package manager", tool)
		}
		plan = append(plan, step)
	}

	// Step: workspace dependency install (always at the workspace root
	// when there is one — that's where workspace symlinks get created;
	// leaf-only installs miss them and bundling fails later).
	depsStep := prepareStep{
		Action: "install_workspace_deps",
		Done:   !prep.NeedsDependencyInstall,
		Reason: "node_modules already present at the workspace root",
		Cwd:    resolvedWorkDir,
	}
	if prep.NeedsDependencyInstall {
		installDir := resolvedWorkDir
		if prep.WorkspaceRoot != "" {
			installDir = prep.WorkspaceRoot
		}
		depsStep.Done = false
		depsStep.Cwd = installDir
		depsStep.Command = installCommandFor(prep.PackageManager)
		switch {
		case prep.WorkspaceRoot != "" && prep.WorkspaceRoot != resolvedWorkDir:
			depsStep.Reason = fmt.Sprintf("workspace root %s has no node_modules; install must run there so workspace symlinks get created", installDir)
		default:
			depsStep.Reason = "node_modules missing"
		}
		if !prep.CanAutoInstallDependencies {
			depsStep.Blocker = fmt.Sprintf("cannot auto-install with %s on this machine — install %s first", prep.PackageManager, prep.PackageManager)
		}
	}
	plan = append(plan, depsStep)

	// Step: dev server start — the webview path. The dashboard's
	// Webview tab depends on this: it polls /dev/status, waits for
	// running:true, then loads the dev URL through the relay (browser
	// CORS prevents direct LAN). Per-framework: vite/next produce a
	// straight web preview; expo serves both Metro AND a sibling
	// "expo --web" process when the dashboard's Web Reload tab fires
	// (auto-spawned in /dev/start), so we mention the expo-web sibling
	// in the reason so the user knows what to expect.
	if intent == "webview" || intent == "both" {
		serveStep := prepareStep{
			Action: "start_dev_server",
			Cwd:    resolvedWorkDir,
		}
		switch framework {
		case "":
			serveStep.Reason = "no framework markers found — caller must pass framework explicitly"
			serveStep.Blocker = "framework not auto-detectable; set framework= in /dev/start or pick a sub-app explicitly"
		case "expo", "react-native":
			serveStep.Reason = "framework=" + framework + "; agent will start Metro and (for the Webview tab) auto-spawn the expo-web sibling on demand"
			serveStep.Command = devCommandFor(framework)
		case "vite", "nextjs":
			serveStep.Reason = "framework=" + framework + "; dev URL serves directly to the dashboard's Webview iframe via the relay"
			serveStep.Command = devCommandFor(framework)
		default:
			serveStep.Reason = "framework=" + framework
			serveStep.Command = devCommandFor(framework)
		}
		plan = append(plan, serveStep)
	}

	// Step: Hermes bundle build + push — the mobile reload path.
	// /dev/build-native produces the .hbc + assets, validates HBC
	// magic + bytecode version, and the paired Yaver container loads
	// it via ExpoReactNativeFactory + RCTAppDependencyProvider. The
	// "/insert <app>" broadcast tells the phone to navigate + pull.
	if intent == "hermes" || intent == "both" {
		hermesStep := prepareStep{
			Action:  "build_native_bundle",
			Cwd:     resolvedWorkDir,
			Reason:  fmt.Sprintf("hermesCompiler=%s", orDash(prep.HermesCompiler)),
			Command: "POST /dev/build-native payload={workDir, platform: \"ios\"|\"android\"}",
		}
		switch prep.HermesCompiler {
		case "embedded":
			hermesStep.Reason = "hermesCompiler=embedded — agent ships its own hermesc; no project install needed for compile"
		case "project":
			hermesStep.Reason = "hermesCompiler=project — using the Hermes compiler from the project's node_modules"
		case "buildable":
			hermesStep.Reason = "hermesCompiler=buildable — agent will build hermesc from the project's runtime ref before compiling"
		case "missing":
			hermesStep.Blocker = "Hermes compiler unavailable: " + orDash(prep.HermesCompilerError)
		default:
			hermesStep.Blocker = "Hermes compiler state unknown — run /doctor/build for details"
		}
		plan = append(plan, hermesStep)

		// Detect whether the project is mobile-shaped at all. Pushing
		// a Hermes bundle for a Vite/Next project would just produce a
		// nonsense bundle the container can't load.
		isMobileShape := framework == "expo" || framework == "react-native"
		if !isMobileShape && manifest != nil {
			// Mobile-ish projects that *don't* have an Expo/RN dev
			// server registered (e.g. plain RN projects with custom
			// scripts) — check package.json for the canonical deps.
			for _, bucket := range []map[string]string{manifest.Dependencies, manifest.DevDependencies} {
				if _, ok := bucket["expo"]; ok {
					isMobileShape = true
					break
				}
				if _, ok := bucket["react-native"]; ok {
					isMobileShape = true
					break
				}
			}
		}
		if !isMobileShape {
			issues = append(issues, prepareIssue{
				Severity: "warning",
				Message:  "intent=hermes but no Expo / React Native markers found in this project",
				Fix:      "Use intent=webview for vite/next/flutter projects, or point workDir at a mobile sub-app.",
			})
		}

		// Push step is informational — the actual push happens via a
		// separate /insert call from the user (or coding agent) once
		// the phone is paired and the bundle is ready.
		pushStep := prepareStep{
			Action:  "push_to_paired_phone",
			Cwd:     resolvedWorkDir,
			Reason:  "after the Hermes bundle is ready, broadcast `/insert <slug>` so the paired Yaver container navigates to Hot Reload + pulls",
			Command: "yaver insert <slug>   (or POST /blackbox/command-stream open_app)",
		}
		// Don't claim it's done — there's no reliable "is a phone
		// paired" probe at this layer. We surface the step so the UX
		// shows the full chain even when the build hasn't run yet.
		plan = append(plan, pushStep)
	}

	out := map[string]interface{}{
		"workDir":         req.WorkDir,
		"resolvedWorkDir": resolvedWorkDir,
		"pickerNote":      pickerNote,
		"framework":       framework,
		"packageManager":  prep.PackageManager,
		"workspaceRoot":   prep.WorkspaceRoot,
		"discovery": map[string]interface{}{
			"missingTools":               prep.MissingTools,
			"dependenciesInstalled":      prep.DependenciesInstalled,
			"needsDependencyInstall":     prep.NeedsDependencyInstall,
			"canAutoInstallDependencies": prep.CanAutoInstallDependencies,
			"hermesCompiler":             prep.HermesCompiler,
			"hermesCompilerError":        prep.HermesCompilerError,
		},
		"plan":   plan,
		"issues": issues,
	}
	return OpsResult{OK: true, Initial: out}
}

// installCommandFor returns an illustrative shell-form install command
// for each package manager. Display only — the agent's real install
// path runs through installProjectDependenciesTo with augmentEnv +
// captured tail.
func installCommandFor(pm string) string {
	switch pm {
	case "yarn":
		return "yarn install"
	case "pnpm":
		return "pnpm install"
	case "bun":
		return "bun install"
	default:
		return "npm install --legacy-peer-deps"
	}
}

// devCommandFor mirrors the per-framework default dev command so the
// plan card can show what's about to spawn.
func devCommandFor(fw string) string {
	switch fw {
	case "expo":
		return "npx expo start"
	case "react-native":
		return "npx react-native start"
	case "vite":
		return "npx vite"
	case "nextjs":
		return "npx next dev"
	case "flutter":
		return "flutter run"
	}
	return "(framework-dependent)"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
