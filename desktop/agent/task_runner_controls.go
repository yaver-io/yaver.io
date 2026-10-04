package main

// task_runner_controls.go is the typed boundary for runner-native controls.
//
// A whole-message `/model` or `/exit` is an interaction, not prose and not a
// terminal transcript. Surfaces GET this task-scoped catalog, render native UI,
// then POST the selected control as JSON. The agent validates against the
// runner installed on THIS machine and reports only operations it actually
// performed. Unknown slash commands keep their existing compatibility path.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type taskRunnerControlOption struct {
	ID          string `json:"id"`
	Command     string `json:"command"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Destructive bool   `json:"destructive,omitempty"`
}

type taskRunnerControlCatalog struct {
	OK              bool                      `json:"ok"`
	Schema          int                       `json:"schema"`
	TaskID          string                    `json:"taskId"`
	RunnerID        string                    `json:"runnerId"`
	Model           string                    `json:"model,omitempty"`
	ReasoningEffort string                    `json:"reasoningEffort,omitempty"`
	ModelSource     string                    `json:"modelSource,omitempty"`
	Models          []runnerModelInfo         `json:"models"`
	Controls        []taskRunnerControlOption `json:"controls"`
	IsAdopted       bool                      `json:"isAdopted,omitempty"`
}

type taskRunnerControlRequest struct {
	Control         string `json:"control"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Confirmed       bool   `json:"confirmed,omitempty"`
}

var codexModelCatalogCache struct {
	sync.Mutex
	models  []runnerModelInfo
	expires time.Time
}

