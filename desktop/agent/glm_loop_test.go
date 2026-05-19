package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Real HTTP server on a random port, no mocks / external deps (repo
// test convention). The fake provider speaks the OpenAI-compatible
// chat-completions shape the owned loop expects.

func collectEvents(ch chan map[string]interface{}) []map[string]interface{} {
	var evs []map[string]interface{}
	for {
		select {
		case e := <-ch:
			evs = append(evs, e)
		default:
			return evs
		}
	}
}

func glmServer(t *testing.T, replies []string) *httptest.Server {
	t.Helper()
	var n int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(replies) {
			i = len(replies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(replies[i]))
	}))
}

func toolCallReply(id, command string) string {
	args, _ := json.Marshal(map[string]string{"command": command})
	body, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{{
			"message": map[string]interface{}{
				"role": "assistant",
				"tool_calls": []map[string]interface{}{{
					"id": id, "type": "function",
					"function": map[string]interface{}{
						"name": "bash", "arguments": string(args),
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	})
	return string(body)
}

func finalReply(text string) string {
	body, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{{
			"message":       map[string]interface{}{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
	})
	return string(body)
}

func TestGLMLoop_RunsCommandAndEmitsNativeEvents(t *testing.T) {
	srv := glmServer(t, []string{
		toolCallReply("call_1", "echo hello-from-glm"),
		finalReply("done — printed the greeting"),
	})
	defer srv.Close()

	task := &Task{ID: "t-glm", eventCh: make(chan map[string]interface{}, 16)}
	out, err := RunGLMLoop(context.Background(), GLMLoopConfig{
		BaseURL: srv.URL, Model: "z-ai/glm-4.6",
	}, task, t.TempDir(), "say hello")
	if err != nil {
		t.Fatalf("RunGLMLoop: %v", err)
	}
	if !strings.Contains(out, "printed the greeting") {
		t.Fatalf("unexpected final text: %q", out)
	}

	evs := collectEvents(task.eventCh)
	var start, end map[string]interface{}
	var stdoutChunk string
	for _, e := range evs {
		switch e["type"] {
		case "command_start":
			start = e
		case "command_output":
			if e["stream"] == "stdout" {
				stdoutChunk = e["chunk"].(string)
			}
		case "command_end":
			end = e
		}
	}
	if start == nil || end == nil {
		t.Fatalf("missing command_start/command_end; got %d events", len(evs))
	}
	if start["command"] != "echo hello-from-glm" || start["runner"] != "glm" {
		t.Fatalf("bad command_start: %#v", start)
	}
	if !strings.Contains(stdoutChunk, "hello-from-glm") {
		t.Fatalf("stdout not captured: %q", stdoutChunk)
	}
	// Native fidelity: real exit code present (exitKnown=true) and 0.
	if ec, ok := end["exitCode"]; !ok || ec.(int) != 0 {
		t.Fatalf("expected exitCode 0 present (native), got %#v", end)
	}
}

func TestGLMLoop_RealNonZeroExitCode(t *testing.T) {
	srv := glmServer(t, []string{
		toolCallReply("c1", "exit 3"),
		finalReply("the command failed with exit 3"),
	})
	defer srv.Close()

	task := &Task{ID: "t2", eventCh: make(chan map[string]interface{}, 16)}
	if _, err := RunGLMLoop(context.Background(), GLMLoopConfig{
		BaseURL: srv.URL, Model: "m",
	}, task, t.TempDir(), "fail please"); err != nil {
		t.Fatalf("RunGLMLoop: %v", err)
	}
	for _, e := range collectEvents(task.eventCh) {
		if e["type"] == "command_end" {
			if e["exitCode"] != 3 {
				t.Fatalf("expected real exitCode 3, got %#v", e)
			}
			return
		}
	}
	t.Fatal("no command_end emitted")
}

func TestGLMLoop_ToleratesMalformedToolArgs(t *testing.T) {
	// First reply: a tool call with non-JSON arguments. The loop must
	// not crash — it feeds an error back and the model then answers.
	badToolCall := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"bash","arguments":"{not json"}}]},"finish_reason":"tool_calls"}]}`
	srv := glmServer(t, []string{badToolCall, finalReply("recovered")})
	defer srv.Close()

	task := &Task{ID: "t3", eventCh: make(chan map[string]interface{}, 8)}
	out, err := RunGLMLoop(context.Background(), GLMLoopConfig{
		BaseURL: srv.URL, Model: "m",
	}, task, t.TempDir(), "do a thing")
	if err != nil {
		t.Fatalf("RunGLMLoop should tolerate bad args, got: %v", err)
	}
	if out != "recovered" {
		t.Fatalf("expected recovery, got %q", out)
	}
	// No command_* events for a rejected tool call.
	for _, e := range collectEvents(task.eventCh) {
		if e["type"] == "command_start" {
			t.Fatalf("malformed args must not start a command: %#v", e)
		}
	}
}
