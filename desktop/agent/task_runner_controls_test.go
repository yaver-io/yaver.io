package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runnerControlTestServer(t *testing.T, runnerID string) (*HTTPServer, *TaskManager, *Task) {
	t.Helper()
	runner := GetRunnerConfig(runnerID)
	tm := NewTaskManager(t.TempDir(), nil, runner)
	task := &Task{
		ID: "runner-control-task", Title: "Control test", Status: TaskStatusReady,
		RunnerID: runner.RunnerID, runner: runner, Model: runner.Model,
		eventCh: make(chan map[string]interface{}, 8), doneCh: make(chan struct{}),
	}
	tm.tasks[task.ID] = task
	return &HTTPServer{taskMgr: tm}, tm, task
}

func TestTaskRunnerControlModelIsTypedAndTaskScoped(t *testing.T) {
	s, tm, task := runnerControlTestServer(t, "codex")
	codexModelCatalogCache.Lock()
	previousModels, previousExpiry := codexModelCatalogCache.models, codexModelCatalogCache.expires
	codexModelCatalogCache.models = []runnerModelInfo{{
		ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", IsDefault: true,
		DefaultReasoningEffort: "medium", SupportedReasoningEffort: codexReasoningEffortOptionsForModel("gpt-5.6-sol"),
	}}
	codexModelCatalogCache.expires = time.Now().Add(time.Minute)
	codexModelCatalogCache.Unlock()
	t.Cleanup(func() {
		codexModelCatalogCache.Lock()
		codexModelCatalogCache.models, codexModelCatalogCache.expires = previousModels, previousExpiry
		codexModelCatalogCache.Unlock()
	})

	getReq := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/control", nil)
	getRec := httptest.NewRecorder()
	s.handleTaskByID(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET control = %d: %s", getRec.Code, getRec.Body.String())
	}
	var catalog taskRunnerControlCatalog
	if err := json.Unmarshal(getRec.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.RunnerID != "codex" || len(catalog.Models) != 1 || catalog.Models[0].ID != "gpt-5.6-sol" {
		t.Fatalf("catalog did not come from the task runner: %+v", catalog)
	}

	body := []byte(`{"control":"model","model":"gpt-5.6-sol","reasoningEffort":"max"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/control", bytes.NewReader(body))
	postRec := httptest.NewRecorder()
	s.handleTaskByID(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST model = %d: %s", postRec.Code, postRec.Body.String())
	}
	tm.mu.RLock()
	gotModel, gotEffort := task.Model, task.ReasoningEffort
	tm.mu.RUnlock()
	if gotModel != "gpt-5.6-sol" || gotEffort != "max" {
		t.Fatalf("task model control not persisted: model=%q effort=%q", gotModel, gotEffort)
	}
}

// The Convex-owned product catalog exposes max only on GPT-5.6 variants. The
// typed boundary remains forward-compatible with a live runner extension, but
// the offline UI fallback must not invent that extension.
func TestCodexReasoningLevelsIncludeCurrentLiveCatalog(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
		if got := normalizeCodexReasoningEffort(effort); got != effort {
			t.Errorf("normalizeCodexReasoningEffort(%q) = %q, want %q", effort, got, effort)
		}
	}

	available := map[string]bool{}
	for _, option := range codexReasoningEffortOptionsForModel("gpt-5.6-sol") {
		available[option.ReasoningEffort] = true
	}
	for _, effort := range []string{"max"} {
		if !available[effort] {
			t.Errorf("fallback catalog is missing live Codex effort %q", effort)
		}
	}
	if available["ultra"] || available["none"] {
		t.Fatalf("fallback catalog exposed non-product options: %#v", available)
	}
	for _, option := range codexReasoningEffortOptionsForModel("gpt-5.5") {
		if option.ReasoningEffort == "max" || option.ReasoningEffort == "ultra" {
			t.Errorf("fallback advertised unsupported %q for gpt-5.5", option.ReasoningEffort)
		}
	}
}

func TestCodexReasoningValidationUsesConvexMatrix(t *testing.T) {
	previous := GetCachedModels()
	LoadModelsFromBackend([]BackendModel{
		{
			RunnerID: "codex", ModelID: "gpt-5.3-codex-spark",
			SupportedReasoningEffort: []string{"low", "medium", "high", "xhigh"},
		},
		{
			RunnerID: "codex", ModelID: "gpt-5.6-sol",
			SupportedReasoningEffort: []string{"low", "medium", "high", "xhigh", "max"},
		},
	})
	t.Cleanup(func() { LoadModelsFromBackend(previous) })

	if err := validateCodexReasoningEffortForModel("gpt-5.6-sol", "more reasoning"); err != nil {
		t.Fatalf("gpt-5.6-sol max should be accepted: %v", err)
	}
	if err := validateCodexReasoningEffortForModel("gpt-5.3-codex-spark", "max"); err == nil {
		t.Fatal("gpt-5.3-codex-spark must reject max when Convex does not advertise it")
	}
	if err := validateCodexReasoningEffortForModel("future-live-model", "max"); err != nil {
		t.Fatalf("an unknown live model should be left to the runner: %v", err)
	}
}

func TestRunnerControlModelsPrefersThisMachinesOpenCodeCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	invalidateOpenCodeModelProbe()
	t.Cleanup(invalidateOpenCodeModelProbe)

	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{
  "model": "local-provider/deepseek-v4-flash",
  "provider": {
    "local-provider": {
      "name": "This machine",
      "models": {
        "deepseek-v4-flash": { "name": "DeepSeek V4 Flash" }
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := GetCachedModels()
	LoadModelsFromBackend([]BackendModel{{
		RunnerID: "opencode", ModelID: "stale-backend/model", Name: "Stale backend model", IsDefault: true,
	}})
	t.Cleanup(func() { LoadModelsFromBackend(previous) })

	models, source := runnerControlModels(t.Context(), "opencode")
	if source != "opencode-config" {
		t.Fatalf("source = %q, want this machine's opencode-config", source)
	}
	if len(models) != 1 || models[0].ID != "local-provider/deepseek-v4-flash" {
		t.Fatalf("models = %#v, want only the model configured on this machine", models)
	}
}

func TestRunnerControlModelsTreatsRemotelessAsOpenCodeBacked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	invalidateOpenCodeModelProbe()
	t.Cleanup(invalidateOpenCodeModelProbe)
	previous := GetCachedModels()
	LoadModelsFromBackend(nil)
	t.Cleanup(func() { LoadModelsFromBackend(previous) })

	models, source := runnerControlModels(t.Context(), "remoteless")
	if source != "builtin" || len(models) == 0 {
		t.Fatalf("source=%q models=%#v, want OpenCode-backed fallback catalog", source, models)
	}
	if models[0].ID != "deepseek/deepseek-flash" {
		t.Fatalf("default model = %q, want deepseek/deepseek-flash", models[0].ID)
	}
}

func TestTaskRunnerControlRejectsUnknownModelAndUnconfirmedExit(t *testing.T) {
	s, _, task := runnerControlTestServer(t, "claude")

	for name, body := range map[string]string{
		"unknown model":    `{"control":"model","model":"not-real"}`,
		"unconfirmed exit": `{"control":"exit"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/control", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()
			s.handleTaskByID(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
			}
			var result map[string]interface{}
			_ = json.Unmarshal(rec.Body.Bytes(), &result)
			if result["ok"] != false || result["code"] == "" {
				t.Fatalf("missing structured failure: %v", result)
			}
		})
	}
}

