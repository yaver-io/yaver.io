package main

import (
	"os"
	"strings"
	"testing"
)

// Every first-class runner must supply assistant prose through a semantic
// boundary. Terminal evidence can still be retained, but it cannot become the
// visible chat answer merely because it arrived on a process pipe.
func TestClaudeStreamJSONSeparatesAssistantReplyFromToolEvidence(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	task := &Task{
		ID: "claude-semantic", RunnerID: "claude",
		runner:   GetRunnerConfig("claude"),
		outputCh: make(chan string, 32), rawOutputCh: make(chan taskRawFrame, 16),
		eventCh: make(chan map[string]interface{}, 32), doneCh: make(chan struct{}),
	}
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"I changed the background and verified it."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"command\":\"npm test\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`,
		`{"type":"user","tool_use_result":{"stdout":"PASS\n","stderr":"","interrupted":false}}`,
		`{"type":"result","result":"I changed the background and verified it.","total_cost_usd":0.01}`,
	}, "\n") + "\n"

	tm.readStreamJSON(task, strings.NewReader(stream))
	if task.ResultText != "I changed the background and verified it." {
		t.Fatalf("ResultText = %q", task.ResultText)
	}
	var assistant []string
	for _, message := range task.Presentation {
		if message.Kind == "message" && message.Role == "assistant" {
			assistant = append(assistant, message.Text)
		}
	}
	if len(assistant) != 1 || assistant[0] != task.ResultText {
		t.Fatalf("assistant presentation = %#v", assistant)
	}
	var activity string
	for _, message := range task.Presentation {
		if message.ID == task.ID+"-activity" {
			activity = message.Text
		}
	}
	if activity != "Running tests." {
		t.Fatalf("human activity = %q, want a plain-language test update", activity)
	}
	if strings.Contains(assistant[0], "npm test") || strings.Contains(assistant[0], "PASS") {
		t.Fatalf("tool evidence leaked into assistant presentation: %q", assistant[0])
	}
}

func TestCodexLastMessageSeparatesAssistantReplyFromConsoleEvidence(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	lastMessage := t.TempDir() + "/last-message.txt"
	if err := os.WriteFile(lastMessage, []byte("I changed the background and verified it.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := &Task{
		ID: "codex-semantic", RunnerID: "codex", codexLastMsgPath: lastMessage,
		runner:   GetRunnerConfig("codex"),
		outputCh: make(chan string, 32), rawOutputCh: make(chan taskRawFrame, 16),
		eventCh: make(chan map[string]interface{}, 32), doneCh: make(chan struct{}),
	}

	tm.readRawOutput(task,
		strings.NewReader("exec\nnpm test\n succeeded in 1s:\nPASS\n"),
		strings.NewReader("OpenAI Codex v0.147.0\nmodel: gpt-5.6-sol\n"),
	)
	if task.ResultText != "I changed the background and verified it." {
		t.Fatalf("ResultText = %q", task.ResultText)
	}
	if len(task.Presentation) != 1 || task.Presentation[0].Text != task.ResultText {
		t.Fatalf("assistant presentation = %#v", task.Presentation)
	}
	for _, evidence := range []string{"npm test", "PASS", "OpenAI Codex"} {
		if !strings.Contains(task.RawOutput, evidence) {
			t.Errorf("raw evidence lost %q: %q", evidence, task.RawOutput)
		}
		if strings.Contains(task.Presentation[0].Text, evidence) {
			t.Errorf("console evidence %q leaked into assistant presentation", evidence)
		}
	}
	if _, err := os.Stat(lastMessage); !os.IsNotExist(err) {
		t.Fatalf("last-message scratch file was not removed: %v", err)
	}
}
