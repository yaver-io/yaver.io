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

// TestAlignProjectRuntimeWritesOverridesAtWorkspaceRoot locks in the
// carrotbet failure mode: workspace root at /root/carrotbet declares
// `"workspaces": ["mobile", ...]`. The mobile child has
// `dependencies.react-native: "^0.81.5"` which npm resolves to 0.81.6, and
// host family wants 0.81.5. npm only honours `overrides` declared in the
// workspace ROOT package.json — the prior align run wrote them in
// mobile/package.json where npm silently ignored them, so the install kept
// resolving 0.81.6 and Hermes reload was blocked. This test checks that
// align (1) writes overrides at the workspace root, (2) pins
// dependencies.react-native to the exact host version in the child, and
// (3) strips the stale child-level overrides block.
func TestAlignProjectRuntimeWritesOverridesAtWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "name": "backgammon-platform",
  "private": true,
  "workspaces": ["apps/*", "packages/*", "mobile"]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "mobile")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "package.json"), []byte(`{
  "name": "mobile",
  "dependencies": {
    "react": "19.1.0",
    "react-dom": "19.1.0",
    "react-native": "0.81.6",
    "expo": "54.0.33"
  },
  "overrides": {
    "react-native": "0.81.5"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-a", React: "19.1.0", ReactNative: "0.81.5", ExpoVersion: "54.0.33", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.1.0", ReactNativeVersion: "0.81.6", ExpoVersion: "54.0.33"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectRuntimeIfNeeded(ctx, child, family, guest, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}
	if report.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", report.WorkspaceRoot, root)
	}
	if report.WorkspaceMember != "mobile" {
		t.Fatalf("WorkspaceMember = %q, want \"mobile\"", report.WorkspaceMember)
	}
	if report.OverridesWritten != filepath.Join(root, "package.json") {
		t.Fatalf("OverridesWritten = %q, want workspace root package.json", report.OverridesWritten)
	}

	// Workspace root must carry the host-family overrides.
	rootRaw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rootPkg map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &rootPkg); err != nil {
		t.Fatalf("workspace root package.json unparseable: %v", err)
	}
	rawOv, ok := rootPkg["overrides"]
	if !ok {
		t.Fatalf("expected overrides in workspace root package.json, got %s", string(rootRaw))
	}
	var ov map[string]string
	if err := json.Unmarshal(rawOv, &ov); err != nil {
		t.Fatalf("workspace root overrides not a string map: %v", err)
	}
	if ov["react-native"] != "0.81.5" {
		t.Fatalf("workspace root overrides.react-native = %q, want 0.81.5 — npm only honours overrides at the workspace root", ov["react-native"])
	}
	if ov["react"] != "19.1.0" || ov["expo"] != "54.0.33" {
		t.Fatalf("workspace root overrides missing react/expo: %v", ov)
	}

	// Child must have its direct dep pinned and the now-redundant
	// child-level overrides stripped (they were silently ignored by npm
	// and just confused diffs).
	childRaw, err := os.ReadFile(filepath.Join(child, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var childPkg map[string]json.RawMessage
	if err := json.Unmarshal(childRaw, &childPkg); err != nil {
		t.Fatalf("workspace child package.json unparseable: %v", err)
	}
	var childDeps map[string]string
	if err := json.Unmarshal(childPkg["dependencies"], &childDeps); err != nil {
		t.Fatalf("workspace child dependencies not a string map: %v", err)
	}
	if childDeps["react-native"] != "0.81.5" {
		t.Fatalf("workspace child dependencies.react-native = %q, want 0.81.5", childDeps["react-native"])
	}
	if _, ok := childPkg["overrides"]; ok {
		t.Fatalf("workspace child still has overrides; should have been stripped to avoid confusion")
	}
}

// TestAlignProjectRuntimeSyncsRootDepsWhenAlsoOverrideTargets locks in
// the carrotbet failure mode. The workspace root declares
// `dependencies.react-native: "0.81.5"` AND an override targeting the
// same key; align bumps the override to the host family (0.81.6) but
// the unsynced root dep makes npm 9+ throw EOVERRIDE on install,
// silently leaving a broken node_modules where another workspace
// member's React 18 hoists into the root next to mobile's React 19.
// The web bundle then crashes at `null is not an object (evaluating
// 'H.H.useState')`. The fix is to also rewrite the root direct dep to
// match the override version. See runtime_align.go § 2.5.
func TestAlignProjectRuntimeSyncsRootDepsWhenAlsoOverrideTargets(t *testing.T) {
	root := t.TempDir()
	// Root package.json mirrors carrotbet: workspace + same package as
	// both direct dep AND override target, with the dep version not
	// matching the override (the EOVERRIDE-trigger shape).
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "name": "backgammon-platform",
  "private": true,
  "workspaces": ["apps/*", "mobile"],
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "expo": "~54.0.33"
  },
  "overrides": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "mobile")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "package.json"), []byte(`{
  "name": "mobile",
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "expo": "54.0.33"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	family := &RuntimeFamily{ID: "family-b", React: "19.1.0", ReactNative: "0.81.6", ExpoVersion: "54.0.33", CompiledIn: true}
	guest := RuntimeFingerprint{ReactVersion: "19.1.0", ReactNativeVersion: "0.81.5", ExpoVersion: "54.0.33"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectRuntimeIfNeeded(ctx, child, family, guest, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}

	rootRaw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rootPkg map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &rootPkg); err != nil {
		t.Fatal(err)
	}

	// The override must have been bumped to the host family.
	var ov map[string]string
	if err := json.Unmarshal(rootPkg["overrides"], &ov); err != nil {
		t.Fatalf("workspace root overrides not a string map: %v", err)
	}
	if ov["react-native"] != "0.81.6" {
		t.Fatalf("overrides.react-native = %q, want 0.81.6", ov["react-native"])
	}

	// The root direct dep must have been synced to match the override —
	// otherwise npm 9+ throws EOVERRIDE and the install silently fails.
	var rootDeps map[string]string
	if err := json.Unmarshal(rootPkg["dependencies"], &rootDeps); err != nil {
		t.Fatalf("workspace root dependencies not a string map: %v", err)
	}
	if rootDeps["react-native"] != "0.81.6" {
		t.Fatalf("root dependencies.react-native = %q, want 0.81.6 (synced to override)", rootDeps["react-native"])
	}

	// react and expo were already at the host-family version, so they
	// should be left alone — only mismatched direct deps get rewritten.
	if rootDeps["react"] != "19.1.0" {
		t.Fatalf("root dependencies.react regressed to %q", rootDeps["react"])
	}
}

// TestAlignProjectRuntimeStandaloneProjectKeepsOverridesInPlace asserts
// that for a non-workspace project (e.g. demo/mobile/todo-rn) we still
// write overrides into the project's own package.json — we must not
// regress the working case while fixing the workspace case.
func TestAlignProjectRuntimeStandaloneProjectKeepsOverridesInPlace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "todo-rn",
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
	report := alignProjectRuntimeIfNeeded(ctx, dir, family, guest, true)
	if report.WorkspaceRoot != "" {
		t.Fatalf("expected no workspace, got WorkspaceRoot=%q", report.WorkspaceRoot)
	}
	if report.OverridesWritten != filepath.Join(dir, "package.json") {
		t.Fatalf("OverridesWritten = %q, want project's own package.json", report.OverridesWritten)
	}

	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatal(err)
	}
	var ov map[string]string
	if err := json.Unmarshal(pkg["overrides"], &ov); err != nil {
		t.Fatalf("standalone project missing overrides: %v", err)
	}
	if ov["react"] != "19.1.0" {
		t.Fatalf("standalone overrides.react = %q, want 19.1.0", ov["react"])
	}
}

// TestAlignProjectNativeModulesPinsMismatchedDirectDep locks in the
// macmini-vs-yaver-test-ephemeral failure mode: project declares
// expo-mail-composer 15.0.8, host has 55.0.13. The framework align
// (React/RN/Expo) leaves this alone because mail-composer is not in the
// runtime family triple — but the post-build compat check blocks the
// Hermes load on it. The native-module align must rewrite the direct
// dep AND record a pin so npm install --save-exact runs with the right
// spec.
func TestAlignProjectNativeModulesPinsMismatchedDirectDep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "sfmg",
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "expo": "54.0.33",
    "expo-mail-composer": "^15.0.8"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatches := []NativeModuleMismatch{
		{Name: "expo-mail-composer", ProjectVersion: "15.0.8", HostVersion: "55.0.13", Reason: "major version differs"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectNativeModulesIfNeeded(ctx, dir, mismatches, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}
	if report.Pins["expo-mail-composer"] != "55.0.13" {
		t.Fatalf("Pins[expo-mail-composer] = %q, want 55.0.13", report.Pins["expo-mail-composer"])
	}

	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatalf("post-align package.json unparseable: %v", err)
	}
	var deps map[string]string
	if err := json.Unmarshal(pkg["dependencies"], &deps); err != nil {
		t.Fatal(err)
	}
	if deps["expo-mail-composer"] != "55.0.13" {
		t.Fatalf("dependencies.expo-mail-composer = %q, want 55.0.13 — native-module align must rewrite the direct dep, not just overrides", deps["expo-mail-composer"])
	}
	var ov map[string]string
	if err := json.Unmarshal(pkg["overrides"], &ov); err != nil {
		t.Fatalf("post-align overrides missing or wrong shape: %v", err)
	}
	if ov["expo-mail-composer"] != "55.0.13" {
		t.Fatalf("overrides.expo-mail-composer = %q, want 55.0.13", ov["expo-mail-composer"])
	}
}

// TestAlignProjectNativeModulesPinsCompanion guards the talos regression:
// react-native-iap 14.7.x hard-imports react-native-nitro-modules, which
// guest package.json files don't declare. When the align step pins iap to
// the host version it MUST also inject the manifest-declared companion or
// `expo export:embed` fails with "Unable to resolve module
// react-native-nitro-modules". The companion is the one sanctioned
// exception to the "only rewrite declared deps" rule.
func TestAlignProjectNativeModulesPinsCompanion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "talos",
  "dependencies": {
    "react": "18.3.1",
    "react-native": "0.76.5",
    "expo": "52.0.0",
    "react-native-iap": "12.10.0"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatches := []NativeModuleMismatch{
		{Name: "react-native-iap", ProjectVersion: "12.10.0", HostVersion: "14.7.20", Reason: "major version differs"},
	}

	// Cancelled ctx: package.json rewrite + report.Pins happen before the
	// npm install round-trip, so we can assert without network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectNativeModulesIfNeeded(ctx, dir, mismatches, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}

	wantCompanion := hostNativeModuleCompanions("react-native-iap")["react-native-nitro-modules"]
	if wantCompanion == "" {
		t.Fatal("sdk-manifest.json must declare react-native-iap → react-native-nitro-modules companion at a resolvable version")
	}
	if report.Pins["react-native-iap"] != "14.7.20" {
		t.Fatalf("Pins[react-native-iap] = %q, want 14.7.20", report.Pins["react-native-iap"])
	}
	if report.Pins["react-native-nitro-modules"] != wantCompanion {
		t.Fatalf("Pins[react-native-nitro-modules] = %q, want %q — companion peer was not injected", report.Pins["react-native-nitro-modules"], wantCompanion)
	}

	pkgRaw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatalf("post-align package.json unparseable: %v", err)
	}
	var deps map[string]string
	if err := json.Unmarshal(pkg["dependencies"], &deps); err != nil {
		t.Fatal(err)
	}
	if deps["react-native-iap"] != "14.7.20" {
		t.Fatalf("dependencies.react-native-iap = %q, want 14.7.20", deps["react-native-iap"])
	}
	if deps["react-native-nitro-modules"] != wantCompanion {
		t.Fatalf("dependencies.react-native-nitro-modules = %q, want %q — companion must be added to dependencies so it survives a plain `npm install`", deps["react-native-nitro-modules"], wantCompanion)
	}
}

// TestAlignProjectNativeModulesNoMismatchIsNoop ensures the function does
// nothing when the compat report has no mismatches — this is the
// yaver-test-ephemeral fast path. We MUST NOT regress hosts where the
// project already happens to be aligned.
func TestAlignProjectNativeModulesNoMismatchIsNoop(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{
  "name": "guest",
  "dependencies": {
    "expo-mail-composer": "55.0.13"
  }
}
`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), original, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := alignProjectNativeModulesIfNeeded(ctx, dir, nil, true)
	if report.Attempted {
		t.Fatalf("expected Attempted=false on empty mismatches, got %#v", report)
	}
	if report.SkippedReason == "" {
		t.Fatalf("expected SkippedReason on empty mismatches, got %#v", report)
	}

	got, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("package.json was mutated when there were no mismatches:\n--- original ---\n%s\n--- got ---\n%s", original, got)
	}
}

