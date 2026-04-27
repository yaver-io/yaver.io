package main

// Web-bundle preflight integrity check.
//
// Mirrors the spirit of mobile's HBC validation (bundlecheck.go +
// YaverBundleValidator.swift) but for the web target. The web bundle
// has no "host" runtime to declare a manifest against — the bundle
// ships its own React, react-dom, expo-router. The recurring failure
// mode is *intra-bundle* drift between packages that should share a
// peer-dep version, which produces silent white-screens-on-init like
// React error #527 (incompatible react vs react-dom versions).
//
// We catch that drift BEFORE the 60s `expo export -p web` runs, so
// the user gets an immediate fail-fast error with the fix command.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// webBundlePreflightReport is the structured result of the preflight.
// `OK` is the bool the caller branches on; the rest is detail for the
// dashboard / log so the user sees what we checked and what to fix.
type webBundlePreflightReport struct {
	OK              bool     `json:"ok"`
	Errors          []string `json:"errors,omitempty"`   // hard-fail; build is aborted
	Warnings        []string `json:"warnings,omitempty"` // soft; build proceeds
	ReactVersion    string   `json:"reactVersion,omitempty"`
	ReactDomVersion string   `json:"reactDomVersion,omitempty"`
	ExpoVersion     string   `json:"expoVersion,omitempty"`
	RNVersion       string   `json:"rnVersion,omitempty"`
}

// preflightWebBundle performs every cheap intra-bundle integrity check
// we have. Errors block the build; warnings just log.
//
// Adding a new check = a new helper below + a new branch here.
func preflightWebBundle(projectPath string) webBundlePreflightReport {
	report := webBundlePreflightReport{OK: true}

	react, _ := readInstalledPackageVersion(projectPath, "react")
	reactDom, _ := readInstalledPackageVersion(projectPath, "react-dom")
	report.ReactVersion = react
	report.ReactDomVersion = reactDom

	// Check 1: react and react-dom must share a version.
	// Why: React's runtime checks both at init; mismatch throws
	// #527 (anonymous in production builds → white screen on iframe).
	// This is what bit sfmg today: package.json pinned both at
	// 19.1.0 but a transitive resolution hoisted react-dom to
	// 19.2.5. Hard-fail with a fix-it command.
	if react != "" && reactDom != "" && react != reactDom {
		fix := fmt.Sprintf("npm install --legacy-peer-deps react@%s react-dom@%s", reactDom, reactDom)
		report.OK = false
		report.Errors = append(report.Errors,
			fmt.Sprintf("React version drift: react@%s vs react-dom@%s. "+
				"React's runtime requires they match exactly. "+
				"In %s, run: %s",
				react, reactDom, projectPath, fix))
	}

	// Check 2: multiple copies of react in the dependency tree.
	// Why: hoisting bugs occasionally leave a second react under
	// some package's own node_modules. RN's hooks blow up on init
	// with "Invalid hook call" when that happens. Cheap walk:
	// count `node_modules/**/react/package.json` paths.
	if extras := scanDuplicateReactCopies(projectPath); len(extras) > 0 {
		// Cap the report size so it doesn't dominate the log.
		preview := extras
		if len(preview) > 5 {
			preview = preview[:5]
		}
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("Multiple react copies detected (%d). Hooks may throw 'Invalid hook call'. "+
				"First few: %s. Try: npm dedupe", len(extras), strings.Join(preview, ", ")))
	}

	// Check 3: react-native peer-dep on react. RN declares an exact
	// react peer-dep ("19.1.0"); when installed react drifts off
	// that pin (often after `npm install --legacy-peer-deps`
	// silently picks the higher version), TurboModule init can
	// panic before the app mounts.
	report.RNVersion, _ = readInstalledPackageVersion(projectPath, "react-native")
	if report.RNVersion != "" && react != "" {
		want := readInstalledPackagePeerDep(projectPath, "react-native", "react")
		if want != "" && !peerDepSatisfied(want, react) {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("react-native@%s expects react@%s but %s is installed. "+
					"Hooks may behave unpredictably.", report.RNVersion, want, react))
		}
	}

	report.ExpoVersion, _ = readInstalledPackageVersion(projectPath, "expo")

	return report
}

// readInstalledPackageVersion reads node_modules/<pkg>/package.json
// and returns its `version` field. Empty string when the package
// isn't installed (preflight is best-effort — we don't fail just
// because a package is missing; we skip the related check).
func readInstalledPackageVersion(projectPath, pkg string) (string, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, "node_modules", pkg, "package.json"))
	if err != nil {
		return "", err
	}
	var p struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return "", err
	}
	return strings.TrimSpace(p.Version), nil
}

// readInstalledPackagePeerDep returns the value of
// node_modules/<pkg>/package.json's peerDependencies[<dep>].
// Empty when the field is missing.
func readInstalledPackagePeerDep(projectPath, pkg, dep string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, "node_modules", pkg, "package.json"))
	if err != nil {
		return ""
	}
	var p struct {
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.PeerDependencies[dep])
}

// scanDuplicateReactCopies returns the relative paths of every extra
// react/package.json under node_modules (besides the top-level one).
// Walks one level deep into nested node_modules; deeper trees are
// uncommon and the walk would get expensive.
func scanDuplicateReactCopies(projectPath string) []string {
	root := filepath.Join(projectPath, "node_modules")
	var extras []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// scoped package directory like @some/scope — recurse one level
		if strings.HasPrefix(e.Name(), "@") {
			scoped, _ := os.ReadDir(filepath.Join(root, e.Name()))
			for _, s := range scoped {
				if s.IsDir() {
					nested := filepath.Join(root, e.Name(), s.Name(), "node_modules", "react", "package.json")
					if _, err := os.Stat(nested); err == nil {
						extras = append(extras, filepath.Join(e.Name(), s.Name()))
					}
				}
			}
			continue
		}
		nested := filepath.Join(root, e.Name(), "node_modules", "react", "package.json")
		if _, err := os.Stat(nested); err == nil {
			extras = append(extras, e.Name())
		}
	}
	return extras
}

// peerDepSatisfied is a deliberately-loose semver match: we only
// reject obvious major-version mismatches. Real semver-range parsing
// would be a yak-shave; the goal is to catch "expected ^19.0.0 but
// got 18.x" not to perfectly mirror npm's resolver.
func peerDepSatisfied(want, got string) bool {
	want = strings.TrimSpace(want)
	got = strings.TrimSpace(got)
	if want == "" || got == "" {
		return true
	}
	if want == got {
		return true
	}
	wantMajor := majorVersion(strings.TrimLeft(want, "^~>=<"))
	gotMajor := majorVersion(got)
	if wantMajor == "" || gotMajor == "" {
		return true // can't tell — don't false-alarm
	}
	return wantMajor == gotMajor
}

func majorVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, " ,|"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, "."); i >= 0 {
		return v[:i]
	}
	return v
}
