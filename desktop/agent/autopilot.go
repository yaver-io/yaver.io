package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AutopilotManager orchestrates batch todo execution in an agent-agnostic way.
//
// Yaver owns the state — the AI agent (Claude, Aider, Codex, Ollama, etc.) is
// just a worker that gets fresh tasks with full context each turn. No --resume,
// no session IDs, no agent-specific flags.
//
// State is persisted to ~/.yaver/autopilot.json so it survives agent restarts.
//
// Flow:
//   1. Mobile saves todos locally, detects agent is online, triggers autopilot.
//   2. AutopilotManager creates a task with the full manifest.
//   3. When the agent finishes, Yaver checks todolist state, builds a new task
//      with: "DONE: X, Y. REMAINING: A, B. Previous output summary: ..."
//   4. Stall timer stops hung tasks after N minutes.
//   5. Repeat until all items are done.

const (
	autopilotStallTimeout = 10 * time.Minute
	autopilotStateFile    = "autopilot.json"
)

// AutopilotState is persisted to disk so autopilot survives restarts.
type AutopilotState struct {
	Enabled    bool              `json:"enabled"`
	RunID      string            `json:"runId,omitempty"`
	StartedAt  *time.Time        `json:"startedAt,omitempty"`
	Items      []AutopilotItem   `json:"items,omitempty"`
	TaskID     string            `json:"currentTaskId,omitempty"`
	TurnCount  int               `json:"turnCount"`
	TotalCost  float64           `json:"totalCost"`
}

// AutopilotItem tracks one todo item through the autopilot run.
type AutopilotItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"` // "pending", "implementing", "done", "failed"
	TaskID      string `json:"taskId,omitempty"`
}

// AutopilotManager manages the auto-driving lifecycle.
type AutopilotManager struct {
	mu          sync.RWMutex
	state       AutopilotState
	stateFile   string
	stallTimer  *time.Timer
	taskMgr     *TaskManager
	todolistMgr *TodoListManager
}

// NewAutopilotManager creates an autopilot manager and loads persisted state.
func NewAutopilotManager(taskMgr *TaskManager, todolistMgr *TodoListManager) *AutopilotManager {
	dir, _ := ConfigDir()
	stateFile := filepath.Join(dir, autopilotStateFile)

	am := &AutopilotManager{
		stateFile:   stateFile,
		taskMgr:     taskMgr,
		todolistMgr: todolistMgr,
	}

	// Load persisted state
	if data, err := os.ReadFile(stateFile); err == nil {
		if json.Unmarshal(data, &am.state) == nil && am.state.Enabled {
			log.Printf("[autopilot] Restored state: enabled=%v, items=%d, turn=%d",
				am.state.Enabled, len(am.state.Items), am.state.TurnCount)
		}
	}

	return am
}

