package main

import (
	"strings"
	"testing"
)

func TestDetectAskIntent(t *testing.T) {
	asks := []string{
		"how do I test STT/TTS",
		"how would I test stt tts etc",
		"where does auth get wired?",
		"why does the relay fall back to QUIC?",
		"what is the privacy contract for Convex",
		"can I run the voice test without a phone?",
		"explain how the Hermes push path works",
		"is there a way to deploy locally instead of CI?",
		"tell me where the beacon discovery lives",
	}
	for _, q := range asks {
		if !detectAskIntent(q) {
			t.Errorf("expected ASK for %q, got work", q)
		}
	}

	work := []string{
		"add a dark-mode toggle to the settings screen", // imperative work
		"refactor the auth module",                      // imperative work
		"fix the failing test in tasks_test.go",         // imperative work
		"run cloud_deploy now",                          // names a yaver verb
		"git_push the release branch",                   // names a verb
		"/tasks",                                        // slash command
		"$ ls -la",                                      // shell sigil
		"serve",                                         // single command token
		"yaver ask how do I test stt",                   // explicit yaver invocation
		"",                                              // empty
	}
	for _, w := range work {
		if detectAskIntent(w) {
			t.Errorf("expected WORK for %q, got ask", w)
		}
	}
}

func TestDetectAskBreadth(t *testing.T) {
	broad := []string{
		"how does auth work end to end?",
		"explain the overall architecture of the relay",
		"what happens when a device heartbeat fails and reconnects across subsystems",
		"trace the Hermes push pipeline from CLI to device",
		"how do the beacon, QUIC and relay layers interact",
	}
	for _, q := range broad {
		if !detectAskBreadth(q) {
			t.Errorf("expected BROAD for %q", q)
		}
	}
	narrow := []string{
		"where is the vault key derived?",
		"what port does the relay use",
		"how do I test STT/TTS",
	}
	for _, q := range narrow {
		if detectAskBreadth(q) {
			t.Errorf("expected NARROW for %q", q)
		}
	}
}

func TestAskGraphTemplate(t *testing.T) {
	nodes := buildAgentGraphTemplate(AgentGraphCreateRequest{
		WorkDir:  "/tmp/repo",
		Prompt:   "how does auth work end to end?",
		Template: "ask",
	})
	if len(nodes) != 3 {
		t.Fatalf("ask template should have 3 nodes, got %d", len(nodes))
	}
	wantIDs := []string{"investigate", "answer", "verify"}
	for i, n := range nodes {
		if n.ID != wantIDs[i] {
			t.Errorf("node %d: want id %q, got %q", i, wantIDs[i], n.ID)
		}
		if !n.AskMode {
			t.Errorf("node %q should be AskMode (read-only grounded)", n.ID)
		}
	}
	if len(nodes[1].DependsOn) == 0 || nodes[1].DependsOn[0] != "investigate" {
		t.Error("answer must depend on investigate")
	}
	if len(nodes[2].DependsOn) == 0 || nodes[2].DependsOn[0] != "answer" {
		t.Error("verify must depend on answer")
	}
}

func TestIsConsoleAskSource(t *testing.T) {
	for _, s := range []string{"cli", "console", "terminal-local", "terminal-remote", "connect", "attach", "mobile-code"} {
		if !isConsoleAskSource(s) {
			t.Errorf("expected %q to be a console ask source", s)
		}
	}
	for _, s := range []string{"mobile", "mcp", "feedback", "vibing", ""} {
		if isConsoleAskSource(s) {
			t.Errorf("expected %q NOT to be a console ask source", s)
		}
	}
}

func TestAskModePreambleContract(t *testing.T) {
	p := askModePreamble()
	for _, must := range []string{"ask mode", "file:line", "yaver_ask_user", "ESCALATE", "BEFORE"} {
		if !strings.Contains(p, must) {
			t.Errorf("askModePreamble missing %q", must)
		}
	}
	// Ask mode must NOT carry the no-questions "never ask, just act" stance.
	if strings.Contains(p, "Do not stop the run to ask") {
		t.Error("askModePreamble should not include the no-questions decision policy")
	}
}
