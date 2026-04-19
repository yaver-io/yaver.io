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
