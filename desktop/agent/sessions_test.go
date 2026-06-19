package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveSessionFileUsesEffectiveWorkDirAndPersistsResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	finishedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	task := &Task{
		ID:          "task1234",
		Status:      TaskStatusFinished,
		ResultText:  "actual runner result",
		FinishedAt:  &finishedAt,
		CreatedAt:   finishedAt,
		Description: "do the thing",
		Turns: []ConversationTurn{
			{Role: "user", Content: "do the thing", Timestamp: finishedAt},
		},
	}

	saveSessionFile(task, "OpenAI Codex", "/opt/talos")

	path := filepath.Join(home, ".yaver", "sessions", "2026-06-19_120000_task1234.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "- **Directory**: /opt/talos") {
		t.Fatalf("expected task workDir in session, got:\n%s", content)
	}
	if !strings.Contains(content, "## Result\n\nactual runner result") {
		t.Fatalf("expected result section in session, got:\n%s", content)
	}
}

func TestTaskManagerEffectiveTaskWorkDirPrefersTaskWorkDir(t *testing.T) {
	tm := &TaskManager{workDir: "/root"}
	task := &Task{WorkDir: "  /opt/talos  "}

	if got := tm.effectiveTaskWorkDir(task); got != "/opt/talos" {
		t.Fatalf("expected trimmed task workDir, got %q", got)
	}
	if got := tm.effectiveTaskWorkDir(&Task{}); got != "/root" {
		t.Fatalf("expected manager fallback workDir, got %q", got)
	}
}
