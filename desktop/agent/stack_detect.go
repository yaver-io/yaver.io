package main

// stack_detect.go — the canonical project-stack detector.
//
// Before this file, Yaver detected a project's framework/backend/hosting
// in six independent places, each with its own vocabulary (Next.js was
// "nextjs", "next" AND "next.js"; Expo was "expo" and "react-native-expo"),
// bridged by hand-written translation maps in devserver_kind.go. None of
// it reached the deploy path — ops_deploy demanded an explicit target and
// the supabase_* tools were blind CLI passthroughs that never checked for
// supabase/config.toml.
//
// This is the one source of truth. It answers: what IS this project, what
// can it deploy to, and what proved it. Everything else — the legacy
// detectors, ops deploy target defaulting, the UI chips on every surface —
// reads through here.
//
// Two design rules:
//
//  1. Adding a provider is a TABLE ENTRY, not a code path. Append to
//     stackProviders and the detector, the ops deploy target, the MCP
//     verb, and every UI surface pick it up for free.
//  2. Every conclusion carries its evidence (which file, at which path,
//     proved it). A detector that can't show its work can't be debugged
//     when it guesses wrong on someone else's monorepo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------
// Canonical vocabulary. ONE spelling each. Legacy detectors translate to
// their own dialect on the way out (see stack_detect_compat.go); nothing
// new should introduce a second spelling.
// ---------------------------------------------------------------------

const (
	FwExpo        = "expo"
	FwReactNative = "react-native"
	FwFlutter     = "flutter"
	FwNextJS      = "nextjs"
	FwVite        = "vite"
	FwReact       = "react"
	FwSwift       = "swift"
	FwKotlin      = "kotlin"
	FwGo          = "go"
	FwRust        = "rust"
	FwPython      = "python"
)

// providerKind groups targets so a UI can section them without knowing
// each provider by name.
type providerKind string

const (
	kindBackend   providerKind = "backend"   // data plane: convex, supabase, firebase
	kindWebHost   providerKind = "web"       // cloudflare, vercel, netlify, fly, railway
	kindMobile    providerKind = "mobile"    // testflight, playstore
	kindContainer providerKind = "container" // docker
	kindORM       providerKind = "orm"       // prisma, drizzle
)

// ---------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------

// StackEvidence records WHY the detector concluded something. Path is
// always relative to the scanned root — never absolute, so this struct is
// safe to send to Convex under the privacy contract (an absolute path
// leaks the user's home-dir username).
type StackEvidence struct {
	Implies string `json:"implies"`          // "supabase"
	Signal  string `json:"signal"`           // "supabase/config.toml"
	Path    string `json:"path"`             // "packages/api" ("" = root)
	Weak    bool   `json:"weak,omitempty"`   // inferred from a dep, not a config file
	Detail  string `json:"detail,omitempty"` // human note
}

// TargetAction is one thing you can DO with a detected provider. This is
// what a UI renders as a button and what an agent calls as an ops verb.
type TargetAction struct {
	ID    string `json:"id"`    // "functions-deploy"
	Label string `json:"label"` // "Deploy Functions"
	// OpsTarget is the value to pass as ops deploy {target}. Empty means
	// this action isn't reachable via the deploy verb (see MCPTool).
	OpsTarget string `json:"opsTarget,omitempty"`
	// MCPTool names a first-class tool when the action isn't a deploy
	// (e.g. supabase_migrations for a read-only migration list).
	MCPTool string `json:"mcpTool,omitempty"`
	// Destructive actions must never be one-tap on any surface. The car
	// surface already gates deploy/push/delete behind a spoken confirm;
	// this flag is how every other surface learns to do the same.
	Destructive bool `json:"destructive,omitempty"`
}

// DetectedTarget is a provider found in the project, plus what it can do.
type DetectedTarget struct {
	ID        string         `json:"id"`   // "supabase"
	Name      string         `json:"name"` // "Supabase"
	Kind      providerKind   `json:"kind"`
	Actions   []TargetAction `json:"actions"`
	Supported bool           `json:"supported"`
	Reason    string         `json:"reason,omitempty"` // why unsupported
	Evidence  string         `json:"evidence"`         // the signal that proved it
	Weak      bool           `json:"weak,omitempty"`   // dep-inferred, not config-proven
}

