package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

type runnerAuthSetRequest struct {
	Runner          string `json:"runner"`
	OpenAIAPIKey    string `json:"openai_api_key"`
	AnthropicAPIKey string `json:"anthropic_api_key"`
	GLMAPIKey       string `json:"glm_api_key"`
	ZAIAPIKey       string `json:"zai_api_key"`
	Notes           string `json:"notes"`
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
	if r.URL.Query().Get("live") == "1" {
		rows = applyLiveRunnerAuthProbe(rows, r.URL.Query().Get("runner"))
	}
	workDir, _ := os.Getwd()
	deviceID := ""
	if s.taskMgr != nil {
		deviceID = strings.TrimSpace(s.taskMgr.DeviceID)
	}
	s.syncRunnerAuthIncidents(rows, workDir, deviceID)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"runners": rows,
	})
}

func (s *HTTPServer) syncRunnerAuthIncidents(rows []runnerAuthStatusRow, workDir, deviceID string) {
	resolveRunnerIncidents := func(target string) {
		for _, candidate := range []string{
			ReasonRunnerCodexNotAuthenticated,
			ReasonRunnerCodexLinuxSandboxBlocked,
			ReasonRunnerClaudeAuthRequired,
			ReasonRunnerOpenCodeUnusable,
		} {
			GlobalIncidentStore().ResolveOpenByKey(IncidentKey{
				Category:    "runner_auth",
				Code:        candidate,
				DeviceID:    strings.TrimSpace(deviceID),
				ProjectPath: strings.TrimSpace(workDir),
				Target:      strings.TrimSpace(target),
			}, "Runner readiness recovered.")
		}
	}

	for _, row := range rows {
		code := ""
		severity := IncidentSeverityWarn
		title := row.Name + " needs attention"
		message := ""
		action := ""
		switch row.ID {
		case "codex":
			if strings.Contains(strings.ToLower(row.Error), "not authenticated") {
				code = ReasonRunnerCodexNotAuthenticated
				severity = IncidentSeverityError
				title = "Codex is not authenticated"
				message = "Codex is installed on the host but cannot run until authentication is configured."
				action = "Run the Codex browser login flow or import subscription credentials from an already-signed-in user-owned device."
			} else if strings.Contains(strings.ToLower(row.Error), "blocking the sandbox") {
				code = ReasonRunnerCodexLinuxSandboxBlocked
				severity = IncidentSeverityError
				title = "Codex sandbox is blocked"
				message = "This Linux host is blocking the sandbox Codex needs for execution."
				action = "Fix the Linux sandbox prerequisites before trying Codex again."
			}
		case "claude":
			if row.Installed && !row.AuthConfigured {
				code = ReasonRunnerClaudeAuthRequired
				title = "Claude Code auth is missing"
				message = "Claude Code is installed, but the host has no confirmed authentication yet."
				action = "Run the Claude browser login flow or import subscription credentials from an already-signed-in user-owned device."
			}
		case "opencode":
			if strings.TrimSpace(row.Error) != "" {
				code = ReasonRunnerOpenCodeUnusable
				title = "OpenCode is not usable yet"
				message = strings.TrimSpace(row.Error)
				action = "Fix the OpenCode provider/auth configuration on the host."
			}
		}
		if code == "" {
			resolveRunnerIncidents(row.ID)
			continue
		}
		GlobalIncidentStore().UpsertOpen(IncidentKey{
			Category:    "runner_auth",
			Code:        code,
			DeviceID:    strings.TrimSpace(deviceID),
			ProjectPath: strings.TrimSpace(workDir),
			Target:      strings.TrimSpace(row.ID),
		}, IncidentEvent{
			Timestamp:       time.Now().UnixMilli(),
			Severity:        severity,
			Category:        "runner_auth",
			Code:            code,
			Source:          "runner-auth/status",
			Title:           title,
			UserMessage:     message,
			TechnicalInfo:   strings.TrimSpace(row.Detail),
			SuggestedAction: action,
			DeviceID:        strings.TrimSpace(deviceID),
			ProjectPath:     strings.TrimSpace(workDir),
			Target:          strings.TrimSpace(row.ID),
			LogsAvailable:   false,
			Recoverable:     true,
			Metadata: map[string]interface{}{
				"runner":         row.ID,
				"installed":      row.Installed,
				"ready":          row.Ready,
				"authConfigured": row.AuthConfigured,
				"authSource":     row.AuthSource,
				"detail":         row.Detail,
				"warning":        row.Warning,
				"error":          row.Error,
			},
		})
	}
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
		"",
		"",
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
	if result.InstallAttempt {
		markInstalledRunnerInventoryDirty()
	}
	s.TriggerHeartbeat()
	jsonReply(w, http.StatusOK, result)
}
