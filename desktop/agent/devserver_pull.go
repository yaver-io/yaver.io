package main

import (
	"fmt"
	"log"
	osexec "os/exec"
	"strings"
	"time"
)

const hotReloadAgentPullTimeout = 30 * time.Second

type hotReloadPullAttempt struct {
	Attempted bool
	Updated   bool
	RunnerID  string
	Status    string
	Summary   string
}

func chooseHotReloadPullRunner(defaultRunnerID string, rows []runnerAuthStatusRow) string {
	byID := map[string]runnerAuthStatusRow{}
	for _, row := range rows {
		byID[normalizeRunnerID(row.ID)] = row
	}
	candidates := []string{}
	add := func(id string) {
		id = normalizeRunnerID(id)
		if id == "" {
			return
		}
		for _, existing := range candidates {
			if existing == id {
				return
			}
		}
		candidates = append(candidates, id)
	}
	add(defaultRunnerID)
	add("claude")
	add("codex")
	add("opencode")
	for _, id := range candidates {
		row, ok := byID[id]
		if !ok {
			continue
		}
		if row.Installed && row.Ready && row.AuthConfigured {
			return id
		}
	}
	return ""
}

func collectHotReloadPullRunnerRows(workDir string) []runnerAuthStatusRow {
	runners := []struct {
		ID   string
		Name string
		Cmd  string
	}{
		{ID: "claude", Name: "Claude Code", Cmd: "claude"},
		{ID: "codex", Name: "OpenAI Codex", Cmd: "codex"},
		{ID: "opencode", Name: "OpenCode", Cmd: "opencode"},
	}
	rows := make([]runnerAuthStatusRow, 0, len(runners))
	for _, runner := range runners {
		_, err := osexec.LookPath(runner.Cmd)
		row := runnerAuthStatusRow{
			ID:        runner.ID,
			Name:      runner.Name,
			Installed: err == nil,
		}
		if err == nil {
			status := DetectRunnerRuntimeStatus(GetRunnerConfig(runner.ID), workDir)
			row.Ready = status.Ready
			row.AuthConfigured = status.AuthConfigured
			row.AuthSource = status.AuthSource
			row.Warning = status.Warning
			row.Error = status.Error
		}
		rows = append(rows, row)
	}
	return rows
}

func interpretHotReloadPullResult(text string) (status string, updated bool) {
	trimmed := strings.TrimSpace(text)
	upper := strings.ToUpper(trimmed)
	switch {
	case strings.HasPrefix(upper, "PULLED:"):
		return "pulled", true
	case strings.HasPrefix(upper, "UP_TO_DATE:"):
		return "up_to_date", false
	case strings.HasPrefix(upper, "SKIPPED:"):
		return "skipped", false
	case strings.HasPrefix(upper, "FAILED:"):
		return "failed", false
	default:
		return "unknown", false
	}
}

func summarizeHotReloadPullResult(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 220 {
		text = text[:220]
	}
	return text
}

func hotReloadPullPrompt() string {
	return strings.TrimSpace(`Prepare this repository for Yaver hot reload before the build starts.

Goal: bring the current checkout up to date from its tracked upstream if, and only if, that is safe.

Rules:
- Work only inside the provided workdir.
- Inspect the repo state first.
- If this is not a git checkout, or it has no upstream remote, stop and report that clearly.
- If the checkout is already up to date, make no changes.
- If local changes, merge/rebase/cherry-pick state, auth prompts, detached HEAD, or any other condition makes update risky, do not leave the repo in a conflicted or half-merged state. Stop and report why you skipped it.
- Do not use destructive commands. Do not reset, discard, stash, or overwrite user changes.
- Prefer the safest git path available.

Return a very short final answer that starts with exactly one of:
PULLED:
UP_TO_DATE:
SKIPPED:
FAILED:`)
}

func (s *HTTPServer) maybePullWithCodingAgentBeforeBuild(workDir string) hotReloadPullAttempt {
	result := hotReloadPullAttempt{Status: "skipped"}
	workDir = strings.TrimSpace(workDir)
	if s == nil || s.taskMgr == nil || workDir == "" {
		result.Summary = "pre-build pull skipped: unavailable"
		return result
	}
	if out, err := runGit(workDir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		result.Summary = "pre-build pull skipped: not a git worktree"
		return result
	}
	if _, err := runGit(workDir, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		result.Summary = "pre-build pull skipped: no upstream tracking branch"
		return result
	}

	rows := collectHotReloadPullRunnerRows(workDir)
	defaultRunnerID := ""
	if s.taskMgr.runner.RunnerID != "" {
		defaultRunnerID = s.taskMgr.runner.RunnerID
	}
	runnerID := chooseHotReloadPullRunner(defaultRunnerID, rows)
	if runnerID == "" {
		result.Summary = "pre-build pull skipped: no ready authenticated coding runner"
		return result
	}

	task, err := s.taskMgr.CreateTaskWithOptions(
		"Pre-build git update",
		hotReloadPullPrompt(),
		"",
		"devserver-prepull",
		runnerID,
		"",
		nil,
		TaskCreateOptions{WorkDir: workDir},
	)
	if err != nil {
		result.RunnerID = runnerID
		result.Status = "failed"
		result.Summary = fmt.Sprintf("pre-build pull via %s failed to start: %v", runnerID, err)
		return result
	}

	result.Attempted = true
	result.RunnerID = runnerID
	deadline := time.Now().Add(hotReloadAgentPullTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		taskState, ok := s.taskMgr.GetTask(task.ID)
		if !ok || taskState == nil {
			result.Status = "failed"
			result.Summary = fmt.Sprintf("pre-build pull via %s lost task state", runnerID)
			return result
		}
		switch taskState.Status {
		case TaskStatusQueued, TaskStatusRunning:
			continue
		case TaskStatusFinished, TaskStatusFailed, TaskStatusStopped:
			finalText := strings.TrimSpace(firstNonEmpty(taskState.ResultText, taskState.Output))
			result.Status, result.Updated = interpretHotReloadPullResult(finalText)
			result.Summary = summarizeHotReloadPullResult(finalText)
			if result.Summary == "" {
				result.Summary = fmt.Sprintf("pre-build pull via %s ended with %s", runnerID, taskState.Status)
			}
			if taskState.Status == TaskStatusFailed && result.Status == "unknown" {
				result.Status = "failed"
			}
			return result
		}
	}

	_ = s.taskMgr.StopTask(task.ID)
	result.Status = "timed_out"
	result.Summary = fmt.Sprintf("pre-build pull via %s timed out after %s", runnerID, hotReloadAgentPullTimeout)
	return result
}

func (s *HTTPServer) maybePullBeforeHotReloadBuild(workDir string) {
	if s == nil || strings.TrimSpace(workDir) == "" {
		return
	}
	attempt := s.maybePullWithCodingAgentBeforeBuild(workDir)
	log.Printf("[dev] %s", attempt.Summary)
	if s.devServerMgr != nil && strings.TrimSpace(attempt.Summary) != "" {
		s.devServerMgr.EmitLog("[pre-build-pull] " + attempt.Summary)
	}
}
