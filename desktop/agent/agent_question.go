package main

// agent_question.go — when a runner (Claude / Codex / OpenCode) genuinely
// needs the human to answer something it cannot default, it calls the
// `yaver_ask_user` MCP tool. That MCP call lands here:
//
//   1. The tool handler (in stdio MCP child) HTTP-POSTs to the running
//      daemon at /tasks/{id}/question with {prompt, kind, choices, ...}.
//      stdin/stdout-bound MCP children cannot share in-process channels
//      with the daemon, so HTTP is the only safe handoff. See main.go's
//      runMCPStdio for the separate-process model.
//
//   2. The daemon registers the question in pendingQuestionRegistry,
//      broadcasts a {type:"agent_question",...} event on the task's
//      eventCh — which the SSE writer in handleTaskByID/streamOutput
//      forwards verbatim to every subscribed mobile/web/CLI client —
//      and parks the inbound HTTP request on the question's answer
//      channel. The runner's tool call is in the meantime parked
//      waiting for that HTTP response, so the runner has effectively
//      stopped its turn until a human answers.
//
//   3. A user types an answer on phone/web/CLI; that surface POSTs to
//      /tasks/{id}/answer with {questionId, answer}. The handler
//      resolves the question's answer channel; both the parked
//      /question request AND the originating MCP tool call complete
//      with the answer string. The runner consumes the result and
//      keeps going.
//
// Cancellation: if the task itself is stopped (or the answer never
// arrives within questionTTL), the registry resolves with a sentinel
// "cancelled" string so the runner doesn't hang forever on a tool call
// that lost its human.
//
// Single-question-per-task constraint: the registry is keyed by task ID,
// not by (task, questionId). A task that already has a pending question
// rejects a second one with errQuestionAlreadyPending — the runner
// should serialize its own questions, and silently queueing them would
// build an unbounded backlog if the user is offline.

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentQuestion is the structured payload the agent sends and the user
// answers. JSON-tagged so the SSE event body is the same shape the
// HTTP handler reads — no second wire format to maintain.
type AgentQuestion struct {
	ID          string   `json:"id"`
	TaskID      string   `json:"taskId"`
	Prompt      string   `json:"prompt"`
	Kind        string   `json:"kind"`                  // "text" (default) | "choice" | "secret"
	Choices     []string `json:"choices,omitempty"`     // populated only when kind=choice
	VaultHint   string   `json:"vaultHint,omitempty"`   // suggested vault entry name; UI offers "use stored value"
	CreatedAtMs int64    `json:"createdAtMs"`
	TimeoutSec  int      `json:"timeoutSec"`
}

// pendingQuestion is the in-memory record. answerCh resolves with the
// raw answer string (kind-checked at handler boundary), or with a
// sentinel value when the question is cancelled.
type pendingQuestion struct {
	q         AgentQuestion
	answerCh  chan string
	expiresAt time.Time
}

const (
	defaultQuestionTimeoutSec = 300
	maxQuestionTimeoutSec     = 1800
	cancelledAnswerSentinel   = "\x00YAVER_QUESTION_CANCELLED\x00"
)

var (
	errQuestionAlreadyPending = errors.New("a question is already pending for this task; serialize your asks")
	errQuestionExpired        = errors.New("question expired before the user answered")
	errQuestionCancelled      = errors.New("question cancelled (task stopped or user dismissed)")
	errQuestionNotFound       = errors.New("no pending question with that id")
)

// pendingQuestionRegistry holds the at-most-one in-flight question per task.
// All access is through the package-level mu. Entries are removed when an
// answer arrives, when the question expires, or when CancelTask wipes them.
type pendingQuestionRegistry struct {
	mu       sync.Mutex
	byTask   map[string]*pendingQuestion // taskID -> question (1:1)
	byID     map[string]string           // questionID -> taskID
}

var globalQuestionRegistry = &pendingQuestionRegistry{
	byTask: make(map[string]*pendingQuestion),
	byID:   make(map[string]string),
}

