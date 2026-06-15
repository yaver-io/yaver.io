package main

// agent_question_http.go — HTTP surface for the agent_question
// subsystem (see agent_question.go for the data layer):
//
//   POST /tasks/{id}/question — called by the stdio MCP child that's
//     wrapping `yaver_ask_user`. Body: {prompt, kind?, choices?,
//     vault_hint?, timeoutSec?}. The handler registers the question,
//     emits an `agent_question` SSE event on the task, then PARKS the
//     HTTP request until either an answer arrives or the question is
//     cancelled. Response body is {answer:"..."} or {cancelled:true}.
//
//   GET /tasks/{id}/question — late-joining UI fetches the currently-
//     pending question for this task, if any. Returns 404 when none.
//
//   POST /tasks/{id}/answer — mobile/web/CLI delivers the human's
//     answer. Body: {questionId, answer}. Resolves the parked
//     /question request and emits an `agent_answered` SSE event so
//     other subscribers (e.g. the desktop and the phone simultaneously)
//     close their open question sheet.
//
// Auth: all three routes inherit the standard owner / paired-token
// path via s.auth() at the route registration (handleTaskByID is wired
// through s.auth in httpserver.go). SDK and feedback-only guests are
// blocked the same way they are blocked from /tasks/{id}/continue.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// handleTaskQuestion serves both POST (register-and-park) and GET
// (peek the pending question). The split here keeps the route surface
// the same shape as /tasks/{id}/output, /stop, /continue, etc.
func (s *HTTPServer) handleTaskQuestion(w http.ResponseWriter, r *http.Request, taskID string) {
	switch r.Method {
	case http.MethodGet:
		s.peekTaskQuestion(w, r, taskID)
	case http.MethodPost:
		s.registerTaskQuestion(w, r, taskID)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET to peek, POST to register")
	}
}

// peekTaskQuestion lets a UI that just opened the task page fetch the
// currently-pending question without re-subscribing to SSE. Returns 404
// when no question is in flight — the UI then waits for a future SSE
// `agent_question` event.
func (s *HTTPServer) peekTaskQuestion(w http.ResponseWriter, _ *http.Request, taskID string) {
	if _, ok := s.taskMgr.GetTask(taskID); !ok {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	q, ok := globalQuestionRegistry.Pending(taskID)
	if !ok {
		jsonError(w, http.StatusNotFound, "no pending question")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "question": q})
}

