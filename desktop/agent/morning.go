package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// morning.go — "good morning" summary of an overnight autodev run.
//
// Data shape:
//   ~/.yaver/autodev-runs/<runId>/summary.json     ← MorningSummary
//   ~/.yaver/recordings/<runId>/<taskId>/video.mp4 ← optional video
//
// A MorningSummary is written incrementally as each task finishes, so
// the mobile match-report UI and yaver-to-yaver viewers always see the
// last committed state even if autodev is killed mid-run. Per-task git
// stats are captured at commit-observation time, not at write time, so
// they survive later repo mutations.
//
// Security: every endpoint that surfaces this data is owner-only; it
// never lands in guestAllowedPrefixes. Recording files are mode 0600.
//
// The data plane never leaves the dev box in the clear: mobile / web /
// yaver-to-yaver all fetch through the existing relay at /d/{deviceId}/
// morning/… and /d/{deviceId}/recordings/…/video.mp4 (byte-range).

// TaskStatusHighlight is the coarse outcome we render on a match card.
type TaskStatusHighlight string

const (
	TaskStatusHighlightShipped    TaskStatusHighlight = "shipped"
	TaskStatusHighlightFailed     TaskStatusHighlight = "failed"
	TaskStatusHighlightSkipped    TaskStatusHighlight = "skipped"
	TaskStatusHighlightRolledBack TaskStatusHighlight = "rolled-back"
)

// TaskHighlight is a single card on the morning match report.
type TaskHighlight struct {
	TaskID         string              `json:"taskId"`
	RunnerID       string              `json:"runnerId,omitempty"`
	Title          string              `json:"title"`
	OneLineSummary string              `json:"oneLineSummary,omitempty"`
	Status         TaskStatusHighlight `json:"status"`
	StartedAt      time.Time           `json:"startedAt"`
	FinishedAt     time.Time           `json:"finishedAt"`
	CostUSD        float64             `json:"costUsd,omitempty"`

	// Git state at the time this task's commits were observed.
	// BaseSHA is the commit before the task started, HeadSHA is after.
	BaseSHA      string   `json:"baseSha,omitempty"`
	HeadSHA      string   `json:"headSha,omitempty"`
	CommitSHAs   []string `json:"commitShas,omitempty"`
	WorkDir      string   `json:"workDir,omitempty"`
	FilesChanged int      `json:"filesChanged,omitempty"`
	LinesAdded   int      `json:"linesAdded,omitempty"`
	LinesRemoved int      `json:"linesRemoved,omitempty"`

	// Recording (optional).
	HasVideo        bool  `json:"hasVideo"`
	VideoDurationMs int   `json:"videoDurationMs,omitempty"`
	VideoSizeBytes  int64 `json:"videoSizeBytes,omitempty"`

	// Rollback state.
	RolledBackAt *time.Time `json:"rolledBackAt,omitempty"`
	RevertSHA    string     `json:"revertSha,omitempty"`

	// Optional failure detail for failed/rolled-back cards.
	FailureNote string `json:"failureNote,omitempty"`
}

// SummaryStats is the headline row of the run.
type SummaryStats struct {
	TasksShipped    int     `json:"tasksShipped"`
	TasksFailed     int     `json:"tasksFailed"`
	TasksRolledBack int     `json:"tasksRolledBack"`
	TasksTotal      int     `json:"tasksTotal"`
	TotalCostUSD    float64 `json:"totalCostUsd"`
	TotalMinutes    int     `json:"totalMinutes"`
}

