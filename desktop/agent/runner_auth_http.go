package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type runnerAuthSetRequest struct {
	Runner               string `json:"runner"`
	OpenAIAPIKey         string `json:"openai_api_key"`
	AnthropicAPIKey      string `json:"anthropic_api_key"`
	AnthropicAuthToken   string `json:"anthropic_auth_token"`
	ClaudeCodeOAuthToken string `json:"claude_code_oauth_token"`
	GLMAPIKey            string `json:"glm_api_key"`
	ZAIAPIKey            string `json:"zai_api_key"`
	Notes                string `json:"notes"`
}

func (s *HTTPServer) handleRunnerAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	rows, err := collectRunnerAuthStatusRows()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "runner auth status: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"runners": rows,
	})
}

func (s *HTTPServer) handleRunnerAuthSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req runnerAuthSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	entries, err := buildRunnerAuthEntries(
		req.Runner,
		req.OpenAIAPIKey,
		req.AnthropicAPIKey,
		req.AnthropicAuthToken,
		req.ClaudeCodeOAuthToken,
		req.GLMAPIKey,
		req.ZAIAPIKey,
		req.Notes,
	)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := setRunnerAuthEntriesLocal(entries); err != nil {
		jsonError(w, http.StatusInternalServerError, "save runner auth: "+err.Error())
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	rows, _ := collectRunnerAuthStatusRows()
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"runner":  normalizeRunnerAuthName(req.Runner),
		"saved":   names,
		"notes":   strings.TrimSpace(req.Notes),
		"runners": rows,
	})
}

func (s *HTTPServer) handleRunnerAuthSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req runnerAuthSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	result, err := applyRunnerAuthSetupLocal(ctx, req)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, result)
}
