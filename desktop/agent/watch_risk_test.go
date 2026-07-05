package main

import (
	"strings"
	"testing"
)

func TestWatchNeedsConfirm(t *testing.T) {
	risky := []string{
		"deploy the web app",
		"force push and delete the branch",
		"ship it to production",
		"reset the database",
		"rollback the last release",
		"rm -rf the cache",
	}
	for _, c := range risky {
		if !watchNeedsConfirm(c) {
			t.Errorf("expected %q to need confirm", c)
		}
	}
	safe := []string{
		"add a test for the auth refresh path",
		"fix the failing build on magara",
		"rename the handler",
		"what's my next meeting",
		"the deltas look fine", // must NOT match "delete"
	}
	for _, c := range safe {
		if watchNeedsConfirm(c) {
			t.Errorf("expected %q to NOT need confirm (kinds=%v)", c, watchRiskKinds(c))
		}
	}
}

func TestWatchConfirmPromptNamesAction(t *testing.T) {
	p := watchConfirmPrompt("deploy to production")
	if !strings.Contains(p, "deploy") || !strings.Contains(p, "production") {
		t.Errorf("prompt should name the matched actions: %q", p)
	}
	if watchConfirmPrompt("add a test") != "" {
		t.Errorf("a safe command should yield no confirm prompt")
	}
}

func TestWatchIsReadCodeRequest(t *testing.T) {
	if !watchIsReadCodeRequest("read me the diff") {
		t.Error("should flag read-the-diff")
	}
	if !watchIsReadCodeRequest("show me the stack trace") {
		t.Error("should flag show-the-stack-trace")
	}
	// Normal coding commands must NOT trip the read-code guard.
	if watchIsReadCodeRequest("add a test and run it") {
		t.Error("normal coding command wrongly flagged as read-code")
	}
}

func TestWatchIntentToTranscript(t *testing.T) {
	if watchIntentToTranscript("run-tests") == "" {
		t.Error("run-tests should expand")
	}
	if got := watchIntentToTranscript("deploy"); got != "deploy" {
		t.Errorf("deploy intent should expand to a deploy transcript, got %q", got)
	}
	if watchIntentToTranscript("nonsense") != "" {
		t.Error("unknown intent should expand to empty")
	}
	// The deploy intent must still be caught by the risk gate downstream.
	if !watchNeedsConfirm(watchIntentToTranscript("deploy")) {
		t.Error("expanded deploy intent must still require confirmation")
	}
}

func TestBuildWatchPromptModes(t *testing.T) {
	cases := []struct {
		in   string
		mode string
		want string
	}{
		{"idea for sfmg owner mode sponsor board", "idea-capture", "Do not edit code"},
		{"add this to sfmg owner mode", "implementation", "permission to work"},
		{"deploy to production", "implementation", "permission to work"},
		{"open browser and check pricing", "browser-automation", "Stop for login"},
		{"what is the status of my remote runtime session", "remote-runtime-question", "one-sentence watch summary"},
	}
	for _, tc := range cases {
		plan := buildWatchPrompt(tc.in)
		if plan.Mode != tc.mode {
			t.Fatalf("%q mode = %q, want %q", tc.in, plan.Mode, tc.mode)
		}
		if !strings.Contains(plan.Prompt, tc.want) {
			t.Fatalf("%q prompt missing %q: %s", tc.in, tc.want, plan.Prompt)
		}
		if !strings.Contains(plan.Prompt, "Watch transcript: "+tc.in) {
			t.Fatalf("prompt missing original transcript: %s", plan.Prompt)
		}
	}
}

func TestSummarizeForWatch(t *testing.T) {
	// Status lead is always present.
	if got := summarizeForWatch("completed", ""); got != "Done." {
		t.Errorf("empty completed body should be just the lead, got %q", got)
	}
	if got := summarizeForWatch("failed", ""); got != "That failed." {
		t.Errorf("failed lead wrong: %q", got)
	}
	// A clean status clause is appended.
	got := summarizeForWatch("completed", "Tests pass on magara.")
	if !strings.HasPrefix(got, "Done.") || !strings.Contains(got, "Tests pass") {
		t.Errorf("should combine lead + clause: %q", got)
	}
	// Code-shaped bodies are refused — only the lead survives.
	codeBody := "const x = foo(); return x;"
	if got := summarizeForWatch("completed", codeBody); got != "Done." {
		t.Errorf("code body must be stripped, got %q", got)
	}
}

func TestSummarizeForWatchClamps(t *testing.T) {
	long := "This is a very long status clause that goes on and on well past any reasonable wrist budget and should be clamped down hard."
	got := summarizeForWatch("completed", long)
	if len([]rune(got)) > watchReadbackMaxChars {
		t.Errorf("summary exceeded watch budget (%d): %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clamped summary should end with ellipsis: %q", got)
	}
}
