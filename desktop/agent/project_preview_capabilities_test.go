package main

// Detection-driven option lists. The rule under test everywhere in this file:
// Hermes is React Native / Expo ONLY, and for other stacks it must be ABSENT —
// not present-and-disabled. A greyed-out button still tells the user the option
// exists for their Flutter app, and it does not.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func capsFor(t *testing.T, files map[string]string, paired bool) ProjectPreviewCapabilities {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return DetectProjectPreviewCapabilities(dir, "", paired)
}

func hasOption(caps ProjectPreviewCapabilities, id string) bool {
	for _, o := range caps.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}

// ── Hermes appears ONLY for RN/Expo ────────────────────────────────────────

func TestHermesOfferedOnlyForReactNativeAndExpo(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		wantFw     string
		wantHermes bool
	}{
		{
			name:       "expo",
			files:      map[string]string{"package.json": `{"name":"todo","dependencies":{"expo":"~52.0.0"}}`},
			wantFw:     "expo",
			wantHermes: true,
		},
		{
			name:       "react-native",
			files:      map[string]string{"package.json": `{"name":"todo","dependencies":{"react-native":"0.76.0"}}`},
			wantFw:     "react-native",
			wantHermes: true,
		},
		{
			name:       "flutter",
			files:      map[string]string{"pubspec.yaml": "name: todo_flutter\n"},
			wantFw:     "flutter",
			wantHermes: false,
		},
		{
			name: "kotlin-android",
			files: map[string]string{
				"build.gradle.kts":                 `plugins { id("com.android.application") }`,
				"settings.gradle.kts":              `rootProject.name = "todo"`,
				"app/src/main/AndroidManifest.xml": `<manifest/>`,
				"app/build.gradle.kts":             `plugins { id("com.android.application") }`,
			},
			wantFw:     "kotlin",
			wantHermes: false,
		},
		{
			name:       "swift",
			files:      map[string]string{"Package.swift": `// swift-tools-version:5.9`},
			wantFw:     "swift",
			wantHermes: false,
		},
		{
			name:       "nextjs",
			files:      map[string]string{"next.config.js": `module.exports = {}`},
			wantFw:     "nextjs",
			wantHermes: false,
		},
		{
			name:       "vite",
			files:      map[string]string{"vite.config.js": `export default {}`},
			wantFw:     "vite",
			wantHermes: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := capsFor(t, tc.files, true)
			if caps.Framework != tc.wantFw {
				t.Fatalf("framework = %q, want %q", caps.Framework, tc.wantFw)
			}
			if got := HermesOfferedFor(caps); got != tc.wantHermes {
				t.Fatalf("hermes offered = %v, want %v (framework %q)", got, tc.wantHermes, caps.Framework)
			}
			if !tc.wantHermes {
				// Absent, not merely disabled.
				for _, o := range caps.Options {
					if o.ID == PreviewOptionHermes || o.ID == PreviewOptionOpenNative {
						t.Fatalf("%s: option %q present (supported=%v) — it must not be listed at all",
							tc.name, o.ID, o.Supported)
					}
				}
			}
			if len(caps.Options) == 0 {
				t.Fatalf("%s: no options at all — a surface would render an empty sheet", tc.name)
			}
			if caps.Reason == "" {
				t.Fatalf("%s: no reason given", tc.name)
			}
		})
	}
}

// Native stacks get a runtime option, and it explains what they need.
func TestNativeStacksGetARuntimeOptionWithAnExplanation(t *testing.T) {
	swift := capsFor(t, map[string]string{"Package.swift": "// swift-tools-version:5.9"}, false)
	if !hasOption(swift, PreviewOptionRemoteRuntime) {
		t.Fatalf("swift has no remote-runtime option: %+v", swift.Options)
	}
	kotlin := capsFor(t, map[string]string{
		"build.gradle.kts":                 `plugins { id("com.android.application") }`,
		"settings.gradle.kts":              `rootProject.name = "t"`,
		"app/src/main/AndroidManifest.xml": `<manifest/>`,
		"app/build.gradle.kts":             `plugins { id("com.android.application") }`,
	}, false)
	if !hasOption(kotlin, PreviewOptionRemoteRuntime) {
		t.Fatalf("kotlin has no remote-runtime option: %+v", kotlin.Options)
	}
	for _, o := range kotlin.Options {
		if o.ID == PreviewOptionRemoteRuntime && o.Reason == "" {
			t.Fatalf("kotlin remote-runtime option has no explanation")
		}
	}
}

// Flutter renders in a browser: dev server leads, Hermes never appears.
func TestFlutterLeadsWithTheDevServer(t *testing.T) {
	caps := capsFor(t, map[string]string{"pubspec.yaml": "name: todo\n"}, true)
	var primary string
	for _, o := range caps.Options {
		if o.Primary {
			primary = o.ID
		}
	}
	if primary != PreviewOptionDevServer {
		t.Fatalf("flutter primary = %q, want dev-server", primary)
	}
	if HermesOfferedFor(caps) {
		t.Fatalf("flutter offered Hermes")
	}
}

// ── Pairing changes support, not the option set ────────────────────────────

