package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const pendingCloudTaskDispatchFile = "pending-cloud-dispatch.json"
const pendingCloudTaskDispatchDefaultTTL = 24 * time.Hour

type pendingCloudTaskDispatch struct {
	LocalTaskID      string                 `json:"localTaskId"`
	PlacementID      string                 `json:"placementId,omitempty"`
	Placement        *TaskPlacementMetadata `json:"placement,omitempty"`
	DispatchIntentID string                 `json:"dispatchIntentId,omitempty"`
	ExpiresAt        time.Time              `json:"expiresAt,omitempty"`
	Status           string                 `json:"status"`
	SourceSurface    string                 `json:"sourceSurface,omitempty"`
	RequestedRunner  string                 `json:"requestedRunner,omitempty"`
	ProjectSlug      string                 `json:"projectSlug,omitempty"`
	BodyJSON         json.RawMessage        `json:"bodyJson"`
	CreatedAt        time.Time              `json:"createdAt"`
	UpdatedAt        time.Time              `json:"updatedAt"`
	Attempts         int                    `json:"attempts"`
	LastError        string                 `json:"lastError,omitempty"`
	BlockedAction    string                 `json:"blockedAction,omitempty"`
	ClearedBlocker   bool                   `json:"clearedBlocker,omitempty"`
}

type pendingCloudTaskDispatchStore struct {
	path string
}

func newPendingCloudTaskDispatchStore() (*pendingCloudTaskDispatchStore, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	return &pendingCloudTaskDispatchStore{path: filepath.Join(dir, pendingCloudTaskDispatchFile)}, nil
}

func (s *pendingCloudTaskDispatchStore) load() ([]pendingCloudTaskDispatch, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil, fmt.Errorf("pending cloud dispatch store unavailable")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []pendingCloudTaskDispatch
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, row := range rows {
		if strings.TrimSpace(row.LocalTaskID) == "" || len(row.BodyJSON) == 0 {
			continue
		}
		out = append(out, normalizePendingCloudTaskDispatch(row, time.Now()))
	}
	return out, nil
}

func pendingCloudTaskDispatchTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "dispatched", "cancelled", "failed", "expired":
		return true
	default:
		return false
	}
}

func pendingCloudTaskDispatchNeedsUserAction(row pendingCloudTaskDispatch) bool {
	if strings.TrimSpace(row.Status) != "blocked" {
		return false
	}
	switch strings.TrimSpace(row.BlockedAction) {
	case "runner_auth_required", "yaver_auth_required", "billing_required", "resize_required", "resize_failed", "wake_failed":
		return true
	default:
		return false
	}
}

func normalizePendingCloudTaskDispatch(row pendingCloudTaskDispatch, now time.Time) pendingCloudTaskDispatch {
	if now.IsZero() {
		now = time.Now()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if row.ExpiresAt.IsZero() {
		row.ExpiresAt = row.CreatedAt.Add(pendingCloudTaskDispatchDefaultTTL)
	}
	if strings.TrimSpace(row.Status) == "" {
		row.Status = "queued"
	}
	if !pendingCloudTaskDispatchTerminalStatus(row.Status) && !row.ExpiresAt.After(now) {
		row.Status = "expired"
		if strings.TrimSpace(row.LastError) == "" {
			row.LastError = "Local Cloud Workspace dispatch window expired."
		}
	}
	return row
}

func (s *pendingCloudTaskDispatchStore) save(rows []pendingCloudTaskDispatch) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("pending cloud dispatch store unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if len(rows) > 50 {
		rows = rows[len(rows)-50:]
	}
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".pending-cloud-dispatch-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func savePendingCloudTaskDispatch(row pendingCloudTaskDispatch) error {
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		return err
	}
	rows, err := store.load()
	if err != nil {
		return err
	}
	now := time.Now()
	hasCreatedAt := !row.CreatedAt.IsZero()
	hasExpiresAt := !row.ExpiresAt.IsZero()
	if !hasCreatedAt {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	if strings.TrimSpace(row.Status) == "" {
		row.Status = "queued"
	}
	row = normalizePendingCloudTaskDispatch(row, now)
	replaced := false
	for i := range rows {
		if rows[i].LocalTaskID == row.LocalTaskID {
			if rows[i].CreatedAt.IsZero() {
				rows[i].CreatedAt = now
			}
			if !hasCreatedAt {
				row.CreatedAt = rows[i].CreatedAt
			}
			if !hasExpiresAt {
				row.ExpiresAt = rows[i].ExpiresAt
			}
			row = normalizePendingCloudTaskDispatch(row, now)
			rows[i] = row
			replaced = true
			break
		}
	}
	if !replaced {
		rows = append(rows, row)
	}
	return store.save(rows)
}

func patchPendingCloudTaskDispatch(localTaskID string, patch func(*pendingCloudTaskDispatch)) error {
	localTaskID = strings.TrimSpace(localTaskID)
	if localTaskID == "" {
		return nil
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		return err
	}
	rows, err := store.load()
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].LocalTaskID == localTaskID {
			patch(&rows[i])
			rows[i].UpdatedAt = time.Now()
			rows[i] = normalizePendingCloudTaskDispatch(rows[i], rows[i].UpdatedAt)
			return store.save(rows)
		}
	}
	return nil
}

func deletePendingCloudTaskDispatch(localTaskID string) error {
	localTaskID = strings.TrimSpace(localTaskID)
	if localTaskID == "" {
		return nil
	}
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		return err
	}
	rows, err := store.load()
	if err != nil {
		return err
	}
	filtered := rows[:0]
	for _, row := range rows {
		if row.LocalTaskID != localTaskID {
			filtered = append(filtered, row)
		}
	}
	return store.save(filtered)
}

func renderPendingCloudTaskDispatchStatus(now time.Time) string {
	store, err := newPendingCloudTaskDispatchStore()
	if err != nil {
		return fmt.Sprintf("Cloud Workspace queue unavailable: %v", err)
	}
	rows, err := store.load()
	if err != nil {
		return fmt.Sprintf("Cloud Workspace queue unavailable: %v", err)
	}
	if len(rows) == 0 {
		return "No pending Cloud Workspace tasks."
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Pending Cloud Workspace tasks (%d)\n", len(rows))
	for _, row := range rows {
		updated := row.UpdatedAt
		if updated.IsZero() {
			updated = row.CreatedAt
		}
		age := "unknown age"
		if !updated.IsZero() {
			age = humanDuration(now.Sub(updated)) + " ago"
		}
		target := "assigned workspace"
		if row.Placement != nil && strings.TrimSpace(row.Placement.TargetDeviceID) != "" {
			target = strings.TrimSpace(row.Placement.TargetDeviceID)
		}
		lane := ""
		if row.Placement != nil {
			lane = strings.TrimSpace(row.Placement.Lane)
		}
		if lane == "" {
			lane = "cloud"
		}
		fmt.Fprintf(&b, "  %s  %s  %s  target=%s  attempts=%d  updated=%s\n",
			row.LocalTaskID,
			firstNonEmpty(strings.TrimSpace(row.Status), "queued"),
			lane,
			target,
			row.Attempts,
			age,
		)
		if errText := strings.TrimSpace(row.LastError); errText != "" {
			if len(errText) > 180 {
				errText = errText[:180] + "..."
			}
			fmt.Fprintf(&b, "      last error: %s\n", errText)
		}
		if action := strings.TrimSpace(row.BlockedAction); action != "" {
			fmt.Fprintf(&b, "      action: %s\n", action)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		sec := int(d.Seconds())
		if sec < 1 {
			sec = 1
		}
		return fmt.Sprintf("%ds", sec)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
