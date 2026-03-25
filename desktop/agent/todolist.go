package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TodoStatus represents the state of a todo item.
type TodoStatus string

const (
	TodoStatusPending      TodoStatus = "pending"
	TodoStatusImplementing TodoStatus = "implementing"
	TodoStatusDone         TodoStatus = "done"
	TodoStatusFailed       TodoStatus = "failed"
)

// TodoItem represents a queued bug/fix collected during testing.
type TodoItem struct {
	ID            string          `json:"id"`
	Description   string          `json:"description"`
	Screenshots   []string        `json:"screenshots,omitempty"`
	AudioPath     string          `json:"audioPath,omitempty"`
	BlackBoxSnap  string          `json:"blackboxSnap,omitempty"`
	Errors        []CapturedError `json:"errors,omitempty"`
	Source        string          `json:"source"`     // "sdk" or "mobile"
	DeviceInfo    DeviceFBInfo    `json:"deviceInfo"`
	Status        TodoStatus      `json:"status"`
	TaskID        string          `json:"taskId,omitempty"`
	CreatedAt     string          `json:"createdAt"`
	ImplementedAt string          `json:"implementedAt,omitempty"`
}

// TodoItemSummary is a lightweight representation for list responses.
type TodoItemSummary struct {
	ID             string     `json:"id"`
	Description    string     `json:"description"`
	Status         TodoStatus `json:"status"`
	NumScreenshots int        `json:"numScreenshots"`
	HasAudio       bool       `json:"hasAudio"`
	CreatedAt      string     `json:"createdAt"`
	TaskID         string     `json:"taskId,omitempty"`
}

// TodoListManager stores and manages queued bug reports for batch implementation.
type TodoListManager struct {
	mu          sync.RWMutex
	items       map[string]*TodoItem
	baseDir     string // ~/.yaver/todolist/
	autoConsume bool   // when true, items are implemented immediately on add
	onNewItem   func(item *TodoItem) // callback when a new item is added (for auto-consume)
}

// NewTodoListManager creates a new todo list manager.
func NewTodoListManager() (*TodoListManager, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Join(dir, "todolist")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, err
	}

	tm := &TodoListManager{
		items:       make(map[string]*TodoItem),
		baseDir:     baseDir,
		autoConsume: true, // auto-consume enabled by default
	}
	tm.loadExisting()
	return tm, nil
}

// loadExisting scans the todolist directory for existing items.
func (tm *TodoListManager) loadExisting() {
	entries, err := os.ReadDir(tm.baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(tm.baseDir, e.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var item TodoItem
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}
		tm.items[item.ID] = &item
	}
}