// StackDetection is the canonical answer for one directory.
type StackDetection struct {
	// Root is ABSOLUTE and must never be synced to Convex.
	Root string `json:"root"`
	// RelPath is relative to the monorepo root ("" for the root itself).
	// This is the Convex-safe location identifier.
	RelPath string `json:"relPath"`
	Name    string `json:"name"`

	Framework string   `json:"framework,omitempty"`
	Backend   string   `json:"backend,omitempty"` // canonical BackendKind value
	Hosting   []string `json:"hosting,omitempty"`
	ORM       string   `json:"orm,omitempty"`
	Services  []string `json:"services,omitempty"`

	// Tags are the UI chips. Union of framework + backend + hosting +
	// orm + language markers, deduped and stable-sorted.
	Tags    []string         `json:"tags,omitempty"`
	Targets []DetectedTarget `json:"targets,omitempty"`

	Evidence []StackEvidence `json:"evidence,omitempty"`
	// Warnings surface things the user should know but that don't block
	// detection (e.g. a self-hosted Convex on the public default admin key).
	Warnings []string `json:"warnings,omitempty"`

	IsMonorepo bool              `json:"isMonorepo,omitempty"`
	Packages   []*StackDetection `json:"packages,omitempty"`
}

// ---------------------------------------------------------------------
// The provider registry — the extension point.
// ---------------------------------------------------------------------

type stackProvider struct {
	ID   string
	Name string
	Kind providerKind

	// Files proves the provider outright (a config file is intent).
	Files []string
	// Dirs proves it when the directory exists.
	Dirs []string
	// PkgDeps is a WEAK signal — a dependency means the project talks to
	// the provider, not that it deploys to it. A repo with
	// @supabase/supabase-js but no supabase/config.toml is a CLIENT of
	// someone else's Supabase; offering it "supabase db push" would be
	// wrong. Weak signals tag but never produce a supported deploy target.
	PkgDeps []string

	// Backend/ORM/Hosting set the corresponding canonical field.
	Backend string
	ORM     string
	Hosting bool

	Tag     string
	Actions []TargetAction

	// Supported=false renders the target disabled with Reason.
	Unsupported string
}