func (am *AutopilotManager) persist() {
	data, err := json.MarshalIndent(am.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(am.stateFile, data, 0600)
}

// IsEnabled returns whether autopilot mode is active.
func (am *AutopilotManager) IsEnabled() bool {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.state.Enabled
}

// Enable starts an autopilot run with all pending todos.
func (am *AutopilotManager) Enable() {
	am.mu.Lock()
	am.state.Enabled = true
	am.persist()
	am.mu.Unlock()

	log.Printf("[autopilot] Enabled")
	go am.startRun()
}

// Disable stops the autopilot.
func (am *AutopilotManager) Disable() {
	am.mu.Lock()
	am.state.Enabled = false
	if am.stallTimer != nil {
		am.stallTimer.Stop()
		am.stallTimer = nil
	}
	am.persist()
	am.mu.Unlock()
	log.Printf("[autopilot] Disabled")
}

// State returns the current autopilot state (for API responses).
func (am *AutopilotManager) State() AutopilotState {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.state
}

// startRun kicks off the autopilot with all pending todo items.
func (am *AutopilotManager) startRun() {
	if am.todolistMgr == nil || am.taskMgr == nil {
		return
	}

	pending := am.todolistMgr.PendingItems()
	if len(pending) == 0 {
		log.Printf("[autopilot] No pending todos — nothing to do")
		am.Disable()
		return
	}

	// Build manifest
	am.mu.Lock()
	now := time.Now()
	am.state.RunID = fmt.Sprintf("ap-%d", now.UnixMilli())
	am.state.StartedAt = &now
	am.state.TurnCount = 0
	am.state.TotalCost = 0
	am.state.Items = make([]AutopilotItem, len(pending))
	for i, item := range pending {
		am.state.Items[i] = AutopilotItem{
			ID:          item.ID,
			Description: item.Description,
			Status:      "pending",
		}
	}
	am.persist()
	am.mu.Unlock()

	log.Printf("[autopilot] Starting run %s with %d items", am.state.RunID, len(pending))
	am.createNextTask(pending, nil)
}

// createNextTask creates a new task for the given items, optionally including
// context from a previous turn's output.
func (am *AutopilotManager) createNextTask(items []*TodoItem, prevSummary *string) {
	// Build prompt with full context
	prompt := am.buildPrompt(items, prevSummary)

	// Collect screenshots from items
	var images []ImageAttachment
	for _, item := range items {
		for _, ssPath := range item.Screenshots {
			data, err := os.ReadFile(ssPath)
			if err == nil {
				images = append(images, ImageAttachment{
					Base64:   base64.StdEncoding.EncodeToString(data),
					MimeType: mimeTypeFromPath(ssPath),
					Filename: filepath.Base(ssPath),
				})
			}
		}
	}

	// Collect item IDs for marking
	itemIDs := make([]string, len(items))
	for i, item := range items {
		itemIDs[i] = item.ID
	}

	task, err := am.taskMgr.CreateTask(prompt, "", "", "todolist", "", "", images)
	if err != nil {
		log.Printf("[autopilot] Failed to create task: %v", err)
		am.Disable()
		return
	}

	// Mark items as implementing
	am.todolistMgr.MarkImplementing(itemIDs, task.ID)

	am.mu.Lock()
	am.state.TaskID = task.ID
	am.state.TurnCount++
	for i := range am.state.Items {
		for _, id := range itemIDs {
			if am.state.Items[i].ID == id {
				am.state.Items[i].Status = "implementing"
				am.state.Items[i].TaskID = task.ID
			}
		}
	}
	am.persist()
	am.mu.Unlock()

	log.Printf("[autopilot] Turn %d: task %s created for %d items", am.state.TurnCount, task.ID, len(items))
	am.resetStallTimer()
}

// buildPrompt constructs the task prompt. On the first turn it's a batch fix
// prompt. On subsequent turns it includes a progress report + previous output summary.
func (am *AutopilotManager) buildPrompt(remaining []*TodoItem, prevSummary *string) string {
	am.mu.RLock()
	turn := am.state.TurnCount
	items := am.state.Items
	am.mu.RUnlock()

	var sb strings.Builder

	if turn == 0 {
		// First turn — standard batch prompt
		sb.WriteString(am.todolistMgr.GenerateBatchFixPrompt(remaining))
	} else {
		// Follow-up turn — include progress report
		sb.WriteString("You are continuing an automated task queue.\n\n")

		// Show completed items
		var doneItems []AutopilotItem
		for _, item := range items {
			if item.Status == "done" {
				doneItems = append(doneItems, item)
			}
		}
		if len(doneItems) > 0 {
			sb.WriteString("COMPLETED by previous agent run:\n")
			for i, item := range doneItems {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, truncate(item.Description, 100)))
			}
			sb.WriteString("\n")
		}

		// Show failed items
		var failedItems []AutopilotItem
		for _, item := range items {
			if item.Status == "failed" {
				failedItems = append(failedItems, item)
			}
		}
		if len(failedItems) > 0 {
			sb.WriteString("FAILED (skip these):\n")
			for i, item := range failedItems {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, truncate(item.Description, 100)))
			}
			sb.WriteString("\n")
		}

		// Show remaining items
		sb.WriteString(fmt.Sprintf("REMAINING (%d items) — work on these now:\n", len(remaining)))
		for i, item := range remaining {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, truncate(item.Description, 100)))
		}

		// Include summary from previous run if available
		if prevSummary != nil && *prevSummary != "" {
			sb.WriteString("\nContext from previous agent run (last 2000 chars of output):\n")
			summary := *prevSummary
			if len(summary) > 2000 {
				summary = summary[len(summary)-2000:]
			}
			sb.WriteString("```\n")
			sb.WriteString(summary)
			sb.WriteString("\n```\n")
		}

		sb.WriteString("\nFix all remaining items listed above.")
	}

	sb.WriteString(autopilotContext())
	return sb.String()
}

