package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// persistedTask is the JSON-serializable subset of Task that gets written to disk.
type persistedTask struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Description     string             `json:"description"`
	Status          TaskStatus         `json:"status"`
	Source          string             `json:"source,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`
	TmuxSession     string             `json:"tmux_session,omitempty"`
	TmuxSessionID   string             `json:"tmux_session_id,omitempty"`
	TmuxWindowIndex string             `json:"tmux_window_index,omitempty"`
	TmuxWindowName  string             `json:"tmux_window_name,omitempty"`
	TmuxPaneIndex   string             `json:"tmux_pane_index,omitempty"`
	TmuxPaneID      string             `json:"tmux_pane_id,omitempty"`
	IsAdopted       bool               `json:"is_adopted,omitempty"`
	Output          string             `json:"output,omitempty"`
	ResultText      string             `json:"result_text,omitempty"`
	CostUSD         float64            `json:"cost_usd,omitempty"`
	Turns           []ConversationTurn `json:"turns,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	StartedAt       *time.Time         `json:"started_at,omitempty"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
}

// TaskStore persists task metadata to a JSON file under ~/.yaver/.
type TaskStore struct {
	path string
	mu   sync.Mutex
}

// NewTaskStore creates a TaskStore that reads/writes ~/.yaver/tasks.json.
func NewTaskStore() (*TaskStore, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("task store config dir: %w", err)
	}
	return &TaskStore{
		path: filepath.Join(dir, "tasks.json"),
	}, nil
}

// Save writes the current task map to disk. Only the serializable fields are
// persisted, and output is truncated to the last 2000 characters.
func (s *TaskStore) Save(tasks map[string]*Task) {
	s.SaveRecords(snapshotPersistedTasks(tasks))
}

func snapshotPersistedTasks(tasks map[string]*Task) []persistedTask {
	records := make([]persistedTask, 0, len(tasks))
	for _, t := range tasks {
		output := t.Output
		if len(output) > 2000 {
			output = output[len(output)-2000:]
		}
		records = append(records, persistedTask{
			ID:              t.ID,
			Title:           t.Title,
			Description:     t.Description,
			Status:          t.Status,
			Source:          t.Source,
			SessionID:       t.SessionID,
			TmuxSession:     t.TmuxSession,
			TmuxSessionID:   t.TmuxSessionID,
			TmuxWindowIndex: t.TmuxWindowIndex,
			TmuxWindowName:  t.TmuxWindowName,
			TmuxPaneIndex:   t.TmuxPaneIndex,
			TmuxPaneID:      t.TmuxPaneID,
			IsAdopted:       t.IsAdopted,
			Output:          output,
			ResultText:      t.ResultText,
			CostUSD:         t.CostUSD,
			Turns:           append([]ConversationTurn(nil), t.Turns...),
			CreatedAt:       t.CreatedAt,
			StartedAt:       t.StartedAt,
			FinishedAt:      t.FinishedAt,
		})
	}
	return records
}

func (s *TaskStore) SaveRecords(records []persistedTask) {
	if s == nil {
		return
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		log.Printf("task store: marshal error: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		log.Printf("task store: write error: %v", err)
	}
}

// Load reads persisted tasks from disk and returns them as a map.
// Running/queued tasks from a previous session are marked as stopped since
// the underlying processes no longer exist.
func (s *TaskStore) Load() map[string]*Task {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Task)
		}
		log.Printf("task store: read error: %v", err)
		return make(map[string]*Task)
	}

	var records []persistedTask
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("task store: parse error: %v", err)
		return make(map[string]*Task)
	}

	tasks := make(map[string]*Task, len(records))
	for _, r := range records {
		status := r.Status
		finishedAt := r.FinishedAt
		// Tasks that were running or queued when we last exited can never be
		// resumed — mark them as stopped so they appear as historical records.
		// Exception: adopted tmux tasks are left as-is; TmuxManager.ReAdoptOnStartup()
		// will check if the session still exists and either re-adopt or mark stopped.
		if (status == TaskStatusRunning || status == TaskStatusQueued) && !r.IsAdopted {
			status = TaskStatusStopped
			if finishedAt == nil {
				now := time.Now()
				finishedAt = &now
			}
		}
		tasks[r.ID] = &Task{
			ID:              r.ID,
			Title:           r.Title,
			Description:     r.Description,
			Status:          status,
			Source:          r.Source,
			SessionID:       r.SessionID,
			TmuxSession:     r.TmuxSession,
			TmuxSessionID:   r.TmuxSessionID,
			TmuxWindowIndex: r.TmuxWindowIndex,
			TmuxWindowName:  r.TmuxWindowName,
			TmuxPaneIndex:   r.TmuxPaneIndex,
			TmuxPaneID:      r.TmuxPaneID,
			IsAdopted:       r.IsAdopted,
			Output:          r.Output,
			ResultText:      r.ResultText,
			CostUSD:         r.CostUSD,
			Turns:           r.Turns,
			CreatedAt:       r.CreatedAt,
			StartedAt:       r.StartedAt,
			FinishedAt:      finishedAt,
			// doneCh is left nil — these are historical records with no process.
		}
	}
	return tasks
}