// TestAlignProjectNativeModulesSkipsTransitive guards against pinning a
// package the project doesn't declare directly. The compat report can
// surface mismatches for packages we walked through node_modules (e.g.
// from a dep-of-a-dep) — we must not inject those into the project's
// package.json.
func TestAlignProjectNativeModulesSkipsTransitive(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{
  "name": "guest",
  "dependencies": {
    "react-native": "0.81.5"
  }
}
`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	mismatches := []NativeModuleMismatch{
		{Name: "expo-mail-composer", ProjectVersion: "15.0.8", HostVersion: "55.0.13", Reason: "major"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := alignProjectNativeModulesIfNeeded(ctx, dir, mismatches, true)
	if report.Attempted {
		t.Fatalf("expected Attempted=false (no direct dep matches), got %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("package.json was mutated even though no direct dep matched the pins")
	}
}

// TestAlignProjectNativeModulesWorkspaceRoot mirrors the carrotbet
// workspace fix from the framework align: pins for packages declared in
// the workspace child must land as overrides in the workspace ROOT (npm
// ignores them otherwise) AND as a direct-dep rewrite in the child.
func TestAlignProjectNativeModulesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "name": "carrotbet",
  "private": true,
  "workspaces": ["mobile"]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "mobile")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "package.json"), []byte(`{
  "name": "mobile",
  "dependencies": {
    "expo-mail-composer": "15.0.8"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatches := []NativeModuleMismatch{
		{Name: "expo-mail-composer", ProjectVersion: "15.0.8", HostVersion: "55.0.13", Reason: "major"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := alignProjectNativeModulesIfNeeded(ctx, child, mismatches, true)
	if !report.Attempted {
		t.Fatalf("expected Attempted=true, got %#v", report)
	}
	if report.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", report.WorkspaceRoot, root)
	}
	if report.OverridesWritten != filepath.Join(root, "package.json") {
		t.Fatalf("OverridesWritten = %q, want workspace-root package.json", report.OverridesWritten)
	}

	rootRaw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rootPkg map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &rootPkg); err != nil {
		t.Fatal(err)
	}
	var ov map[string]string
	if err := json.Unmarshal(rootPkg["overrides"], &ov); err != nil {
		t.Fatalf("workspace root missing overrides: %v", err)
	}
	if ov["expo-mail-composer"] != "55.0.13" {
		t.Fatalf("workspace-root overrides.expo-mail-composer = %q, want 55.0.13", ov["expo-mail-composer"])
	}

	childRaw, err := os.ReadFile(filepath.Join(child, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var childPkg map[string]json.RawMessage
	if err := json.Unmarshal(childRaw, &childPkg); err != nil {
		t.Fatal(err)
	}
	var deps map[string]string
	if err := json.Unmarshal(childPkg["dependencies"], &deps); err != nil {
		t.Fatal(err)
	}
	if deps["expo-mail-composer"] != "55.0.13" {
		t.Fatalf("workspace-child dependencies.expo-mail-composer = %q, want 55.0.13", deps["expo-mail-composer"])
	}
}

// TestDetectNpmWorkspaceRootGlobMember covers the apps/* glob case so
// monorepos that put their RN app at apps/mobile (sfmg-style turbo layout)
// also resolve correctly.
func TestDetectNpmWorkspaceRootGlobMember(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "name": "monorepo",
  "workspaces": ["apps/*", "packages/*"]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "package.json"), []byte(`{"name": "@monorepo/mobile"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	gotRoot, gotMember := detectNpmWorkspaceRoot(child)
	if gotRoot != root {
		t.Fatalf("detectNpmWorkspaceRoot root = %q, want %q", gotRoot, root)
	}
	if gotMember != "@monorepo/mobile" {
		t.Fatalf("detectNpmWorkspaceRoot member = %q, want @monorepo/mobile", gotMember)
	}
}