func TestTaskRunnerControlConfirmedExitReportsAlreadyExited(t *testing.T) {
	s, _, task := runnerControlTestServer(t, "opencode")
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/control", bytes.NewBufferString(`{"control":"exit","confirmed":true}`))
	rec := httptest.NewRecorder()
	s.handleTaskByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("exit = %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		OK            bool `json:"ok"`
		Verified      bool `json:"verified"`
		AlreadyExited bool `json:"alreadyExited"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if !result.OK || !result.Verified || !result.AlreadyExited {
		t.Fatalf("exit did not report verified terminal state: %+v", result)
	}
}

func TestTaskRunnerControlExitClosesSettledLiveSeat(t *testing.T) {
	if _, err := exec.LookPath(tmuxCmdName()); err != nil {
		t.Skip("tmux is not installed")
	}
	s, _, task := runnerControlTestServer(t, "opencode")
	session := automaticTaskTmuxSessionName(task.ID, task.RunnerID)
	cleanup := createTestTmuxSession(t, session)
	t.Cleanup(cleanup)
	task.TmuxSession = session
	task.Status = TaskStatusReview

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/control", bytes.NewBufferString(`{"control":"exit","confirmed":true}`))
	rec := httptest.NewRecorder()
	s.handleTaskByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("exit = %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		OK            bool `json:"ok"`
		Verified      bool `json:"verified"`
		AlreadyExited bool `json:"alreadyExited"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if !result.OK || !result.Verified || result.AlreadyExited {
		t.Fatalf("exit did not verify the live seat was closed: %+v", result)
	}
	if tmuxSessionExists(session) {
		t.Fatalf("runner seat %q is still live after verified exit", session)
	}
}