func (s *HTTPServer) handleTaskRunnerControl(w http.ResponseWriter, r *http.Request, taskID string) {
	task, ok := s.taskMgr.GetTask(taskID)
	if !ok || task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		catalog := s.taskRunnerControlCatalog(r.Context(), task)
		jsonReply(w, http.StatusOK, catalog)
	case http.MethodPost:
		var body taskRunnerControlRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil && err != io.EOF {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		switch strings.ToLower(strings.TrimSpace(body.Control)) {
		case "model":
			s.applyTaskModelControl(w, r, task, body)
		case "exit":
			s.applyTaskExitControl(w, task, body)
		default:
			jsonError(w, http.StatusBadRequest, "control must be model or exit")
		}
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) taskRunnerControlCatalog(ctx context.Context, task *Task) taskRunnerControlCatalog {
	runnerID := normalizeRunnerID(task.RunnerID)
	models, source := runnerControlModels(ctx, runnerID)
	model := strings.TrimSpace(task.Model)
	if model == "" {
		model = strings.TrimSpace(task.runner.Model)
	}
	if model == "" {
		for _, item := range models {
			if item.IsDefault {
				model = item.ID
				break
			}
		}
	}
	effort := strings.TrimSpace(task.ReasoningEffort)
	if runnerID == "codex" && effort == "" {
		for _, item := range models {
			if item.ID == model {
				effort = normalizeCodexReasoningEffort(item.DefaultReasoningEffort)
				break
			}
		}
		if effort == "" {
			effort = "medium"
		}
	}
	return taskRunnerControlCatalog{
		OK: true, Schema: 1, TaskID: task.ID, RunnerID: runnerID,
		Model: model, ReasoningEffort: effort, ModelSource: source, Models: models,
		Controls: []taskRunnerControlOption{
			{ID: "model", Command: "/model", Label: "Change model", Description: "Choose the model for this conversation's next turn.", Kind: "model_picker"},
			{ID: "exit", Command: "/exit", Label: "Exit session", Description: "Stop this runner session after confirmation.", Kind: "confirmation", Destructive: true},
		},
		IsAdopted: task.IsAdopted,
	}
}

func runnerControlModels(ctx context.Context, runnerID string) ([]runnerModelInfo, string) {
	runnerID = normalizeRunnerID(runnerID)
	if runnerID == "codex" {
		if models, err := codexAppServerModels(ctx); err == nil && len(models) > 0 {
			return models, "codex-app-server"
		}
	}
	// OpenCode's global config belongs to THIS runner machine and may contain
	// private/local providers that the shared backend inventory cannot know
	// about. Prefer it whenever it names models; a stale backend row must never
	// hide the exact provider/model ids the local `opencode run --model` accepts.
	if runnerID == "opencode" || runnerID == "remoteless" {
		if cfg, err := loadOpenCodeConfigSummary(); err == nil {
			var backend []runnerModelInfo
			for _, model := range GetCachedModels() {
				if normalizeRunnerID(model.RunnerID) != "opencode" {
					continue
				}
				backend = append(backend, runnerModelInfo{
					ID: model.ModelID, Name: model.Name, Description: model.Description,
					Provider: model.ProviderID, ProviderName: model.ProviderName,
					Lifecycle: model.Lifecycle, Source: "backend", IsDefault: model.IsDefault,
				})
			}
			active, _ := probeOpenCodeModels(ctx)
			local := projectOpenCodeRunnerModels(active, cfg.Models, backend, firstNonEmpty(cfg.Model, yaverDefaultModelForRunner("opencode")))
			if len(local) > 0 {
				if len(active) > 0 {
					return local, "opencode-cli"
				}
				return local, "opencode-config"
			}
		}
	}
	var cached []runnerModelInfo
	for _, model := range GetCachedModels() {
		if normalizeRunnerID(model.RunnerID) != runnerID {
			continue
		}
		cached = append(cached, backendModelRunnerInfo(model))
	}
	if len(cached) > 0 {
		return cached, "backend"
	}
	if runnerID == "remoteless" {
		models := fallbackRunnerModels("opencode")
		if len(models) > 0 {
			// Remoteless is OpenCode-backed today, but it is a separate lane
			// with its own stable provider/model contract. Do not let the
			// product-wide OpenCode default rewrite that hosted-lane default.
			models[0] = runnerModelInfo{
				ID: "deepseek/deepseek-flash", Name: "DeepSeek Flash",
				Provider: "deepseek", Source: "builtin", IsDefault: true,
			}
		}
		return models, "builtin"
	}
	return fallbackRunnerModels(runnerID), "builtin"
}

func (s *HTTPServer) applyTaskModelControl(w http.ResponseWriter, r *http.Request, task *Task, body taskRunnerControlRequest) {
	model := strings.TrimSpace(body.Model)
	if model == "" {
		jsonError(w, http.StatusBadRequest, "model is required")
		return
	}
	catalog := s.taskRunnerControlCatalog(r.Context(), task)
	var selected *runnerModelInfo
	for i := range catalog.Models {
		if catalog.Models[i].ID == model {
			selected = &catalog.Models[i]
			break
		}
	}
	if selected == nil {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "code": "model_not_in_runner_catalog",
			"error":   fmt.Sprintf("%s is not in the live %s model catalog on this machine", model, catalog.RunnerID),
			"catalog": catalog,
		})
		return
	}

	effort := ""
	if catalog.RunnerID == "codex" {
		effort = normalizeCodexReasoningEffort(body.ReasoningEffort)
		if effort == "" {
			effort = normalizeCodexReasoningEffort(selected.DefaultReasoningEffort)
		}
		if effort == "" {
			effort = "medium"
		}
		if len(selected.SupportedReasoningEffort) > 0 {
			supported := false
			for _, option := range selected.SupportedReasoningEffort {
				if option.ReasoningEffort == effort {
					supported = true
					break
				}
			}
			if !supported {
				jsonError(w, http.StatusBadRequest, "reasoningEffort is not supported by the selected model")
				return
			}
		}
	} else if strings.TrimSpace(body.ReasoningEffort) != "" {
		jsonError(w, http.StatusBadRequest, "reasoningEffort is supported by Codex tasks only")
		return
	}

	if task.IsAdopted {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "code": "adopted_runner_model_control_unavailable",
			"error":   "This task is attached to an already-running terminal session. Yaver will not guess at terminal menu positions; choose the model in that runner's live Details view.",
			"catalog": catalog,
		})
		return
	}

	s.taskMgr.mu.Lock()
	current := s.taskMgr.tasks[task.ID]
	if current == nil {
		s.taskMgr.mu.Unlock()
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	current.Model = model
	current.ReasoningEffort = effort
	current.runner.Model = model
	current.runner.ReasoningEffort = effort
	s.taskMgr.persist()
	status := current.Status
	s.taskMgr.mu.Unlock()

	label := model
	if effort != "" {
		label += " · " + effort
	}
	s.taskMgr.present(current, taskPresentationInput{
		ID: current.ID + "-runner-control", Kind: "status", Text: "Model set to " + label + " for the next turn.",
		Phase: "conversation", State: string(status), Surface: current.LastSurface,
	})
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok": true, "schema": 1, "taskId": current.ID, "control": "model",
		"model": model, "reasoningEffort": effort, "display": label,
		"appliesTo": "next_turn", "status": status,
	})
}

