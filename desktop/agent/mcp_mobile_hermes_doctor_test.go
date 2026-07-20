package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMobileHermesDoctorResolvesMonorepoMobileApp(t *testing.T) {
	root := t.TempDir()
	mobileDir := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(mobileDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"demo-mobile","version":"0.0.0","dependencies":{"expo":"^52.0.0","react":"18.3.1","react-native":"0.76.0"}}`
	if err := os.WriteFile(filepath.Join(mobileDir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mobileDir, "app.json"), []byte(`{"expo":{"name":"demo","slug":"demo"}}`), 0o644); err != nil {
		t.Fatalf("write app.json: %v", err)
	}

	got := mobileHermesDoctor(mobileHermesDoctorInput{Directory: root})
	if got["projectDir"] != mobileDir {
		t.Fatalf("projectDir = %v, want %s", got["projectDir"], mobileDir)
	}
	if got["framework"] != "expo" {
		t.Fatalf("framework = %v, want expo", got["framework"])
	}
	if _, ok := got["projectStatus"].(map[string]interface{}); !ok {
		t.Fatalf("projectStatus missing or wrong type: %#v", got["projectStatus"])
	}
	if got["target"] != "mobile-hermes" {
		t.Fatalf("target = %v, want mobile-hermes", got["target"])
	}
}

// helper: does any string in xs contain sub?
func anyContains(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

// The doctor must agree with the build gate: a MISSING native module is a
// warning, not a blocker. Until 2026-07-20 the doctor blocked on it — the exact
// inverse of what handleBuildNativeBundle enforces — so it would tell the agent
// a talos-style app (guarded expo-gl require) is "not ready to build" when the
// build path loads it fine. A doctor that disagrees with the gate is a false
// green pointing the wrong way.
func TestMobileHermesDoctorTreatsMissingModuleAsWarningNotBlocker(t *testing.T) {
	root := t.TempDir()
	pkg := `{"name":"guest","version":"0.0.0","dependencies":{` +
		`"expo":"~54.0.0","react":"19.1.0","react-native":"0.81.5",` +
		`"react-native-yaver-fictional-test-module":"0.0.1"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.json"), []byte(`{"expo":{"name":"guest","slug":"guest"}}`), 0o644); err != nil {
		t.Fatalf("write app.json: %v", err)
	}
	// A native package marker so it is classed native (not JS-only), matching
	// how the compat report distinguishes the two.
	mkdirAll(t, filepath.Join(root, "node_modules", "react-native-yaver-fictional-test-module", "android"))

	got := mobileHermesDoctor(mobileHermesDoctorInput{Directory: root})
	blockers, _ := got["blockers"].([]string)
	warnings, _ := got["warnings"].([]string)

	if anyContains(blockers, "react-native-yaver-fictional-test-module") {
		t.Fatalf("missing module must NOT be a blocker (the build gate only warns); blockers=%v", blockers)
	}
	if !anyContains(warnings, "react-native-yaver-fictional-test-module") {
		t.Fatalf("missing module must still be WARNED about, so the agent knows it will throw if called unguarded; warnings=%v", warnings)
	}
}

func TestMobileHermesDoctorNoProjectSuggestsSelfHostedCreate(t *testing.T) {
	got := mobileHermesDoctor(mobileHermesDoctorInput{Directory: t.TempDir()})
	actions, ok := got["nextActions"].([]map[string]string)
	if !ok {
		t.Fatalf("nextActions missing or wrong type: %#v", got["nextActions"])
	}
	for _, action := range actions {
		if action["tool"] == "project_self_host_create" {
			return
		}
	}
	t.Fatalf("expected project_self_host_create next action, got %#v", actions)
}
