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
