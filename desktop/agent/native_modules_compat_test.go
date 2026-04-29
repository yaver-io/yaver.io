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

func TestBuildCompatReport_DetectsRecordScreen(t *testing.T) {
	// The exact crash class from SFMG TestFlight 246: SFMG declares
	// react-native-record-screen, Yaver does not register it.
	tmp := t.TempDir()
	pkg := `{
  "dependencies": {
    "react": "19.1.0",
    "react-native": "0.81.5",
    "@react-native-async-storage/async-storage": "2.2.0",
    "react-native-record-screen": "0.6.1"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	report, err := BuildNativeModuleCompatReport(tmp)
	if err != nil {
		t.Fatalf("compat report: %v", err)
	}
	if len(report.Incompatible) == 0 {
		t.Fatalf("expected react-native-record-screen to be flagged incompatible, got none")
	}
	found := false
	for _, m := range report.Incompatible {
		if m == "react-native-record-screen" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected react-native-record-screen in Incompatible list, got %v", report.Incompatible)
	}
	// And async-storage should be in Matched.
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
		{"yaver-feedback-react-native", true},
	}
	for _, c := range cases {
		got := isLikelyNativeModule(c.name)
		if got != c.want {
			t.Errorf("isLikelyNativeModule(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
