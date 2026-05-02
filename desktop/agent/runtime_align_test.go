package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPickCompiledInRuntimeFamilyFiltersOutNonCompiled(t *testing.T) {
	families := []RuntimeFamily{
		{ID: "family-a", React: "19.2.5", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true},
		{ID: "family-b", React: "19.2.5", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: false},
	}
	chosen, reason := pickCompiledInRuntimeFamily(RuntimeFingerprint{ReactVersion: "19.2.5", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}, families)
	if chosen == nil {
		t.Fatalf("expected a compiledIn family, got nil (reason=%s)", reason)
	}
	if chosen.ID != "family-a" {
		t.Fatalf("got %q, want family-a", chosen.ID)
	}
}

func TestPickCompiledInRuntimeFamilyReturnsNilWhenNoneCompiled(t *testing.T) {
	families := []RuntimeFamily{
		{ID: "family-x", React: "19.0.0", CompiledIn: false},
	}
	chosen, reason := pickCompiledInRuntimeFamily(RuntimeFingerprint{ReactVersion: "19.2.5"}, families)
	if chosen != nil {
		t.Fatalf("expected nil, got %#v", chosen)
	}
	if reason == "" {
		t.Fatalf("expected reason, got empty string")
	}
}

func TestAlignProjectRuntimeSkipsWhenAlreadyMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "guest",
  "dependencies": {
    "react": "19.2.5",
    "react-dom": "19.2.5",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.2.5", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.2.5", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := alignProjectRuntimeIfNeeded(ctx, dir, family, guest, true)
	if report.Attempted {
		t.Fatalf("expected attempted=false when already matches, got %#v", report)
	}
	if report.SkippedReason == "" {
		t.Fatalf("expected SkippedReason, got %#v", report)
	}
}

func TestAlignProjectRuntimeRespectsAutoAlignFalse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "guest",
  "dependencies": {
    "react": "19.0.0"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.2.5", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.0.0"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := alignProjectRuntimeIfNeeded(ctx, dir, family, guest, false)
	if report.SkippedReason != "autoAlignRuntime=false" {
		t.Fatalf("expected skipped autoAlign=false, got %q", report.SkippedReason)
	}
}

func TestAlignProjectRuntimeWritesOverridesWhenMismatched(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "guest",
  "dependencies": {
    "react": "19.0.0",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.2.5", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.0.0", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}

	// Avoid running real npm in CI: cancel before npm install kicks off,
	// then re-read package.json to confirm overrides were written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectRuntimeIfNeeded(ctx, dir, family, guest, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}
	// npm install should have failed due to cancelled context but the
	// overrides write happens before that.
	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatalf("post-align package.json is unparseable: %v", err)
	}
	rawOv, ok := pkg["overrides"]
	if !ok {
		t.Fatalf("expected overrides in package.json, got %s", string(pkgRaw))
	}
	var ov map[string]string
	if err := json.Unmarshal(rawOv, &ov); err != nil {
		t.Fatalf("overrides not a string-string map: %v", err)
	}
	if ov["react"] != "19.2.5" {
		t.Fatalf("overrides.react = %q, want 19.2.5 (full overrides=%v)", ov["react"], ov)
	}
}

// TestAlignProjectRuntimeRewritesDirectDependency locks in the sfmg failure
// mode: project's package.json has `dependencies.react: "^19.2.5"`, host
// family wants 19.1.0. Writing only `overrides.react = "19.1.0"` is what the
// pre-fix code did — npm would not honour an override on a top-level direct
// dep, leaving node_modules/react at 19.2.5 and triggering
// RUNTIME_FAMILY_MISMATCH at the post-align re-probe. The fix must rewrite
// `dependencies.react` to the host family value AND build an explicit
// `npm install react@19.1.0 ...` arg list so the install plan actually
// downgrades. This test asserts the package.json half (the npm side is
// covered by the cancelled-ctx contract used by the older test).
func TestAlignProjectRuntimeRewritesDirectDependency(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "sfmg",
  "dependencies": {
    "react": "^19.2.5",
    "react-dom": "^19.2.5",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  },
  "overrides": {
    "react": "19.2.5",
    "react-dom": "19.2.5"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.1.0", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true}
	// Guest fingerprint comes from node_modules/react/package.json — assume
	// it currently still reflects the un-aligned 19.2.5.
	guest := RuntimeFingerprint{ReactVersion: "19.2.5", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectRuntimeIfNeeded(ctx, dir, family, guest, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}

	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatalf("post-align package.json is unparseable: %v", err)
	}

	// dependencies.react and dependencies.react-dom must now be the host
	// family value, not the original caret range.
	var deps map[string]string
	if err := json.Unmarshal(pkg["dependencies"], &deps); err != nil {
		t.Fatalf("dependencies not a string-string map: %v", err)
	}
	if deps["react"] != "19.1.0" {
		t.Fatalf("dependencies.react = %q, want 19.1.0 — auto-align must rewrite the direct dep, not just overrides", deps["react"])
	}
	if deps["react-dom"] != "19.1.0" {
		t.Fatalf("dependencies.react-dom = %q, want 19.1.0", deps["react-dom"])
	}
	if deps["react-native"] != "0.81.5" {
		t.Fatalf("dependencies.react-native = %q, want 0.81.5 (already matched, should be untouched)", deps["react-native"])
	}

	var ov map[string]string
	if err := json.Unmarshal(pkg["overrides"], &ov); err != nil {
		t.Fatalf("overrides not a string-string map: %v", err)
	}
	if ov["react"] != "19.1.0" {
		t.Fatalf("overrides.react = %q, want 19.1.0 — overrides must be rewritten too so transitive deps follow", ov["react"])
	}
}

// TestAlignProjectRuntimeDoesNotAddMissingDirectDependency asserts that the
// dependencies-block patch only rewrites keys the project ALREADY has. We
// must not accidentally inject `react-dom` into a project that genuinely
// doesn't depend on it (e.g. a pure RN app with no web target).
func TestAlignProjectRuntimeDoesNotAddMissingDirectDependency(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "rn-only",
  "dependencies": {
    "react": "^19.2.5",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.1.0", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.2.5", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = alignProjectRuntimeIfNeeded(ctx, dir, family, guest, true)

	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatal(err)
	}
	var deps map[string]string
	if err := json.Unmarshal(pkg["dependencies"], &deps); err != nil {
		t.Fatal(err)
	}
	if _, ok := deps["react-dom"]; ok {
		t.Fatalf("auto-align injected react-dom into a project that did not depend on it: %v", deps)
	}
}
