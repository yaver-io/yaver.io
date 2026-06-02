package main

// deploy_detect.go — makes `yaver deploy all` work in ANY repo, not just
// yaver.io. yaver.io keeps its bespoke path (versions.json + the canonical
// scripts/*.sh + the cli/v* npm release). Every other repo (carrotbet,
// talos, …) is resolved deterministically: detect which deploy targets the
// repo actually has from its own scripts/deploy-*.sh + yaver.workspace.yaml,
// find every version site, and cache the resolved plan to
// .yaver/deploy-plan.json. No LLM, no yaver.io assumptions.
//
// Design notes live in docs/deploy-all-generic.md. Decisions baked in here:
//   - deterministic detection only (no model at deploy time)
//   - prefer the repo's OWN scripts/deploy-<target>.sh
//   - generic per-app version bump when there's no versions.json
//   - generic repos are NOT committed/tagged/pushed — deploy all bumps the
//     version files in the working tree and runs the scripts; the repo owner
//     commits. The sweep-commit + cli/v* tag is yaver.io-only.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Per-kind version regexes. Group 2 is always the X.Y.Z to rewrite.
var (
	reJSONVersion       = regexp.MustCompile(`("version"\s*:\s*")(\d+\.\d+\.\d+)(")`)
	rePlistShortVersion = regexp.MustCompile(`(<key>CFBundleShortVersionString</key>\s*<string>)(\d+\.\d+\.\d+)(</string>)`)
	rePbxMarketing      = regexp.MustCompile(`(MARKETING_VERSION = )(\d+\.\d+\.\d+)(;)`)
	reGradleVersionName = regexp.MustCompile(`(versionName\s+")(\d+\.\d+\.\d+)(")`)
	// Android versionCode is a plain integer build number, not semver. It's
	// handled outside the siteRegex/semver machinery (readGradleVersionCode /
	// writeGradleVersionCode) because it increments by 1, not by patch/minor/
	// major. Play rejects re-uploading a code it has already seen, so a stale
	// versionCode is the single most common generic-deploy failure (it broke
	// talos: "Version code 337 has already been used").
	reGradleVersionCode = regexp.MustCompile(`(versionCode\s+)(\d+)`)
)

// readGradleVersionCode returns the first `versionCode N` integer in a
// build.gradle, or 0 if absent.
func readGradleVersionCode(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	m := reGradleVersionCode.FindSubmatch(data)
	if m == nil {
		return 0, nil
	}
	return strconv.Atoi(strings.TrimSpace(string(m[2])))
}

// writeGradleVersionCode rewrites only the first `versionCode N` occurrence
// (the defaultConfig one) and leaves any flavor overrides untouched.
func writeGradleVersionCode(path string, code int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	done := false
	out := reGradleVersionCode.ReplaceAllFunc(data, func(b []byte) []byte {
		if done {
			return b
		}
		done = true
		return reGradleVersionCode.ReplaceAll(b, []byte("${1}"+strconv.Itoa(code)))
	})
	return os.WriteFile(path, out, 0o644)
}

// siteRegex maps a version-site kind to its regex and whether to rewrite all
// occurrences (pbxproj has MARKETING_VERSION twice) or only the first (JSON
// files have a single relevant top-level "version").
func siteRegex(kind string) (*regexp.Regexp, bool) {
	switch kind {
	case "json-expo-version", "json-version":
		return reJSONVersion, false
	case "plist-short-version":
		return rePlistShortVersion, true
	case "pbxproj-marketing-version":
		return rePbxMarketing, true
	case "gradle-version-name":
		return reGradleVersionName, true
	default:
		return nil, false
	}
}

func readVersionSite(path, kind string) (string, error) {
	re, _ := siteRegex(kind)
	if re == nil {
		return "", fmt.Errorf("unknown version-site kind %q", kind)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := re.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[2]), nil
}

