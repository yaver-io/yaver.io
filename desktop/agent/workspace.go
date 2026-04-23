package main

// workspace.go — declarative monorepo manifest.
//
// A single file at repo root (yaver.workspace.yaml by default) declares
// every app in the monorepo, the stack each one is built on, which
// providers it deploys to, and which shared infrastructure the whole
// repo uses. Commands like `yaver workspace init` walk that manifest
// and wire every app with its own autoinit context, per-app primary
// device, shared vault keys, and sensible provider defaults — so a
// 12-app monorepo onboards in a minute instead of a half-day.
//
// Design principles:
//
//   1. Declarative. The manifest is the source of truth. Commands
//      converge the machine state to match — they never invent
//      config the user didn't write.
//
//   2. Additive. Re-running `workspace init` is idempotent. Apps
//      already initialised are skipped (unless --force). Apps in
//      the manifest but missing on disk are surfaced as warnings,
//      not errors, so the user can migrate their monorepo over time.
//
//   3. Stack-aware. Each app declares its stack (nextjs, react-native,
//      go, convex, relay, ...); the workspace engine knows the
//      canonical commands (build/test/deploy) for each so a caller
//      can do `ops workspace:build --app=mobile` without remembering
//      whether that app is Expo vs native.
//
//   4. Provider-neutral. `provider:` blocks are hints; actual
//      execution still goes through the individual MCP tools (cf_deploy,
//      vercel_env, etc.). A workspace can move apps between providers
//      by editing the manifest; `workspace switch` (follow-up) will
//      run the full switch-engine 7-layer migration per app.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspaceManifest is the parsed yaver.workspace.yaml. Every field
// is optional so the simplest valid manifest is just `apps: [...]`.
type WorkspaceManifest struct {
	Version   int                    `yaml:"version" json:"version"`
	Name      string                 `yaml:"name" json:"name,omitempty"`
	Workspace WorkspaceConfig        `yaml:"workspace" json:"workspace"`
	Apps      []WorkspaceApp         `yaml:"apps" json:"apps"`
	Shared    WorkspaceShared        `yaml:"shared" json:"shared,omitempty"`
	Meta      map[string]interface{} `yaml:"meta,omitempty" json:"meta,omitempty"`
}

// WorkspaceConfig holds repo-wide defaults.
type WorkspaceConfig struct {
	// Root: repo root relative to the manifest file. "." for typical use.
	Root string `yaml:"root" json:"root,omitempty"`
	// PrimaryDevice: the machine that should own builds by default.
	// "auto" resolves to userSettings.primaryDeviceId. Explicit deviceId
	// pins every `workspace build` / `ops primary ...` to that host.
	PrimaryDevice string `yaml:"primary_device" json:"primaryDevice,omitempty"`
	// Relay: "managed" | "self-hosted" | "disabled". Hints to `workspace
	// init` whether to auto-configure relayUrl from platform config or
	// leave it empty for the user to fill.
	Relay string `yaml:"relay" json:"relay,omitempty"`
	// Vault: "local" | "shared". Shared vault is a follow-up.
	Vault string `yaml:"vault" json:"vault,omitempty"`
	// Placement: repo-wide defaults for runtime-role resolution. Phase 1 only
	// reads these for summaries; later phases will use them during apply/plan.
	Placement WorkspacePlacementConfig `yaml:"placement,omitempty" json:"placement,omitempty"`
}

type WorkspacePlacementConfig struct {
	DefaultExecutionRole string `yaml:"default_execution_role,omitempty" json:"defaultExecutionRole,omitempty"`
	ManagedCloudFallback bool   `yaml:"managed_cloud_fallback,omitempty" json:"managedCloudFallback,omitempty"`
	BudgetMode           string `yaml:"budget_mode,omitempty" json:"budgetMode,omitempty"`
}

