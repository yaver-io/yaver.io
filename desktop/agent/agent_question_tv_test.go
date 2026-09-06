package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTVQuestionTestServer(t *testing.T, question AgentQuestion) (*HTTPServer, AgentQuestion, <-chan string) {
	t.Helper()
	taskID := "tv-question-task"
	globalQuestionRegistry.CancelTask(taskID)
	t.Cleanup(func() { globalQuestionRegistry.CancelTask(taskID) })

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.mu.Lock()
	tm.tasks[taskID] = &Task{ID: taskID, Status: TaskStatusRunning, eventCh: make(chan map[string]interface{}, 4)}
	tm.mu.Unlock()

	registered, answerCh, err := globalQuestionRegistry.Register(taskID, question)
	if err != nil {
		t.Fatalf("register question: %v", err)
	}
	return &HTTPServer{taskMgr: tm}, registered, answerCh
}

func TestTVScopedTaskCanAnswerOrdinaryQuestion(t *testing.T) {
	srv, question, answerCh := newTVQuestionTestServer(t, AgentQuestion{Prompt: "Which approach?", Kind: "choice", Choices: []string{"Safe", "Fast"}})
	body := `{"questionId":"` + question.ID + `","answer":"Safe"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks/tv-question-task/answer", strings.NewReader(body))
	req.Header.Set("X-Yaver-SessionScope", "tv")
	w := httptest.NewRecorder()

	srv.handleTaskAnswer(w, req, "tv-question-task")

	if w.Code != http.StatusOK {
		t.Fatalf("TV task answer returned %d: %s", w.Code, w.Body.String())
	}
	if got := <-answerCh; got != "Safe" {
		t.Fatalf("runner received %q, want Safe", got)
	}
}

func TestTVScopedTaskCannotAnswerSecretQuestion(t *testing.T) {
	srv, question, _ := newTVQuestionTestServer(t, AgentQuestion{Prompt: "Password?", Kind: "secret", VaultHint: "service.password"})
	body := `{"questionId":"` + question.ID + `","answer":"must-not-cross"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks/tv-question-task/answer", strings.NewReader(body))
	req.Header.Set("X-Yaver-SessionScope", "tv")
	w := httptest.NewRecorder()

	srv.handleTaskAnswer(w, req, "tv-question-task")

	if w.Code != http.StatusForbidden {
		t.Fatalf("secret TV answer returned %d, want 403: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ReasonAuthSessionScopeDenied) {
		t.Fatalf("secret TV refusal lost stable scope code: %s", w.Body.String())
	}
	if _, ok := globalQuestionRegistry.Pending("tv-question-task"); !ok {
		t.Fatal("secret question was consumed even though the TV answer was refused")
	}
}

func TestPeekTaskQuestionTreatsNoPendingQuestionAsEmptyState(t *testing.T) {
	taskID := "question-empty-state"
	globalQuestionRegistry.CancelTask(taskID)
	t.Cleanup(func() { globalQuestionRegistry.CancelTask(taskID) })

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.mu.Lock()
	tm.tasks[taskID] = &Task{ID: taskID, Status: TaskStatusReady, eventCh: make(chan map[string]interface{}, 1)}
	tm.mu.Unlock()
	srv := &HTTPServer{taskMgr: tm}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/question", nil)

	srv.handleTaskQuestion(w, req, taskID)

	if w.Code != http.StatusOK {
		t.Fatalf("empty question state returned %d: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"ok":true,"question":null}` {
		t.Fatalf("empty question state body = %s", got)
	}
}