func writeVersionSite(path, kind, next string) error {
	re, all := siteRegex(kind)
	if re == nil {
		return fmt.Errorf("unknown version-site kind %q", kind)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	repl := []byte("${1}" + next + "${3}")
	var out []byte
	if all {
		out = re.ReplaceAll(data, repl)
	} else {
		done := false
		out = re.ReplaceAllFunc(data, func(b []byte) []byte {
			if done {
				return b
			}
			done = true
			return re.ReplaceAll(b, repl)
		})
	}
	return os.WriteFile(path, out, 0o644)
}

// semverLess reports whether a < b for plain X.Y.Z strings.
func semverLess(a, b string) bool {
	am := semverRe.FindStringSubmatch(strings.TrimSpace(a))
	bm := semverRe.FindStringSubmatch(strings.TrimSpace(b))
	if am == nil || bm == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		ai, _ := strconv.Atoi(am[i])
		bi, _ := strconv.Atoi(bm[i])
		if ai != bi {
			return ai < bi
		}
	}
	return false
}

// DetectedStage is one deploy target resolved from the repo.
type DetectedStage struct {
	Name   string `json:"name"`   // human label, e.g. "Cloudflare (web)"
	ID     string `json:"id"`     // stable id, e.g. "cloudflare"
	Script string `json:"script"` // repo-relative script path to run via bash
}

// DetectedVersionSite is one file+location that carries a marketing version.
type DetectedVersionSite struct {
	App  string `json:"app"`  // logical app, e.g. "mobile" / "web"
	File string `json:"file"` // repo-relative path
	Kind string `json:"kind"` // see bumpVersionSite for the kinds
}

// DeployPlan is the cached, reviewable resolution of a repo's deploy.
type DeployPlan struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Repo          string                `json:"repo"`
	GeneratedBy   string                `json:"generatedBy"` // "detect" | "hand-edited"
	Bump          string                `json:"bump"`        // patch|minor|major
	Stages        []DetectedStage       `json:"stages"`
	VersionSites  []DetectedVersionSite `json:"versionSites"`
	Notes         []string              `json:"notes,omitempty"`
}

// findDeployRepoRoot accepts any git repo that looks deployable: it has a
// versions.json, a yaver.workspace.yaml, or at least one scripts/deploy-*.sh.
// Returns (root, isYaverIo). isYaverIo gates the bespoke yaver.io pipeline.
func findDeployRepoRoot() (root string, isYaverIo bool, err error) {
	out, gerr := runGit("", "rev-parse", "--show-toplevel")
	if gerr != nil {
		return "", false, fmt.Errorf("`yaver deploy all` must run inside a git repo (git rev-parse --show-toplevel failed: %w)", gerr)
	}
	root = strings.TrimSpace(out)
	if root == "" {
		return "", false, fmt.Errorf("git returned empty repo root")
	}

	// yaver.io is identified by versions.json + its canonical web script.
	_, vErr := os.Stat(filepath.Join(root, "versions.json"))
	_, wErr := os.Stat(filepath.Join(root, "scripts", "deploy-web.sh"))
	isYaverIo = vErr == nil && wErr == nil

	if isYaverIo {
		return root, true, nil
	}

	// Generic repo: require at least one signal that it's deployable.
	if hasAnyDeployScript(root) || fileExists(filepath.Join(root, "yaver.workspace.yaml")) || vErr == nil {
		return root, false, nil
	}
	return "", false, fmt.Errorf("repo root %s has no deploy signals (no scripts/deploy-*.sh, no yaver.workspace.yaml, no versions.json)", root)
}

func hasAnyDeployScript(root string) bool {
	matches, _ := filepath.Glob(filepath.Join(root, "scripts", "deploy-*.sh"))
	return len(matches) > 0
}

// detectDeployPlan resolves a generic repo's deploy plan from its scripts and
// layout. Stable, ordered, side-effect-free (other than reading files).
func detectDeployPlan(root, bump string) *DeployPlan {
	plan := &DeployPlan{
		SchemaVersion: 1,
		Repo:          filepath.Base(root),
		GeneratedBy:   "detect",
		Bump:          bump,
	}

	// Stages from the repo's own scripts, in fail-loud order (cheap/idempotent
	// backend first, mobile uploads last). cloudflare prefers deploy-cloudflare.sh
	// but falls back to deploy-web.sh.
	type cand struct{ id, name string; scripts []string }
	cands := []cand{
		{"convex", "Convex backend", []string{"deploy-convex.sh"}},
		{"cloudflare", "Cloudflare (web)", []string{"deploy-cloudflare.sh", "deploy-web.sh"}},
		{"testflight", "TestFlight", []string{"deploy-testflight.sh"}},
		{"playstore", "Play Store (internal)", []string{"deploy-playstore.sh"}},
	}
	for _, c := range cands {
		for _, s := range c.scripts {
			rel := filepath.Join("scripts", s)
			if fileExists(filepath.Join(root, rel)) {
				plan.Stages = append(plan.Stages, DetectedStage{Name: c.name, ID: c.id, Script: rel})
				break
			}
		}
	}

	plan.VersionSites = detectVersionSites(root)
	if len(plan.Stages) == 0 {
		plan.Notes = append(plan.Notes, "no scripts/deploy-*.sh found — nothing to run")
	}
	if len(plan.VersionSites) == 0 {
		plan.Notes = append(plan.Notes, "no marketing-version sites found — bump is a no-op (build numbers still handled by the scripts)")
	}
	return plan
}

