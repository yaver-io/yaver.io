package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRunnerTurn_ErrorCodeMapping(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:          "bad_payload",
		http.StatusNotFound:            "not_found",
		http.StatusConflict:            "awaiting",
		http.StatusInternalServerError: "internal",
	}
	for status, want := range cases {
		if got := runnerTurnErrorCode(status); got != want {
			t.Errorf("status %d: got %q want %q", status, got, want)
		}
	}
}

func TestRunnerTurn_IsVoiceSurface(t *testing.T) {
	for _, s := range []string{"voice", "CAR", " watch ", "glass", "wear"} {
		if !isVoiceSurface(s) {
			t.Errorf("%q should be eyes-free", s)
		}
	}
	for _, s := range []string{"", "screen", "tv", "mcp"} {
		if isVoiceSurface(s) {
			t.Errorf("%q should NOT be eyes-free", s)
		}
	}
}

func TestRunnerTurn_SummaryNeverSpeaksCode(t *testing.T) {
	// A pane whose top line is code must never be read aloud verbatim.
	reply := runnerSessionTurnResponse{OK: true, Sent: "prompt", Pane: "func main() {\n  doThing()\n}"}
	spoken := summarizeRunnerTurnForSpeech(reply)
	if strings.Contains(spoken, "func") || strings.Contains(spoken, "{") {
		t.Fatalf("SECURITY: spoken summary leaked code: %q", spoken)
	}
	if !strings.HasPrefix(spoken, "Working.") {
		t.Fatalf("expected a working lead, got %q", spoken)
	}
}

func TestRunnerTurn_SummaryAwaitingChoiceReadsOptions(t *testing.T) {
	reply := runnerSessionTurnResponse{AwaitingChoice: true, Options: []string{"1. Yes", "2. No"}}
	spoken := summarizeRunnerTurnForSpeech(reply)
	if !strings.Contains(spoken, "waiting for a choice") {
		t.Fatalf("awaiting-choice summary missing prompt: %q", spoken)
	}
	if !strings.Contains(spoken, "Yes") || !strings.Contains(spoken, "No") {
		t.Fatalf("options not read back: %q", spoken)
	}
}

func TestRunnerTurn_SummaryCleanStatusLine(t *testing.T) {
	reply := runnerSessionTurnResponse{OK: true, Sent: "prompt", Pane: "Ran the tests, all passing."}
	spoken := summarizeRunnerTurnForSpeech(reply)
	if !strings.Contains(spoken, "Ran the tests") {
		t.Fatalf("clean status line dropped: %q", spoken)
	}
}

// TestRunnerTurn_HandlerValidation exercises the payload guards that return
// before any tmux interaction, so it needs no live session.
func TestRunnerTurn_HandlerValidation(t *testing.T) {
	call := func(payload string) OpsResult {
		return opsRunnerTurnHandler(OpsContext{}, json.RawMessage(payload))
	}

	// Neither text nor choice.
	if r := call(`{}`); r.OK || r.Code != "bad_payload" {
		t.Fatalf("empty turn should be bad_payload, got ok=%v code=%q", r.OK, r.Code)
	}
	// Both text and choice.
	if r := call(`{"text":"hi","choice":"1"}`); r.OK || r.Code != "bad_payload" {
		t.Fatalf("both text+choice should be bad_payload, got ok=%v code=%q", r.OK, r.Code)
	}
	// Non-numeric choice.
	if r := call(`{"choice":"yes"}`); r.OK || r.Code != "bad_payload" {
		t.Fatalf("non-numeric choice should be bad_payload, got ok=%v code=%q", r.OK, r.Code)
	}
	// Malformed JSON.
	if r := call(`{not json`); r.OK || r.Code != "bad_payload" {
		t.Fatalf("bad json should be bad_payload, got ok=%v code=%q", r.OK, r.Code)
	}
}

// TestRunnerTurn_VerbsRegistered confirms both verbs self-registered in init().
func TestRunnerTurn_VerbsRegistered(t *testing.T) {
	for _, name := range []string{"runner_turn", "runner_sessions"} {
		opsRegistryMu.RLock()
		_, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Fatalf("verb %q not registered", name)
		}
	}
}
