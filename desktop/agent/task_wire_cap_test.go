package main

import (
	"strings"
	"testing"
	"time"
)

// GET /tasks/{id} must stay small enough to poll through a relay. On
// 2026-07-27 one long runner turn pushed the detail body to 3.5MB and the web
// UI re-downloaded it every 2 seconds — the browser-side freeze the user read
// as "stuck". Output was capped; ResultText and Turns were not. If someone
// removes capTaskTranscript from taskInfoFromTask or widens the caps into
// megabytes, this fails.
func TestCapTaskTranscriptBoundsWirePayload(t *testing.T) {
	big := strings.Repeat("x", 3*1024*1024)
	turns := make([]ConversationTurn, 60)
	for i := range turns {
		turns[i] = ConversationTurn{Role: "assistant", Content: big, Timestamp: time.Now()}
	}
	info := TaskInfo{ResultText: big, Turns: turns}
	capTaskTranscript(&info)

	if !info.TranscriptTruncated {
		t.Fatal("transcriptTruncated flag not set — surfaces can't tell tail from whole")
	}
	if len(info.ResultText) != taskWireResultTextCap {
		t.Fatalf("resultText not tail-capped: %d bytes", len(info.ResultText))
	}
	if len(info.Turns) != taskWireMaxTurns {
		t.Fatalf("turns not capped: %d", len(info.Turns))
	}
	total := len(info.ResultText)
	for _, turn := range info.Turns {
		total += len(turn.Content)
	}
	if total > 1024*1024 {
		t.Fatalf("capped task detail still %d bytes — relay polling budget blown", total)
	}
}

// The cap must trim a COPY. info.Turns aliases the task manager's live slice;
// trimming in place would corrupt the stored transcript on the first poll.
func TestCapTaskTranscriptDoesNotMutateSource(t *testing.T) {
	big := strings.Repeat("y", taskWireTurnContentCap+10)
	source := []ConversationTurn{{Role: "assistant", Content: big}}
	info := TaskInfo{Turns: source}
	capTaskTranscript(&info)
	if len(source[0].Content) != len(big) {
		t.Fatal("capTaskTranscript mutated the task's stored turns")
	}
}

func TestCapTaskTranscriptNoopOnSmallTasks(t *testing.T) {
	info := TaskInfo{ResultText: "done", Turns: []ConversationTurn{{Role: "user", Content: "hi"}}}
	capTaskTranscript(&info)
	if info.TranscriptTruncated {
		t.Fatal("small task flagged as truncated")
	}
	if info.ResultText != "done" || info.Turns[0].Content != "hi" {
		t.Fatal("small task content altered")
	}
}

func TestTaskPresentationBoundsPersistedAndListPayloads(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{ID: "presentation-bounds", RunnerID: "codex", ProjectName: "yaver"}
	for i := 0; i < 10; i++ {
		tm.present(task, taskPresentationInput{
			ID: "answer-" + string(rune('a'+i)), Kind: "message", Role: "assistant",
			Text: strings.Repeat(string(rune('a'+i)), 10*1024),
		})
	}
	if got := presentationTextBytes(task.Presentation); got > maxTaskPresentationTotal {
		t.Fatalf("persisted presentation is %d bytes, want <= %d", got, maxTaskPresentationTotal)
	}
	list := taskPresentationListSnapshot(task)
	if len(list) > 2 {
		t.Fatalf("list snapshot has %d rows, want at most status + answer", len(list))
	}
	for _, message := range list {
		if len(message.Text) > maxTaskPresentationListText+len("…") {
			t.Fatalf("list message is %d bytes, cap %d", len(message.Text), maxTaskPresentationListText)
		}
	}
}

func TestTaskPresentationSnapshotUsesAuthoritativeTerminalState(t *testing.T) {
	task := &Task{ID: "terminal", RunnerName: "Codex", ProjectName: "yaver", Status: TaskStatusRunning}
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.present(task, taskRunningPresentation(task))
	task.Status = TaskStatusFinished
	rows := taskPresentationSnapshot(task)
	if len(rows) != 1 || rows[0].Text != "Completed on yaver." || rows[0].State != "completed" {
		t.Fatalf("stale activity survived completion: %+v", rows)
	}
}

