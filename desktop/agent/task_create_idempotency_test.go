package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskCreateIdempotencyCoalescesTransportRetries(t *testing.T) {
	s := &HTTPServer{}
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	create := func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		jsonReply(w, http.StatusCreated, map[string]any{"ok": true, "taskId": "one-task"})
	}

	key := "same-logical-task"
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/tasks", nil),
		httptest.NewRequest(http.MethodPost, "/tasks", nil),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.serveIdempotentTaskCreateWith(recorders[0], requests[0], key, create)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first create did not start")
	}
	go func() {
		defer wg.Done()
		s.serveIdempotentTaskCreateWith(recorders[1], requests[1], key, create)
	}()
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("underlying task creates = %d, want 1", got)
	}
	for i, recorder := range recorders {
		if recorder.Code != http.StatusCreated || recorder.Body.String() != recorders[0].Body.String() {
			t.Fatalf("response %d = status %d body %q; first = status %d body %q", i, recorder.Code, recorder.Body.String(), recorders[0].Code, recorders[0].Body.String())
		}
	}
	if got := recorders[1].Header().Get("X-Yaver-Idempotent-Replay"); got != "true" {
		t.Fatalf("replayed response header = %q, want true", got)
	}
}

func TestRemoteTaskIdempotencyUsesOneKeyAcrossCandidateTransports(t *testing.T) {
	candidates := []RemoteAgentCandidate{{BaseURL: "http://lan"}, {BaseURL: "https://relay", Headers: map[string]string{"X-Relay-Password": "test"}}}
	applyRemoteTaskIdempotency(candidates, "logical-task")
	for i, candidate := range candidates {
		if got := candidate.Headers["Idempotency-Key"]; got != "logical-task" {
			t.Fatalf("candidate %d key = %q", i, got)
		}
	}
	if candidates[1].Headers["X-Relay-Password"] != "test" {
		t.Fatal("existing transport headers were overwritten")
	}
}
