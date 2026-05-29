package main

// deploy_version_bump.go — deterministic monorepo version increment for
// `yaver deploy all`.
//
// The contract the user asked for: "no LLM agent picks the version." Before
// this file, `versions.json` was bumped by hand (commits like
// "chore(release): bump mobile 1.18.131") and `deploy all` only ever bumped
// the *build numbers* (CFBundleVersion / versionCode) and the cli semver. The
// user-facing *marketing* versions for mobile / web / backend never moved on
// their own, so shipping the same code twice produced two TestFlight builds
// that both read "1.18.131".
//
// bumpMonorepoVersions closes that gap: it reads versions.json, increments the
// patch (or the requested kind) for each component being shipped, writes it
// back, and runs scripts/sync-versions.sh so every downstream file
// (app.json, Info.plist, project.pbxproj, build.gradle, package.json, …) picks
// up the new number. Pure arithmetic — same inputs, same outputs, every time.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// bumpMonorepoVersions bumps the given versions.json components by `kind`
// (patch|minor|major), writes versions.json, and propagates via
// sync-versions.sh. Returns a map of component → "old → new" for the summary.
// In dry-run it computes and prints the bumps but writes nothing.
func bumpMonorepoVersions(repoRoot string, components []string, kind string, dryRun bool, ctx *deployAllCtx) (map[string]string, error) {
	if len(components) == 0 {
		return map[string]string{}, nil
	}

	versionsPath := filepath.Join(repoRoot, "versions.json")
	data, err := os.ReadFile(versionsPath)
	if err != nil {
		return nil, fmt.Errorf("read versions.json: %w", err)
	}
	var current map[string]string
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, fmt.Errorf("parse versions.json: %w", err)
	}

	// Stable ordering so the printed plan is deterministic regardless of
	// which stages were enabled.
	sorted := append([]string(nil), components...)
	sort.Strings(sorted)

	changes := make(map[string]string, len(sorted))
	for _, comp := range sorted {
		cur, ok := current[comp]
		if !ok || cur == "" {
			return nil, fmt.Errorf("versions.json has no %q version to bump", comp)
		}
		next, err := bumpSemver(cur, kind)
		if err != nil {
			return nil, fmt.Errorf("bump %s: %w", comp, err)
		}
		changes[comp] = fmt.Sprintf("%s → %s", cur, next)
		fmt.Printf("  %s %-8s %s → %s\n", ctx.prefix, comp, cur, next)

		if dryRun {
			continue
		}
		// Reuse updateJSONField (deploy_all_cmd.go): regex-rewrites a single
		// top-level key without reordering keys or normalising whitespace, so
		// versions.json keeps its hand-maintained shape.
		if err := updateJSONField(versionsPath, comp, next); err != nil {
			return nil, fmt.Errorf("write versions.json %s: %w", comp, err)
		}
	}

	if dryRun {
		fmt.Printf("  %s [dry-run] would run scripts/sync-versions.sh to propagate\n", ctx.prefix)
		return changes, nil
	}

	// sync-versions.sh reads versions.json and rewrites every downstream
	// file (the 6 mobile sites + web/backend/installer/cli package.json +
	// the Go version consts). Running the canonical script keeps this code
	// path identical to a manual `./scripts/sync-versions.sh`.
	syncScript := filepath.Join(repoRoot, "scripts", "sync-versions.sh")
	if err := ctx.runScript(syncScript); err != nil {
		return nil, fmt.Errorf("sync-versions.sh: %w", err)
	}
	return changes, nil
}

// newVersionOf extracts the post-bump version from a "old → new" change
// string produced by bumpMonorepoVersions; falls back to the whole string.
func newVersionOf(change string) string {
	if i := strings.LastIndex(change, "→"); i >= 0 {
		return strings.TrimSpace(change[i+len("→"):])
	}
	return strings.TrimSpace(change)
}