// detectVersionSites finds every marketing-version location, grouped by app.
// No versions.json required — this is the carrotbet/talos path.
func detectVersionSites(root string) []DetectedVersionSite {
	var sites []DetectedVersionSite

	// Mobile (Expo / RN). Only if a mobile/ dir with app.json exists.
	mobileAppJSON := filepath.Join("mobile", "app.json")
	if fileExists(filepath.Join(root, mobileAppJSON)) {
		sites = append(sites, DetectedVersionSite{App: "mobile", File: mobileAppJSON, Kind: "json-expo-version"})
		// iOS Info.plist (CFBundleShortVersionString) — glob the app dir.
		for _, p := range globRel(root, filepath.Join("mobile", "ios", "*", "Info.plist")) {
			sites = append(sites, DetectedVersionSite{App: "mobile", File: p, Kind: "plist-short-version"})
		}
		// iOS Xcode project (MARKETING_VERSION, usually ×2).
		for _, p := range globRel(root, filepath.Join("mobile", "ios", "*.xcodeproj", "project.pbxproj")) {
			sites = append(sites, DetectedVersionSite{App: "mobile", File: p, Kind: "pbxproj-marketing-version"})
		}
		// Android versionName (marketing) + versionCode (integer build number).
		if bg := filepath.Join("mobile", "android", "app", "build.gradle"); fileExists(filepath.Join(root, bg)) {
			sites = append(sites, DetectedVersionSite{App: "mobile", File: bg, Kind: "gradle-version-name"})
			sites = append(sites, DetectedVersionSite{App: "mobile", File: bg, Kind: "gradle-version-code"})
		}
		// mobile/package.json (the 2nd of the canonical mobile version sites).
		if mp := filepath.Join("mobile", "package.json"); fileExists(filepath.Join(root, mp)) {
			sites = append(sites, DetectedVersionSite{App: "mobile", File: mp, Kind: "json-version"})
		}
	}

	// Web — common Vite/Next locations.
	for _, p := range []string{
		filepath.Join("apps", "web", "package.json"),
		filepath.Join("web", "package.json"),
	} {
		if fileExists(filepath.Join(root, p)) {
			sites = append(sites, DetectedVersionSite{App: "web", File: p, Kind: "json-version"})
			break
		}
	}

	return sites
}

func globRel(root, pattern string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, pattern))
	rels := make([]string, 0, len(matches))
	for _, m := range matches {
		if rel, err := filepath.Rel(root, m); err == nil {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)
	return rels
}

// writeDeployPlanCache persists the resolved plan to .yaver/deploy-plan.json
// for review/reuse. Best-effort: a write failure is logged, not fatal.
func writeDeployPlanCache(root string, plan *DeployPlan) {
	dir := filepath.Join(root, ".yaver")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "deploy-plan.json"), append(data, '\n'), 0o644)
}

// genericDeployOpts carries the `deploy all` flags into the generic pipeline.
type genericDeployOpts struct {
	bump           string
	dryRun         bool
	keepGoing      bool
	skipBump       bool
	skipConvex     bool
	skipCloudflare bool
	skipTestflight bool
	skipPlaystore  bool
}

