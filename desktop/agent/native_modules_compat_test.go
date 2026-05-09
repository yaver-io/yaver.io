package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The agent embeds desktop/agent/sdk-manifest.json. It must stay
// byte-for-byte identical to mobile/sdk-manifest.json — the canonical
// copy that ships inside the iOS app bundle and is generated alongside
// the Yaver mobile build. Drift means the agent and the phone disagree
// on what native modules exist, which is exactly the bug class the
// compat handshake is supposed to catch.
func TestSDKManifestInSync(t *testing.T) {
	agentCopy, err := os.ReadFile("sdk-manifest.json")
	if err != nil {
		t.Fatalf("read agent sdk-manifest.json: %v", err)
	}
	mobileMaster, err := os.ReadFile(filepath.Join("..", "..", "mobile", "sdk-manifest.json"))
	if err != nil {
		t.Skipf("mobile/sdk-manifest.json not reachable from this checkout: %v", err)
	}
	// Compare the parsed structures, not the bytes — formatting
	// drift (extra whitespace, key ordering) is fine, content drift
	// is not.
	var a, b map[string]interface{}
	if err := json.Unmarshal(agentCopy, &a); err != nil {
		t.Fatalf("parse agent copy: %v", err)
	}
	if err := json.Unmarshal(mobileMaster, &b); err != nil {
		t.Fatalf("parse mobile master: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("desktop/agent/sdk-manifest.json is out of sync with mobile/sdk-manifest.json. " +
			"Run `cp mobile/sdk-manifest.json desktop/agent/sdk-manifest.json` and re-test.")
	}
}

func TestExtractProjectNativeModules(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "expo": "54.0.0",
    "expo-camera": "17.0.0",
    "@react-native-async-storage/async-storage": "2.2.0",
    "@gorhom/bottom-sheet": "5.2.9",
    "react-native-record-screen": "0.6.1",
    "react-native-uuid": "2.0.3",
    "zustand": "5.0.0",
    "convex": "1.19.0",
    "i18n-js": "5.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "expo-camera", "ios"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@react-native-async-storage", "async-storage", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-record-screen", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@gorhom", "bottom-sheet"))
	mods, err := ExtractProjectNativeModules(tmp)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := append([]string{}, mods...)
	sort.Strings(got)
	want := []string{
		"@react-native-async-storage/async-storage",
		"expo-camera",
		"react-native-record-screen",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("native module extraction mismatch.\n got: %v\nwant: %v", got, want)
	}
}

func TestExtractProjectNativeModules_UsesPackageMarkersToAvoidFalsePositives(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react-native-modal": "14.0.0",
    "react-native-reanimated-carousel": "4.0.3",
    "posthog-react-native": "4.42.1",
    "react-native-worklets": "0.7.4"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-modal"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-reanimated-carousel"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "posthog-react-native"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-worklets", "android"))
	mods, err := ExtractProjectNativeModules(tmp)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !reflect.DeepEqual(mods, []string{"react-native-worklets"}) {
		t.Fatalf("expected only package-marker-backed native modules, got %v", mods)
	}
}

func TestBuildCompatReport_DetectsMissingModule(t *testing.T) {
	// Use a fictional module that will never end up in Yaver's
	// manifest — keeps the test stable as the manifest grows. Pairs
	// with @react-native-async-storage/async-storage which is in the
	// manifest, so we exercise both buckets of the report.
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "@react-native-async-storage/async-storage": "2.2.0",
    "react-native-yaver-fictional-test-module": "0.0.1"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@react-native-async-storage", "async-storage", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-yaver-fictional-test-module", "android"))
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	foundIncompat := false
	for _, m := range report.Incompatible {
		if m == "react-native-yaver-fictional-test-module" {
			foundIncompat = true
			break
		}
	}
	if !foundIncompat {
		t.Fatalf("expected fictional module in Incompatible list, got %v", report.Incompatible)
	}
	matched := false
	for _, m := range report.Matched {
		if m == "@react-native-async-storage/async-storage" {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected @react-native-async-storage/async-storage in Matched, got %v", report.Matched)
	}
}

func TestBuildCompatReport_IgnoresFeedbackSDKPackage(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "yaver-feedback-react-native": "0.8.7",
    "@react-native-async-storage/async-storage": "2.2.0"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@react-native-async-storage", "async-storage", "android"))
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	for _, m := range report.ProjectModules {
		if m == "yaver-feedback-react-native" {
			t.Fatalf("feedback sdk should be ignored for Open in Yaver compatibility, got project modules %v", report.ProjectModules)
		}
	}
	foundIgnored := false
	for _, m := range report.Ignored {
		if m == "yaver-feedback-react-native" {
			foundIgnored = true
			break
		}
	}
	if !foundIgnored {
		t.Fatalf("expected feedback sdk in ignored list, got %v", report.Ignored)
	}
	for _, m := range report.Incompatible {
		if m == "yaver-feedback-react-native" {
			t.Fatalf("feedback sdk should not block compatibility, got incompatible %v", report.Incompatible)
		}
	}
}

