package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ScheduledTask represents a task scheduled for future or recurring execution.
type ScheduledTask struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	Model         string `json:"model,omitempty"`
	Runner        string `json:"runner,omitempty"`
	CustomCommand string `json:"customCommand,omitempty"`

	// Scheduling
	RunAt          string `json:"runAt,omitempty"`          // ISO8601 for one-shot scheduled tasks
	Cron           string `json:"cron,omitempty"`           // Cron expression for recurring (e.g. "0 9 * * 1-5")
	RepeatInterval int    `json:"repeatInterval,omitempty"` // Repeat every N minutes (simpler than cron)

	// State
	Status     string `json:"status"` // "scheduled", "running", "completed", "failed", "paused"
	LastRunAt  string `json:"lastRunAt,omitempty"`
	LastTaskID string `json:"lastTaskId,omitempty"`
	NextRunAt  string `json:"nextRunAt,omitempty"`
	RunCount   int    `json:"runCount"`
	MaxRuns    int    `json:"maxRuns,omitempty"` // 0 = unlimited
	CreatedAt  string `json:"createdAt"`

	// Results
	History []ScheduleRun `json:"history,omitempty"`
}

type ScheduleRun struct {
	TaskID    string  `json:"taskId"`
	Status    string  `json:"status"`
	StartedAt string  `json:"startedAt"`
	Duration  int     `json:"durationMs"`
	CostUSD   float64 `json:"costUsd,omitempty"`
}

// Scheduler manages scheduled and recurring tasks.
type Scheduler struct {
	mu        sync.RWMutex
	tasks     map[string]*ScheduledTask
	taskMgr   *TaskManager
	storePath string
	cancel    context.CancelFunc
}

// NewScheduler creates a scheduler that persists to ~/.yaver/schedules.json.
func NewScheduler(taskMgr *TaskManager) *Scheduler {
	dir, _ := ConfigDir()
	s := &Scheduler{
		tasks:     make(map[string]*ScheduledTask),
		taskMgr:   taskMgr,
		storePath: filepath.Join(dir, "schedules.json"),
	}
	s.load()
	return s
}

// Start begins the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go s.loop(ctx)
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAndRun()
		}
	}
}

func (s *Scheduler) checkAndRun() {
	s.mu.RLock()
	var toRun []*ScheduledTask
	now := time.Now()
	for _, st := range s.tasks {
		if st.Status == "paused" {
			continue
		}
		if st.MaxRuns > 0 && st.RunCount >= st.MaxRuns {
			continue
		}
		if st.NextRunAt == "" {
			continue
		}
		nextRun, err := time.Parse(time.RFC3339, st.NextRunAt)
		if err != nil {
			continue
		}
		if now.After(nextRun) {
			toRun = append(toRun, st)
		}
	}
	s.mu.RUnlock()

	for _, st := range toRun {
		s.executeScheduled(st)
	}
}

func (s *Scheduler) executeScheduled(st *ScheduledTask) {
	log.Printf("[scheduler] Running scheduled task %s: %s", st.ID, st.Title)

	task, err := s.taskMgr.CreateTask(st.Title, st.Description, st.Model, "scheduler", st.Runner, st.CustomCommand, nil, nil)
	if err != nil {
		log.Printf("[scheduler] Failed to create task for schedule %s: %v", st.ID, err)
		return
	}

	s.mu.Lock()
	st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	st.LastTaskID = task.ID
	st.RunCount++
	st.Status = "running"

	// Calculate next run
	if st.RepeatInterval > 0 {
		next := time.Now().Add(time.Duration(st.RepeatInterval) * time.Minute)
		st.NextRunAt = next.UTC().Format(time.RFC3339)
	} else if st.Cron != "" {
		next := nextCronRun(st.Cron)
		if !next.IsZero() {
			st.NextRunAt = next.UTC().Format(time.RFC3339)
		} else {
			st.NextRunAt = ""
		}
	} else {
		// One-shot: no next run
		st.NextRunAt = ""
		st.Status = "completed"
	}
	s.mu.Unlock()

	s.save()

	// Monitor task completion in background
	go func() {
		for i := 0; i < 3600; i++ { // max 1 hour monitoring
			time.Sleep(5 * time.Second)
			t, ok := s.taskMgr.GetTask(task.ID)
			if !ok {
				break
			}
			s.taskMgr.mu.RLock()
			status := t.Status
			cost := t.CostUSD
			s.taskMgr.mu.RUnlock()

			if status == TaskStatusFinished || status == TaskStatusFailed || status == TaskStatusStopped {
				s.mu.Lock()
				run := ScheduleRun{
					TaskID:    task.ID,
					Status:    string(status),
					StartedAt: st.LastRunAt,
					CostUSD:   cost,
				}
				if started, err := time.Parse(time.RFC3339, st.LastRunAt); err == nil {
					run.Duration = int(time.Since(started).Milliseconds())
				}
				st.History = append(st.History, run)
				// Keep only last 50 runs
				if len(st.History) > 50 {
					st.History = st.History[len(st.History)-50:]
				}
				if st.Status == "running" {
					if st.NextRunAt != "" {
						st.Status = "scheduled"
					} else {
						st.Status = "completed"
					}
				}
				s.mu.Unlock()
				s.save()
				break
			}
		}
	}()
}