// stackProviders is the whole provider surface. ADD A PROVIDER HERE and
// it lights up in detection, ops deploy, MCP, and every UI surface.
var stackProviders = []stackProvider{
	{
		ID: "supabase", Name: "Supabase", Kind: kindBackend,
		Files:   []string{"supabase/config.toml", "supabase.json"},
		Dirs:    []string{"supabase"},
		PkgDeps: []string{"@supabase/supabase-js", "supabase"},
		Backend: string(BackendSupabase), Tag: "supabase",
		Actions: []TargetAction{
			{ID: "functions-deploy", Label: "Deploy Functions", OpsTarget: "supabase-functions"},
			{ID: "db-push", Label: "Push DB", OpsTarget: "supabase-db", Destructive: true},
			{ID: "migrations", Label: "Migrations", MCPTool: "supabase_migrations"},
			{ID: "status", Label: "Status", MCPTool: "supabase_status"},
		},
	},
	{
		ID: "convex", Name: "Convex", Kind: kindBackend,
		Files:   []string{"convex.json", "convex/_generated"},
		Dirs:    []string{"convex"},
		PkgDeps: []string{"convex"},
		Backend: string(BackendConvex), Tag: "convex",
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy Backend", OpsTarget: "convex"},
			{ID: "schema", Label: "Schema", MCPTool: "convex_schema"},
			{ID: "logs", Label: "Logs", MCPTool: "convex_logs"},
		},
	},
	{
		ID: "firebase", Name: "Firebase", Kind: kindBackend,
		Files:   []string{"firebase.json", ".firebaserc"},
		PkgDeps: []string{"firebase", "firebase-admin"},
		Tag:     "firebase",
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy", OpsTarget: "firebase"},
			{ID: "rollback", Label: "Rollback Hosting", OpsTarget: "firebase", Destructive: true},
			{ID: "crashlytics", Label: "Crashlytics", MCPTool: "firebase_crashlytics"},
		},
	},
	{
		ID: "cloudflare", Name: "Cloudflare", Kind: kindWebHost,
		Files: []string{"wrangler.toml", "wrangler.jsonc", "wrangler.json"},
		Tag:   "cloudflare", Hosting: true,
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy Worker", OpsTarget: "cloudflare"},
			{ID: "pages", Label: "Deploy Pages", OpsTarget: "pages"},
			{ID: "rollback", Label: "Rollback", OpsTarget: "cloudflare", Destructive: true},
		},
	},
	{
		ID: "vercel", Name: "Vercel", Kind: kindWebHost,
		Files: []string{"vercel.json", ".vercel"},
		Tag:   "vercel", Hosting: true,
		Actions: []TargetAction{
			{ID: "deploy-preview", Label: "Deploy Preview", OpsTarget: "vercel"},
			{ID: "deploy-prod", Label: "Deploy Production", OpsTarget: "vercel", Destructive: true},
			{ID: "rollback", Label: "Rollback", OpsTarget: "vercel", Destructive: true},
			{ID: "logs", Label: "Logs", MCPTool: "vercel_logs"},
			{ID: "env", Label: "Env", MCPTool: "vercel_env"},
		},
	},
	{
		ID: "netlify", Name: "Netlify", Kind: kindWebHost,
		Files: []string{"netlify.toml"},
		Tag:   "netlify", Hosting: true,
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy", OpsTarget: "netlify"},
			{ID: "rollback", Label: "Rollback", OpsTarget: "netlify", Destructive: true},
		},
	},
	{
		ID: "fly", Name: "Fly.io", Kind: kindWebHost,
		Files: []string{"fly.toml"},
		Tag:   "fly", Hosting: true,
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy", OpsTarget: "fly"},
			{ID: "rollback", Label: "Rollback", OpsTarget: "fly", Destructive: true},
		},
	},
	{
		ID: "railway", Name: "Railway", Kind: kindWebHost,
		Files: []string{"railway.json", "railway.toml"},
		Tag:   "railway", Hosting: true,
		Actions: []TargetAction{
			{ID: "deploy", Label: "Deploy", OpsTarget: "railway"},
			{ID: "rollback", Label: "Rollback", OpsTarget: "railway", Destructive: true},
		},
	},
	{
		ID: "docker", Name: "Docker", Kind: kindContainer,
		Files: []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yaml"},
		Tag:   "docker",
		Actions: []TargetAction{
			{ID: "compose-up", Label: "Run Container", MCPTool: "docker_compose"},
		},
	},
	{
		ID: "prisma", Name: "Prisma", Kind: kindORM,
		Files:   []string{"prisma/schema.prisma"},
		Dirs:    []string{"prisma"},
		PkgDeps: []string{"prisma", "@prisma/client"},
		ORM:     "prisma", Tag: "prisma",
		Actions: []TargetAction{
			{ID: "generate", Label: "Generate", MCPTool: "prisma_generate"},
			{ID: "push", Label: "Push Schema", MCPTool: "prisma_push", Destructive: true},
		},
	},
	{
		ID: "drizzle", Name: "Drizzle", Kind: kindORM,
		Files:   []string{"drizzle.config.ts", "drizzle.config.js"},
		PkgDeps: []string{"drizzle-orm", "drizzle-kit"},
		ORM:     "drizzle", Tag: "drizzle",
		Actions: []TargetAction{
			{ID: "generate", Label: "Generate", MCPTool: "drizzle_generate"},
			{ID: "push", Label: "Push Schema", MCPTool: "drizzle_push", Destructive: true},
		},
	},
}

// ---------------------------------------------------------------------
// Framework detection — ordered, first match wins.
//
// Order is load-bearing and mirrors the ordering contract already
// documented in classify.go:319: JS/Flutter checks MUST run before the
// native ones, so a React Native repo (which always has ios/*.xcodeproj)
// is never labelled "swift", and a Kotlin JVM backend is never labelled
// as a Kotlin *mobile* project.
// ---------------------------------------------------------------------

