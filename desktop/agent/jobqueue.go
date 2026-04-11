package main

// jobqueue.go — persistent background job runner. Replaces
// Inngest / Trigger.dev / BullMQ for solo devs who want delayed
// execution with retries and a dead-letter queue, but don't
// want to stand up Redis or a paid platform.
//
// Architecture:
//
//   - Jobs live as individual JSON files in ~/.yaver/jobs/queue/
//     so a crash loses at most the in-flight worker batch, not
//     the queue.
//   - A pool of worker goroutines poll the queue every 2s and
//     move due jobs to in-flight/, then execute, then either
//     delete (success) or push to retry/ with a backoff or
//     dlq/ (exhausted).
//   - Handlers are registered at process start by name; anything
//     not registered ends up in dlq with reason "unknown handler".
//
// This is intentionally file-based, not sqlite, because:
//
//   1. fewer deps = faster boot + smaller binary
//   2. a `cat jobs/queue/*.json | jq` is always debuggable
//   3. solo-dev volume is low; 500 jobs/day never hurts.
//
// HTTP surface:
//
//   POST /jobs/enqueue      owner — create a new job
//   GET  /jobs              owner — list queue + in-flight + dlq
//   POST /jobs/:id/retry    owner — requeue a dlq job
//   POST /jobs/:id/cancel   owner — drop a pending job

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Job is one entry in the queue.
type Job struct {
	ID          string          `json:"id"`
	Handler     string          `json:"handler"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	RunAt       time.Time       `json:"runAt"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"maxAttempts"`
	LastError   string          `json:"lastError,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	BackoffSec  int             `json:"backoffSec"`
}

// JobHandler is a side-effecting function registered by name.
// Returning an error triggers retry/DLQ logic.
type JobHandler func(payload json.RawMessage) error

var (
	jobHandlers  = map[string]JobHandler{}
	jobHandlerMu sync.RWMutex
)

// RegisterJobHandler hooks a named handler. Call from init() or
// from whatever module owns the side-effect (newsletter send,
// image resize, pdf render, etc.).
func RegisterJobHandler(name string, h JobHandler) {
	jobHandlerMu.Lock()
	defer jobHandlerMu.Unlock()
	jobHandlers[name] = h
}

// --- storage ---------------------------------------------------------------

func jobsDir(sub string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "jobs", sub)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func writeJob(sub string, job *Job) error {
	dir, err := jobsDir(sub)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, job.ID+".json"), data, 0o600)
}

func removeJob(sub, id string) error {
	dir, err := jobsDir(sub)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, id+".json"))
}

func listJobs(sub string) ([]Job, error) {
	dir, err := jobsDir(sub)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if err := json.Unmarshal(data, &j); err == nil {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt.Before(out[j].RunAt) })
	return out, nil
}

// EnqueueJob adds a new job to the queue, scheduled for immediate
// run unless RunAt is set in the future.
func EnqueueJob(handler string, payload interface{}, opts ...JobOption) (*Job, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	job := &Job{
		ID:          randomFormID(),
		Handler:     handler,
		Payload:     payloadJSON,
		RunAt:       time.Now().UTC(),
		MaxAttempts: 5,
		BackoffSec:  30,
		CreatedAt:   time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(job)
	}
	if err := writeJob("queue", job); err != nil {
		return nil, err
	}
	return job, nil
}

// JobOption tweaks a job before it lands on disk.
type JobOption func(*Job)

func WithRunAt(t time.Time) JobOption       { return func(j *Job) { j.RunAt = t } }
func WithDelay(d time.Duration) JobOption   { return func(j *Job) { j.RunAt = time.Now().UTC().Add(d) } }
func WithMaxAttempts(n int) JobOption       { return func(j *Job) { j.MaxAttempts = n } }
func WithBackoffSec(sec int) JobOption      { return func(j *Job) { j.BackoffSec = sec } }

// --- worker loop -----------------------------------------------------------

var jobQueueStarted bool

