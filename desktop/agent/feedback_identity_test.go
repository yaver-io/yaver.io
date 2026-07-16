package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newTestFeedbackManager builds a FeedbackManager rooted in a temp HOME,
// matching the setup in feedback_test.go.
func newTestFeedbackManager(t *testing.T) *FeedbackManager {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir .yaver: %v", err)
	}
	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	return fm
}

// writeExpoAppJSON writes the app.json surface that projectBundleIDMatches
// reads for Expo projects.
func writeExpoAppJSON(t *testing.T, dir, bundleID string) {
	t.Helper()
	body := `{"expo":{"name":"Talos","slug":"talos-mobile","ios":{"bundleIdentifier":"` + bundleID +
		`"},"android":{"package":"` + bundleID + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(body), 0600); err != nil {
		t.Fatalf("write app.json: %v", err)
	}
}

// The React Native SDK sent its device block under `device` until 0.9.2,
// while FeedbackReport, the Flutter SDK, and the web SDK all use
// `deviceInfo`. The mismatch meant every RN report's device block was
// dropped on the floor: no platform (so black-box correlation never fired)
// and no app name (so the fix router had nothing to resolve and fell back
// to whatever directory the agent happened to be sitting in).
//
// These tests pin both halves of the contract: the current wire format
// routes, and builds already in the field still report a usable device.

// feedbackIdentityMetadata is the metadata block emitted by
// resolveReportIdentity() in the RN SDK, as an Expo app would produce it.
const feedbackIdentityMetadata = `{
  "timestamp": "2026-07-16T10:00:00Z",
  "deviceInfo": {
    "platform": "ios",
    "osVersion": "18.5",
    "model": "iOS Device",
    "screenWidth": 393,
    "screenHeight": 852,
    "appName": "Talos"
  },
  "app": { "bundleId": "works.talos.mobile", "version": "1.9.157", "buildNumber": "427" },
  "project": {
    "appName": "Talos",
    "projectName": "Talos",
    "bundleId": "works.talos.mobile",
    "surface": "mobile"
  },
  "userNote": "[Auto-report via shake]"
}`

func TestReceiveFeedbackParsesSDKIdentity(t *testing.T) {
	fm := newTestFeedbackManager(t)

	report, err := fm.ReceiveFeedback(json.RawMessage(feedbackIdentityMetadata), nil)
	if err != nil {
		t.Fatalf("ReceiveFeedback: %v", err)
	}

	if got := report.DeviceInfo.Platform; got != "ios" {
		t.Errorf("DeviceInfo.Platform = %q, want %q", got, "ios")
	}
	// The key the fix router reads first.
	if got := report.DeviceInfo.AppName; got != "Talos" {
		t.Errorf("DeviceInfo.AppName = %q, want %q", got, "Talos")
	}
	// The only unambiguous routing key.
	if got := report.Project.BundleID; got != "works.talos.mobile" {
		t.Errorf("Project.BundleID = %q, want %q", got, "works.talos.mobile")
	}
	if got := report.Project.ProjectName; got != "Talos" {
		t.Errorf("Project.ProjectName = %q, want %q", got, "Talos")
	}
}

// Builds shipped before RN SDK 0.9.2 are installed on real phones via
// TestFlight/Play and cannot be updated retroactively. Their device block
// must still land.
func TestReceiveFeedbackAcceptsLegacyDeviceKey(t *testing.T) {
	fm := newTestFeedbackManager(t)

	legacy := `{
	  "timestamp": "2026-07-16T10:00:00Z",
	  "device": { "platform": "android", "osVersion": "15", "model": "Android Device" },
	  "app": {},
	  "userNote": "[Auto-report via shake]"
	}`

	report, err := fm.ReceiveFeedback(json.RawMessage(legacy), nil)
	if err != nil {
		t.Fatalf("ReceiveFeedback: %v", err)
	}
	if got := report.DeviceInfo.Platform; got != "android" {
		t.Errorf("legacy device.platform = %q, want %q", got, "android")
	}
	if got := report.DeviceInfo.Model; got != "Android Device" {
		t.Errorf("legacy device.model = %q, want %q", got, "Android Device")
	}
}

// A report that carries deviceInfo must not have it clobbered by the
// legacy alias.
func TestReceiveFeedbackPrefersDeviceInfoOverLegacyKey(t *testing.T) {
	fm := newTestFeedbackManager(t)

	both := `{
	  "timestamp": "2026-07-16T10:00:00Z",
	  "deviceInfo": { "platform": "ios", "model": "iOS Device" },
	  "device": { "platform": "android", "model": "Android Device" },
	  "app": {}
	}`

	report, err := fm.ReceiveFeedback(json.RawMessage(both), nil)
	if err != nil {
		t.Fatalf("ReceiveFeedback: %v", err)
	}
	if got := report.DeviceInfo.Platform; got != "ios" {
		t.Errorf("Platform = %q, want %q (deviceInfo must win)", got, "ios")
	}
}

