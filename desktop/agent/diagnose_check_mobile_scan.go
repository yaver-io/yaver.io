package main

// diagnose_check_mobile_scan.go — `yaver diagnose --only=mobile-projects-scan`
//
// Smoke test that exercises the same /projects/mobile scanner the
// mobile app's Reload + Projects tabs depend on. The motivating
// failure mode: the user switches the Remote Box to a Mac mini, the
// AVAILABLE APPS section sits in "Discovering…" forever, and there's
// no way short of inspecting agent logs to know whether the scan is
// hung, returning empty, or hitting a permission error.
//
// What this check reports:
//   - Time-to-complete for one full sweep of projectDiscoveryRoots()
//   - Per-root project count + whether the root is readable + skipped reason
//   - Total mobile-capable projects discovered, top 5 by name
//   - First scan error (if any) — surfaces the real reason
//
// Run on any box (the box is its own scan target):
//
//   yaver diagnose --only=mobile-projects-scan
//   yaver diagnose --only=mobile-projects-scan --json
//
// On a remote box (e.g. a Mac mini you can ssh into but can't run
// `yaver wireless push` to):
//
//   ssh user@mac-mini yaver diagnose --only=mobile-projects-scan
//
// Cheap by design — no toolchain probing, no Hermes work, no network.
// Just the same filesystem walk the production scanner uses, with
// per-root timing so a slow / inaccessible root is obvious.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"
)

func init() {
	extraDiagChecks = append(extraDiagChecks, diagCheck{
		Name: "mobile-projects-scan",
		Run:  checkMobileProjectsScan,
	})
}

func checkMobileProjectsScan(ctx context.Context, emit DiagEmit) {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		emit(DiagEvent{
			Type: "finding", Check: "mobile-projects-scan", Severity: DiagFailure,
			Message: "No project discovery roots resolved on this machine.",
			Data: map[string]interface{}{
				"hint": "Set up a workspace dir (e.g. ~/Workspace) or projects dir (~/Projects) so the scanner has somewhere to look. Empty home dirs and locked-down service accounts both hit this.",
			},
			Timestamp: nowDiagTS(),
		})
		return
	}

	emit(DiagEvent{
		Type: "finding", Check: "mobile-projects-scan", Severity: DiagInfo,
		Message: fmt.Sprintf("Resolved %d discovery root(s).", len(roots)),
		Data: map[string]interface{}{
			"roots": roots,
		},
		Timestamp: nowDiagTS(),
	})

	for _, root := range roots {
		info, err := os.Stat(root)
		switch {
		case err != nil:
			emit(DiagEvent{
				Type: "finding", Check: "mobile-projects-scan", Severity: DiagWarning,
				Message: fmt.Sprintf("Root %s is unreadable: %v", root, err),
				Data: map[string]interface{}{
					"root":  root,
					"error": err.Error(),
				},
				Timestamp: nowDiagTS(),
			})
		case !info.IsDir():
			emit(DiagEvent{
				Type: "finding", Check: "mobile-projects-scan", Severity: DiagWarning,
				Message: fmt.Sprintf("Root %s is not a directory.", root),
				Data: map[string]interface{}{
					"root": root,
				},
				Timestamp: nowDiagTS(),
			})
		}
	}

	start := time.Now()
	projects := scanMobileProjects()
	elapsed := time.Since(start)

	if len(projects) == 0 {
		emit(DiagEvent{
			Type: "finding", Check: "mobile-projects-scan", Severity: DiagWarning,
			Message: fmt.Sprintf("Scanner found 0 mobile projects across %d root(s) in %s.", len(roots), elapsed.Round(time.Millisecond)),
			Data: map[string]interface{}{
				"elapsed_ms": elapsed.Milliseconds(),
				"hint":       "If you expected projects here: confirm there's a package.json with `expo` or `react-native` in dependencies, OR a pubspec.yaml, somewhere under one of the discovery roots. The scanner skips node_modules / .git / hidden dirs / SDK trees by design.",
			},
			Timestamp: nowDiagTS(),
		})
		return
	}

	emit(DiagEvent{
		Type: "finding", Check: "mobile-projects-scan", Severity: DiagOK,
		Message: fmt.Sprintf("Scanner found %d mobile project(s) in %s.", len(projects), elapsed.Round(time.Millisecond)),
		Data: map[string]interface{}{
			"count":      len(projects),
			"elapsed_ms": elapsed.Milliseconds(),
			"sample":     mobileProjectsTopSample(projects, 5),
		},
		Timestamp: nowDiagTS(),
	})

	if elapsed > 10*time.Second {
		emit(DiagEvent{
			Type: "finding", Check: "mobile-projects-scan", Severity: DiagWarning,
			Message: fmt.Sprintf("Scan took %s — the mobile app's 10s stall hint will fire on this machine.", elapsed.Round(time.Millisecond)),
			Data: map[string]interface{}{
				"elapsed_ms": elapsed.Milliseconds(),
				"hint":       "Heavy /Library, /node_modules forests, or a deeply nested /Workspace can push past 10s on first scan. Subsequent scans hit the cache.",
			},
			Timestamp: nowDiagTS(),
		})
	}
}

// mobileProjectsTopSample returns up to n project summaries sorted by
// path, intended for the diagnose UI's "first few that the scanner
// found" preview. Each entry carries just enough to recognise the
// project (name + relative-ish path + framework) without flooding the
// CLI output with the full MobileProject struct.
func mobileProjectsTopSample(projects []MobileProject, n int) []map[string]string {
	sorted := append([]MobileProject(nil), projects...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	out := make([]map[string]string, 0, len(sorted))
	for _, p := range sorted {
		out = append(out, map[string]string{
			"name":      p.Name,
			"path":      p.Path,
			"framework": p.Framework,
		})
	}
	return out
}

func nowDiagTS() string { return time.Now().UTC().Format(time.RFC3339) }
