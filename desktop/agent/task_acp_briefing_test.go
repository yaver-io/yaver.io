package main

import (
	"encoding/json"
	"testing"
)

func TestACPToolActivityLabelNamesUsefulWork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		input string
		want  string
	}{
		{name: "adapter title wins", title: "Inspecting theme tokens", input: `{"command":"rg theme"}`, want: "Inspecting theme tokens"},
		{name: "shell title never reaches primary UI", title: "sed -n '8700,8735p' tasks.tsx; GOCACHE=/tmp go test ./...", input: `{"command":"sed -n '8700,8735p' tasks.tsx; GOCACHE=/tmp go test ./..."}`, want: "Running verification"},
		{name: "git", input: `{"command":"git status --short"}`, want: "Checking Git state"},
		{name: "search", input: `{"command":"rg -n background src"}`, want: "Searching the project"},
		{name: "test", input: `{"command":"npm test -- colors"}`, want: "Running verification"},
		{name: "missing adapter detail", input: `{}`, want: "Checking the next step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage = []byte(tc.input)
			if got := acpToolActivityLabel(tc.title, raw); got != tc.want {
				t.Fatalf("activity label = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJoinACPAssistantChunkPreservesTokensAndSeparatesSentences(t *testing.T) {
	if got := joinACPAssistantChunk("config", "uration"); got != "uration" {
		t.Fatalf("word continuation = %q, want unchanged", got)
	}
	if got := joinACPAssistantChunk("Checked the theme.", "The change is ready."); got != "\n\nThe change is ready." {
		t.Fatalf("sentence continuation = %q, want paragraph boundary", got)
	}
}
