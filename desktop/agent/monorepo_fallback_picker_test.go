package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMonorepoFallbackPicker_PrefersWebOverDemoSibling reproduces the
// real-world bug from yaver-test-ephemeral 2026-05-09: when a monorepo has
// two same-framework apps (e.g. yaver.io has both `web/` and
// `demo/web/todo-web/`), the auto-picker used to grab whichever came first
// alphabetically and route the dashboard's Webview tab into the demo
// instead of the main dashboard. The picker now (1) prefers conventional
// app names like `web`, (2) demotes anything under `demo/` /
// `examples/` / etc., (3) prefers shallower paths.
func TestMonorepoFallbackPicker_PrefersWebOverDemoSibling(t *testing.T) {
	root := t.TempDir()

	// Real app at /web — Next.js (next.config.js + package.json)
	mustMkNextProject(t, filepath.Join(root, "web"))
	// Sibling demo at /demo/web/todo-web — also Next.js
	mustMkNextProject(t, filepath.Join(root, "demo", "web", "todo-web"))

	_, picked, err := monorepoFallbackDevServer(root)
	if err != nil {
		t.Fatalf("monorepoFallbackDevServer: %v", err)
	}
	want := filepath.Join(root, "web")
	if picked != want {
		t.Fatalf("picker chose %q, want %q (top-level web/ should beat demo/web/todo-web/)", picked, want)
	}
}

// TestMonorepoFallbackPicker_PrefersAppsWebWhenNoTopLevelWeb covers the
// shape where every app lives under apps/* (e.g. carrotbet). The picker
// should still pick apps/web over apps/admin or apps/marketing because
// `web` is the conventional main-app name.
func TestMonorepoFallbackPicker_PrefersAppsWebWhenNoTopLevelWeb(t *testing.T) {
	root := t.TempDir()
	mustMkNextProject(t, filepath.Join(root, "apps", "admin"))
	mustMkNextProject(t, filepath.Join(root, "apps", "marketing"))
	mustMkNextProject(t, filepath.Join(root, "apps", "web"))

	_, picked, err := monorepoFallbackDevServer(root)
	if err != nil {
		t.Fatalf("monorepoFallbackDevServer: %v", err)
	}
	want := filepath.Join(root, "apps", "web")
	if picked != want {
		t.Fatalf("picker chose %q, want %q (apps/web is the conventional main app)", picked, want)
	}
}

// TestMonorepoFallbackPicker_FrameworkPreferenceStillWins checks that the
// new tiebreakers don't override the legacy framework-preference order:
// a Vite app should still beat a Next app even if the Next app has a
// "more conventional" name.
func TestMonorepoFallbackPicker_FrameworkPreferenceStillWins(t *testing.T) {
	root := t.TempDir()
	// Next at the conventional `web/` location.
	mustMkNextProject(t, filepath.Join(root, "web"))
	// Vite tucked away under `tools/` — not a conventional name, but
	// Vite still wins because it gets a usable preview faster.
	mustMkViteProject(t, filepath.Join(root, "tools", "playground"))

	_, picked, err := monorepoFallbackDevServer(root)
	if err != nil {
		t.Fatalf("monorepoFallbackDevServer: %v", err)
	}
	want := filepath.Join(root, "tools", "playground")
	if picked != want {
		t.Fatalf("picker chose %q, want %q (vite > next in framework preference)", picked, want)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func mustMkNextProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	pkg := `{"name":"app","version":"0.0.0","scripts":{"dev":"next dev"},"dependencies":{"next":"^14.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "next.config.js"), []byte("module.exports = {};\n"), 0o644); err != nil {
		t.Fatalf("write next.config.js: %v", err)
	}
}

func mustMkViteProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	pkg := `{"name":"app","version":"0.0.0","scripts":{"dev":"vite"},"dependencies":{"vite":"^5.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vite.config.js"), []byte("export default {};\n"), 0o644); err != nil {
		t.Fatalf("write vite.config.js: %v", err)
	}
}