// runGenericDeployAll is the non-yaver.io path: detect the plan, bump version
// sites, and run the repo's own deploy scripts in order. Deliberately does NOT
// commit/tag/push — it leaves the version bumps in the working tree for the
// repo owner to review and commit (important: other repos may be owned by a
// different session).
func runGenericDeployAll(repoRoot string, opts genericDeployOpts) {
	plan := detectDeployPlan(repoRoot, opts.bump)
	// Persist the resolved plan for review/reuse — but never on a dry-run,
	// so `deploy all --dry-run` is fully read-only (it may be inspecting a
	// repo owned by another session).
	if !opts.dryRun {
		writeDeployPlanCache(repoRoot, plan)
	}

	skipByID := map[string]bool{
		"convex":     opts.skipConvex,
		"cloudflare": opts.skipCloudflare,
		"testflight": opts.skipTestflight,
		"playstore":  opts.skipPlaystore,
	}

	lg := newDeployAllLogger(filepath.Base(repoRoot))
	defer lg.close()

	lg.println("yaver deploy all")
	lg.println("repo:", repoRoot, "(generic — detected from scripts/ + yaver.workspace.yaml)")
	lg.println("plan: .yaver/deploy-plan.json")
	if lg.path != "" {
		lg.println("log:", lg.path)
	}
	if len(plan.Stages) == 0 {
		lg.println("\nNo deploy targets detected (no scripts/deploy-*.sh). Nothing to do.")
		for _, n := range plan.Notes {
			lg.println("  note:", n)
		}
		os.Exit(0)
	}
	stageIDs := make([]string, 0, len(plan.Stages))
	for _, st := range plan.Stages {
		stageIDs = append(stageIDs, st.ID)
	}
	lg.println("targets:", strings.Join(stageIDs, " → "))
	for _, n := range plan.Notes {
		lg.println("  note:", n)
	}
	lg.println()

	results := make([]deployAllResult, 0, len(plan.Stages)+1)
	overallStart := time.Now()
	failed := false

	// Version-bump preflight (marketing versions only; build numbers stay with
	// the scripts). Skipped with --skip-bump or when no sites were found.
	if !opts.skipBump && len(plan.VersionSites) > 0 {
		bumpStage := deployAllStage{name: "Version bump", id: "bump"}
		ctx := &deployAllCtx{dryRun: opts.dryRun, prefix: "[bump]", out: lg.out, errW: lg.errW}
		lg.printf("──[ %-22s ]── %s-bumping detected version sites…\n", bumpStage.name, opts.bump)
		start := time.Now()
		changes, err := bumpDetectedVersions(repoRoot, plan.VersionSites, opts.bump, opts.dryRun, ctx)
		dur := time.Since(start).Round(time.Second)
		if err != nil {
			lg.printf("\n──[ %-22s ]── FAILED in %s: %v\n\n", bumpStage.name, dur, err)
			results = append(results, deployAllResult{stage: bumpStage, err: err, duration: dur})
			printDeployAllSummary(lg.out, results, time.Since(overallStart).Round(time.Second))
			os.Exit(1)
		}
		_ = changes
		lg.printf("\n──[ %-22s ]── ok (%s)\n\n", bumpStage.name, dur)
		results = append(results, deployAllResult{stage: bumpStage, duration: dur})
	}

	for _, st := range plan.Stages {
		stage := deployAllStage{name: st.Name, id: st.ID}
		if skipByID[st.ID] {
			lg.printf("──[ %-22s ]── SKIPPED (--skip-%s)\n\n", st.Name, st.ID)
			results = append(results, deployAllResult{stage: stage, skipped: true})
			continue
		}
		ctx := &deployAllCtx{dryRun: opts.dryRun, prefix: "[" + st.ID + "]", out: lg.out, errW: lg.errW}
		lg.printf("──[ %-22s ]── starting… (%s)\n", st.Name, st.Script)
		start := time.Now()
		runErr := ctx.runScript(filepath.Join(repoRoot, st.Script))
		dur := time.Since(start).Round(time.Second)
		if runErr != nil {
			lg.printf("\n──[ %-22s ]── FAILED in %s: %v\n\n", st.Name, dur, runErr)
			results = append(results, deployAllResult{stage: stage, err: runErr, duration: dur})
			failed = true
			if !opts.keepGoing {
				printDeployAllSummary(lg.out, results, time.Since(overallStart).Round(time.Second))
				if lg.path != "" {
					lg.println("full log:", lg.path)
				}
				os.Exit(1)
			}
			continue
		}
		lg.printf("\n──[ %-22s ]── ok (%s)\n\n", st.Name, dur)
		results = append(results, deployAllResult{stage: stage, duration: dur})
	}

	printDeployAllSummary(lg.out, results, time.Since(overallStart).Round(time.Second))
	if lg.path != "" {
		lg.println("full log:", lg.path)
	}
	if !opts.dryRun {
		lg.println("\nVersion bumps are in your working tree (uncommitted). Review with `git diff`,")
		lg.println("then commit when ready — generic `deploy all` never commits/tags/pushes for you.")
	}
	if failed {
		os.Exit(1)
	}
}