// OnTaskDone is called when any todolist task finishes. The autopilot checks
// progress and either creates a new task for remaining items or shuts down.
func (am *AutopilotManager) OnTaskDone(task *Task) {
	am.mu.RLock()
	isTracked := am.state.TaskID == task.ID
	am.mu.RUnlock()

	if !isTracked {
		return
	}

	// Stop stall timer
	am.mu.Lock()
	if am.stallTimer != nil {
		am.stallTimer.Stop()
		am.stallTimer = nil
	}
	am.state.TotalCost += task.CostUSD
	am.persist()
	am.mu.Unlock()

	// Sync autopilot items with todolist state
	am.syncItemStatuses()

	am.mu.RLock()
	var doneCount, failedCount, remainingCount int
	for _, item := range am.state.Items {
		switch item.Status {
		case "done":
			doneCount++
		case "failed":
			failedCount++
		default:
			remainingCount++
		}
	}
	am.mu.RUnlock()

	log.Printf("[autopilot] Task %s finished (status=%s). Done=%d, Failed=%d, Remaining=%d, Cost=$%.4f",
		task.ID, task.Status, doneCount, failedCount, remainingCount, task.CostUSD)

	// Task failed/stopped — mark implementing items as failed
	if task.Status == TaskStatusFailed || task.Status == TaskStatusStopped {
		am.markImplementingAs("failed", task.ID)
		am.syncItemStatuses()

		// Recount remaining
		remaining := am.getRemainingTodoItems()
		if len(remaining) == 0 {
			log.Printf("[autopilot] All items processed — done (cost=$%.4f)", am.state.TotalCost)
			am.Disable()
			return
		}
		log.Printf("[autopilot] Task failed — starting fresh for %d remaining items", len(remaining))
		// Reset failed implementing items back to pending
		am.resetImplementingToPending(task.ID)
		summary := task.Output
		go am.createNextTask(remaining, &summary)
		return
	}

	// Task finished — check what remains
	remaining := am.getRemainingTodoItems()
	if len(remaining) == 0 {
		// Mark any leftover implementing items as done
		am.markImplementingAs("done", task.ID)
		am.syncItemStatuses()
		log.Printf("[autopilot] All items completed in %d turns (cost=$%.4f)", am.state.TurnCount, am.state.TotalCost)
		am.Disable()
		return
	}

	// More work — create a new task with context from this run
	log.Printf("[autopilot] %d items remaining — creating turn %d", len(remaining), am.state.TurnCount+1)
	// Mark current implementing items as done (the agent finished them)
	am.markImplementingAs("done", task.ID)
	am.syncItemStatuses()
	summary := task.Output
	go am.createNextTask(remaining, &summary)
}