// RunScheduleNow fires a scheduled task immediately. Used by the
// "Run now" button in the web + mobile UIs so the user can kick off
// a scheduled prompt without waiting for the next fire time. Does
// not reset NextRunAt — the cron / interval keeps its cadence.
func (s *Scheduler) RunScheduleNow(id string) error {
	st, ok := s.GetSchedule(id)
	if !ok {
		return fmt.Errorf("schedule %q not found", id)
	}
	go s.executeScheduled(st)
	return nil
}

// AddSchedule creates a new scheduled task.
func (s *Scheduler) AddSchedule(st *ScheduledTask) error {
	if st.Title == "" {
		return fmt.Errorf("title is required")
	}
	if st.RunAt == "" && st.Cron == "" && st.RepeatInterval == 0 {
		return fmt.Errorf("one of runAt, cron, or repeatInterval is required")
	}

	st.ID = fmt.Sprintf("sched-%s", generateTaskID())
	st.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	st.Status = "scheduled"

	// Calculate first run time
	if st.RunAt != "" {
		st.NextRunAt = st.RunAt
	} else if st.RepeatInterval > 0 {
		st.NextRunAt = time.Now().Add(time.Duration(st.RepeatInterval) * time.Minute).UTC().Format(time.RFC3339)
	} else if st.Cron != "" {
		next := nextCronRun(st.Cron)
		if !next.IsZero() {
			st.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	}

	s.mu.Lock()
	s.tasks[st.ID] = st
	s.mu.Unlock()
	s.save()

	log.Printf("[scheduler] Added schedule %s: %s (next: %s)", st.ID, st.Title, st.NextRunAt)
	return nil
}

// RemoveSchedule removes a scheduled task.
func (s *Scheduler) RemoveSchedule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("schedule not found: %s", id)
	}
	delete(s.tasks, id)
	s.save()
	return nil
}

// PauseSchedule pauses a scheduled task.
func (s *Scheduler) PauseSchedule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("schedule not found: %s", id)
	}
	st.Status = "paused"
	s.save()
	return nil
}

// ResumeSchedule resumes a paused schedule.
func (s *Scheduler) ResumeSchedule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("schedule not found: %s", id)
	}
	st.Status = "scheduled"
	// Recalculate next run
	if st.RepeatInterval > 0 {
		st.NextRunAt = time.Now().Add(time.Duration(st.RepeatInterval) * time.Minute).UTC().Format(time.RFC3339)
	} else if st.Cron != "" {
		next := nextCronRun(st.Cron)
		if !next.IsZero() {
			st.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	}
	s.save()
	return nil
}

// ListSchedules returns all scheduled tasks.
func (s *Scheduler) ListSchedules() []*ScheduledTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*ScheduledTask, 0, len(s.tasks))
	for _, st := range s.tasks {
		result = append(result, st)
	}
	return result
}

// GetSchedule returns a specific schedule.
func (s *Scheduler) GetSchedule(id string) (*ScheduledTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.tasks[id]
	return st, ok
}

func (s *Scheduler) save() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, _ := json.MarshalIndent(s.tasks, "", "  ")
	os.WriteFile(s.storePath, data, 0600)
}

func (s *Scheduler) load() {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.tasks)
}

// nextCronRun is a simple cron parser supporting: minute hour day-of-month month day-of-week.
// Returns the next occurrence. Supports * and comma-separated values.
// For a full cron implementation, a library would be used, but this covers common cases.
func nextCronRun(expr string) time.Time {
	// Simple implementation: parse "M H D MO DOW"
	// For now, support basic patterns
	parts := splitFields(expr)
	if len(parts) != 5 {
		return time.Time{}
	}

	now := time.Now()
	// Try each minute in the next 48 hours
	for i := 1; i <= 2880; i++ {
		candidate := now.Add(time.Duration(i) * time.Minute)
		if matchCronField(parts[0], candidate.Minute()) &&
			matchCronField(parts[1], candidate.Hour()) &&
			matchCronField(parts[2], candidate.Day()) &&
			matchCronField(parts[3], int(candidate.Month())) &&
			matchCronField(parts[4], int(candidate.Weekday())) {
			return candidate.Truncate(time.Minute)
		}
	}
	return time.Time{}
}

func splitFields(s string) []string {
	var fields []string
	current := ""
	for _, c := range s {
		if c == ' ' || c == '\t' {
			if current != "" {
				fields = append(fields, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		fields = append(fields, current)
	}
	return fields
}

func matchCronField(field string, value int) bool {
	if field == "*" {
		return true
	}
	// Handle ranges like "1-5"
	if len(field) >= 3 {
		for _, part := range splitComma(field) {
			if matchCronPart(part, value) {
				return true
			}
		}
		return false
	}
	// Simple number
	var n int
	if _, err := fmt.Sscanf(field, "%d", &n); err == nil {
		return n == value
	}
	return false
}

func matchCronPart(part string, value int) bool {
	// Range: "1-5"
	var low, high int
	if n, _ := fmt.Sscanf(part, "%d-%d", &low, &high); n == 2 {
		return value >= low && value <= high
	}
	// Single value
	var n int
	if _, err := fmt.Sscanf(part, "%d", &n); err == nil {
		return n == value
	}
	return part == "*"
}

func splitComma(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
