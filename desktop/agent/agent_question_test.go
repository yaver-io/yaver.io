package main

// agent_question_test.go — table tests for the in-memory registry.
// HTTP/MCP integration is exercised by the existing /tasks routes
// (covered by tasks_test.go); these tests cover the bits unique to
// the registry: serialization, expiry, cancellation, kind validation.

import (
	"testing"
	"time"
)

func TestQuestionRegistry_HappyPath(t *testing.T) {
	r := &pendingQuestionRegistry{
		byTask: make(map[string]*pendingQuestion),
		byID:   make(map[string]string),
	}

	q, ch, err := r.Register("task-1", AgentQuestion{
		Prompt:     "Pick framework",
		Kind:       "choice",
		Choices:    []string{"react", "vue"},
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if q.ID == "" {
		t.Fatal("Register: empty question ID")
	}
	if q.TaskID != "task-1" {
		t.Fatalf("Register: wrong task id %q", q.TaskID)
	}

	if err := r.Answer(q.ID, "react"); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	select {
	case got := <-ch:
		if got != "react" {
			t.Errorf("answer channel: got %q want %q", got, "react")
		}
	case <-time.After(time.Second):
		t.Fatal("answer never delivered")
	}

	// After answer the registry must drop the entry — a peek now
	// should report no pending question, and a second Answer must
	// 404.
	if _, ok := r.Pending("task-1"); ok {
		t.Error("Pending still reports a question after answer")
	}
	if err := r.Answer(q.ID, "vue"); err != errQuestionNotFound {
		t.Errorf("second Answer: got %v want errQuestionNotFound", err)
	}
}

func TestQuestionRegistry_RejectsConcurrent(t *testing.T) {
	r := &pendingQuestionRegistry{
		byTask: make(map[string]*pendingQuestion),
		byID:   make(map[string]string),
	}
	if _, _, err := r.Register("task-1", AgentQuestion{Prompt: "x", TimeoutSec: 60}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, _, err := r.Register("task-1", AgentQuestion{Prompt: "y", TimeoutSec: 60}); err != errQuestionAlreadyPending {
		t.Errorf("second Register: got %v want errQuestionAlreadyPending", err)
	}
}

func TestQuestionRegistry_Cancel(t *testing.T) {
	r := &pendingQuestionRegistry{
		byTask: make(map[string]*pendingQuestion),
		byID:   make(map[string]string),
	}
	_, ch, err := r.Register("task-c", AgentQuestion{Prompt: "x", TimeoutSec: 60})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	r.CancelTask("task-c")

	select {
	case got := <-ch:
		if !IsCancelledAnswer(got) {
			t.Errorf("expected cancellation sentinel; got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel never resolved the channel")
	}
	if _, ok := r.Pending("task-c"); ok {
		t.Error("Pending still reports a question after cancel")
	}
}

func TestQuestionRegistry_AutoExpire(t *testing.T) {
	r := &pendingQuestionRegistry{
		byTask: make(map[string]*pendingQuestion),
		byID:   make(map[string]string),
	}
	// Register with the smallest accepted TTL. defaultQuestionTimeoutSec
	// is 300; the registry doesn't enforce a *minimum* on TimeoutSec,
	// so any positive value (incl. 1) is honoured. Picking 1 keeps the
	// test fast; if we ever bump a min-TTL guard, switch to the new floor.
	q, ch, err := r.Register("task-e", AgentQuestion{Prompt: "x", TimeoutSec: 1})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	select {
	case got := <-ch:
		if !IsCancelledAnswer(got) {
			t.Errorf("expected cancellation sentinel; got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("auto-expire never fired")
	}
	if _, ok := r.Pending("task-e"); ok {
		t.Error("Pending still reports a question after expire")
	}
	// Late Answer for an expired question must 404, not panic.
	if err := r.Answer(q.ID, "late"); err != errQuestionNotFound {
		t.Errorf("late Answer: got %v want errQuestionNotFound", err)
	}
}

func TestQuestionRegistry_KindValidation(t *testing.T) {
	r := &pendingQuestionRegistry{
		byTask: make(map[string]*pendingQuestion),
		byID:   make(map[string]string),
	}

	cases := []struct {
		name    string
		q       AgentQuestion
		wantErr bool
	}{
		{"text default", AgentQuestion{Prompt: "x"}, false},
		{"text explicit", AgentQuestion{Prompt: "x", Kind: "text"}, false},
		{"secret", AgentQuestion{Prompt: "x", Kind: "secret"}, false},
		{"choice with options", AgentQuestion{Prompt: "x", Kind: "choice", Choices: []string{"a"}}, false},
		{"choice without options", AgentQuestion{Prompt: "x", Kind: "choice"}, true},
		{"unknown kind", AgentQuestion{Prompt: "x", Kind: "blob"}, true},
		{"empty prompt", AgentQuestion{Prompt: ""}, true},
	}

	for i, tc := range cases {
		taskID := "kindcheck-" + tc.name
		_, _, err := r.Register(taskID, tc.q)
		gotErr := err != nil
		if gotErr != tc.wantErr {
			t.Errorf("case %d %q: gotErr=%v wantErr=%v err=%v", i, tc.name, gotErr, tc.wantErr, err)
		}
		// Cancel so the goroutine + map entry don't leak between cases.
		r.CancelTask(taskID)
	}
}
