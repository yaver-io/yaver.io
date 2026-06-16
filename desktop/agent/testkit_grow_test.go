package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies the self-grow planner: it discovers Next/Expo routes, treats a route
// already referenced by an existing spec as covered, and proposes only the
// uncovered ones. This is the deterministic half of "tests write themselves".
func TestGrowTestPlan_RouteCoverage(t *testing.T) {
	root := t.TempDir()

	// Two app-router routes: /dashboard (will be covered) and /settings (not).
	growMustWrite(t, filepath.Join(root, "app", "dashboard", "page.tsx"), "export default function P(){return null}")
	growMustWrite(t, filepath.Join(root, "app", "settings", "page.tsx"), "export default function P(){return null}")
	// noise that must be ignored
	growMustWrite(t, filepath.Join(root, "node_modules", "x", "app", "evil", "page.tsx"), "x")
	growMustWrite(t, filepath.Join(root, "app", "dashboard", "_layout.tsx"), "x")

	// An existing spec covering /dashboard.
	growMustWrite(t, filepath.Join(root, "yaver-tests", "dash.test.yaml"),
		"name: dash\ntarget: web\nurl: http://localhost:3000/dashboard\nsteps:\n  - goto: /dashboard\n")

	plan, err := growTestPlan(root, false)
	if err != nil {
		t.Fatalf("growTestPlan: %v", err)
	}
	if plan.CoveredCount != 1 {
		t.Errorf("CoveredCount = %d, want 1", plan.CoveredCount)
	}
	if !growContainsRoute(plan.CoveredRoutes, "/dashboard") {
		t.Errorf("/dashboard should be covered, got %v", plan.CoveredRoutes)
	}
	gotSettings, gotDashboard, gotEvil := false, false, false
	for _, u := range plan.Uncovered {
		switch u.Route {
		case "/settings":
			gotSettings = true
		case "/dashboard":
			gotDashboard = true
		case "/evil":
			gotEvil = true
		}
	}
	if !gotSettings {
		t.Errorf("/settings should be uncovered; uncovered=%+v", plan.Uncovered)
	}
	if gotDashboard {
		t.Errorf("/dashboard is covered and must not appear as uncovered")
	}
	if gotEvil {
		t.Errorf("node_modules routes must be ignored")
	}
	if plan.AuthorPrompt == "" {
		t.Error("AuthorPrompt should be non-empty for the runner")
	}
}

func TestGrowTestPlan_ApplyWritesLedger(t *testing.T) {
	root := t.TempDir()
	growMustWrite(t, filepath.Join(root, "app", "home", "page.tsx"), "x")
	growMustWrite(t, filepath.Join(root, "yaver-tests", "h.test.yaml"),
		"name: home\ntarget: web\nurl: http://localhost:3000/home\nsteps:\n  - goto: /home\n")

	plan, err := growTestPlan(root, true)
	if err != nil {
		t.Fatalf("growTestPlan: %v", err)
	}
	if !plan.Applied {
		t.Fatal("expected ledger to be applied")
	}
	if _, err := os.Stat(plan.LedgerPath); err != nil {
		t.Fatalf("ledger not written at %s: %v", plan.LedgerPath, err)
	}
}

func growMustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func growContainsRoute(rs []string, want string) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}
