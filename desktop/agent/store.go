package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// persistedTask is the JSON-serializable subset of Task that gets written to disk.
type persistedTask struct {
	ID                   string                    `json:"id"`
	Title                string                    `json:"title"`
	Description          string                    `json:"description"`
	Status               TaskStatus                `json:"status"`
	Source               string                    `json:"source,omitempty"`
	YaverSessionID       string                    `json:"yaver_session_id,omitempty"`
	RemoteBoxID          string                    `json:"remote_box_id,omitempty"`
	RunnerName           string                    `json:"runner_name,omitempty"`
	RunnerID             string                    `json:"runner_id,omitempty"`
	SessionStartedFrom   string                    `json:"session_started_from,omitempty"`
	StartedFromSurface   string                    `json:"started_from_surface,omitempty"`
	InitialSurface       string                    `json:"initial_surface,omitempty"`
	SessionStartedAt     time.Time                 `json:"session_started_at,omitempty"`
	LastSurface          string                    `json:"last_surface,omitempty"`
	LastActiveAt         time.Time                 `json:"last_active_at,omitempty"`
	FirstUserMessageAt   *time.Time                `json:"first_user_message_at,omitempty"`
	FirstAgentResponseAt *time.Time                `json:"first_agent_response_at,omitempty"`
	LastUserMessageAt    *time.Time                `json:"last_user_message_at,omitempty"`
	LastAgentResponseAt  *time.Time                `json:"last_agent_response_at,omitempty"`
	SessionSettings      *ClientSessionSettings    `json:"session_settings,omitempty"`
	DeletedAt            *time.Time                `json:"deleted_at,omitempty"`
	Transport            string                    `json:"transport,omitempty"`
	SessionID            string                    `json:"session_id,omitempty"`
	TmuxSession          string                    `json:"tmux_session,omitempty"`
	TmuxSessionID        string                    `json:"tmux_session_id,omitempty"`
	TmuxWindowIndex      string                    `json:"tmux_window_index,omitempty"`
	TmuxWindowName       string                    `json:"tmux_window_name,omitempty"`
	TmuxPaneIndex        string                    `json:"tmux_pane_index,omitempty"`
	TmuxPaneID           string                    `json:"tmux_pane_id,omitempty"`
	IsAdopted            bool                      `json:"is_adopted,omitempty"`
	Output               string                    `json:"output,omitempty"`
	ResultText           string                    `json:"result_text,omitempty"`
	Presentation         []TaskPresentationMessage `json:"presentation,omitempty"`
	PresentationSeq      int64                     `json:"presentation_seq,omitempty"`
	Failure              *TaskFailureDiagnosis     `json:"failure,omitempty"`
	CostUSD              float64                   `json:"cost_usd,omitempty"`
	Turns                []ConversationTurn        `json:"turns,omitempty"`
	WorkDir              string                    `json:"work_dir,omitempty"`
	ProjectName          string                    `json:"project_name,omitempty"`
	VideoClipID          string                    `json:"video_clip_id,omitempty"`
	VideoStatus          string                    `json:"video_status,omitempty"`
	ProofStatus          string                    `json:"proof_status,omitempty"`
	CommitSHA            string                    `json:"commit_sha,omitempty"`
	CommitSubject        string                    `json:"commit_subject,omitempty"`
	CommitBranch         string                    `json:"commit_branch,omitempty"`
	DiffShortstat        string                    `json:"diff_shortstat,omitempty"`
	FeedbackID           string                    `json:"feedback_id,omitempty"`
	CreatedAt            time.Time                 `json:"created_at"`
	StartedAt            *time.Time                `json:"started_at,omitempty"`
	FinishedAt           *time.Time                `json:"finished_at,omitempty"`
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
			ID:                   t.ID,
			Title:                t.Title,
			Description:          t.Description,
			Status:               t.Status,
			Source:               t.Source,
			YaverSessionID:       t.YaverSessionID,
			RemoteBoxID:          t.RemoteBoxID,
			RunnerName:           t.RunnerName,
			RunnerID:             t.RunnerID,
			SessionStartedFrom:   t.SessionStartedFrom,
			StartedFromSurface:   t.StartedFromSurface,
			InitialSurface:       t.InitialSurface,
			SessionStartedAt:     t.SessionStartedAt,
			LastSurface:          t.LastSurface,
			LastActiveAt:         t.LastActiveAt,
			FirstUserMessageAt:   t.FirstUserMessageAt,
			FirstAgentResponseAt: t.FirstAgentResponseAt,
			LastUserMessageAt:    t.LastUserMessageAt,
			LastAgentResponseAt:  t.LastAgentResponseAt,
			SessionSettings:      cloneClientSessionSettings(t.SessionSettings),
			DeletedAt:            t.DeletedAt,
			Transport:            t.Transport,
			SessionID:            t.SessionID,
			TmuxSession:          t.TmuxSession,
			TmuxSessionID:        t.TmuxSessionID,
			TmuxWindowIndex:      t.TmuxWindowIndex,
			TmuxWindowName:       t.TmuxWindowName,
			TmuxPaneIndex:        t.TmuxPaneIndex,
			TmuxPaneID:           t.TmuxPaneID,
			IsAdopted:            t.IsAdopted,
			Output:               output,
			ResultText:           t.ResultText,
			Presentation:         append([]TaskPresentationMessage(nil), t.Presentation...),
			PresentationSeq:      t.PresentationSeq,
			Failure:              t.Failure,
			CostUSD:              t.CostUSD,
			Turns:                append([]ConversationTurn(nil), t.Turns...),
			WorkDir:              t.WorkDir,
			ProjectName:          t.ProjectName,
			VideoClipID:          t.VideoClipID,
			VideoStatus:          t.VideoStatus,
			ProofStatus:          t.ProofStatus,
			CommitSHA:            t.CommitSHA,
			CommitSubject:        t.CommitSubject,
			CommitBranch:         t.CommitBranch,
			DiffShortstat:        t.DiffShortstat,
			FeedbackID:           t.FeedbackID,
			CreatedAt:            t.CreatedAt,
			StartedAt:            t.StartedAt,
			FinishedAt:           t.FinishedAt,
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
// Running/queued direct-exec tasks from a previous session are marked as failed
// with a structured restart diagnosis since their underlying process no longer
// exists. Task-owned and adopted tmux seats are preserved for the startup tmux
// reconciler, which probes the real pane before deciding their state.
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
		failure := r.Failure
		// Direct child processes cannot survive their owning agent. tmux seats can,
		// so leave both adopted and exact task-owned seats for the operation-level
		// startup probe instead of producing a false restart failure first.
		persistedOwnsTmuxSeat := taskOwnsNamedTmuxSeat(&Task{
			ID: r.ID, RunnerID: r.RunnerID, TmuxSession: r.TmuxSession,
			ProjectName: r.ProjectName, WorkDir: r.WorkDir, CreatedAt: r.CreatedAt,
			IsAdopted: r.IsAdopted,
		})
		if (status == TaskStatusRunning || status == TaskStatusQueued) && !r.IsAdopted && !persistedOwnsTmuxSeat {
			status = TaskStatusFailed
			if finishedAt == nil {
				now := time.Now()
				finishedAt = &now
			}
			failure = &TaskFailureDiagnosis{
				Kind:       "agent_lifecycle",
				Code:       ReasonTaskInterruptedByAgentRestart,
				Title:      "Task interrupted by an agent restart",
				Reason:     "The Yaver agent exited while this task was running, so its runner process is no longer attached.",
				Remedy:     "Review any partial edits left in the project, then use Retry on this task. If it repeats, open machine diagnostics before retrying again.",
				Probe:      "persisted_running_task_on_startup",
				DetectedAt: *finishedAt,
			}
		}
		presentation := append([]TaskPresentationMessage(nil), r.Presentation...)
		presentationSeq := r.PresentationSeq
		// Presentation was not persisted before schema v2. Recover only a clean,
		// explicitly visible assistant turn whose text exactly matches ResultText;
		// never promote the raw Output terminal tail into chat.
		if len(presentation) == 0 && strings.TrimSpace(r.ResultText) != "" {
			for i := len(r.Turns) - 1; i >= 0; i-- {
				turn := r.Turns[i]
				if turn.Role != "assistant" || turn.Hidden || strings.TrimSpace(turn.Content) != strings.TrimSpace(r.ResultText) {
					continue
				}
				clean := strings.TrimSpace(humanReadableRunnerAnswer(r.ResultText))
				if clean == strings.TrimSpace(r.ResultText) {
					createdAt := turn.Timestamp
					if createdAt.IsZero() {
						createdAt = r.CreatedAt
					}
					presentation = []TaskPresentationMessage{{
						ID: r.ID + "-assistant-restored", Kind: "message", Role: "assistant",
						Text: clean, Phase: "complete", State: "completed",
						Runner: normalizeRunnerID(r.RunnerID), Project: strings.TrimSpace(r.ProjectName),
						CreatedAt: createdAt, UpdatedAt: createdAt,
					}}
					presentationSeq = 1
				}
				break
			}
		}
		tasks[r.ID] = &Task{
			ID:                   r.ID,
			Title:                r.Title,
			Description:          r.Description,
			Status:               status,
			Source:               r.Source,
			YaverSessionID:       r.YaverSessionID,
			RemoteBoxID:          r.RemoteBoxID,
			RunnerName:           r.RunnerName,
			RunnerID:             r.RunnerID,
			SessionStartedFrom:   r.SessionStartedFrom,
			StartedFromSurface:   r.StartedFromSurface,
			InitialSurface:       r.InitialSurface,
			SessionStartedAt:     r.SessionStartedAt,
			LastSurface:          r.LastSurface,
			LastActiveAt:         r.LastActiveAt,
			FirstUserMessageAt:   r.FirstUserMessageAt,
			FirstAgentResponseAt: r.FirstAgentResponseAt,
			LastUserMessageAt:    r.LastUserMessageAt,
			LastAgentResponseAt:  r.LastAgentResponseAt,
			SessionSettings:      cloneClientSessionSettings(r.SessionSettings),
			DeletedAt:            r.DeletedAt,
			Transport:            r.Transport,
			SessionID:            r.SessionID,
			TmuxSession:          r.TmuxSession,
			TmuxSessionID:        r.TmuxSessionID,
			TmuxWindowIndex:      r.TmuxWindowIndex,
			TmuxWindowName:       r.TmuxWindowName,
			TmuxPaneIndex:        r.TmuxPaneIndex,
			TmuxPaneID:           r.TmuxPaneID,
			IsAdopted:            r.IsAdopted,
			Output:               r.Output,
			ResultText:           r.ResultText,
			Presentation:         presentation,
			PresentationSeq:      presentationSeq,
			Failure:              failure,
			CostUSD:              r.CostUSD,
			Turns:                r.Turns,
			WorkDir:              r.WorkDir,
			ProjectName:          r.ProjectName,
			VideoClipID:          r.VideoClipID,
			VideoStatus:          r.VideoStatus,
			ProofStatus:          r.ProofStatus,
			CommitSHA:            r.CommitSHA,
			CommitSubject:        r.CommitSubject,
			CommitBranch:         r.CommitBranch,
			DiffShortstat:        r.DiffShortstat,
			FeedbackID:           r.FeedbackID,
			CreatedAt:            r.CreatedAt,
			StartedAt:            r.StartedAt,
			FinishedAt:           finishedAt,
			// doneCh is left nil — these are historical records with no process.
		}
	}
	return tasks
}