// Register creates a new question for the given task. The question's
// ID is generated here. Returns the registered question (with ID
// populated) and the channel the caller blocks on for the answer.
//
// timeoutSec ≤ 0 picks defaultQuestionTimeoutSec; values above
// maxQuestionTimeoutSec are clamped down. Both bounds are server-side
// because the runner cannot be trusted to pick sane numbers.
func (r *pendingQuestionRegistry) Register(taskID string, q AgentQuestion) (AgentQuestion, <-chan string, error) {
	if taskID == "" {
		return AgentQuestion{}, nil, errors.New("taskID required")
	}
	if q.Prompt == "" {
		return AgentQuestion{}, nil, errors.New("prompt required")
	}
	switch q.Kind {
	case "", "text":
		q.Kind = "text"
	case "choice":
		if len(q.Choices) == 0 {
			return AgentQuestion{}, nil, errors.New("kind=choice requires non-empty choices")
		}
	case "secret":
		// allowed
	default:
		return AgentQuestion{}, nil, fmt.Errorf("unknown kind %q", q.Kind)
	}
	if q.TimeoutSec <= 0 {
		q.TimeoutSec = defaultQuestionTimeoutSec
	}
	if q.TimeoutSec > maxQuestionTimeoutSec {
		q.TimeoutSec = maxQuestionTimeoutSec
	}
	q.ID = "q_" + uuid.New().String()[:12]
	q.TaskID = taskID
	q.CreatedAtMs = time.Now().UnixMilli()

	r.mu.Lock()
	if _, busy := r.byTask[taskID]; busy {
		r.mu.Unlock()
		return AgentQuestion{}, nil, errQuestionAlreadyPending
	}
	pq := &pendingQuestion{
		q:         q,
		answerCh:  make(chan string, 1),
		expiresAt: time.Now().Add(time.Duration(q.TimeoutSec) * time.Second),
	}
	r.byTask[taskID] = pq
	r.byID[q.ID] = taskID
	r.mu.Unlock()

	// Auto-expire: if no answer arrives, drop the entry and resolve
	// the channel with the cancellation sentinel so the parked
	// /question handler returns and the runner's tool call unblocks.
	go func(id string, after time.Duration) {
		time.Sleep(after)
		r.expire(id)
	}(q.ID, time.Duration(q.TimeoutSec)*time.Second)

	return q, pq.answerCh, nil
}

// Answer resolves a pending question. answer is sent verbatim to the
// runner — for kind=secret callers should NOT log the value. Returns
// errQuestionNotFound if the id is unknown OR already answered/expired.
func (r *pendingQuestionRegistry) Answer(questionID, answer string) error {
	r.mu.Lock()
	taskID, ok := r.byID[questionID]
	if !ok {
		r.mu.Unlock()
		return errQuestionNotFound
	}
	pq := r.byTask[taskID]
	delete(r.byTask, taskID)
	delete(r.byID, questionID)
	r.mu.Unlock()
	if pq == nil {
		return errQuestionNotFound
	}
	select {
	case pq.answerCh <- answer:
	default:
		// Channel buffered to 1; if full, the answer was already
		// delivered. Treat as a no-op, not an error.
	}
	return nil
}

// CancelTask drops any pending question for the given task and resolves
// it with the cancellation sentinel. Used when the task is stopped /
// killed so the runner's tool call unparks instead of waiting out the
// full TTL.
func (r *pendingQuestionRegistry) CancelTask(taskID string) {
	r.mu.Lock()
	pq, ok := r.byTask[taskID]
	if ok {
		delete(r.byTask, taskID)
		delete(r.byID, pq.q.ID)
	}
	r.mu.Unlock()
	if pq != nil {
		select {
		case pq.answerCh <- cancelledAnswerSentinel:
		default:
		}
	}
}

// Pending returns a snapshot of the pending question for taskID, if any.
// Used by the GET /tasks/{id}/question handler so a late-joining UI can
// fetch the current question without re-subscribing to the SSE stream.
func (r *pendingQuestionRegistry) Pending(taskID string) (AgentQuestion, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pq, ok := r.byTask[taskID]
	if !ok {
		return AgentQuestion{}, false
	}
	return pq.q, true
}

// expire is called by the auto-expire goroutine. No-op if the question
// was already answered (the byID entry will be missing).
func (r *pendingQuestionRegistry) expire(questionID string) {
	r.mu.Lock()
	taskID, ok := r.byID[questionID]
	if !ok {
		r.mu.Unlock()
		return
	}
	pq := r.byTask[taskID]
	delete(r.byTask, taskID)
	delete(r.byID, questionID)
	r.mu.Unlock()
	if pq != nil {
		select {
		case pq.answerCh <- cancelledAnswerSentinel:
		default:
		}
	}
}

// IsCancelled reports whether the answer string is the registry's
// cancellation sentinel (timeout or task-stopped). Lets the MCP tool
// handler return a clean error instead of leaking the sentinel to
// the runner as a literal answer.
func IsCancelledAnswer(s string) bool {
	return s == cancelledAnswerSentinel
}
