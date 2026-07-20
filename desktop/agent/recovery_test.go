package main

import (
	"strings"
	"testing"
)

func TestBuildRecoveryPrompt_IncludesContext(t *testing.T) {
	cases := []struct {
		name string
		ctx  RecoveryContext
		want []string // substrings that must appear in prompt
	}{
		{
			name: "hermes build failure routes with project + error + Metro guidance",
			ctx: RecoveryContext{
				Kind:      RecoveryHermesBuildFailed,
				Framework: "expo",
				WorkDir:   "/Users/kivanc/repo/myapp",
				Error:     "hermes bytecode version mismatch",
			},
			want: []string{
				"myapp",
				"expo",
				"hermes bytecode version mismatch",
				"Metro",
				"Do not run `expo run:ios`",
			},
		},
		{
			name: "missing runtime pins tool name + per-user install guidance",
			ctx: RecoveryContext{
				Kind:     RecoveryMissingRuntime,
				Tool:     "node",
				WorkDir:  "/home/me/myflapp",
				UserGoal: "run the Flutter flush",
			},
			want: []string{
				"node",
				"~/.yaver/runtimes/node",
				"run the Flutter flush",
			},
		},
		{
			name: "flutter flush failure tells agent not to treat 'ios' as a device id",
			ctx: RecoveryContext{
				Kind:    RecoveryFlutterFlushFailed,
				WorkDir: "/repos/flapp",
				Error:   "device id 'ios' is not valid",
			},
			want: []string{
				"flapp",
				"device id 'ios' is not valid",
				"--platform ios",
				"resolve the actual phone id",
			},
		},
		{
			name: "swift build failure mentions xcodebuild + darwin-only",
			ctx: RecoveryContext{
				Kind:    RecoverySwiftBuildFailed,
				WorkDir: "/repos/swapp",
				Error:   "No profiles for 'com.acme.sw'",
			},
			want: []string{
				"swapp",
				"xcodebuild",
				"No profiles",
			},
		},
		{
			name: "kotlin install suggests enabling unknown apps",
			ctx: RecoveryContext{
				Kind:  RecoveryKotlinInstallFailed,
				Error: "INSTALL_PARSE_FAILED",
			},
			want: []string{
				"Install unknown apps",
				"INSTALL_PARSE_FAILED",
			},
		},
		{
			name: "generic kind still produces a runnable prompt with error",
			ctx: RecoveryContext{
				Kind:     RecoveryGeneric,
				UserGoal: "open the app on my phone",
				Error:    "whoops",
			},
			want: []string{
				"open the app on my phone",
				"whoops",
				"Investigate and fix",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title, prompt := BuildRecoveryPrompt(tc.ctx)
			if strings.TrimSpace(title) == "" {
				t.Fatalf("title was empty")
			}
			for _, s := range tc.want {
				if !strings.Contains(prompt, s) {
					t.Errorf("prompt missing %q\n--- prompt ---\n%s", s, prompt)
				}
			}
		})
	}
}

// The compat-blocked fix prompt must name the EXACT blocking modules and their
// version pairs from the structured report — that precision is the whole reason
// RecoveryContext.Compat exists. A generic "align your versions" prompt sends the
// runner guessing; naming `react-native-foo 3.0.0 vs 2.0.0` does not.
func TestBuildRecoveryPrompt_HermesCompatBlocked_NamesModulesAndVersions(t *testing.T) {
	_, prompt := BuildRecoveryPrompt(RecoveryContext{
		Kind:      RecoveryHermesCompatBlocked,
		Framework: "expo",
		WorkDir:   "/Users/kivanc/repo/talos/mobile",
		Compat: &CompatReport{
			Incompatible: []string{"expo-gl"},
			VersionMismatches: []NativeModuleMismatch{
				{Name: "react-native-foo", ProjectVersion: "3.0.0", HostVersion: "2.0.0", Reason: "major bump"},
			},
			RuntimeFamily: &RuntimeFamilySelection{
				Selected:      RuntimeFamily{Label: "Family A"},
				SupportedHint: "Expo 54 / RN 0.81",
			},
			GuestRuntime: RuntimeFingerprint{ExpoVersion: "54.0.33", ReactNativeVersion: "0.81.5", ReactVersion: "19.1.0"},
		},
	})
	for _, want := range []string{
		"react-native-foo",     // the fatal module
		"3.0.0",                // project version
		"2.0.0",                // host version — align DOWN to this
		"expo-gl",              // the warning-only module
		"Cell3D",               // the gold-standard guard pattern
		"try {",                // the actual guard snippet
		"align the GUEST DOWN", // the constraint that matters most
		"POST /dev/build-native",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("compat fix prompt missing %q\n--- prompt ---\n%s", want, prompt)
		}
	}
	// It must explicitly FORBID the wrong fix (bumping/adding to the shared host),
	// not suggest it. Assert the prohibition is present rather than trying to
	// detect a suggestion — the words "host" and "add" legitimately appear in the
	// prohibition itself.
	if !strings.Contains(prompt, "never bump the host") && !strings.Contains(prompt, "align the GUEST DOWN to the host, never the host up") {
		t.Error("compat fix prompt must explicitly forbid bumping the shared host")
	}
	if !strings.Contains(prompt, "do NOT add a native module the host lacks") && !strings.Contains(prompt, "shared across every user") {
		t.Error("compat fix prompt must forbid adding native deps to the shared host")
	}
}

func TestBuildRecoveryPrompt_NeverForbidsHermesRunIos(t *testing.T) {
	// The Hermes path must keep telling the agent not to fall back to
	// expo run:ios — that was a real regression before.
	_, prompt := BuildRecoveryPrompt(RecoveryContext{
		Kind:    RecoveryHermesBuildFailed,
		WorkDir: "/x/myapp",
		Error:   "some metro error",
	})
	for _, forbidden := range []string{"expo run:ios", "xcodebuild", "gradlew"} {
		if !strings.Contains(prompt, forbidden) {
			t.Errorf("Hermes recovery prompt should mention %q so the agent knows to avoid it\n--- prompt ---\n%s", forbidden, prompt)
		}
	}
}
