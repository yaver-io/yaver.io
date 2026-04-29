package main

import (
	"encoding/json"
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
	mods, err := ExtractProjectNativeModules(tmp)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := append([]string{}, mods...)
	sort.Strings(got)
	// Heuristic intentionally narrow: must contain "react-native" or
	// start with "expo-" / "@expo/". `@gorhom/bottom-sheet` is native but
	// won't pass the heuristic — that's an accepted false-negative since
	// it's already in Yaver's sdk-manifest, so the manifest match path
	// catches it during compat reporting. `expo` (umbrella) is in
	// jsOnlyExact so it doesn't get flagged as missing-from-manifest.
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
    "react": "19.2.5",
    "react-native": "0.81.5",
    "react-native-worklets": "^0.7.4",
    "react-native-record-screen": "^0.6.2"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	foundWorklets := false
	for _, mismatch := range report.VersionMismatches {
		if mismatch.Name == "react-native-worklets" {
			foundWorklets = true
			if mismatch.ProjectVersion != "0.7.4" {
				t.Fatalf("unexpected project version: %+v", mismatch)
			}
			if mismatch.HostVersion != "0.5.1" {
				t.Fatalf("unexpected host version: %+v", mismatch)
			}
			if mismatch.Reason != "0.x minor version differs" {
				t.Fatalf("unexpected mismatch reason: %+v", mismatch)
			}
		}
		if mismatch.Name == "react-native-record-screen" {
			t.Fatalf("record-screen should not be flagged when versions match, got %+v", mismatch)
		}
	}
	if !foundWorklets {
		t.Fatalf("expected react-native-worklets version mismatch, got %+v", report.VersionMismatches)
	}
	if report.ReactVersionMismatch != nil {
		t.Fatalf("react 19.x minor drift should not hard-block, got %+v", report.ReactVersionMismatch)
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

func TestIsLikelyNativeModule_FalsePositiveGuards(t *testing.T) {
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
		{"@expo/vector-icons", true},
		{"convex", false},
		{"zustand", false},
		{"yaver-feedback-react-native", false},
	}
	for _, c := range cases {
		got := isLikelyNativeModule(c.name)
		if got != c.want {
			t.Errorf("isLikelyNativeModule(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
