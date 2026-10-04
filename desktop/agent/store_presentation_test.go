package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskStorePersistsSemanticPresentation(t *testing.T) {
	store := &TaskStore{path: filepath.Join(t.TempDir(), "tasks.json")}
	now := time.Now()
	task := &Task{
		ID: "semantic-roundtrip", Status: TaskStatusReady, RunnerID: "codex",
		ResultText: "I fixed the connection and verified it.", CreatedAt: now,
		PresentationSeq: 7,
		Presentation: []TaskPresentationMessage{{
			ID: "answer", Kind: "message", Role: "assistant",
			Text: "I fixed the connection and verified it.", Phase: "complete", State: "completed",
			CreatedAt: now, UpdatedAt: now,
		}},
	}
	store.Save(map[string]*Task{task.ID: task})

	loaded := store.Load()[task.ID]
	if loaded == nil || len(loaded.Presentation) != 1 {
		t.Fatalf("semantic presentation was lost after restart: %#v", loaded)
	}
	if loaded.Presentation[0].Text != task.ResultText || loaded.PresentationSeq != 7 {
		t.Fatalf("semantic presentation changed after restart: %#v seq=%d", loaded.Presentation, loaded.PresentationSeq)
	}
}

func TestTaskStoreRestoresCleanLegacyAssistantButNeverRawDetails(t *testing.T) {
	dir := t.TempDir()
	store := &TaskStore{path: filepath.Join(dir, "tasks.json")}
	now := time.Now()
	records := []persistedTask{
		{
			ID: "legacy-clean", Status: TaskStatusReady, RunnerID: "codex",
			ResultText: "Fixed the handoff and verified the tests.", CreatedAt: now,
			Turns: []ConversationTurn{{Role: "assistant", Content: "Fixed the handoff and verified the tests.", Timestamp: now}},
		},
		{
			ID: "legacy-raw", Status: TaskStatusReady, RunnerID: "custom",
			ResultText: "$ npm test\nPASS", CreatedAt: now,
			Turns: []ConversationTurn{{Role: "assistant", Content: "$ npm test\nPASS", Timestamp: now, Hidden: true}},
		},
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded := store.Load()
	if got := loaded["legacy-clean"].Presentation; len(got) != 1 || got[0].Text != records[0].ResultText {
		t.Fatalf("clean legacy answer was not restored: %#v", got)
	}
	if got := loaded["legacy-raw"].Presentation; len(got) != 0 {
		t.Fatalf("raw terminal details were promoted into chat: %#v", got)
	}
}