func TestBuildCompatReport_FlagsBreakingVersionDrift(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "react-native-worklets": "^0.7.4",
    "react-native-record-screen": "^0.6.2"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-worklets", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-record-screen", "android"))
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	for _, mismatch := range report.VersionMismatches {
		if mismatch.Name == "react-native-worklets" {
			t.Fatalf("worklets should not be flagged when project matches current host line, got %+v", mismatch)
		}
		if mismatch.Name == "react-native-record-screen" {
			t.Fatalf("record-screen should not be flagged when versions match, got %+v", mismatch)
		}
	}
	if report.ReactVersionMismatch != nil {
		t.Fatalf("react exact match should not hard-block, got %+v", report.ReactVersionMismatch)
	}
	if report.ExpoVersionMismatch != nil {
		t.Fatalf("expo exact match should not hard-block, got %+v", report.ExpoVersionMismatch)
	}
	if report.RNVersionMismatch != nil {
		t.Fatalf("react-native exact match should not hard-block, got %+v", report.RNVersionMismatch)
	}
}

func TestBuildCompatReport_FlagsCurrentBreakingVersionDrift(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.2.5",
    "react-native": "0.81.5",
    "react-native-worklets": "^0.9.0"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-worklets", "android"))
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	foundWorklets := false
	for _, mismatch := range report.VersionMismatches {
		if mismatch.Name != "react-native-worklets" {
			continue
		}
		foundWorklets = true
		if mismatch.ProjectVersion != "0.9.0" {
			t.Fatalf("unexpected project version: %+v", mismatch)
		}
		if mismatch.HostVersion != "0.7.4" {
			t.Fatalf("unexpected host version: %+v", mismatch)
		}
		if mismatch.Reason != "0.x minor version differs" {
			t.Fatalf("unexpected mismatch reason: %+v", mismatch)
		}
	}
	if !foundWorklets {
		t.Fatalf("expected react-native-worklets version mismatch, got %+v", report.VersionMismatches)
	}
}