func detectCanonicalFramework(dir string, pkg pkgJSON) (string, StackEvidence) {
	// Expo before react-native: every Expo app also depends on react-native.
	if pkg.hasDep("expo") {
		return FwExpo, StackEvidence{Implies: FwExpo, Signal: "package.json:expo"}
	}
	if pkg.hasDep("react-native") {
		return FwReactNative, StackEvidence{Implies: FwReactNative, Signal: "package.json:react-native"}
	}
	if f := firstExisting(dir, "pubspec.yaml"); f != "" {
		return FwFlutter, StackEvidence{Implies: FwFlutter, Signal: f}
	}
	if f := firstExisting(dir, "next.config.ts", "next.config.js", "next.config.mjs"); f != "" {
		return FwNextJS, StackEvidence{Implies: FwNextJS, Signal: f}
	}
	if f := firstExisting(dir, "vite.config.ts", "vite.config.js", "vite.config.mts"); f != "" {
		return FwVite, StackEvidence{Implies: FwVite, Signal: f}
	}
	if pkg.hasDep("react") {
		return FwReact, StackEvidence{Implies: FwReact, Signal: "package.json:react"}
	}
	// Native only after JS/Flutter fall through.
	if f := firstExisting(dir, "Package.swift"); f != "" {
		return FwSwift, StackEvidence{Implies: FwSwift, Signal: f}
	}
	if hasDir(dir, ".xcodeproj") {
		return FwSwift, StackEvidence{Implies: FwSwift, Signal: "*.xcodeproj"}
	}
	if isKotlinAndroidProject(dir) {
		return FwKotlin, StackEvidence{Implies: FwKotlin, Signal: "build.gradle.kts"}
	}
	if f := firstExisting(dir, "go.mod"); f != "" {
		return FwGo, StackEvidence{Implies: FwGo, Signal: f}
	}
	if f := firstExisting(dir, "Cargo.toml"); f != "" {
		return FwRust, StackEvidence{Implies: FwRust, Signal: f}
	}
	if f := firstExisting(dir, "pyproject.toml", "setup.py", "requirements.txt"); f != "" {
		return FwPython, StackEvidence{Implies: FwPython, Signal: f}
	}
	return "", StackEvidence{}
}

// ---------------------------------------------------------------------
// package.json — parsed, not substring-sniffed.
//
// The legacy detectors do strings.Contains(data, `"expo"`) which is
// order-dependent on JSON formatting and matches the dep name anywhere in
// the file (including inside a "scripts" command or a resolutions block).
// Parsing the actual dependency maps and looking up exact keys is cheap
// and doesn't have those failure modes.
// ---------------------------------------------------------------------

type pkgJSON struct {
	Name    string
	deps    map[string]string
	present bool
	// Workspaces drives monorepo package discovery.
	Workspaces []string
}

func readPkgJSON(dir string) pkgJSON {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pkgJSON{}
	}
	var raw struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		PeerDeps        map[string]string `json:"peerDependencies"`
		// npm/yarn allow either form: ["packages/*"] or {"packages": [...]}.
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return pkgJSON{}
	}
	p := pkgJSON{Name: raw.Name, present: true, deps: map[string]string{}}
	for _, m := range []map[string]string{raw.Dependencies, raw.DevDependencies, raw.PeerDeps} {
		for k, v := range m {
			p.deps[k] = v
		}
	}
	p.Workspaces = parseWorkspaces(raw.Workspaces)
	return p
}

func parseWorkspaces(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Packages
	}
	return nil
}

func (p pkgJSON) hasDep(name string) bool {
	if !p.present {
		return false
	}
	_, ok := p.deps[name]
	return ok
}

// ---------------------------------------------------------------------
// The detector
// ---------------------------------------------------------------------

// stackDetect is the canonical entry point. It scans one directory and,
// when that directory is a monorepo root, each of its workspace packages.
//
// It is pure and does no network I/O — safe to call on every project list
// render. Callers that need it hot should cache; there is deliberately no
// cache here so a detector bug can't become a stale-state bug.
func stackDetect(root string) *StackDetection {
	d := detectOneDir(root, root)
	d.Packages = discoverPackages(root)
	if len(d.Packages) > 0 {
		d.IsMonorepo = true
		// Tags roll up; Targets deliberately do NOT.
		//
		// Tags DESCRIBE ("this repo contains Supabase somewhere") and let a
		// monorepo card show its whole stack without the UI walking the
		// tree. Targets ACT, and an action needs the one directory it runs
		// in — a "Deploy Supabase" button on the root of a repo whose
		// Supabase lives in packages/api would run supabase in the wrong
		// cwd. So the root card shows chips, and per-package cards carry
		// the buttons.
		for _, p := range d.Packages {
			d.Tags = append(d.Tags, p.Tags...)
		}
		d.Tags = dedupeSorted(d.Tags)
	}
	return d
}

