package main

// hybrid_http.go — HTTP surface for the hybrid orchestrator.
//
//   POST /hybrid/run        synchronous: plan + execute, returns HybridReport
//   POST /hybrid/plan       plan only — useful for previewing the subtask
//                           list before paying for implementer calls
//   POST /hybrid/stream     same as /run but streams HybridEvent over SSE
//                           so the UI renders live progress instead of
//                           blocking for minutes
//
// The endpoints sit behind the normal auth() middleware. Guests are
// intentionally NOT allowed to invoke hybrid runs — the planner can
// read the whole repo and that's outside the guest surface defined
// in CLAUDE.md. Registration lives in httpserver.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type hybridRunRequest struct {
	Planner                string `json:"planner,omitempty"`
	Implementer            string `json:"implementer,omitempty"`
	Model                  string `json:"model,omitempty"`
	BaseURL                string `json:"baseUrl,omitempty"`
	WorkDir                string `json:"workDir"`
	Prompt                 string `json:"prompt"`
	MaxSubtasks            int    `json:"maxSubtasks,omitempty"`
	MaxRetries             int    `json:"maxRetries,omitempty"`
	MaxConsecutiveFailures int    `json:"maxConsecutiveFailures,omitempty"`
	TimeoutSec             int    `json:"timeoutSec,omitempty"`
}

func (req hybridRunRequest) toSpec() HybridSpec {
	s := HybridSpec{
		Planner:                req.Planner,
		Implementer:            req.Implementer,
		Model:                  req.Model,
		BaseURL:                req.BaseURL,
		WorkDir:                req.WorkDir,
		Prompt:                 req.Prompt,
		MaxSubtasks:            req.MaxSubtasks,
		MaxRetries:             req.MaxRetries,
		MaxConsecutiveFailures: req.MaxConsecutiveFailures,
	}
	if req.TimeoutSec > 0 {
		s.Timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	return s
}

func (s *HTTPServer) handleHybridRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hybridRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	spec := req.toSpec()

	// Long runs: let the context live past the default server
	// timeout, but still honor Timeout from the spec.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rep, err := RunHybrid(ctx, spec)
	if err != nil {
		status := http.StatusInternalServerError
		payload := map[string]any{"error": err.Error()}
		if rep != nil {
			payload["report"] = rep
		}
		jsonReply(w, status, payload)
		return
	}
	jsonReply(w, http.StatusOK, rep)
}

func (s *HTTPServer) handleHybridPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hybridRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	spec := req.toSpec()
	if err := applyHybridDefaults(&spec); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spec.Timeout)
	defer cancel()

	planOut, err := runPlanner(ctx, spec)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]any{
			"error":      err.Error(),
			"planOutput": planOut,
		})
		return
	}
	subtasks, perr := parseHybridPlan(planOut, spec.MaxSubtasks)
	if perr != nil {
		jsonReply(w, http.StatusUnprocessableEntity, map[string]any{
			"error":      perr.Error(),
			"planOutput": planOut,
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"spec":       spec,
		"subtasks":   subtasks,
		"planOutput": planOut,
	})
}

// handleHybridStream runs a hybrid spec and streams HybridEvent as
// server-sent events. The client holds the connection open for the
// duration of the run and receives live updates instead of blocking
// on /hybrid/run for minutes.
//
// Protocol:
//   - One SSE `data:` line per event; no custom `event:` names
//     (clients just JSON.parse the data).
//   - Keep-alive via a `: heartbeat\n\n` comment every 15 s.
//   - Terminates after `run_done` or `error` is emitted.
//
// Intentionally POST: the spec includes a multi-line prompt that is
// awkward to cram into a query string, and SSE does not require GET.
func (s *HTTPServer) handleHybridStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hybridRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	spec := req.toSpec()

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported on this transport")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	// Buffered channel so a brief network hiccup doesn't block the
	// orchestrator. Draining happens in the SSE writer loop below.
	events := make(chan HybridEvent, 64)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Run the hybrid orchestrator in its own goroutine so we can
	// push heartbeats while it's working. Close events when done so
	// the writer loop terminates cleanly.
	go func() {
		defer close(events)
		_, err := RunHybridWithProgress(ctx, spec, func(ev HybridEvent) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
		if err != nil {
			select {
			case events <- HybridEvent{Type: "error", At: time.Now(), Message: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	// Writer loop: drain events to the SSE wire, send a heartbeat
	// every 15 s so proxies that idle-close connections don't drop
	// long planner calls.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case ev, stillOpen := <-events:
			if !stillOpen {
				return
			}
			payload, merr := json.Marshal(ev)
			if merr != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			if ev.Type == "run_done" {
				return
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