// WorkspaceApp is one app / package / service in the monorepo.
type WorkspaceApp struct {
	// Name: unique identifier within the workspace. Required.
	Name string `yaml:"name" json:"name"`
	// Path: path to the app root relative to Workspace.Root. Required.
	Path string `yaml:"path" json:"path"`
	// Stack: canonical stack label. Determines which build/test/deploy
	// commands the workspace engine uses. Known stacks: nextjs,
	// react-native-expo, react-native, flutter, go, convex, relay,
	// bun, node, python, rust.
	Stack string `yaml:"stack" json:"stack,omitempty"`
	// Provider: per-action hints, e.g. {deploy: cloudflare, release:
	// testflight, analytics: plausible}. Values are opaque to the
	// workspace engine; the action handlers interpret them.
	Provider map[string]string `yaml:"provider" json:"provider,omitempty"`
	// Scripts: overrides for the canonical commands. A rare escape
	// hatch; usually the stack-default is enough. Keys: build, test,
	// deploy, start, clean.
	Scripts map[string]string `yaml:"scripts" json:"scripts,omitempty"`
	// Depends: app names this one depends on (for `workspace build`
	// ordering). Cycles are rejected at parse time.
	Depends []string `yaml:"depends" json:"depends,omitempty"`
	// Env: per-app env var names to populate from the shared vault /
	// host environment when kicking any action.
	Env []string `yaml:"env" json:"env,omitempty"`
	// Runtime: coarse runtime hints for app-level placement.
	Runtime WorkspaceAppRuntime `yaml:"runtime,omitempty" json:"runtime,omitempty"`
}

type WorkspaceAppRuntime struct {
	PublicSurface bool   `yaml:"public_surface,omitempty" json:"publicSurface,omitempty"`
	MachineRole   string `yaml:"machine_role,omitempty" json:"machineRole,omitempty"`
}

// WorkspaceShared declares repo-wide shared bits (env, secrets).
type WorkspaceShared struct {
	// Env: names of env vars every app expects. workspace init checks
	// they're set (or in the vault) before wiring each app.
	Env []string `yaml:"env" json:"env,omitempty"`
	// Secrets: glob patterns of vault keys every app should inherit.
	// Example: "yaver-*" picks up yaver-apple-key, yaver-play-key, ...
	Secrets []string `yaml:"secrets" json:"secrets,omitempty"`
}

// WorkspaceManifestPath is the file the workspace engine reads from.
// Callers can override via WorkspaceManifestPathOverride for tests.
const WorkspaceManifestPath = "yaver.workspace.yaml"

// WorkspaceManifestPathOverride is consulted first when set — lets
// tests inject a manifest without mutating the repo root. Empty means
// "use the default WorkspaceManifestPath".
var WorkspaceManifestPathOverride string

