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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
	FwUnity       = "unity"
	FwYaverXML    = "yaver-xml"
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

	// Framework is the PRIMARY framework for legacy callers and compact
	// UI display. Frameworks carries every detected framework. Primary is
	// chosen by the ordered precedence in detectCanonicalFrameworks:
	// Expo before React Native, JS/Flutter before native. That ordering
	// is load-bearing and matches classify.go:319 so a RN app is never
	// labelled swift just because ios/*.xcodeproj exists.
	Framework  string   `json:"framework,omitempty"`
	Frameworks []string `json:"frameworks,omitempty"`
	// Stack is the primary workspace stack label. Stacks carries every
	// detected development stack in a vocabulary suitable for UI filters and
	// yaver.workspace.yaml. Framework remains for legacy callers.
	Stack  string   `json:"stack,omitempty"`
	Stacks []string `json:"stacks,omitempty"`
	// Surfaces are product targets this project can plausibly build for.
	// TestSurfaces are the Yaver preview/runtime surfaces that can exercise
	// them (browser, rn-hermes, simulator, emulator, webrtc). Both are
	// explicit so mobile/web/watch/tv/car/vision UIs do not need to reverse
	// engineer a stack string.
	Surfaces     []string `json:"surfaces,omitempty"`
	TestSurfaces []string `json:"testSurfaces,omitempty"`
	// Feedback metadata describes how the SDK loop reaches this stack.
	FeedbackSDK       string   `json:"feedbackSdk,omitempty"`
	FeedbackTransport string   `json:"feedbackTransport,omitempty"`
	VoiceCapabilities []string `json:"voiceCapabilities,omitempty"`
	Role              string   `json:"role,omitempty"`
	Backend           string   `json:"backend,omitempty"` // canonical BackendKind value
	Hosting           []string `json:"hosting,omitempty"`
	ORM               string   `json:"orm,omitempty"`
	Services          []string `json:"services,omitempty"`

	// Tags are the UI chips. Union of framework + backend + hosting +
	// orm + language markers, deduped and stable-sorted.
	Tags    []string         `json:"tags,omitempty"`
	Targets []DetectedTarget `json:"targets,omitempty"`

	Evidence []StackEvidence `json:"evidence,omitempty"`
	// Warnings surface things the user should know but that don't block
	// detection (e.g. a self-hosted Convex on the public default admin key).
	Warnings []string `json:"warnings,omitempty"`
	// Roles is only populated on a monorepo root: role -> primary stack.
	Roles       map[string]string `json:"roles,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	DetectedAt  time.Time         `json:"detectedAt,omitempty"`

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

func detectCanonicalFrameworks(dir string, pkg pkgJSON) ([]string, []StackEvidence) {
	var frameworks []string
	var evidence []StackEvidence
	add := func(framework, signal string) {
		if framework == "" || signal == "" {
			return
		}
		frameworks = append(frameworks, framework)
		evidence = append(evidence, StackEvidence{Implies: framework, Signal: signal})
	}
	// Expo before react-native: every Expo app also depends on react-native.
	if pkg.hasDep("expo") {
		add(FwExpo, "package.json:expo")
	}
	if pkg.hasDep("react-native") {
		add(FwReactNative, "package.json:react-native")
	}
	if f := firstExisting(dir, "pubspec.yaml"); f != "" {
		add(FwFlutter, f)
	}
	if f := firstExisting(dir, "next.config.ts", "next.config.js", "next.config.mjs"); f != "" {
		add(FwNextJS, f)
	}
	if f := firstExisting(dir, "vite.config.ts", "vite.config.js", "vite.config.mts"); f != "" {
		add(FwVite, f)
	}
	if pkg.hasDep("react") {
		add(FwReact, "package.json:react")
	}
	// Native only after JS/Flutter fall through.
	if f := firstExisting(dir, "Package.swift"); f != "" {
		add(FwSwift, f)
	}
	if hasDir(dir, ".xcodeproj") {
		add(FwSwift, "*.xcodeproj")
	}
	if isKotlinAndroidProject(dir) {
		add(FwKotlin, "build.gradle.kts")
	}
	if f := firstExisting(dir, "go.mod"); f != "" {
		add(FwGo, f)
	}
	if f := firstExisting(dir, "Cargo.toml"); f != "" {
		add(FwRust, f)
	}
	if f := firstExisting(dir, "pyproject.toml", "setup.py", "requirements.txt"); f != "" {
		add(FwPython, f)
	}
	if f := firstExisting(dir, "ProjectSettings/ProjectVersion.txt", "Packages/manifest.json"); f != "" && isDir(filepath.Join(dir, "Assets")) {
		add(FwUnity, f)
	}
	if f := firstExisting(dir, "yaver.xml", "yaver.config.xml", ".yaver/project.xml"); f != "" {
		add(FwYaverXML, f)
	}
	frameworks = dedupeSorted(frameworks)
	sort.SliceStable(evidence, func(i, j int) bool {
		return frameworkRank(evidence[i].Implies) < frameworkRank(evidence[j].Implies)
	})
	return frameworks, evidence
}

func frameworkRank(framework string) int {
	switch framework {
	case FwExpo:
		return 0
	case FwReactNative:
		return 1
	case FwFlutter:
		return 2
	case FwNextJS:
		return 3
	case FwVite:
		return 4
	case FwReact:
		return 5
	case FwSwift:
		return 6
	case FwKotlin:
		return 7
	case FwGo:
		return 8
	case FwRust:
		return 9
	case FwPython:
		return 10
	case FwUnity:
		return 11
	case FwYaverXML:
		return 12
	default:
		return 999
	}
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

// stackDetect is the canonical uncached entry point. It scans one
// directory and, when that directory is a monorepo root, each of its
// workspace packages. Use stackDetectCached on hot HTTP paths.
// ─── Default stack for a NEW project ────────────────────────────────────────
//
// When a user asks for a workspace without saying what they are building, this
// is what they get: React Native + TypeScript, previewed through the web dev
// server in Chrome (WebRTC), with Hermes pushed to their OWN phone.
//
// Why this specific default, from docs/architecture/yaver-four-tier-deep-analysis.md §9:
//   - RN + Hermes-push-to-a-real-phone is Yaver's differentiator, so the
//     opinionated path should be the one the product is best at.
//   - It is LIGHTWEIGHT: browser preview means no emulator, no Redroid, no GPU.
//     That is precisely what lets the default machine be 2c/4GB and the $29
//     tier hold ~71% margin. Defaulting to a native-Android loop would force
//     8-16 GB on every workspace to serve a case most users never hit.
//   - The phone is the user's own hardware, so the device side costs us nothing
//     and is a more honest test than an emulator anyway.
//
// ⚠️ This is a CREATION default, never a detection default. stackDetect() must
// keep reporting "unknown" for a directory it cannot identify — inventing a
// stack for existing code would make every downstream decision (build command,
// deploy target, machine class) confidently wrong. Truth for what exists;
// opinion only for what does not exist yet.
func defaultStackForNewProject() *StackDetection {
	return &StackDetection{
		Role:       "app",
		Frameworks: []string{"react-native", "expo"},
		Tags:       []string{"react-native", "expo", "typescript"},
		DetectedAt: time.Now().UTC(),
	}
}

// defaultMachineClassForStack maps a detected/defaulted stack to the machine
// class the workspace should be placed on.
//
// "standard" is 2c/4GB and covers the default path: Metro/Expo dev server, the
// TypeScript language server, Chrome headless and WebRTC passthrough. The
// heavier classes are OPT-IN, because sizing every workspace for its worst
// possible minute is what turns a 71% tier into a 42% one.
//
// Known ceiling: Metro on a large monorepo will exhaust 4 GB. The intended
// response is to DETECT that and offer the upgrade, not to pre-provision for
// it — see the class ladder in the deep analysis §6.4.
func defaultMachineClassForStack(d *StackDetection) string {
	if d == nil {
		return "standard"
	}
	for _, tag := range d.Tags {
		switch tag {
		// Android-in-a-container and native Gradle builds genuinely need the
		// memory; nothing else in the default loop does.
		case "redroid", "android-native", "kotlin", "gradle":
			return "build"
		}
	}
	// A monorepo is the known Metro ceiling — start one class up rather than
	// letting the first bundle build OOM.
	if d.IsMonorepo {
		return "heavy"
	}
	return "standard"
}

// WorkspacePlacementDefaults is the complete "user said nothing" answer:
// which stack, how it is previewed, and what machine that implies.
//
// Kept as ONE function because the three decisions are coupled — the preview
// mode drives the machine class, and choosing them independently is how a
// browser-previewed RN project ends up on an 8 GB box (or, worse, a Redroid
// project ends up on 4 GB and OOMs on first run).
type WorkspacePlacementDefaults struct {
	Stack        string      `json:"stack"`
	Preview      PreviewMode `json:"preview"`
	MachineClass string      `json:"machineClass"`
	Reason       string      `json:"reason"`
}

// DefaultWorkspacePlacement resolves defaults for a workspace.
//
// `detected` may be nil (a brand-new project with nothing on disk), in which
// case the creation default applies: React Native + TypeScript, browser
// preview, 2c/4GB. For an EXISTING directory the detected stack wins — we never
// invent a stack for code that is already there, because every downstream
// decision (build command, deploy target, machine class) would inherit the lie.
func DefaultWorkspacePlacement(detected *StackDetection) WorkspacePlacementDefaults {
	if detected == nil || len(detected.Frameworks) == 0 {
		return WorkspacePlacementDefaults{
			Stack:        "react-native-expo",
			Preview:      PreviewBrowser,
			MachineClass: "standard",
			Reason:       "no stack specified — defaulting to React Native + TypeScript with browser preview",
		}
	}
	stack := primaryFramework(detected.Frameworks)
	preview := DefaultPreviewModeForStack(stack)
	class := defaultMachineClassForStack(detected)
	// The preview mode can force a bigger box even when the stack would not.
	if PreviewModeNeedsHeavyMachine(preview) && class == "standard" {
		class = "build"
	}
	return WorkspacePlacementDefaults{
		Stack:        stack,
		Preview:      preview,
		MachineClass: class,
		Reason:       "detected " + stack,
	}
}

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
		d.Role = "unknown"
		d.Roles = rollupMonorepoRoles(d)
	}
	d.Fingerprint = computeStackFingerprint(d)
	d.DetectedAt = time.Now().UTC()
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

	if frameworks, evidence := detectCanonicalFrameworks(dir, pkg); len(frameworks) > 0 {
		d.Frameworks = frameworks
		d.Framework = primaryFramework(frameworks)
		d.Stacks = stackLabelsForFrameworks(frameworks)
		d.Stack = primaryStack(d.Stacks)
		for _, ev := range evidence {
			ev.Path = rel
			d.Evidence = append(d.Evidence, ev)
		}
		d.Tags = append(d.Tags, frameworks...)
		d.Tags = append(d.Tags, d.Stacks...)
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
	d.Role = detectStackRole(d)
	d.Surfaces = detectDevelopmentSurfaces(dir, d)
	d.TestSurfaces = detectTestSurfaces(d)
	d.FeedbackSDK = FeedbackSDKPackage(d.Stack)
	if d.FeedbackSDK == "" {
		d.FeedbackSDK = FeedbackSDKPackage(d.Framework)
	}
	d.FeedbackTransport = string(ResolveFeedbackBehaviour(firstNonEmptyStackString(d.Stack, d.Framework), false, false).Transport)
	d.VoiceCapabilities = feedbackVoiceCapabilities(d)
	d.Tags = dedupeSorted(d.Tags)
	d.Services = dedupeSorted(d.Services)
	d.Hosting = dedupeSorted(d.Hosting)
	sort.SliceStable(d.Evidence, func(i, j int) bool {
		if d.Evidence[i].Path != d.Evidence[j].Path {
			return d.Evidence[i].Path < d.Evidence[j].Path
		}
		if d.Evidence[i].Signal != d.Evidence[j].Signal {
			return d.Evidence[i].Signal < d.Evidence[j].Signal
		}
		return d.Evidence[i].Implies < d.Evidence[j].Implies
	})
	return d
}

func stackLabelsForFrameworks(frameworks []string) []string {
	var out []string
	for _, fw := range frameworks {
		switch fw {
		case FwExpo:
			out = append(out, "react-native-expo")
		case FwReactNative:
			out = append(out, "react-native")
		case FwNextJS:
			out = append(out, "nextjs")
		case FwYaverXML:
			out = append(out, "yaver-xml")
		case FwUnity:
			out = append(out, "unity")
		default:
			out = append(out, fw)
		}
	}
	return dedupeSorted(out)
}

func primaryStack(stacks []string) string {
	if len(stacks) == 0 {
		return ""
	}
	for _, want := range []string{"react-native-expo", "react-native", "flutter", "unity", "nextjs", "vite", "react", "swift", "kotlin", "go", "rust", "python", "yaver-xml"} {
		for _, s := range stacks {
			if s == want {
				return s
			}
		}
	}
	return stacks[0]
}

func detectDevelopmentSurfaces(dir string, d *StackDetection) []string {
	var out []string
	if d == nil {
		return nil
	}
	if d.Backend != "" || containsAnyString(d.Frameworks, FwGo, FwRust, FwPython) || hasTargetID(d, "docker") {
		out = append(out, "backend")
	}
	if containsAnyString(d.Frameworks, FwNextJS, FwVite, FwReact) {
		out = append(out, "web")
	}
	if containsAnyString(d.Frameworks, FwExpo, FwReactNative, FwFlutter) {
		out = append(out, "mobile", "web")
	}
	if containsAnyString(d.Frameworks, FwSwift) {
		out = append(out, "mobile")
	}
	if containsAnyString(d.Frameworks, FwKotlin) {
		out = append(out, "mobile")
	}
	if containsAnyString(d.Frameworks, FwYaverXML) {
		out = append(out, "mobile", "web", "backend", "watch", "tv", "car", "vision")
	}
	for _, marker := range []struct {
		surface string
		paths   []string
	}{
		{"watch", []string{"watch", "wear", "watchos", "WatchKit Extension"}},
		{"tv", []string{"tvos", "tv", "androidtv", "android-tv"}},
		{"car", []string{"car", "carplay", "android-auto", "automotive"}},
		{"vision", []string{"visionos", "vision", "xr", "ar", "vr"}},
	} {
		for _, p := range marker.paths {
			if fileExists(filepath.Join(dir, filepath.FromSlash(p))) || isDir(filepath.Join(dir, filepath.FromSlash(p))) {
				out = append(out, marker.surface)
				break
			}
		}
	}
	if containsAnyString(d.Frameworks, FwUnity) {
		out = append(out, "mobile", "web", "tv", "vision")
	}
	return dedupeSorted(out)
}

func detectTestSurfaces(d *StackDetection) []string {
	var out []string
	for _, surface := range d.Surfaces {
		switch surface {
		case "web":
			out = append(out, "browser")
		case "mobile":
			if containsAnyString(d.Frameworks, FwExpo, FwReactNative) {
				out = append(out, "rn-hermes", "browser", "ios-simulator", "android-emulator", "webrtc")
			} else if containsAnyString(d.Frameworks, FwFlutter) {
				out = append(out, "browser", "android-emulator", "webrtc")
			} else {
				out = append(out, "ios-simulator", "android-emulator", "webrtc")
			}
		case "watch":
			out = append(out, "watchos-simulator", "android-wear", "webrtc")
		case "tv":
			out = append(out, "tvos-simulator", "android-tv", "webrtc")
		case "car":
			out = append(out, "carplay-simulator", "android-auto", "webrtc")
		case "vision":
			out = append(out, "visionos-simulator", "android-xr", "browser", "webrtc")
		case "backend":
			out = append(out, "cli", "http")
		}
	}
	return dedupeSorted(out)
}

func feedbackVoiceCapabilities(d *StackDetection) []string {
	var out []string
	if d == nil {
		return nil
	}
	if d.FeedbackTransport != "" {
		out = append(out, "voice-notes", "voice-vibing", "stt", "tts")
	}
	if containsAnyString(d.Surfaces, "web") {
		out = append(out, "browser-mic", "browser-tts")
	}
	if containsAnyString(d.Surfaces, "mobile", "watch", "tv", "car", "vision") {
		out = append(out, "device-mic", "device-tts")
	}
	return dedupeSorted(out)
}

func firstNonEmptyStackString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func primaryFramework(frameworks []string) string {
	if len(frameworks) == 0 {
		return ""
	}
	best := frameworks[0]
	for _, fw := range frameworks[1:] {
		if frameworkRank(fw) < frameworkRank(best) {
			best = fw
		}
	}
	return best
}

func detectStackRole(d *StackDetection) string {
	switch {
	case d == nil:
		return "unknown"
	case d.IsMonorepo:
		return "unknown"
	case d.Backend != "" && !containsAnyString(d.Frameworks, FwExpo, FwReactNative, FwFlutter, FwSwift, FwKotlin, FwNextJS, FwVite, FwReact):
		return "backend"
	case containsAnyString(d.Frameworks, FwExpo, FwReactNative, FwFlutter, FwSwift, FwKotlin):
		return "mobile"
	case containsAnyString(d.Frameworks, FwNextJS, FwVite, FwReact):
		return "frontend"
	case containsAnyString(d.Frameworks, FwGo, FwRust, FwPython):
		if hasAnyHostingConfig(d.Hosting) || hasTargetID(d, "docker") {
			return "backend"
		}
		return "cli"
	case hasTargetID(d, "docker"):
		return "infra"
	case d.Framework == "" && len(d.Targets) == 0:
		return "unknown"
	default:
		return "library"
	}
}

func containsAnyString(in []string, want ...string) bool {
	for _, s := range in {
		for _, w := range want {
			if s == w {
				return true
			}
		}
	}
	return false
}

func hasAnyHostingConfig(hosting []string) bool { return len(hosting) > 0 }

func hasTargetID(d *StackDetection, id string) bool {
	for _, t := range d.Targets {
		if t.ID == id {
			return true
		}
	}
	return false
}

func rollupMonorepoRoles(root *StackDetection) map[string]string {
	if root == nil || len(root.Packages) == 0 {
		return nil
	}
	type rolePick struct {
		stack   string
		relPath string
	}
	candidates := map[string]rolePick{}
	var warnings []string
	for _, pkg := range root.Packages {
		role := strings.TrimSpace(pkg.Role)
		if role == "" || role == "unknown" {
			continue
		}
		stack := pkg.Framework
		if stack == "" {
			stack = pkg.Backend
		}
		if stack == "" && len(pkg.Hosting) > 0 {
			stack = pkg.Hosting[0]
		}
		if stack == "" {
			stack = pkg.Role
		}
		if cur, ok := candidates[role]; ok {
			if shorterRelPath(pkg.RelPath, cur.relPath) {
				warnings = append(warnings, "multiple "+role+" packages detected; preferred "+pkg.RelPath+" over "+cur.relPath)
				candidates[role] = rolePick{stack: stack, relPath: pkg.RelPath}
			} else {
				warnings = append(warnings, "multiple "+role+" packages detected; preferred "+cur.relPath+" over "+pkg.RelPath)
			}
			continue
		}
		candidates[role] = rolePick{stack: stack, relPath: pkg.RelPath}
	}
	if len(candidates) == 0 {
		return nil
	}
	roles := make(map[string]string, len(candidates))
	keys := make([]string, 0, len(candidates))
	for role := range candidates {
		keys = append(keys, role)
	}
	sort.Strings(keys)
	for _, role := range keys {
		roles[role] = candidates[role].stack
	}
	root.Warnings = append(root.Warnings, dedupeSorted(warnings)...)
	return roles
}

func shorterRelPath(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
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

const stackDetectCacheCap = 256

var (
	stackDetectCacheMu sync.RWMutex
	stackDetectCache   = map[string]*StackDetection{}
	stackDetectSeen    = map[string]time.Time{}
)

// stackDetectCached returns the cached detection when the stat-only
// fingerprint still matches. changed reports whether the cached value
// was refreshed because the tree changed or the cache was cold.
func stackDetectCached(root string) (*StackDetection, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	stackDetectCacheMu.RLock()
	cached := stackDetectCache[absRoot]
	stackDetectCacheMu.RUnlock()
	if cached != nil {
		fp := computeStackFingerprint(cached)
		if fp == cached.Fingerprint {
			return cached, false
		}
	}
	det := stackDetect(absRoot)
	stackDetectCacheMu.Lock()
	stackDetectCache[absRoot] = det
	stackDetectSeen[absRoot] = time.Now().UTC()
	for len(stackDetectCache) > stackDetectCacheCap {
		evictOldestStackDetect()
	}
	stackDetectCacheMu.Unlock()
	return det, true
}

func stackDetectInvalidate(root string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	stackDetectCacheMu.Lock()
	delete(stackDetectCache, absRoot)
	delete(stackDetectSeen, absRoot)
	stackDetectCacheMu.Unlock()
}

func evictOldestStackDetect() {
	var oldestKey string
	var oldest time.Time
	for key, seenAt := range stackDetectSeen {
		if oldestKey == "" || seenAt.Before(oldest) {
			oldestKey = key
			oldest = seenAt
		}
	}
	if oldestKey != "" {
		delete(stackDetectCache, oldestKey)
		delete(stackDetectSeen, oldestKey)
	}
}

func computeStackFingerprint(d *StackDetection) string {
	if d == nil {
		return ""
	}
	var inputs []string
	seenDirs := map[string]bool{}
	addDir := func(path string) {
		if path == "" || seenDirs[path] {
			return
		}
		seenDirs[path] = true
		if st, err := os.Stat(path); err == nil {
			inputs = append(inputs, "dir:"+path+":"+st.ModTime().UTC().Format(time.RFC3339Nano)+":"+itoa64(st.Size()))
		}
	}
	addFile := func(path string) {
		if path == "" {
			return
		}
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			inputs = append(inputs, "file:"+path+":"+st.ModTime().UTC().Format(time.RFC3339Nano)+":"+itoa64(st.Size()))
		}
	}
	collectFingerprintInputs(d, addDir, addFile)
	sort.Strings(inputs)
	sum := sha256.Sum256([]byte(strings.Join(inputs, "\n")))
	return hex.EncodeToString(sum[:])
}

func collectFingerprintInputs(d *StackDetection, addDir, addFile func(string)) {
	if d == nil {
		return
	}
	addDir(d.Root)
	addFile(filepath.Join(d.Root, "package.json"))
	addFile(filepath.Join(d.Root, "pnpm-workspace.yaml"))
	for _, ev := range d.Evidence {
		signalPath := signalToFingerprintPath(ev.Signal)
		if signalPath == "" {
			continue
		}
		addFile(filepath.Join(d.Root, filepath.FromSlash(signalPath)))
	}
	for _, path := range stackFingerprintMarkerPaths() {
		addFile(filepath.Join(d.Root, filepath.FromSlash(path)))
	}
	for _, p := range d.Packages {
		collectFingerprintInputs(p, addDir, addFile)
	}
}

func stackFingerprintMarkerPaths() []string {
	var out []string
	out = append(out,
		"package.json",
		"pnpm-workspace.yaml",
		"pubspec.yaml",
		"firebase.json", ".firebaserc", "convex.json",
		"supabase/config.toml", "supabase.toml",
		"yaver.serverless.yaml", "yaver.serverless.yml",
		"next.config.ts", "next.config.js", "next.config.mjs",
		"vite.config.ts", "vite.config.js", "vite.config.mts",
		"Package.swift", "go.mod", "Cargo.toml", "pyproject.toml", "setup.py", "requirements.txt",
		"build.gradle.kts", "settings.gradle.kts", "build.gradle", "settings.gradle",
	)
	for _, provider := range stackProviders {
		out = append(out, provider.Files...)
	}
	return dedupeSorted(out)
}

func signalToFingerprintPath(signal string) string {
	if signal == "" {
		return ""
	}
	if i := strings.IndexByte(signal, ':'); i >= 0 {
		return signal[:i]
	}
	if strings.HasSuffix(signal, "/") || strings.Contains(signal, "*") {
		return ""
	}
	return signal
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
