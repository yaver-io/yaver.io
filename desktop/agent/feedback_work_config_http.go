package main

import (
	"encoding/json"
	"net/http"
)

type feedbackWorkConfigResponse struct {
	OK                   bool   `json:"ok"`
	Enabled              bool   `json:"enabled"`
	Running              bool   `json:"running"`
	IntervalSeconds      int    `json:"intervalSeconds,omitempty"`
	WorkerID             string `json:"workerId,omitempty"`
	ProjectSlug          string `json:"projectSlug,omitempty"`
	CreateProviderIssues bool   `json:"createProviderIssues"`
	RuntimeReason        string `json:"runtimeReason,omitempty"`
}

type feedbackWorkConfigPatch struct {
	Enabled              *bool   `json:"enabled,omitempty"`
	IntervalSeconds      *int    `json:"intervalSeconds,omitempty"`
	WorkerID             *string `json:"workerId,omitempty"`
	ProjectSlug          *string `json:"projectSlug,omitempty"`
	CreateProviderIssues *bool   `json:"createProviderIssues,omitempty"`
}

func (s *HTTPServer) handleFeedbackWorkConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := LoadConfig()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "feedback work config: "+err.Error())
			return
		}
		jsonReply(w, http.StatusOK, s.feedbackWorkConfigSummary(cfg))
	case http.MethodPost, http.MethodPatch:
		var patch feedbackWorkConfigPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		cfg, err := LoadConfig()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "feedback work config: "+err.Error())
			return
		}
		if cfg.FeedbackWorkWorker == nil {
			cfg.FeedbackWorkWorker = &FeedbackWorkWorkerConfig{}
		}
		if patch.Enabled != nil {
			cfg.FeedbackWorkWorker.Enabled = *patch.Enabled
		}
		if patch.IntervalSeconds != nil {
			cfg.FeedbackWorkWorker.IntervalSeconds = *patch.IntervalSeconds
		}
		if patch.WorkerID != nil {
			cfg.FeedbackWorkWorker.WorkerID = *patch.WorkerID
		}
		if patch.ProjectSlug != nil {
			cfg.FeedbackWorkWorker.ProjectSlug = *patch.ProjectSlug
		}
		if patch.CreateProviderIssues != nil {
			cfg.FeedbackWorkWorker.CreateProviderIssues = *patch.CreateProviderIssues
		}
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, "save feedback work config: "+err.Error())
			return
		}
		runtimeStatus := s.configureFeedbackWorkWorker(nil, cfg)
		jsonReply(w, http.StatusOK, s.feedbackWorkConfigSummaryWithRuntime(cfg, runtimeStatus))
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) feedbackWorkConfigSummary(cfg *Config) feedbackWorkConfigResponse {
	return s.feedbackWorkConfigSummaryWithRuntime(cfg, s.feedbackWorkWorkerRuntimeStatus())
}

func (s *HTTPServer) feedbackWorkConfigSummaryWithRuntime(cfg *Config, runtimeStatus feedbackWorkWorkerRuntimeStatus) feedbackWorkConfigResponse {
	out := feedbackWorkConfigResponse{OK: true}
	if cfg == nil || cfg.FeedbackWorkWorker == nil {
		out.Running = runtimeStatus.Running
		out.RuntimeReason = runtimeStatus.Reason
		return out
	}
	fw := cfg.FeedbackWorkWorker
	out.Enabled = fw.Enabled
	out.Running = runtimeStatus.Running
	out.IntervalSeconds = fw.IntervalSeconds
	out.WorkerID = fw.WorkerID
	out.ProjectSlug = fw.ProjectSlug
	out.CreateProviderIssues = fw.CreateProviderIssues
	out.RuntimeReason = runtimeStatus.Reason
	return out
}