// LoadWorkspaceManifest parses the manifest file at repoRoot (or the
// current working directory if repoRoot is empty). Returns nil +
// typed error if the file doesn't exist; callers decide whether that
// is fatal (init) or informational (list).
func LoadWorkspaceManifest(repoRoot string) (*WorkspaceManifest, error) {
	path := WorkspaceManifestPathOverride
	if path == "" {
		if repoRoot == "" {
			repoRoot = "."
		}
		path = filepath.Join(repoRoot, WorkspaceManifestPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no workspace manifest at %s (run `yaver workspace init --scaffold` to create one)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m WorkspaceManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateWorkspaceManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateWorkspaceManifest checks shape + dependency cycles. Every
// failure is a typed error suitable for CLI / MCP output.
func validateWorkspaceManifest(m *WorkspaceManifest) error {
	if m == nil {
		return fmt.Errorf("nil manifest")
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d (expected 1)", m.Version)
	}
	if len(m.Apps) == 0 {
		return fmt.Errorf("manifest has no apps — declare at least one under `apps:`")
	}
	seen := map[string]bool{}
	for i, a := range m.Apps {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("apps[%d]: name is required", i)
		}
		if seen[a.Name] {
			return fmt.Errorf("apps[%d]: duplicate name %q", i, a.Name)
		}
		seen[a.Name] = true
		if strings.TrimSpace(a.Path) == "" {
			return fmt.Errorf("app %q: path is required", a.Name)
		}
	}
	// Dependency sanity: every `depends` entry must reference a known
	// app. Cycles are detected by topological sort below.
	for _, a := range m.Apps {
		for _, dep := range a.Depends {
			if !seen[dep] {
				return fmt.Errorf("app %q depends on unknown app %q", a.Name, dep)
			}
		}
	}
	if _, err := TopoSortApps(m); err != nil {
		return err
	}
	return nil
}

// TopoSortApps returns apps in dependency order (deps first). Useful
// for `workspace build`: build leaf dependencies before the apps that
// depend on them.
func TopoSortApps(m *WorkspaceManifest) ([]string, error) {
	// Kahn's algorithm. indegree + reverse-adjacency.
	indeg := map[string]int{}
	rev := map[string][]string{}
	names := make([]string, 0, len(m.Apps))
	for _, a := range m.Apps {
		names = append(names, a.Name)
		indeg[a.Name] += 0
		for _, d := range a.Depends {
			indeg[a.Name]++
			rev[d] = append(rev[d], a.Name)
		}
	}
	sort.Strings(names)
	// Sort rev[dep] alphabetically too so tiebreaks are deterministic
	// regardless of declaration order in the manifest.
	for k := range rev {
		sort.Strings(rev[k])
	}
	queue := make([]string, 0, len(names))
	for _, n := range names {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	var out []string
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		out = append(out, head)
		for _, next := range rev[head] {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(out) != len(names) {
		return nil, fmt.Errorf("workspace dependency cycle detected (apps involved: %v)", missingFromSlice(names, out))
	}
	return out, nil
}

func missingFromSlice(all, present []string) []string {
	seen := map[string]bool{}
	for _, p := range present {
		seen[p] = true
	}
	var out []string
	for _, a := range all {
		if !seen[a] {
			out = append(out, a)
		}
	}
	return out
}

// ScaffoldWorkspaceManifest generates a starter yaver.workspace.yaml
// from the contents of repoRoot. Scans one level deep for obvious
// app markers (package.json, go.mod, Cargo.toml, pubspec.yaml, ...)
// so the user gets a runnable manifest without hand-writing it.
//
// Returns the YAML bytes and the list of apps detected. Writing the
// file is the caller's responsibility — keeps this function testable
// without a real filesystem root.
func ScaffoldWorkspaceManifest(repoRoot string) ([]byte, *WorkspaceManifest, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	name := filepath.Base(mustAbs(repoRoot))
	m := &WorkspaceManifest{
		Version: 1,
		Name:    name,
		Workspace: WorkspaceConfig{
			Root:          ".",
			PrimaryDevice: "auto",
			Relay:         "managed",
			Vault:         "local",
		},
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
			continue
		}
		app := detectWorkspaceApp(filepath.Join(repoRoot, e.Name()), e.Name())
		if app != nil {
			m.Apps = append(m.Apps, *app)
		}
	}
	// Sort apps alphabetically for a stable scaffold.
	sort.Slice(m.Apps, func(i, j int) bool { return m.Apps[i].Name < m.Apps[j].Name })
	if len(m.Apps) == 0 {
		return nil, nil, fmt.Errorf("no apps detected in %s — scaffold cannot guess; write apps: by hand", repoRoot)
	}
	buf, err := yaml.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	return buf, m, nil
}

// detectWorkspaceApp inspects a candidate directory and returns a
// WorkspaceApp describing it, or nil when nothing recognisable is
// present. Order matters — the first match wins so a repo with
// package.json + go.mod at the top prefers the language more likely
// to be the primary build target (Go for us, but tunable).
func detectWorkspaceApp(dir, name string) *WorkspaceApp {
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, rel))
		return err == nil
	}
	app := &WorkspaceApp{Name: name, Path: "./" + name}
	switch {
	case has("app.json"), has("app.config.js"), has("app.config.ts"), has("eas.json"):
		app.Stack = "react-native-expo"
	case has("pubspec.yaml"):
		app.Stack = "flutter"
	case has("Cargo.toml"):
		app.Stack = "rust"
	case has("go.mod"):
		app.Stack = "go"
	case has("next.config.js"), has("next.config.ts"), has("next.config.mjs"):
		app.Stack = "nextjs"
	case has("convex.json"), has("convex/schema.ts"), has("convex/_generated"):
		app.Stack = "convex"
	case has("bun.lock"), has("bunfig.toml"):
		app.Stack = "bun"
	case has("package.json"):
		app.Stack = "node"
	case has("pyproject.toml"), has("setup.py"):
		app.Stack = "python"
	case has("build.gradle"), has("build.gradle.kts"):
		app.Stack = "gradle"
	default:
		return nil
	}
	return app
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