func TestTaskRunningPresentationNamesModelInsteadOfRunnerBrand(t *testing.T) {
	task := &Task{
		ID: "model-status", RunnerID: "codex", RunnerName: "Codex",
		Model: "gpt-5.6-sol", ReasoningEffort: "high", ProjectName: "yaver",
	}
	got := taskRunningPresentation(task)
	if got.Text != "gpt-5.6-sol · high is working on yaver." {
		t.Fatalf("running presentation = %q", got.Text)
	}
}

func TestTaskAcknowledgementIsImmediateAndFirstStreamChunkReplacesIt(t *testing.T) {
	task := &Task{
		ID: "ack", RunnerID: "codex", LastSurface: "yaver-mobile-app",
		Turns: []ConversationTurn{{Role: "user", Content: "Fix it"}},
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)

	tm.present(task, taskAcceptedPresentation(task))
	if len(task.Presentation) != 1 || task.Presentation[0].State != "acknowledged" {
		t.Fatalf("accepted task has no immediate acknowledgement: %#v", task.Presentation)
	}
	wantID := taskAssistantPresentationID(task)
	if task.Presentation[0].ID != wantID || !strings.Contains(task.Presentation[0].Text, "I’m on it") {
		t.Fatalf("acknowledgement = %#v, want assistant slot %q", task.Presentation[0], wantID)
	}

	first := tm.presentLocked(task, taskPresentationInput{
		ID: wantID, Kind: "message", Role: "assistant", Text: "I found the issue.",
		Phase: "responding", State: "streaming", Append: true,
	})
	if first.Op != "upsert" || len(task.Presentation) != 1 || task.Presentation[0].Text != "I found the issue." {
		t.Fatalf("first streamed update did not replace acknowledgement: event=%#v rows=%#v", first, task.Presentation)
	}
	second := tm.presentLocked(task, taskPresentationInput{
		ID: wantID, Kind: "message", Role: "assistant", Text: " Testing now.",
		Phase: "responding", State: "streaming", Append: true,
	})
	if second.Op != "append" || task.Presentation[0].Text != "I found the issue. Testing now." {
		t.Fatalf("later streamed update did not append: event=%#v rows=%#v", second, task.Presentation)
	}
}

func TestTaskSemanticAssistantEvidenceRejectsRawCompatibilityText(t *testing.T) {
	task := &Task{Presentation: []TaskPresentationMessage{
		{ID: "state", Kind: "status", Text: "The runner is working."},
		{ID: "answer", Kind: "message", Role: "assistant", Text: "I changed the background and verified it."},
	}}
	if !taskHasSemanticAssistantTextLocked(task, "I changed the background and verified it.") {
		t.Fatal("semantic assistant text was not recognized")
	}
	for _, raw := range []string{
		"$ npm test\nPASS\n@@ -1 +1 @@",
		"\x1b[32mcompiling\x1b[0m\ndiff --git a/app.tsx b/app.tsx",
	} {
		if taskHasSemanticAssistantTextLocked(task, raw) {
			t.Fatalf("raw compatibility text was accepted as semantic assistant output: %q", raw)
		}
	}
}

func TestTaskInfoFriendlyPresentationPrefersNewestAssistantMessage(t *testing.T) {
	info := TaskInfo{Presentation: []TaskPresentationMessage{
		{ID: "status", Kind: "status", Text: "Working on yaver."},
		{ID: "answer-1", Kind: "message", Role: "assistant", Text: "First answer."},
		{ID: "warning", Kind: "warning", Text: "Raw warning must not replace the answer."},
		{ID: "answer-2", Kind: "message", Role: "assistant", Text: "Finished cleanly."},
	}}
	got := taskInfoFriendlyPresentation(&info)
	if got == nil || got.ID != "answer-2" || got.Text != "Finished cleanly." {
		t.Fatalf("friendly presentation = %+v, want newest assistant answer", got)
	}
}

func TestTaskInfoFriendlyPresentationFallsBackToActionableState(t *testing.T) {
	info := TaskInfo{Presentation: []TaskPresentationMessage{
		{ID: "tool", Kind: "tool", Text: "go test ./..."},
		{ID: "attention", Kind: "action_required", Text: "Choose a runner sign-in method."},
	}}
	got := taskInfoFriendlyPresentation(&info)
	if got == nil || got.ID != "attention" {
		t.Fatalf("friendly presentation = %+v, want actionable status", got)
	}
}