func TestPairedDeviceDrivesOpenInYaverSupport(t *testing.T) {
	files := map[string]string{"package.json": `{"dependencies":{"expo":"*"}}`}

	unpaired := capsFor(t, files, false)
	for _, o := range unpaired.Options {
		if o.ID == PreviewOptionOpenNative {
			if o.Supported {
				t.Fatalf("open-native supported with no paired device")
			}
			if o.Reason == "" {
				t.Fatalf("open-native disabled with no explanation — the user cannot tell what to fix")
			}
		}
	}
	paired := capsFor(t, files, true)
	for _, o := range paired.Options {
		if o.ID == PreviewOptionOpenNative && !o.Supported {
			t.Fatalf("open-native unsupported despite a paired device: %q", o.Reason)
		}
	}
}

// With no device, streaming should lead rather than a disabled device action.
func TestUnpairedRNLeadsWithStreaming(t *testing.T) {
	caps := capsFor(t, map[string]string{"package.json": `{"dependencies":{"expo":"*"}}`}, false)
	for _, o := range caps.Options {
		if o.Primary && o.ID != PreviewOptionRemoteRuntime {
			t.Fatalf("primary = %q with no paired device, want remote-runtime", o.ID)
		}
	}
}

// ── Yaver self-development ─────────────────────────────────────────────────

func TestSelfDevelopmentReplacesHermesWithStreaming(t *testing.T) {
	caps := capsFor(t, map[string]string{
		"package.json": `{"name":"yaver-mobile","dependencies":{"expo":"*"}}`,
	}, true)

	if !caps.SelfDevelopment {
		t.Fatalf("yaver-mobile not detected as self-development")
	}
	if HermesOfferedFor(caps) {
		t.Fatalf("Hermes offered for Yaver self-development — that is the recursion trap")
	}
	if !hasOption(caps, PreviewOptionRemoteRuntime) {
		t.Fatalf("no streaming option offered as the replacement: %+v", caps.Options)
	}
	if caps.Reason == "" {
		t.Fatalf("self-development gave no reason")
	}
}

// A third-party RN app inside a yaver.io checkout keeps Hermes.
func TestThirdPartyRNInsideRepoKeepsHermes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "yaver.io")
	dir := filepath.Join(root, "demo", "mobile", "todo-rn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"todo-rn","dependencies":{"expo":"*"}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	caps := DetectProjectPreviewCapabilities(dir, "", true)
	if caps.SelfDevelopment {
		t.Fatalf("third-party fixture inside the repo marked as self-development")
	}
	if !HermesOfferedFor(caps) {
		t.Fatalf("third-party RN app lost Hermes: %+v", caps.Options)
	}
}

// ── Detection beats the caller's hint ──────────────────────────────────────

// A surface that guesses wrong must not be able to conjure Hermes for a Flutter
// project. Disk wins.
func TestDetectionOverridesAWrongFrameworkHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: todo\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	caps := DetectProjectPreviewCapabilities(dir, "react-native", true)
	if caps.Framework != "flutter" {
		t.Fatalf("framework = %q, want flutter — the caller's hint overrode disk", caps.Framework)
	}
	if HermesOfferedFor(caps) {
		t.Fatalf("a wrong hint conjured Hermes for a Flutter project")
	}
}

// The hint is still used when the agent genuinely cannot see the project.
func TestFrameworkHintUsedOnlyWhenNothingIsDetectable(t *testing.T) {
	caps := DetectProjectPreviewCapabilities("", "expo", true)
	if caps.Framework != "expo" {
		t.Fatalf("framework = %q, want the hint to apply when no dir is readable", caps.Framework)
	}
	if !HermesOfferedFor(caps) {
		t.Fatalf("hinted RN project lost Hermes")
	}
}

// ── The ops verb every surface calls ───────────────────────────────────────

func TestOpsProjectPreviewOptionsReturnsDetectedOptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: todo\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := json.Marshal(map[string]interface{}{"workDir": dir, "hasPairedDevice": true})
	res := opsProjectPreviewOptionsHandler(OpsContext{}, body)
	if !res.OK {
		t.Fatalf("verb failed: %+v", res)
	}
	caps, ok := res.Initial.(ProjectPreviewCapabilities)
	if !ok {
		t.Fatalf("unexpected result type %T", res.Initial)
	}
	if caps.Framework != "flutter" {
		t.Fatalf("framework = %q", caps.Framework)
	}
	if HermesOfferedFor(caps) {
		t.Fatalf("verb offered Hermes for Flutter")
	}
}

// Every option a surface renders must carry a label — an unlabelled row is an
// unpressable button.
func TestEveryOptionHasAnIDAndLabel(t *testing.T) {
	fixtures := []map[string]string{
		{"package.json": `{"dependencies":{"expo":"*"}}`},
		{"pubspec.yaml": "name: t\n"},
		{"Package.swift": "// x"},
		{"next.config.js": "module.exports={}"},
	}
	for i, f := range fixtures {
		caps := capsFor(t, f, i%2 == 0)
		for _, o := range caps.Options {
			if o.ID == "" || o.Label == "" {
				t.Fatalf("fixture %d: option with empty id/label: %+v", i, o)
			}
			if !o.Supported && o.Reason == "" {
				t.Fatalf("fixture %d: option %q disabled with no reason", i, o.ID)
			}
		}
	}
}
