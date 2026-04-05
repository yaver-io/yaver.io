package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ── Session Audit (Task Garbage Collector) ────────────────────────
// Periodically reviews completed task sessions for discussed-but-not-implemented
// items and auto-generates todo items that sync to the mobile app.

// SessionAuditor watches completed tasks and extracts missed items.
type SessionAuditor struct {
	taskMgr     *TaskManager
	todoMgr     *TodoListManager
	interval    time.Duration
	auditedTasks sync.Map // taskID -> true (already audited)
	stopCh      chan struct{}
}

// NewSessionAuditor creates a new auditor that checks for completed tasks.
func NewSessionAuditor(taskMgr *TaskManager, todoMgr *TodoListManager) *SessionAuditor {
	return &SessionAuditor{
		taskMgr:  taskMgr,
		todoMgr:  todoMgr,
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the periodic audit loop in a background goroutine.
func (sa *SessionAuditor) Start() {
	go sa.loop()
	log.Printf("[session-audit] started (interval: %v)", sa.interval)
}

// Stop signals the audit loop to exit.
func (sa *SessionAuditor) Stop() {
	close(sa.stopCh)
}

func (sa *SessionAuditor) loop() {
	// Initial delay — let agent settle before first audit
	select {
	case <-time.After(2 * time.Minute):
	case <-sa.stopCh:
		return
	}

	ticker := time.NewTicker(sa.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sa.auditCompletedTasks()
		case <-sa.stopCh:
			return
		}
	}
}

// auditCompletedTasks scans recently completed tasks for missed items.
func (sa *SessionAuditor) auditCompletedTasks() {
	tasks := sa.taskMgr.ListTasks()

	for _, info := range tasks {
		// Only audit completed/finished tasks
		if info.Status != "completed" {
			continue
		}

		// Skip already audited
		if _, done := sa.auditedTasks.Load(info.ID); done {
			continue
		}

		// Mark as audited immediately to avoid re-processing
		sa.auditedTasks.Store(info.ID, true)

		// Get full task with output
		task, ok := sa.taskMgr.GetTask(info.ID)
		if !ok || task == nil {
			continue
		}

		// Only audit tasks with substantial output (real agent sessions)
		if len(task.Output) < 500 {
			continue
		}

		// Skip tasks older than 24h
		if time.Since(task.CreatedAt) > 24*time.Hour {
			continue
		}

		go sa.auditTask(task)
	}
}

// auditTask analyzes a completed task's output for missed items.
func (sa *SessionAuditor) auditTask(task *Task) {
	log.Printf("[session-audit] auditing task %s: %s", task.ID, task.Title)

	// Extract missed items from the task output using pattern matching.
	// This is a lightweight approach that doesn't require an LLM call —
	// it looks for common patterns in agent output that indicate planned
	// but unfinished work.
	missedItems := extractMissedItems(task.Title, task.Output)

	if len(missedItems) == 0 {
		log.Printf("[session-audit] task %s: no missed items found", task.ID)
		return
	}

	log.Printf("[session-audit] task %s: found %d missed items", task.ID, len(missedItems))

	// Add each missed item to the todo list
	for _, item := range missedItems {
		meta := map[string]interface{}{
			"description": item,
			"source":      "session-audit",
			"sourceTask":  task.ID,
			"sourceTitle": task.Title,
		}
		metaJSON, _ := json.Marshal(meta)
		_, err := sa.todoMgr.AddItem(json.RawMessage(metaJSON), nil, "")
		if err != nil {
			log.Printf("[session-audit] failed to add todo: %v", err)
		}
	}
}

// extractMissedItems scans task output for patterns indicating unfinished work.
// Looks for: TODO comments, "will do later", "skipping for now", "remaining",
// "not implemented", numbered lists of planned items, etc.
func extractMissedItems(title, output string) []string {
	var items []string
	seen := make(map[string]bool)

	// Truncate very long outputs to last 4000 chars (most relevant)
	if len(output) > 4000 {
		output = output[len(output)-4000:]
	}

	lines := strings.Split(output, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Skip empty or very short lines
		if len(trimmed) < 10 {
			continue
		}

		// Pattern: explicit TODO/FIXME/HACK markers
		if strContainsAny(lower, []string{"todo:", "fixme:", "hack:", "xxx:"}) {
			item := cleanItem(trimmed)
			if item != "" && !seen[item] {
				seen[item] = true
				items = append(items, item)
			}
			continue
		}

		// Pattern: "remaining", "still need", "not yet", "skipping", "later"
		if strContainsAny(lower, []string{
			"still need to",
			"remaining:",
			"not yet implemented",
			"skipping for now",
			"will do later",
			"left to do",
			"didn't get to",
			"haven't implemented",
			"not done yet",
			"needs to be done",
			"out of scope for now",
			"deferred:",
			"postponed:",
		}) {
			item := cleanItem(trimmed)
			if item != "" && !seen[item] {
				seen[item] = true
				items = append(items, item)
			}
			continue
		}

		// Pattern: numbered list items that mention missing/remaining work
		// e.g. "3. Add dark mode support (not implemented)"
		if len(trimmed) > 3 && (trimmed[0] >= '0' && trimmed[0] <= '9') && strings.Contains(trimmed, ".") {
			if strContainsAny(lower, []string{"not implemented", "missing", "todo", "remaining", "skipped", "deferred"}) {
				item := cleanItem(trimmed)
				if item != "" && !seen[item] {
					seen[item] = true
					items = append(items, item)
				}
			}
		}
	}

	// Also check for structured JSON missed items (if agent outputs them)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "{") {
			continue
		}
		var wrapper struct {
			YaverMissed *struct {
				Description string `json:"description"`
			} `json:"yaver_missed"`
		}
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err == nil && wrapper.YaverMissed != nil {
			item := wrapper.YaverMissed.Description
			if item != "" && !seen[item] {
				seen[item] = true
				items = append(items, item)
			}
		}
	}

	return items
}

// AuditTaskNow triggers an immediate audit of a specific task (called from HTTP handler).
func (sa *SessionAuditor) AuditTaskNow(taskID string) ([]string, error) {
	task, ok := sa.taskMgr.GetTask(taskID)
	if !ok || task == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	items := extractMissedItems(task.Title, task.Output)

	// Add to todo list
	for _, item := range items {
		meta := map[string]interface{}{
			"description": item,
			"source":      "session-audit",
			"sourceTask":  task.ID,
			"sourceTitle": task.Title,
		}
		metaJSON, _ := json.Marshal(meta)
		sa.todoMgr.AddItem(json.RawMessage(metaJSON), nil, "")
	}

	sa.auditedTasks.Store(taskID, true)
	return items, nil
}

func strContainsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func cleanItem(s string) string {
	// Remove common prefixes
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"- ", "* ", "• ", "TODO:", "FIXME:", "HACK:", "XXX:"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSpace(s)
	if len(s) < 5 || len(s) > 500 {
		return ""
	}
	return s
}