// AddItem stores a new todo item with its files.
func (tm *TodoListManager) AddItem(metadata json.RawMessage, files map[string][]byte, blackboxSnap string) (*TodoItem, error) {
	var item TodoItem
	if err := json.Unmarshal(metadata, &item); err != nil {
		return nil, fmt.Errorf("invalid metadata: %w", err)
	}

	if item.ID == "" {
		item.ID = uuid.New().String()[:8]
	}
	if item.CreatedAt == "" {
		item.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	item.Status = TodoStatusPending
	item.BlackBoxSnap = blackboxSnap

	// Create item directory
	itemDir := filepath.Join(tm.baseDir, item.ID)
	if err := os.MkdirAll(itemDir, 0700); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	// Save files
	for name, data := range files {
		filePath := filepath.Join(itemDir, name)
		if err := os.WriteFile(filePath, data, 0600); err != nil {
			log.Printf("[todolist] failed to write %s: %v", name, err)
			continue
		}

		switch {
		case strings.HasSuffix(name, ".m4a") || strings.HasSuffix(name, ".aac") || strings.HasSuffix(name, ".wav"):
			item.AudioPath = filePath
		case strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png"):
			item.Screenshots = append(item.Screenshots, filePath)
		}
	}

	// Save blackbox snapshot to file
	if blackboxSnap != "" {
		os.WriteFile(filepath.Join(itemDir, "blackbox_snapshot.txt"), []byte(blackboxSnap), 0600)
	}

	// Save metadata
	tm.saveItem(&item)

	tm.mu.Lock()
	tm.items[item.ID] = &item
	tm.mu.Unlock()

	log.Printf("[todolist] Added item %s: %q (screenshots=%d)", item.ID, truncate(item.Description, 60), len(item.Screenshots))

	// Trigger auto-consume callback if set
	if tm.autoConsume && tm.onNewItem != nil {
		go tm.onNewItem(&item)
	}

	return &item, nil
}

// GetItem returns an item by ID.
func (tm *TodoListManager) GetItem(id string) (*TodoItem, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	item, ok := tm.items[id]
	return item, ok
}

// ListItems returns summaries of all items, sorted by creation time (newest first).
func (tm *TodoListManager) ListItems() []TodoItemSummary {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]TodoItemSummary, 0, len(tm.items))
	for _, item := range tm.items {
		result = append(result, TodoItemSummary{
			ID:             item.ID,
			Description:    item.Description,
			Status:         item.Status,
			NumScreenshots: len(item.Screenshots),
			HasAudio:       item.AudioPath != "",
			CreatedAt:      item.CreatedAt,
			TaskID:         item.TaskID,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

// PendingItems returns only items with pending status.
func (tm *TodoListManager) PendingItems() []*TodoItem {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []*TodoItem
	for _, item := range tm.items {
		if item.Status == TodoStatusPending {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result
}

// Count returns the number of pending items.
func (tm *TodoListManager) Count() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	count := 0
	for _, item := range tm.items {
		if item.Status == TodoStatusPending {
			count++
		}
	}
	return count
}

// RemoveItem removes an item and its files.
func (tm *TodoListManager) RemoveItem(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, ok := tm.items[id]; !ok {
		return fmt.Errorf("todo item %q not found", id)
	}

	os.RemoveAll(filepath.Join(tm.baseDir, id))
	delete(tm.items, id)
	return nil
}

// ClearAll removes all items.
func (tm *TodoListManager) ClearAll() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	count := len(tm.items)
	for id := range tm.items {
		os.RemoveAll(filepath.Join(tm.baseDir, id))
	}
	tm.items = make(map[string]*TodoItem)
	return count
}

// MarkImplementing marks items as implementing and links them to a task.
func (tm *TodoListManager) MarkImplementing(ids []string, taskID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range ids {
		if item, ok := tm.items[id]; ok {
			item.Status = TodoStatusImplementing
			item.TaskID = taskID
			item.ImplementedAt = now
			tm.saveItemLocked(item)
		}
	}
}

// MarkDone marks items as done.
func (tm *TodoListManager) MarkDone(ids []string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, id := range ids {
		if item, ok := tm.items[id]; ok {
			item.Status = TodoStatusDone
			tm.saveItemLocked(item)
		}
	}
}

// MarkFailed marks items as failed.
func (tm *TodoListManager) MarkFailed(ids []string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, id := range ids {
		if item, ok := tm.items[id]; ok {
			item.Status = TodoStatusFailed
			tm.saveItemLocked(item)
		}
	}
}

// SetAutoConsume enables or disables auto-consume mode.
// When enabled, new items are immediately dispatched to the AI agent for fixing.
// The callback receives the newly added item.
func (tm *TodoListManager) SetAutoConsume(enabled bool, callback func(item *TodoItem)) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.autoConsume = enabled
	tm.onNewItem = callback
}

// IsAutoConsume returns whether auto-consume is enabled.
func (tm *TodoListManager) IsAutoConsume() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.autoConsume
}

// GenerateBatchFixPrompt creates a combined prompt for all pending items.
func (tm *TodoListManager) GenerateBatchFixPrompt(items []*TodoItem) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You have %d queued bug reports from device testing. Fix all of them.\n", len(items)))
	sb.WriteString("Address each bug below. Prioritize fatal crashes first, then UI bugs.\n\n")

	for i, item := range items {
		sb.WriteString(fmt.Sprintf("--- Bug #%d (id: %s) ---\n", i+1, item.ID))
		sb.WriteString(fmt.Sprintf("Description: %s\n", item.Description))

		// Device info
		if item.DeviceInfo.Platform != "" {
			sb.WriteString(fmt.Sprintf("Device: %s %s, %s %s\n",
				item.DeviceInfo.Model, item.DeviceInfo.Platform,
				item.DeviceInfo.Platform, item.DeviceInfo.OSVersion))
		}

		// Captured errors
		if len(item.Errors) > 0 {
			sb.WriteString("Captured errors:\n")
			for j, e := range item.Errors {
				fatal := ""
				if e.IsFatal {
					fatal = " [FATAL]"
				}
				sb.WriteString(fmt.Sprintf("  Error %d%s: %s\n", j+1, fatal, e.Message))
				for _, frame := range e.Stack {
					sb.WriteString(fmt.Sprintf("    %s\n", frame))
				}
			}
		}

		// Screenshots
		if len(item.Screenshots) > 0 {
			sb.WriteString(fmt.Sprintf("Screenshots: %d attached\n", len(item.Screenshots)))
		}

		// BlackBox snapshot
		if item.BlackBoxSnap != "" {
			sb.WriteString("\n[BlackBox context at time of report]\n")
			sb.WriteString(item.BlackBoxSnap)
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
	}

	sb.WriteString("Please fix all these issues. After fixing, the changes will be hot-reloaded to the device.\n")
	return sb.String()
}

// GenerateSingleFixPrompt creates a prompt for a single item.
func (tm *TodoListManager) GenerateSingleFixPrompt(item *TodoItem) string {
	return tm.GenerateBatchFixPrompt([]*TodoItem{item})
}

// saveItem persists an item's metadata to disk.
func (tm *TodoListManager) saveItem(item *TodoItem) {
	itemDir := filepath.Join(tm.baseDir, item.ID)
	data, _ := json.MarshalIndent(item, "", "  ")
	os.WriteFile(filepath.Join(itemDir, "metadata.json"), data, 0600)
}

// saveItemLocked is saveItem but assumes the lock is already held.
func (tm *TodoListManager) saveItemLocked(item *TodoItem) {
	tm.saveItem(item)
}