func (s *HTTPServer) applyTaskExitControl(w http.ResponseWriter, task *Task, body taskRunnerControlRequest) {
	if !body.Confirmed {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "code": "confirmation_required",
			"error": "Confirm exit to stop this runner session.",
		})
		return
	}

	s.taskMgr.mu.RLock()
	current := s.taskMgr.tasks[task.ID]
	if current == nil {
		s.taskMgr.mu.RUnlock()
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	status := current.Status
	session := strings.TrimSpace(current.TmuxSession)
	isAdopted := current.IsAdopted && session != "" && s.taskMgr.TmuxMgr != nil
	isTaskOwnedTmux := taskOwnsNamedTmuxSeat(current)
	hasLiveTmuxSeat := session != "" && tmuxSessionExists(session)
	s.taskMgr.mu.RUnlock()

	// A settled turn is not the same thing as an exited conversation. Ready and
	// review tasks deliberately retain the exact tmux seat Yaver created so a
	// follow-up can reuse the runner. Probe that seat before reporting success:
	// `/exit` must close the operation, not merely believe the task status.
	if status != TaskStatusRunning && status != TaskStatusQueued && !hasLiveTmuxSeat {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok": true, "schema": 1, "taskId": task.ID, "control": "exit",
			"status": status, "verified": true, "alreadyExited": true,
		})
		return
	}

	var err error
	if isAdopted {
		err = s.taskMgr.TmuxMgr.CloseAdoptedTask(task.ID)
	} else if isTaskOwnedTmux && hasLiveTmuxSeat {
		s.taskMgr.closeTaskOwnedTmuxSeat(task.ID)
		if tmuxSessionExists(session) {
			err = fmt.Errorf("runner session %s is still present after exit", session)
		} else {
			s.taskMgr.mu.Lock()
			if stopped := s.taskMgr.tasks[task.ID]; stopped != nil {
				stopped.Status = TaskStatusStopped
				now := time.Now()
				stopped.FinishedAt = &now
				s.taskMgr.persist()
			}
			s.taskMgr.mu.Unlock()
		}
	} else {
		err = s.taskMgr.GracefulStopTask(task.ID)
	}
	if err != nil {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "code": "runner_exit_failed", "error": err.Error(), "verified": false,
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok": true, "schema": 1, "taskId": task.ID, "control": "exit",
		"status": TaskStatusStopped, "verified": true,
	})
}

func codexAppServerModels(parent context.Context) ([]runnerModelInfo, error) {
	codexModelCatalogCache.Lock()
	if len(codexModelCatalogCache.models) > 0 && time.Now().Before(codexModelCatalogCache.expires) {
		models := append([]runnerModelInfo(nil), codexModelCatalogCache.models...)
		codexModelCatalogCache.Unlock()
		return models, nil
	}
	codexModelCatalogCache.Unlock()

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	path := resolveRunnerBinary("codex")
	if path == "" {
		return nil, fmt.Errorf("codex executable is not installed on this machine")
	}
	cmd := exec.CommandContext(ctx, path, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	if err := enc.Encode(map[string]interface{}{
		"method": "initialize", "id": 1,
		"params": map[string]interface{}{"clientInfo": map[string]string{"name": "yaver", "title": "Yaver", "version": "1"}},
	}); err != nil {
		return nil, err
	}
	if _, err := scanAppServerResponse(scanner, 1, nil); err != nil {
		return nil, err
	}
	if err := enc.Encode(map[string]interface{}{"method": "initialized", "params": map[string]interface{}{}}); err != nil {
		return nil, err
	}
	if err := enc.Encode(map[string]interface{}{"method": "model/list", "id": 2, "params": map[string]interface{}{"limit": 100, "includeHidden": false}}); err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			ID                       string                      `json:"id"`
			Model                    string                      `json:"model"`
			DisplayName              string                      `json:"displayName"`
			DefaultReasoningEffort   string                      `json:"defaultReasoningEffort"`
			SupportedReasoningEffort []runnerReasoningEffortInfo `json:"supportedReasoningEfforts"`
			IsDefault                bool                        `json:"isDefault"`
		} `json:"data"`
	}
	if _, err := scanAppServerResponse(scanner, 2, &result); err != nil {
		return nil, err
	}
	models := make([]runnerModelInfo, 0, len(result.Data))
	for _, item := range result.Data {
		id := strings.TrimSpace(item.Model)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = id
		}
		models = append(models, runnerModelInfo{
			ID: id, Name: name, Source: "codex-app-server", IsDefault: item.IsDefault,
			DefaultReasoningEffort:   item.DefaultReasoningEffort,
			SupportedReasoningEffort: append([]runnerReasoningEffortInfo(nil), item.SupportedReasoningEffort...),
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("codex model/list returned no visible models")
	}
	codexModelCatalogCache.Lock()
	codexModelCatalogCache.models = append([]runnerModelInfo(nil), models...)
	codexModelCatalogCache.expires = time.Now().Add(5 * time.Minute)
	codexModelCatalogCache.Unlock()
	return models, nil
}

func scanAppServerResponse(scanner *bufio.Scanner, id int, out interface{}) (json.RawMessage, error) {
	for scanner.Scan() {
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil || envelope.ID != id {
			continue
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("codex app-server %d: %s", envelope.Error.Code, envelope.Error.Message)
		}
		if out != nil {
			if err := json.Unmarshal(envelope.Result, out); err != nil {
				return nil, err
			}
		}
		return envelope.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.ErrUnexpectedEOF
}
