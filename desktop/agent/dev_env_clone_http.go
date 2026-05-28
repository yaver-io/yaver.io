package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleDevEnvironmentClonePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req DevEnvironmentCloneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	plan := buildDevEnvironmentClonePlan(r.Context(), s, req)
	status := http.StatusOK
	if !plan.OK {
		status = http.StatusBadRequest
	}
	jsonReply(w, status, plan)
}

func (s *HTTPServer) handleDevEnvironmentCloneStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req DevEnvironmentCloneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	plan := buildDevEnvironmentClonePlan(r.Context(), s, req)
	if !plan.OK {
		jsonReply(w, http.StatusBadRequest, plan)
		return
	}
	job := startDevEnvironmentCloneJob(s, req, plan)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":     true,
		"jobId":  job.ID,
		"status": job.Status,
		"plan":   plan,
	})
}

func (s *HTTPServer) handleDevEnvironmentCloneStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "id is required")
		return
	}
	job, ok := getDevEnvironmentCloneJob(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "dev environment clone job not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":  true,
		"job": job,
	})
}