// MorningSummary is what the mobile/web clients fetch to render the
// overnight report. One per autodev run.
type MorningSummary struct {
	RunID      string          `json:"runId"`
	Project    string          `json:"project"`
	WorkDir    string          `json:"workDir"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Tasks      []TaskHighlight `json:"tasks"`
	Stats      SummaryStats    `json:"stats"`
	Note       string          `json:"note,omitempty"`
}

func (s *MorningSummary) recomputeStats() {
	var stats SummaryStats
	for _, t := range s.Tasks {
		stats.TasksTotal++
		stats.TotalCostUSD += t.CostUSD
		switch t.Status {
		case TaskStatusHighlightShipped:
			stats.TasksShipped++
		case TaskStatusHighlightFailed:
			stats.TasksFailed++
		case TaskStatusHighlightRolledBack:
			stats.TasksRolledBack++
		}
	}
	if s.FinishedAt != nil {
		stats.TotalMinutes = int(s.FinishedAt.Sub(s.StartedAt).Minutes())
	} else {
		stats.TotalMinutes = int(time.Since(s.StartedAt).Minutes())
	}
	s.Stats = stats
}

// ── Store ──────────────────────────────────────────────────────────────

// MorningStore persists MorningSummary JSON files in
// ~/.yaver/autodev-runs/<runId>/summary.json. Writes are atomic via
// temp+rename so a process kill mid-write can never leave a reader
// seeing a half-serialized summary.
type MorningStore struct {
	mu   sync.Mutex
	root string
}

func NewMorningStore(root string) *MorningStore {
	return &MorningStore{root: root}
}

// DefaultMorningStore returns a store rooted at ~/.yaver/autodev-runs.
// If ConfigDir resolution fails (unusual on a live agent) the store
// falls back to the current working dir so tests can still use it.
func DefaultMorningStore() *MorningStore {
	base, err := ConfigDir()
	if err != nil {
		base = "."
	}
	return NewMorningStore(filepath.Join(base, "autodev-runs"))
}

func (s *MorningStore) runDir(runID string) string {
	return filepath.Join(s.root, sanitizeMorningID(runID))
}

func (s *MorningStore) summaryPath(runID string) string {
	return filepath.Join(s.runDir(runID), "summary.json")
}

// sanitizeMorningID scrubs any filesystem-dangerous characters out of
// a run id so the store cannot be used to escape its base directory.
// We accept the same characters isSafeGraphNodeID does; anything else
// is rendered as an underscore.
func sanitizeMorningID(id string) string {
	if id == "" {
		return "unknown"
	}
	if len(id) > 64 {
		id = id[:64]
	}
	var out strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	s := out.String()
	if s == "" || s == "." || s == ".." {
		return "unknown"
	}
	return s
}

// Load returns the summary for runID, or (nil, false) if missing.
func (s *MorningStore) Load(runID string) (*MorningSummary, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(runID)
}

func (s *MorningStore) loadLocked(runID string) (*MorningSummary, bool) {
	data, err := os.ReadFile(s.summaryPath(runID))
	if err != nil {
		return nil, false
	}
	var summary MorningSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, false
	}
	return &summary, true
}

// Save persists the summary atomically. Recomputes stats first so
// callers don't have to remember.
func (s *MorningStore) Save(summary *MorningSummary) error {
	if summary == nil {
		return fmt.Errorf("nil summary")
	}
	if summary.RunID == "" {
		return fmt.Errorf("summary has no runId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(summary)
}

func (s *MorningStore) saveLocked(summary *MorningSummary) error {
	summary.recomputeStats()
	dir := s.runDir(summary.RunID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	final := s.summaryPath(summary.RunID)
	tmp := final + ".tmp"
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// List returns summaries newest-first. Malformed files are skipped
// silently (a partially-written file shouldn't break the listing).
func (s *MorningStore) List(limit int) []*MorningSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	out := make([]*MorningSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if sum, ok := s.loadLocked(e.Name()); ok {
			out = append(out, sum)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// UpsertTask merges a TaskHighlight into the given run's summary,
// creating the summary if it doesn't yet exist. The merge is keyed by
// TaskID and updates only non-zero fields from incoming — this lets
// autodev hooks emit partial updates (e.g. just the git stats, or just
// the recording metadata) without clobbering earlier fields.
func (s *MorningStore) UpsertTask(runID, project, workDir string, task TaskHighlight) (*MorningSummary, error) {
	if strings.TrimSpace(task.TaskID) == "" {
		return nil, fmt.Errorf("task highlight has no taskId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	summary, ok := s.loadLocked(runID)
	if !ok {
		summary = &MorningSummary{
			RunID:     runID,
			Project:   project,
			WorkDir:   workDir,
			StartedAt: task.StartedAt,
		}
		if summary.StartedAt.IsZero() {
			summary.StartedAt = time.Now().UTC()
		}
	}
	if summary.Project == "" {
		summary.Project = project
	}
	if summary.WorkDir == "" {
		summary.WorkDir = workDir
	}
	merged := false
	for i := range summary.Tasks {
		if summary.Tasks[i].TaskID == task.TaskID {
			summary.Tasks[i] = mergeTaskHighlight(summary.Tasks[i], task)
			merged = true
			break
		}
	}
	if !merged {
		summary.Tasks = append(summary.Tasks, task)
	}
	return summary, s.saveLocked(summary)
}

// MarkRollback records that a task was rolled back. Returns the updated
// summary or an error if the task / run is missing.
func (s *MorningStore) MarkRollback(runID, taskID, revertSHA string) (*MorningSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary, ok := s.loadLocked(runID)
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	for i := range summary.Tasks {
		if summary.Tasks[i].TaskID == taskID {
			now := time.Now().UTC()
			summary.Tasks[i].Status = TaskStatusHighlightRolledBack
			summary.Tasks[i].RolledBackAt = &now
			summary.Tasks[i].RevertSHA = revertSHA
			return summary, s.saveLocked(summary)
		}
	}
	return nil, fmt.Errorf("task %s not found in run %s", taskID, runID)
}

// Finalize marks the run as finished.
func (s *MorningStore) Finalize(runID, note string) (*MorningSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary, ok := s.loadLocked(runID)
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	now := time.Now().UTC()
	summary.FinishedAt = &now
	if strings.TrimSpace(note) != "" {
		summary.Note = note
	}
	return summary, s.saveLocked(summary)
}

// mergeTaskHighlight copies incoming fields over base where incoming is
// non-zero. Zero-check is conservative — callers pass what they know.
func mergeTaskHighlight(base, incoming TaskHighlight) TaskHighlight {
	out := base
	if incoming.RunnerID != "" {
		out.RunnerID = incoming.RunnerID
	}
	if incoming.Title != "" {
		out.Title = incoming.Title
	}
	if incoming.OneLineSummary != "" {
		out.OneLineSummary = incoming.OneLineSummary
	}
	if incoming.Status != "" {
		out.Status = incoming.Status
	}
	if !incoming.StartedAt.IsZero() {
		out.StartedAt = incoming.StartedAt
	}
	if !incoming.FinishedAt.IsZero() {
		out.FinishedAt = incoming.FinishedAt
	}
	if incoming.CostUSD > 0 {
		out.CostUSD = incoming.CostUSD
	}
	if incoming.BaseSHA != "" {
		out.BaseSHA = incoming.BaseSHA
	}
	if incoming.HeadSHA != "" {
		out.HeadSHA = incoming.HeadSHA
	}
	if len(incoming.CommitSHAs) > 0 {
		out.CommitSHAs = incoming.CommitSHAs
	}
	if incoming.WorkDir != "" {
		out.WorkDir = incoming.WorkDir
	}
	if incoming.FilesChanged > 0 {
		out.FilesChanged = incoming.FilesChanged
	}
	if incoming.LinesAdded > 0 {
		out.LinesAdded = incoming.LinesAdded
	}
	if incoming.LinesRemoved > 0 {
		out.LinesRemoved = incoming.LinesRemoved
	}
	if incoming.HasVideo {
		out.HasVideo = true
	}
	if incoming.VideoDurationMs > 0 {
		out.VideoDurationMs = incoming.VideoDurationMs
	}
	if incoming.VideoSizeBytes > 0 {
		out.VideoSizeBytes = incoming.VideoSizeBytes
	}
	if incoming.RolledBackAt != nil {
		out.RolledBackAt = incoming.RolledBackAt
	}
	if incoming.RevertSHA != "" {
		out.RevertSHA = incoming.RevertSHA
	}
	if incoming.FailureNote != "" {
		out.FailureNote = incoming.FailureNote
	}
	return out
}

// ── Git helpers used by the autodev hook ──────────────────────────────

// GitHeadSHA returns the HEAD sha of the given repo, "" on error.
func GitHeadSHA(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitCommitSHAsBetween returns commits in the range base..head, newest
// first (git log order). Empty base or head → empty slice. Handles the
// "base is unreachable" case (e.g. force-push) by falling back to just
// head so the rollback still has something to work with.
func GitCommitSHAsBetween(dir, base, head string) []string {
	if dir == "" || base == "" || head == "" || base == head {
		return nil
	}
	out, err := exec.Command("git", "-C", dir, "rev-list", base+".."+head).Output()
	if err != nil {
		// Fall back: just use head if the range doesn't resolve
		return []string{head}
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			shas = append(shas, line)
		}
	}
	return shas
}

// GitDiffStats returns (files_changed, lines_added, lines_removed)
// across base..head. All zeros on error.
func GitDiffStats(dir, base, head string) (int, int, int) {
	if dir == "" || base == "" || head == "" {
		return 0, 0, 0
	}
	out, err := exec.Command("git", "-C", dir, "diff", "--numstat", base, head).Output()
	if err != nil {
		return 0, 0, 0
	}
	var files, added, removed int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		files++
		if n, err := parseIntSafe(fields[0]); err == nil {
			added += n
		}
		if n, err := parseIntSafe(fields[1]); err == nil {
			removed += n
		}
	}
	return files, added, removed
}

func parseIntSafe(s string) (int, error) {
	// git may emit "-" for binary files — treat as zero.
	if s == "-" {
		return 0, nil
	}
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
