package main

// hybrid_http.go — HTTP surface for the hybrid orchestrator.
//
//   POST /hybrid/run        synchronous: plan + execute, returns HybridReport
//   POST /hybrid/plan       plan only — useful for previewing the subtask
//                           list before paying for implementer calls
//
// The endpoints sit behind the normal auth() middleware. Guests are
// intentionally NOT allowed to invoke hybrid runs — the planner can
// read the whole repo and that's outside the guest surface defined
// in CLAUDE.md. Registration lives in httpserver.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type hybridRunRequest struct {
	Planner     string `json:"planner,omitempty"`
	Implementer string `json:"implementer,omitempty"`
	Model       string `json:"model,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	WorkDir     string `json:"workDir"`
	Prompt      string `json:"prompt"`
	MaxSubtasks int    `json:"maxSubtasks,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
}

func (req hybridRunRequest) toSpec() HybridSpec {
	s := HybridSpec{
		Planner:     req.Planner,
		Implementer: req.Implementer,
		Model:       req.Model,
		BaseURL:     req.BaseURL,
		WorkDir:     req.WorkDir,
		Prompt:      req.Prompt,
		MaxSubtasks: req.MaxSubtasks,
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