// registerTaskQuestion is the long-poll endpoint: the stdio MCP child
// POSTs here on behalf of the runner's `yaver_ask_user` call, and we
// hold the HTTP response open until a human answers. The runner's tool
// call sits in the same wait. When an answer arrives, both unblock.
//
// We intentionally do NOT use SSE for the answer round-trip — it's a
// single request/response and SSE would burden the MCP child with
// stream parsing for no benefit.
func (s *HTTPServer) registerTaskQuestion(w http.ResponseWriter, r *http.Request, taskID string) {
	task, ok := s.taskMgr.GetTask(taskID)
	if !ok {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	var body struct {
		Prompt     string   `json:"prompt"`
		Header     string   `json:"header"`
		Kind       string   `json:"kind"`
		Choices    []string `json:"choices"`
		Multi      bool     `json:"multi"`
		VaultHint  string   `json:"vault_hint"`
		TimeoutSec int      `json:"timeoutSec"`
		Screenshot string   `json:"screenshot"` // F3 handoff
		Step       string   `json:"step"`       // F3 handoff
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// F4 (Access Layer): if the runner asked for a credential by vault_hint and we already
	// hold it, answer straight from the vault — no human prompt, no SSE broadcast (the secret
	// stays off neighbouring devices). This is the "log in once, reused after" payoff: the
	// human supplies the credential the first time (or via the Vault UI), and every later run
	// auto-resolves. Gated to kind=secret so only credential lookups auto-resolve, never a
	// value-judgement question. The runner receives the value exactly as a normal secret answer.
	if body.Kind == "secret" && strings.TrimSpace(body.VaultHint) != "" && s.vaultStore != nil {
		if entry, gerr := s.vaultStore.Get("", strings.TrimSpace(body.VaultHint)); gerr == nil && entry != nil && entry.Value != "" {
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":         true,
				"questionId": "vault:" + strings.TrimSpace(body.VaultHint),
				"answer":     entry.Value,
				"fromVault":  true,
			})
			return
		}
	}
	registered, answerCh, err := globalQuestionRegistry.Register(taskID, AgentQuestion{
		Prompt:     body.Prompt,
		Header:     body.Header,
		Kind:       body.Kind,
		Choices:    body.Choices,
		Multi:      body.Multi,
		VaultHint:  body.VaultHint,
		TimeoutSec: body.TimeoutSec,
		Screenshot: body.Screenshot,
		Step:       body.Step,
	})
	if err != nil {
		switch {
		case errors.Is(err, errQuestionAlreadyPending):
			jsonError(w, http.StatusConflict, err.Error())
		default:
			jsonError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	// Broadcast to every SSE subscriber of this task. Non-blocking
	// send: if a phone is offline and its eventCh buffer is full,
	// drop the event rather than stalling the runner. The peek
	// endpoint (GET /question) is the recovery path for that.
	emitTaskEvent(task, map[string]interface{}{
		"type":     "agent_question",
		"question": registered,
	})

	// Park until answered, expired, or task stopped.
	select {
	case <-r.Context().Done():
		// MCP child gave up waiting — drop the registration so a
		// stale answer doesn't get applied to a future re-register.
		globalQuestionRegistry.CancelTask(taskID)
		emitTaskEvent(task, map[string]interface{}{
			"type":       "agent_question_cancelled",
			"questionId": registered.ID,
			"reason":     "client_disconnected",
		})
		// No response body — the connection is already gone.
		return
	case answer := <-answerCh:
		if IsCancelledAnswer(answer) {
			emitTaskEvent(task, map[string]interface{}{
				"type":       "agent_question_cancelled",
				"questionId": registered.ID,
				"reason":     "expired_or_stopped",
			})
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":         true,
				"cancelled":  true,
				"questionId": registered.ID,
			})
			return
		}
		// Notify other subscribers that the question is closed
		// (so a second device can hide its sheet). Answer text is
		// NOT included in the event for kind=secret.
		safeAnswer := answer
		if registered.Kind == "secret" {
			safeAnswer = ""
		}
		emitTaskEvent(task, map[string]interface{}{
			"type":       "agent_answered",
			"questionId": registered.ID,
			"answer":     safeAnswer,
			"answeredAt": time.Now().UnixMilli(),
		})
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":         true,
			"questionId": registered.ID,
			"answer":     answer,
		})
	}
}

// handleTaskAnswer takes the human's answer and resolves the pending
// question. Idempotent on second call (returns 404 the second time).
func (s *HTTPServer) handleTaskAnswer(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if _, ok := s.taskMgr.GetTask(taskID); !ok {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	var body struct {
		QuestionID string `json:"questionId"`
		Answer     string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.QuestionID == "" {
		jsonError(w, http.StatusBadRequest, "questionId required")
		return
	}
	// F4 (Access Layer): capture the question's vault intent BEFORE Answer removes it, so we
	// can remember a credential the human just typed. The runner setting vault_hint IS the
	// intent signal that this answer is a reusable credential.
	var rememberHint, rememberKind string
	if pq, ok := globalQuestionRegistry.Pending(taskID); ok && pq.ID == body.QuestionID {
		rememberHint, rememberKind = pq.VaultHint, pq.Kind
	}
	if err := globalQuestionRegistry.Answer(body.QuestionID, body.Answer); err != nil {
		switch {
		case errors.Is(err, errQuestionNotFound):
			jsonError(w, http.StatusNotFound, err.Error())
		default:
			jsonError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	// F4: persist a freshly-supplied secret so every later run auto-resolves (see register handler).
	if rememberKind == "secret" && strings.TrimSpace(rememberHint) != "" && s.vaultStore != nil && body.Answer != "" {
		_ = s.vaultStore.Set(VaultEntry{
			Name:     strings.TrimSpace(rememberHint),
			Value:    body.Answer,
			Category: "custom",
			Notes:    "captured via yaver_ask_user handoff (F4)",
		})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// emitTaskEvent is the non-blocking broadcaster used by the question
// HTTP handlers (and any future structured-event source). Dropping is
// the right policy here — the agent_question event is replayable via
// GET /tasks/{id}/question, and downstream agent_answered events are
// purely informational.
func emitTaskEvent(task *Task, ev map[string]interface{}) {
	if task == nil || task.eventCh == nil {
		return
	}
	select {
	case task.eventCh <- ev:
	default:
		// Buffer full — a stalled SSE consumer is on the other end.
		// We do not block the runner / the question registry on it.
	}
}