// detectOneDir detects a single directory with no recursion.
func detectOneDir(dir, root string) *StackDetection {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		rel = ""
	}
	d := &StackDetection{Root: dir, RelPath: rel, Name: filepath.Base(dir)}

	pkg := readPkgJSON(dir)
	if pkg.Name != "" {
		d.Name = pkg.Name
	}

	if fw, ev := detectCanonicalFramework(dir, pkg); fw != "" {
		d.Framework = fw
		ev.Path = rel
		d.Evidence = append(d.Evidence, ev)
		d.Tags = append(d.Tags, fw)
	}

	for _, p := range stackProviders {
		hit, signal, weak := providerMatches(dir, p, pkg)
		if !hit {
			continue
		}
		d.Evidence = append(d.Evidence, StackEvidence{
			Implies: p.ID, Signal: signal, Path: rel, Weak: weak,
			Detail: weakDetail(weak, p),
		})
		if p.Tag != "" {
			d.Tags = append(d.Tags, p.Tag)
		}
		d.Services = append(d.Services, p.ID)
		if p.Backend != "" && d.Backend == "" && !weak {
			d.Backend = p.Backend
		}
		if p.ORM != "" && d.ORM == "" {
			d.ORM = p.ORM
		}
		if p.Hosting && !weak {
			d.Hosting = append(d.Hosting, p.ID)
		}

		t := DetectedTarget{
			ID: p.ID, Name: p.Name, Kind: p.Kind,
			Actions: p.Actions, Evidence: signal, Weak: weak,
			Supported: p.Unsupported == "", Reason: p.Unsupported,
		}
		// A weak (dep-only) hit means this project CONSUMES the provider
		// but doesn't own its deployment. Show the chip, disable the
		// deploy — pushing someone else's schema is not a recoverable
		// mistake.
		if weak {
			t.Supported = false
			t.Reason = "detected from a dependency, not a local config — no deployable " + p.Name + " project here"
		}
		d.Targets = append(d.Targets, t)
	}

	d.Warnings = append(d.Warnings, detectStackWarnings(dir, d)...)
	d.Tags = dedupeSorted(d.Tags)
	d.Services = dedupeSorted(d.Services)
	d.Hosting = dedupeSorted(d.Hosting)
	return d
}

func weakDetail(weak bool, p stackProvider) string {
	if !weak {
		return ""
	}
	return "package.json depends on " + p.Name + " but no local config file was found"
}

// providerMatches reports whether p is present in dir, the signal that
// proved it, and whether the proof was weak (dependency-only).
func providerMatches(dir string, p stackProvider, pkg pkgJSON) (hit bool, signal string, weak bool) {
	for _, f := range p.Files {
		if fileExists(filepath.Join(dir, filepath.FromSlash(f))) {
			return true, f, false
		}
	}
	for _, sub := range p.Dirs {
		if isDir(filepath.Join(dir, filepath.FromSlash(sub))) {
			return true, sub + "/", false
		}
	}
	for _, dep := range p.PkgDeps {
		if pkg.hasDep(dep) {
			return true, "package.json:" + dep, true
		}
	}
	return false, "", false
}

// detectStackWarnings surfaces things worth telling the user that don't
// block detection.
func detectStackWarnings(dir string, d *StackDetection) []string {
	var w []string
	// Self-hosted Convex on the upstream default admin key. convex.go
	// falls back to this key silently when nothing is configured, which
	// turns "misconfigured" into "wide open to anyone who can reach the
	// port" without ever surfacing an error. The value is public (it ships
	// in Convex OSS), so this is a posture warning, not a leaked secret.
	if d.Backend == string(BackendConvex) {
		if data, err := os.ReadFile(filepath.Join(dir, ".env.local")); err == nil {
			s := string(data)
			if strings.Contains(s, "CONVEX_SELF_HOSTED_URL") && !strings.Contains(s, "CONVEX_SELF_HOSTED_ADMIN_KEY") {
				w = append(w, "self-hosted Convex configured without CONVEX_SELF_HOSTED_ADMIN_KEY — the agent will fall back to the public default admin key")
			}
		}
	}
	return w
}

