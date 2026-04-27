package main

import (
	"fmt"
	"strings"
)

// code_hybrid_session.go sketches the session-level orchestration model for
// "one vibing terminal, many underlying runners". The key idea is that Yaver
// owns the canonical task/session state and can fork bounded child work to a
// different runner with a compacted context package instead of asking the user
// to manually restate the whole thread.

type CodeForkMode string

const (
	CodeForkOverlay CodeForkMode = "overlay"
	CodeForkInline  CodeForkMode = "inline"
)

type CodeForkRequest struct {
	ParentTaskID string       `json:"parentTaskId"`
	RunnerID     string       `json:"runnerId"`
	Model        string       `json:"model,omitempty"`
	Prompt       string       `json:"prompt"`
	Mode         CodeForkMode `json:"mode,omitempty"`
	WorkDir      string       `json:"workDir,omitempty"`
}

type CodeCompactContext struct {
	TaskID         string   `json:"taskId"`
	SessionID      string   `json:"sessionId,omitempty"`
	RunnerID       string   `json:"runnerId,omitempty"`
	Model          string   `json:"model,omitempty"`
	WorkDir        string   `json:"workDir,omitempty"`
	Title          string   `json:"title,omitempty"`
	UserIntent     string   `json:"userIntent,omitempty"`
	RecentTurns    []string `json:"recentTurns,omitempty"`
	LastResult     string   `json:"lastResult,omitempty"`
	AttachmentHint []string `json:"attachmentHints,omitempty"`
}

// buildCodeCompactContext extracts the smallest useful package of context to
// hand to another runner. This is intentionally lossy: frontier sessions can be
// long, but delegated child work should be bounded and file-scoped.
func buildCodeCompactContext(task *Task, maxTurns int) CodeCompactContext {
	if task == nil {
		return CodeCompactContext{}
	}
	if maxTurns <= 0 {
		maxTurns = 6
	}
	ctx := CodeCompactContext{
		TaskID:     task.ID,
		SessionID:  task.SessionID,
		RunnerID:   task.RunnerID,
		Model:      task.Model,
		WorkDir:    strings.TrimSpace(task.WorkDir),
		Title:      strings.TrimSpace(task.Title),
		UserIntent: truncateCodeCompactField(strings.TrimSpace(firstNonEmpty(lastUserTurn(task.Turns), strings.TrimSpace(task.Description), strings.TrimSpace(task.Title))), 1200),
		LastResult: truncateCodeCompactField(strings.TrimSpace(task.ResultText), 2400),
	}
	start := 0
	if len(task.Turns) > maxTurns {
		start = len(task.Turns) - maxTurns
	}
	for _, turn := range task.Turns[start:] {
		role := strings.TrimSpace(turn.Role)
		if role == "" {
			role = "turn"
		}
		ctx.RecentTurns = append(ctx.RecentTurns, fmt.Sprintf("%s: %s", role, truncateCodeCompactField(strings.TrimSpace(turn.Content), 700)))
	}
	for i, path := range task.ImagePaths {
		if i >= 4 {
			break
		}
		ctx.AttachmentHint = append(ctx.AttachmentHint, path)
	}
	return ctx
}

func renderCodeCompactContextPrompt(ctx CodeCompactContext) string {
	var lines []string
	lines = append(lines, "[Compacted parent context]")
	if ctx.TaskID != "" || ctx.SessionID != "" {
		lines = append(lines, fmt.Sprintf("Parent task: %s  Session: %s", firstNonEmpty(ctx.TaskID, "n/a"), firstNonEmpty(ctx.SessionID, "n/a")))
	}
	if ctx.RunnerID != "" || ctx.Model != "" {
		lines = append(lines, fmt.Sprintf("Current runner: %s  Model: %s", firstNonEmpty(ctx.RunnerID, "n/a"), firstNonEmpty(ctx.Model, "default")))
	}
	if ctx.WorkDir != "" {
		lines = append(lines, "Work dir: "+ctx.WorkDir)
	}
	if ctx.Title != "" {
		lines = append(lines, "Original task: "+ctx.Title)
	}
	if ctx.UserIntent != "" {
		lines = append(lines, "Current user intent: "+ctx.UserIntent)
	}
	if len(ctx.RecentTurns) > 0 {
		lines = append(lines, "Recent turns:")
		lines = append(lines, ctx.RecentTurns...)
	}
	if ctx.LastResult != "" {
		lines = append(lines, "Most recent result:")
		lines = append(lines, ctx.LastResult)
	}
	if len(ctx.AttachmentHint) > 0 {
		lines = append(lines, "Relevant attachments:")
		lines = append(lines, ctx.AttachmentHint...)
	}
	lines = append(lines,
		"Operate as a delegated child runner.",
		"Do only the scoped work requested by the child prompt.",
		"Return concise progress and preserve native runner output style when possible.")
	return strings.Join(lines, "\n")
}

func lastUserTurn(turns []ConversationTurn) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(turns[i].Role), "user") {
			return truncateCodeCompactField(turns[i].Content, 1200)
		}
	}
	return ""
}

func truncateCodeCompactField(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