// projectBundleIDMatches is what makes bundle-id routing unambiguous.
// A display name cannot do this job: the scanner names this project
// "talos / mobile", which matches neither "Talos" nor "talos-mobile",
// and the repo holds five other mobile projects whose names all start
// with "talos".
func TestProjectBundleIDMatchesExpoProject(t *testing.T) {
	dir := t.TempDir()
	writeExpoAppJSON(t, dir, "works.talos.mobile")

	if !projectBundleIDMatches(dir, "works.talos.mobile") {
		t.Error("projectBundleIDMatches = false, want true for the app.json bundle id")
	}
	if projectBundleIDMatches(dir, "works.talos.androidtv") {
		t.Error("projectBundleIDMatches = true for a sibling project's bundle id, want false")
	}
	if projectBundleIDMatches(dir, "") {
		t.Error("projectBundleIDMatches = true for an empty bundle id, want false")
	}
}

// A report is filed against the app the user was looking at, but on a
// monorepo the cause often isn't in that app — the mobile screen is a view
// over a backend in a sibling directory. Confined to mobile/, a fix task
// either can't reach the bug or "fixes" the symptom in the wrong layer.
func TestFeedbackFixWorkDirPrefersMonorepoRoot(t *testing.T) {
	monorepo := &MobileProject{
		Name:         "talos / mobile",
		Path:         "/Users/dev/Workspace/talos/mobile",
		MonorepoRoot: "/Users/dev/Workspace/talos",
		MonorepoApp:  "mobile",
	}
	if got, want := feedbackFixWorkDir(monorepo), "/Users/dev/Workspace/talos"; got != want {
		t.Errorf("feedbackFixWorkDir = %q, want %q (monorepo root)", got, want)
	}

	// Standalone repos have no MonorepoRoot; the project path is already
	// the repo root.
	standalone := &MobileProject{Name: "sfmg / mobile", Path: "/Users/dev/Workspace/sfmg"}
	if got, want := feedbackFixWorkDir(standalone), "/Users/dev/Workspace/sfmg"; got != want {
		t.Errorf("feedbackFixWorkDir = %q, want %q (project path)", got, want)
	}

	// An unresolved project must yield "", so the caller's guest guard
	// rejects rather than falling through to the agent's own directory.
	if got := feedbackFixWorkDir(nil); got != "" {
		t.Errorf("feedbackFixWorkDir(nil) = %q, want empty", got)
	}
}

// The manifest is JSON, so whitespace must not decide whether an app is
// routable. writeExpoAppJSON emits compact JSON on purpose: the previous
// substring matcher needed the literal `"bundleIdentifier": "<id>"` and
// missed this exact shape.
func TestExpoConfigHasBundleIDIgnoresFormatting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"compact", `{"expo":{"ios":{"bundleIdentifier":"works.talos.mobile"}}}`},
		{"pretty", "{\n  \"expo\": {\n    \"ios\": {\n      \"bundleIdentifier\": \"works.talos.mobile\"\n    }\n  }\n}"},
		{"extra spacing", `{"expo":{"ios":{"bundleIdentifier"   :    "works.talos.mobile"}}}`},
		{"android only", `{"expo":{"android":{"package":"works.talos.mobile"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(tc.body), 0600); err != nil {
				t.Fatalf("write app.json: %v", err)
			}
			if !expoConfigHasBundleID(dir, "works.talos.mobile") {
				t.Error("expoConfigHasBundleID = false, want true")
			}
			if expoConfigHasBundleID(dir, "com.example.other") {
				t.Error("expoConfigHasBundleID = true for an unrelated bundle id, want false")
			}
		})
	}
}

// A manifest that isn't valid JSON must degrade to the old substring
// behaviour rather than to "no match".
func TestExpoConfigHasBundleIDToleratesMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	// Trailing comma — invalid JSON, but pretty-printed.
	body := "{\n  \"expo\": {\n    \"ios\": { \"bundleIdentifier\": \"works.talos.mobile\", }\n  },\n}"
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(body), 0600); err != nil {
		t.Fatalf("write app.json: %v", err)
	}
	if !expoConfigHasBundleID(dir, "works.talos.mobile") {
		t.Error("expoConfigHasBundleID = false on malformed JSON, want the substring fallback to match")
	}
}