// syncItemStatuses syncs autopilot item statuses from the todolist manager.
func (am *AutopilotManager) syncItemStatuses() {
	if am.todolistMgr == nil {
		return
	}
	items := am.todolistMgr.ListItems()
	statusMap := make(map[string]string, len(items))
	for _, item := range items {
		statusMap[item.ID] = string(item.Status)
	}

	am.mu.Lock()
	for i := range am.state.Items {
		if s, ok := statusMap[am.state.Items[i].ID]; ok {
			am.state.Items[i].Status = s
		}
	}
	am.persist()
	am.mu.Unlock()
}

// markImplementingAs marks all items with "implementing" status linked to a
// specific task as done or failed (in the todolist manager).
func (am *AutopilotManager) markImplementingAs(status string, taskID string) {
	if am.todolistMgr == nil {
		return
	}
	items := am.todolistMgr.ListItems()
	var ids []string
	for _, item := range items {
		if item.TaskID == taskID && item.Status == TodoStatusImplementing {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	switch status {
	case "done":
		am.todolistMgr.MarkDone(ids)
	case "failed":
		am.todolistMgr.MarkFailed(ids)
	}
}

// resetImplementingToPending resets items that were being implemented by a failed
// task back to pending so they can be retried.
func (am *AutopilotManager) resetImplementingToPending(taskID string) {
	if am.todolistMgr == nil {
		return
	}
	items := am.todolistMgr.ListItems()
	for _, item := range items {
		if item.TaskID == taskID && item.Status == TodoStatusImplementing {
			if todoItem, ok := am.todolistMgr.GetItem(item.ID); ok {
				todoItem.Status = TodoStatusPending
				todoItem.TaskID = ""
				am.todolistMgr.saveItem(todoItem)
			}
		}
	}
}

// getRemainingTodoItems returns todo items that are still pending.
func (am *AutopilotManager) getRemainingTodoItems() []*TodoItem {
	if am.todolistMgr == nil {
		return nil
	}

	am.mu.RLock()
	runItemIDs := make(map[string]bool, len(am.state.Items))
	for _, item := range am.state.Items {
		runItemIDs[item.ID] = true
	}
	am.mu.RUnlock()

	var remaining []*TodoItem
	for id := range runItemIDs {
		if item, ok := am.todolistMgr.GetItem(id); ok {
			if item.Status == TodoStatusPending || item.Status == TodoStatusImplementing {
				remaining = append(remaining, item)
			}
		}
	}
	return remaining
}

// resetStallTimer sets up the stall detection timer.
func (am *AutopilotManager) resetStallTimer() {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.stallTimer != nil {
		am.stallTimer.Stop()
	}
	taskID := am.state.TaskID
	am.stallTimer = time.AfterFunc(autopilotStallTimeout, func() {
		am.handleStall(taskID)
	})
}

// handleStall stops a hung task so OnTaskDone fires and moves to the next item.
func (am *AutopilotManager) handleStall(taskID string) {
	if !am.IsEnabled() {
		return
	}

	am.mu.RLock()
	current := am.state.TaskID
	am.mu.RUnlock()

	if current != taskID {
		return
	}

	task, ok := am.taskMgr.GetTask(taskID)
	if !ok || task == nil {
		return
	}

	if task.Status == TaskStatusRunning {
		log.Printf("[autopilot] Task %s stalled after %v — stopping", taskID, autopilotStallTimeout)
		_ = am.taskMgr.StopTask(taskID)
	}
}

// --- HTTP handler ---

// handleAutopilot handles GET/POST /autopilot.
func (s *HTTPServer) handleAutopilot(w http.ResponseWriter, r *http.Request) {
	if s.autopilot == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "autopilot not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		state := s.autopilot.State()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"enabled":   state.Enabled,
			"runId":     state.RunID,
			"turnCount": state.TurnCount,
			"totalCost": state.TotalCost,
			"items":     state.Items,
		})
	case http.MethodPost:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		if req.Enabled {
			s.autopilot.Enable()
		} else {
			s.autopilot.Disable()
		}

		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"enabled": req.Enabled,
		})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