// ---------------------------------------------------------------------
// Monorepo package discovery
// ---------------------------------------------------------------------

// stackScanMaxDepth bounds the convention-scan fallback. Depth 2 is what
// real layouts need: yaver.io itself keeps its agent at desktop/agent, one
// level below a desktop/ dir that is itself not a project.
const stackScanMaxDepth = 2

// stackScanSkipDirs are never projects. Build output and platform folders
// contain marker files that would otherwise detect as bogus projects —
// mobile/ios holds an .xcodeproj that is part of the RN app, not a
// separate Swift project.
var stackScanSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"out": true, "coverage": true, "target": true, "__pycache__": true,
	"ios": true, "android": true, "Pods": true, "pods": true,
	".next": true, ".expo": true, ".git": true,
	"docs": true, "assets": true, "demo-videos": true, "keys": true,
}

// monorepoPackageDirs finds the deployable packages under root.
//
// Declared workspaces (package.json "workspaces", pnpm-workspace.yaml) are
// authoritative when present. But most real repos — yaver.io included —
// are CONVENTION monorepos: sibling web/ mobile/ backend/ dirs with no
// workspace declaration anywhere. Trusting only declarations meant this
// detector returned absolutely nothing on its own repo, so we fall back to
// a bounded scan.
//
// This is not the depth-6 walk in monorepo_detect.go. It stops descending
// as soon as a directory detects as a project: web/ is the deployable
// unit, and whatever lives at web/functions is part of web/, not a peer.
// It returns the detections themselves rather than paths: the scan has to
// detect a directory to know whether it IS a package, and throwing that
// result away only to recompute it is pure waste on a path that runs on
// every project-list render.
func discoverPackages(root string) []*StackDetection {
	var out []*StackDetection
	if declared := declaredWorkspaceDirs(root); len(declared) > 0 {
		for _, dir := range declared {
			if d := detectPackage(dir, root); d != nil {
				out = append(out, d)
			}
		}
	} else {
		scanForPackages(root, root, 1, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}

// detectPackage returns a detection for dir, or nil when dir holds nothing
// deployable or runnable — an empty package is noise, not a package.
func detectPackage(dir, root string) *StackDetection {
	d := detectOneDir(dir, root)
	if d.Framework == "" && len(d.Targets) == 0 {
		return nil
	}
	return d
}

func declaredWorkspaceDirs(root string) []string {
	globs := readPkgJSON(root).Workspaces
	globs = append(globs, pnpmWorkspaceGlobs(root)...)

	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		// filepath.Glob handles the common "packages/*" and "apps/*".
		// Recursive "**" globs aren't supported by Glob; strip the "**"
		// segment and match one level, which covers the realistic layouts.
		g = strings.ReplaceAll(g, "**/", "")
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if !isDir(m) || seen[m] || m == root {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// scanForPackages walks one level at a time, recording directories that
// detect as projects and pruning beneath them.
func scanForPackages(dir, root string, depth int, out *[]*StackDetection) {
	if depth > stackScanMaxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || stackScanSkipDirs[e.Name()] {
			continue
		}
		child := filepath.Join(dir, e.Name())
		if d := detectPackage(child, root); d != nil {
			*out = append(*out, d)
			continue // prune: sub-dirs belong to this package
		}
		scanForPackages(child, root, depth+1, out)
	}
}

func pnpmWorkspaceGlobs(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	// Deliberately not pulling in a YAML parse for a two-line file shape:
	// `packages:` followed by `  - "glob"` entries.
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		g := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- ")), `"'`)
		if g != "" && !strings.HasPrefix(g, "!") {
			out = append(out, g)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// firstExisting lives in monorepo_detect.go — it does exactly this, plus
// a *.xcodeproj glob branch we want anyway.

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// DeployableTargets returns the targets that can actually be deployed to
// right now — supported, and carrying at least one ops-reachable action.
// This is what ops_deploy uses to default its target and what a UI uses to
// decide which deploy buttons to render.
func (d *StackDetection) DeployableTargets() []DetectedTarget {
	var out []DetectedTarget
	for _, t := range d.Targets {
		if !t.Supported {
			continue
		}
		for _, a := range t.Actions {
			if a.OpsTarget != "" {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