// registerBuiltinJobHandlers hooks the handlers that ship in
// the binary itself — one-liners that let other subsystems
// fire-and-forget into the queue. Safe to call more than once.
func registerBuiltinJobHandlers() {
	RegisterJobHandler("newsletter.send", func(p json.RawMessage) error {
		var body struct {
			CampaignID string `json:"campaignId"`
		}
		if err := json.Unmarshal(p, &body); err != nil {
			return err
		}
		broadcastCampaign(body.CampaignID)
		return nil
	})
	RegisterJobHandler("form.notify", func(p json.RawMessage) error {
		var sub Submission
		if err := json.Unmarshal(p, &sub); err != nil {
			return err
		}
		form := findForm(sub.FormID)
		if form == nil {
			return fmt.Errorf("form %s not found", sub.FormID)
		}
		sendFormNotification(form, &sub)
		return nil
	})
	RegisterJobHandler("pdf.render", func(p json.RawMessage) error {
		var opts PDFRenderOptions
		if err := json.Unmarshal(p, &opts); err != nil {
			return err
		}
		_, err := RenderPDF(opts)
		return err
	})
}

// StartJobQueue spins up a worker that polls queue/ every 2s.
// Safe to call multiple times — only the first wins.
func StartJobQueue() {
	jobHandlerMu.Lock()
	defer jobHandlerMu.Unlock()
	if jobQueueStarted {
		return
	}
	jobQueueStarted = true
	go jobQueueLoop()
}

func jobQueueLoop() {
	for {
		time.Sleep(2 * time.Second)
		pending, err := listJobs("queue")
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		for _, j := range pending {
			if j.RunAt.After(now) {
				continue
			}
			runJob(j)
		}
	}
}

func runJob(j Job) {
	jobHandlerMu.RLock()
	h, ok := jobHandlers[j.Handler]
	jobHandlerMu.RUnlock()
	if !ok {
		j.LastError = "unknown handler: " + j.Handler
		_ = removeJob("queue", j.ID)
		_ = writeJob("dlq", &j)
		return
	}
	j.Attempts++
	err := safeJobCall(h, j.Payload)
	if err == nil {
		_ = removeJob("queue", j.ID)
		return
	}
	j.LastError = err.Error()
	if j.Attempts >= j.MaxAttempts {
		_ = removeJob("queue", j.ID)
		_ = writeJob("dlq", &j)
		return
	}
	// Exponential backoff: backoffSec * 2^(attempts-1)
	delay := time.Duration(j.BackoffSec) * time.Second
	for i := 1; i < j.Attempts; i++ {
		delay *= 2
	}
	if delay > 1*time.Hour {
		delay = 1 * time.Hour
	}
	j.RunAt = time.Now().UTC().Add(delay)
	_ = writeJob("queue", &j)
}

func safeJobCall(h JobHandler, p json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(p)
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	queue, _ := listJobs("queue")
	dlq, _ := listJobs("dlq")
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"queue": queue,
		"dlq":   dlq,
	})
}

func (s *HTTPServer) handleJobsEnqueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Handler     string          `json:"handler"`
		Payload     json.RawMessage `json:"payload"`
		DelaySec    int             `json:"delaySec,omitempty"`
		MaxAttempts int             `json:"maxAttempts,omitempty"`
		BackoffSec  int             `json:"backoffSec,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Handler == "" {
		jsonError(w, http.StatusBadRequest, "handler required")
		return
	}
	opts := []JobOption{}
	if body.DelaySec > 0 {
		opts = append(opts, WithDelay(time.Duration(body.DelaySec)*time.Second))
	}
	if body.MaxAttempts > 0 {
		opts = append(opts, WithMaxAttempts(body.MaxAttempts))
	}
	if body.BackoffSec > 0 {
		opts = append(opts, WithBackoffSec(body.BackoffSec))
	}
	j, err := EnqueueJob(body.Handler, json.RawMessage(body.Payload), opts...)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "job": j})
}

// handleJobAction routes /jobs/:id/{retry,cancel}.
func (s *HTTPServer) handleJobAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, http.StatusNotFound, "invalid path")
		return
	}
	id := parts[1]
	action := parts[2]
	switch action {
	case "retry":
		dlq, _ := listJobs("dlq")
		for _, j := range dlq {
			if j.ID == id {
				j.Attempts = 0
				j.LastError = ""
				j.RunAt = time.Now().UTC()
				_ = removeJob("dlq", id)
				_ = writeJob("queue", &j)
				jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
				return
			}
		}
		jsonError(w, http.StatusNotFound, "job not in dlq")
	case "cancel":
		_ = removeJob("queue", id)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusBadRequest, "unknown action")
	}
}