// --- generic version-site bump (no versions.json) ---

// bumpDetectedVersions bumps every app's marketing version by `kind`, writing
// all of that app's sites to the same new value (which also re-aligns drift —
// e.g. carrotbet's iOS 1.0.1 vs Android 1.0.0). Returns app → "old → new".
func bumpDetectedVersions(root string, sites []DetectedVersionSite, kind string, dryRun bool, ctx *deployAllCtx) (map[string]string, error) {
	if len(sites) == 0 {
		return map[string]string{}, nil
	}
	// Group by app, preserving a stable app order.
	byApp := map[string][]DetectedVersionSite{}
	var apps []string
	for _, s := range sites {
		if _, ok := byApp[s.App]; !ok {
			apps = append(apps, s.App)
		}
		byApp[s.App] = append(byApp[s.App], s)
	}
	sort.Strings(apps)

	changes := map[string]string{}
	for _, app := range apps {
		// Split semver (versionName / Info.plist / pbxproj / package.json)
		// sites from the integer Android versionCode — they bump differently
		// (patch/minor/major vs +1) and must not contaminate each other.
		var semverSites, codeSites []DetectedVersionSite
		for _, s := range byApp[app] {
			if s.Kind == "gradle-version-code" {
				codeSites = append(codeSites, s)
			} else {
				semverSites = append(semverSites, s)
			}
		}

		// --- semver marketing version ---
		// Current version = the highest version any site reports (so we never
		// bump backwards when sites have drifted).
		cur := ""
		drift := false
		for _, s := range semverSites {
			v, err := readVersionSite(filepath.Join(root, s.File), s.Kind)
			if err != nil || v == "" {
				continue
			}
			if cur == "" {
				cur = v
			} else if v != cur {
				drift = true
				if semverLess(cur, v) {
					cur = v
				}
			}
		}
		if cur == "" {
			fmt.Fprintf(ctx.stdout(), "  %s %-6s no readable version site — skipped\n", ctx.prefix, app)
		} else {
			next, err := bumpSemver(cur, kind)
			if err != nil {
				return nil, fmt.Errorf("bump %s (%s): %w", app, cur, err)
			}
			changes[app] = fmt.Sprintf("%s → %s", cur, next)
			note := ""
			if drift {
				note = " (re-aligned drifted sites)"
			}
			fmt.Fprintf(ctx.stdout(), "  %s %-6s %s → %s%s\n", ctx.prefix, app, cur, next, note)
			if !dryRun {
				for _, s := range semverSites {
					if err := writeVersionSite(filepath.Join(root, s.File), s.Kind, next); err != nil {
						return nil, fmt.Errorf("write %s: %w", s.File, err)
					}
				}
			}
		}

		// --- Android versionCode (integer +1) ---
		// Always increment, even when the semver bump was a no-op: a stale
		// versionCode is what makes Play reject the upload ("Version code N
		// has already been used"). +1 from the highest code any site reports.
		// The bump is written in place and persists across `deploy all` runs
		// until reverted, so re-running keeps climbing (337 → 338 → 339).
		if len(codeSites) > 0 {
			maxCode := 0
			for _, s := range codeSites {
				c, err := readGradleVersionCode(filepath.Join(root, s.File))
				if err != nil {
					return nil, fmt.Errorf("read versionCode %s: %w", s.File, err)
				}
				if c > maxCode {
					maxCode = c
				}
			}
			if maxCode > 0 {
				nextCode := maxCode + 1
				fmt.Fprintf(ctx.stdout(), "  %s %-6s versionCode %d → %d\n", ctx.prefix, app, maxCode, nextCode)
				if !dryRun {
					for _, s := range codeSites {
						if err := writeGradleVersionCode(filepath.Join(root, s.File), nextCode); err != nil {
							return nil, fmt.Errorf("write versionCode %s: %w", s.File, err)
						}
					}
				}
			}
		}
	}
	return changes, nil
}