func TestDetectVersionMismatch(t *testing.T) {
	cases := []struct {
		name    string
		project string
		host    string
		wantNil bool
		reason  string
	}{
		{name: "matching", project: "^0.6.2", host: "0.6.2", wantNil: true},
		{name: "major mismatch", project: "^1.2.0", host: "2.0.0", reason: "major version differs"},
		{name: "0.x minor mismatch", project: "^0.7.4", host: "0.5.1", reason: "0.x minor version differs"},
		{name: "non-zero minor mismatch allowed", project: "^19.2.5", host: "19.1.0", wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectVersionMismatch(tc.project, tc.host)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected mismatch")
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestDetectFrameworkVersionMismatch(t *testing.T) {
	cases := []struct {
		name    string
		project string
		host    string
		wantNil bool
		reason  string
	}{
		{name: "matching", project: "^19.1.0", host: "19.1.0", wantNil: true},
		{name: "react minor mismatch", project: "^19.2.5", host: "19.1.0", reason: "exact runtime version differs"},
		{name: "expo patch mismatch", project: "~54.0.33", host: "54.0.0", reason: "exact runtime version differs"},
		{name: "rn patch mismatch", project: "0.81.6", host: "0.81.5", reason: "exact runtime version differs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectFrameworkVersionMismatch(tc.project, tc.host)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected mismatch")
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestBuildCompatReport_FlagsFrameworkRuntimeDrift(t *testing.T) {
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "expo": "54.0.0",
    "react": "19.2.5",
    "react-native": "0.81.6"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	if report.ExpoVersionMismatch == nil {
		t.Fatalf("expected expo version mismatch")
	}
	if report.ReactVersionMismatch == nil {
		t.Fatalf("expected react version mismatch")
	}
	if report.RNVersionMismatch == nil {
		t.Fatalf("expected react-native version mismatch")
	}
}

func TestSelectRuntimeFamily_ExactMatch(t *testing.T) {
	families, err := HostRuntimeFamilies()
	if err != nil {
		t.Fatalf("host runtime families: %v", err)
	}
	if len(families) == 0 {
		t.Fatalf("expected at least one runtime family")
	}
	guest := RuntimeFingerprint{
		ExpoVersion:        families[0].ExpoVersion,
		ReactNativeVersion: families[0].ReactNative,
		ReactVersion:       families[0].React,
	}
	selection := SelectRuntimeFamily(guest, families)
	if !selection.ExactMatch {
		t.Fatalf("expected exact match, got %+v", selection)
	}
	if selection.MatchKind != "exact" {
		t.Fatalf("unexpected match kind: %+v", selection)
	}
	if selection.Selected.ID != families[0].ID {
		t.Fatalf("selected family = %q, want %q", selection.Selected.ID, families[0].ID)
	}
}

func TestSelectRuntimeFamily_ClosestMatch(t *testing.T) {
	families, err := HostRuntimeFamilies()
	if err != nil {
		t.Fatalf("host runtime families: %v", err)
	}
	if len(families) == 0 {
		t.Fatalf("expected at least one runtime family")
	}
	guest := RuntimeFingerprint{
		ExpoVersion:        "54.0.0",
		ReactNativeVersion: "0.81.6",
		ReactVersion:       "19.2.5",
	}
	selection := SelectRuntimeFamily(guest, families)
	if selection.ExactMatch {
		t.Fatalf("expected closest match, got exact %+v", selection)
	}
	if selection.MatchKind != "closest" {
		t.Fatalf("unexpected match kind: %+v", selection)
	}
	if selection.Selected.ID != families[0].ID {
		t.Fatalf("selected family = %q, want %q", selection.Selected.ID, families[0].ID)
	}
	if selection.Distance <= 0 {
		t.Fatalf("expected positive distance, got %+v", selection)
	}
}

func TestSelectRuntimeFamily_PrefersPreferredPackageOnTie(t *testing.T) {
	families := []RuntimeFamily{
		{
			ID:              "family-a",
			Label:           "Family A",
			ExpoVersion:     "54.0.33",
			ReactNative:     "0.81.5",
			React:           "19.1.0",
			HermesBCVersion: 96,
			CompiledIn:      true,
		},
		{
			ID:                    "family-b",
			Label:                 "Family B",
			ExpoVersion:           "54.0.33",
			ReactNative:           "0.81.5",
			React:                 "19.2.5",
			HermesBCVersion:       96,
			CompiledIn:            true,
			PreferredPackageNames: []string{"sfmg"},
		},
	}
	guest := RuntimeFingerprint{
		PackageName:        "sfmg",
		ExpoVersion:        "54.0.33",
		ReactNativeVersion: "0.81.5",
		ReactVersion:       "19.2.5",
	}
	selection := SelectRuntimeFamily(guest, families)
	if selection.Selected.ID != "family-b" {
		t.Fatalf("selected family = %q, want family-b", selection.Selected.ID)
	}
}

func TestBuildNativeModuleCompatReportWithFamilies_UsesSelectedFamilyAsHostContract(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
  "name": "sfmg",
  "dependencies": {
    "expo": "54.0.33",
    "react-native": "0.81.5",
    "react": "19.2.5"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	report, err := BuildNativeModuleCompatReportWithFamilies(dir, []RuntimeFamily{
		{
			ID:              "family-a",
			Label:           "Family A",
			ExpoVersion:     "54.0.33",
			ReactNative:     "0.81.5",
			React:           "19.1.0",
			HermesBCVersion: 96,
			CompiledIn:      true,
		},
		{
			ID:                    "family-b",
			Label:                 "Family B",
			ExpoVersion:           "54.0.33",
			ReactNative:           "0.81.5",
			React:                 "19.2.5",
			HermesBCVersion:       96,
			CompiledIn:            true,
			PreferredPackageNames: []string{"sfmg"},
		},
	})
	if err != nil {
		t.Fatalf("BuildNativeModuleCompatReportWithFamilies: %v", err)
	}
	if report.RuntimeFamily == nil {
		t.Fatal("expected runtime family selection")
	}
	if report.RuntimeFamily.Selected.ID != "family-b" {
		t.Fatalf("selected family = %q, want family-b", report.RuntimeFamily.Selected.ID)
	}
	if report.ReactVersionMismatch != nil {
		t.Fatalf("react mismatch = %#v, want nil because selected family-b matches project", report.ReactVersionMismatch)
	}
	if report.HostReact != "19.2.5" {
		t.Fatalf("host react = %q, want 19.2.5 from selected family", report.HostReact)
	}
}

func TestHostRuntimeFamilies_UsesEmbeddedManifestFamilies(t *testing.T) {
	families, err := HostRuntimeFamilies()
	if err != nil {
		t.Fatalf("HostRuntimeFamilies: %v", err)
	}
	if len(families) < 2 {
		t.Fatalf("got %d runtime families, want at least 2", len(families))
	}
	var familyB *RuntimeFamily
	for i := range families {
		if families[i].ID == "family-b" {
			familyB = &families[i]
			break
		}
	}
	if familyB == nil {
		t.Fatalf("family-b missing from embedded runtime families: %#v", families)
	}
	if !familyB.CompiledIn {
		t.Fatalf("family-b compiledIn = false, want true")
	}
	if familyB.Status != "pilot" {
		t.Fatalf("family-b status = %q, want pilot", familyB.Status)
	}
	if len(familyB.PreferredPackageNames) != 1 || familyB.PreferredPackageNames[0] != "sfmg" {
		t.Fatalf("family-b preferredPackageNames = %#v, want [sfmg]", familyB.PreferredPackageNames)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func TestIsLikelyNativeModule_FalsePositiveGuards(t *testing.T) {
	tmp := t.TempDir()
	mkdirAll(t, filepath.Join(tmp, "node_modules", "react-native-async-storage", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@react-native-async-storage", "async-storage", "android"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "expo-camera", "ios"))
	mkdirAll(t, filepath.Join(tmp, "node_modules", "@expo", "vector-icons"))
	cases := []struct {
		name string
		want bool
	}{
		{"react-native", false},                 // engine
		{"react-native-web", false},             // pure JS
		{"react-native-svg-transformer", false}, // build tool
		{"react-native-async-storage", true},
		{"@react-native-async-storage/async-storage", true},
		{"expo-camera", true},
		{"@expo/vector-icons", false},
		{"convex", false},
		{"zustand", false},
		{"yaver-feedback-react-native", false},
	}
	for _, c := range cases {
		got := isLikelyNativeModule(tmp, c.name)
		if got != c.want {
			t.Errorf("isLikelyNativeModule(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBuildNativeModuleCompatReportWith_HostOverlayUnblocksMissingModule
// locks in the dynamic-handshake contract: when the mobile reports a
// native module via consumerNativeModules that the agent's embedded
// manifest hasn't picked up yet, the compat check treats it as
// available. Without this the embedded manifest dictates compat for
// every agent install in the wild and a fresh mobile module breaks
// self-load until the agent npm bumps and reaches every box.
func TestBuildNativeModuleCompatReportWith_HostOverlayUnblocksMissingModule(t *testing.T) {
	dir := t.TempDir()
	// Pick a native module that's vanishingly unlikely to ever ship in
	// the embedded manifest — a fictional name keeps this test stable
	// even if mobile/sdk-manifest.json grows over time.
	moduleName := "react-native-yaver-overlay-test-fixture"
	pkg := fmt.Sprintf(`{
  "name": "overlay-test",
  "dependencies": {
    "expo": "54.0.33",
    "react": "19.1.0",
    "react-native": "0.81.5",
    "%s": "1.0.0"
  }
}`, moduleName)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	// Drop a marker file under node_modules/<name> so isLikelyNativeModule
	// considers the dep native; otherwise it's filtered out as JS-only.
	nm := filepath.Join(dir, "node_modules", moduleName, "ios")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatalf("mkdir nm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(nm), "package.json"), []byte(`{"name":"`+moduleName+`","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write nm package.json: %v", err)
	}
	// Drop a podspec so the heuristic flags it native.
	if err := os.WriteFile(filepath.Join(filepath.Dir(nm), moduleName+".podspec"), []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write podspec: %v", err)
	}

	// Without overlay → module is missing.
	bare, err := BuildNativeModuleCompatReportWith(dir, nil, nil)
	if err != nil {
		t.Fatalf("compat without overlay: %v", err)
	}
	foundMissing := false
	for _, m := range bare.Incompatible {
		if m == moduleName {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("baseline expected %s in Incompatible, got %#v (matched=%#v)", moduleName, bare.Incompatible, bare.Matched)
	}

	// With overlay → module is matched.
	overlay := map[string]string{moduleName: "1.0.0"}
	withOverlay, err := BuildNativeModuleCompatReportWith(dir, nil, overlay)
	if err != nil {
		t.Fatalf("compat with overlay: %v", err)
	}
	for _, m := range withOverlay.Incompatible {
		if m == moduleName {
			t.Fatalf("overlay should have unblocked %s, still in Incompatible: %#v", moduleName, withOverlay.Incompatible)
		}
	}
	matchedHasModule := false
	for _, m := range withOverlay.Matched {
		if m == moduleName {
			matchedHasModule = true
		}
	}
	if !matchedHasModule {
		t.Fatalf("overlay should have moved %s to Matched, got %#v", moduleName, withOverlay.Matched)
	}
}
