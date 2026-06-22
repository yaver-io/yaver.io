package main

// doctor_permissions.go — the build-doctor's permissions preflight. Before a
// TestFlight/Play upload, confirm app.json already declares every iOS usage
// string + Android permission the code needs (reusing the caps inference +
// the additive merge: any "change" the merge WOULD make = a gap). Missing iOS
// usage strings CRASH the app on launch, so they FAIL the TestFlight doctor;
// missing Android permissions silently break features, so they warn.
//
// Only runs when app.json is statically readable — an app.config.js-only
// project can't be read without executing JS, so we skip (never false-block).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveProjectDir maps a project name → its directory via the workspace
// manifest; "" ⇒ cwd. Returns "" when it can't be resolved.
func resolveProjectDir(project string) string {
	if project == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return ""
	}
	_, path, root := resolveAppFromWorkspaceFull(project)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		base := root
		if base == "" {
			if cwd, err := os.Getwd(); err == nil {
				base = cwd
			}
		}
		path = filepath.Join(base, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// manifestGaps returns the iOS usage strings + Android permissions the code
// requires but app.json doesn't declare. readable=false ⇒ couldn't evaluate
// (no package.json / no readable app.json) and the caller must skip.
func manifestGaps(projectDir string) (iosMissing, androidMissing []string, readable bool) {
	if projectDir == "" {
		return nil, nil, false
	}
	if _, err := os.Stat(filepath.Join(projectDir, "package.json")); err != nil {
		return nil, nil, false // not an RN project
	}
	b, err := os.ReadFile(filepath.Join(projectDir, "app.json"))
	if err != nil {
		return nil, nil, false // app.config.js only — can't read statically
	}
	cfg := map[string]interface{}{}
	if json.Unmarshal(b, &cfg) != nil {
		return nil, nil, false
	}
	_, changes := mergeManifestIntoAppConfig(cfg, buildManifestPlan(projectDir))
	for _, c := range changes {
		if strings.HasPrefix(c, "ios.infoPlist.") {
			iosMissing = append(iosMissing, strings.TrimPrefix(c, "ios.infoPlist."))
		} else if strings.HasPrefix(c, "android.permissions") {
			androidMissing = append(androidMissing, strings.TrimSpace(strings.TrimPrefix(c, "android.permissions +=")))
		}
	}
	return iosMissing, androidMissing, true
}

// checkManifestCompleteness annotates the doctor report for RN store targets.
func checkManifestCompleteness(report *BuildDoctorReport, target, project string) {
	if report.Stack != "react-native-expo" {
		return
	}
	switch target {
	case "testflight", "playstore", "playstore-production":
	default:
		return
	}
	iosM, andM, readable := manifestGaps(resolveProjectDir(project))
	applyManifestGaps(report, target, project, iosM, andM, readable)
}

// applyManifestGaps is the pure annotation step (no filesystem) so the
// blocking policy is unit-testable.
func applyManifestGaps(report *BuildDoctorReport, target, project string, iosM, andM []string, readable bool) {
	if !readable {
		return
	}
	complete := len(iosM) == 0 && len(andM) == 0
	report.PermissionsComplete = &complete
	report.MissingDeclarations = append(append([]string(nil), iosM...), andM...)

	if len(iosM) > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"Missing iOS usage strings %v — the app CRASHES when it uses these. Fix: yaver caps generate --write%s",
			iosM, projectFlag(project)))
		// A guaranteed launch crash on iOS — block the TestFlight doctor.
		if target == "testflight" {
			report.OK = false
		}
	}
	if len(andM) > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"Missing Android permissions (%d) — those features will silently fail. Fix: yaver caps generate --write%s",
			len(andM), projectFlag(project)))
	}
}
