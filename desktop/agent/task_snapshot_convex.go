package main

// Privacy-safe task lifecycle snapshot synced to Convex.
//
// Full task records (title, prompt, source, output, paths and transcript) stay
// on the owning machine. Convex receives only enough identity + lifecycle to
// invalidate stale client caches and keep cross-device counts honest.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type convexTaskLifecycle struct {
	TaskID         string `json:"taskId"`
	YaverSessionID string `json:"yaverSessionId,omitempty"`
	Status         string `json:"status"`
	HostKind       string `json:"hostKind,omitempty"`
	UpdatedAt      int64  `json:"updatedAt"`
}

type convexTaskDeletion struct {
	TaskID    string `json:"taskId"`
	DeletedAt int64  `json:"deletedAt"`
}

// reconcileTaskTombstonesFromConvex consumes durable delete intent before
// publishing local lifecycle. It runs on every one-minute state-sync tick, so
// a box that was offline closes the task promptly after connectivity returns.
func (s *convexSyncer) reconcileTaskTombstonesFromConvex(ctx context.Context, tm *TaskManager) {
	if s == nil || tm == nil || strings.TrimSpace(s.deviceID) == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.convexURL, "/")+"/task-tombstones?deviceId="+url.QueryEscape(s.deviceID), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.authToken)
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode >= http.StatusBadRequest {
		_, _ = io.Copy(io.Discard, res.Body)
		return
	}
	var rows []convexTaskDeletion
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rows) != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		tm.mu.RLock()
		task := tm.tasks[strings.TrimSpace(row.TaskID)]
		needsDelete := task != nil && task.DeletedAt == nil
		tm.mu.RUnlock()
		if needsDelete {
			if err := tm.DeleteTask(row.TaskID); err != nil {
				log.Printf("[task %s] central deletion reconciliation: %v", row.TaskID, err)
			}
		}
	}
}

type taskSnapshotSyncState struct {
	mu         sync.Mutex
	lastHash   uint64
	lastSentAt time.Time
	sent       bool
}

type convexTaskLifecycleHash struct {
	TaskID         string `json:"taskId"`
	YaverSessionID string `json:"yaverSessionId,omitempty"`
	Status         string `json:"status"`
	HostKind       string `json:"hostKind,omitempty"`
}

var globalTaskSnapshotSync taskSnapshotSyncState

const taskSnapshotForcedRefreshInterval = 2 * time.Hour
const maxConvexTaskSnapshotRows = 200
const taskSnapshotRecorderPath = "agentTaskSnapshots:sync"

func localTaskLifecycleSnapshot(tm *TaskManager) []convexTaskLifecycle {
	if tm == nil {
		return []convexTaskLifecycle{}
	}
	tm.mu.RLock()
	rows := make([]convexTaskLifecycle, 0, len(tm.tasks))
	for _, task := range tm.tasks {
		if task == nil || task.DeletedAt != nil {
			continue
		}
		updated := task.LastActiveAt
		if updated.IsZero() && task.FinishedAt != nil {
			updated = *task.FinishedAt
		}
		if updated.IsZero() {
			updated = task.CreatedAt
		}
		rows = append(rows, convexTaskLifecycle{
			TaskID: task.ID, YaverSessionID: task.YaverSessionID,
			Status: string(task.Status), HostKind: taskHostKind(task), UpdatedAt: updated.UnixMilli(),
		})
	}
	tm.mu.RUnlock()

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].TaskID < rows[j].TaskID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	if len(rows) > maxConvexTaskSnapshotRows {
		rows = rows[:maxConvexTaskSnapshotRows]
	}
	return rows
}

func syncTaskSnapshotToConvex(ctx context.Context, tm *TaskManager) {
	if globalConvexSync == nil || tm == nil {
		return
	}
	rows := localTaskLifecycleSnapshot(tm)
	// LastActiveAt can move for every output chunk. It is useful metadata on a
	// real lifecycle publication, but it must not turn a coding stream into a
	// Convex write stream. Hash identity + status only; a quiet machine gets one
	// two-hour freshness write and state transitions publish immediately.
	hashRows := make([]convexTaskLifecycleHash, 0, len(rows))
	for _, row := range rows {
		hashRows = append(hashRows, convexTaskLifecycleHash{
			TaskID: row.TaskID, YaverSessionID: row.YaverSessionID, Status: row.Status, HostKind: row.HostKind,
		})
	}
	hashPayload, err := json.Marshal(hashRows)
	if err != nil {
		return
	}
	h := hashBytes(hashPayload)

	globalTaskSnapshotSync.mu.Lock()
	unchanged := globalTaskSnapshotSync.sent && h == globalTaskSnapshotSync.lastHash
	fresh := time.Since(globalTaskSnapshotSync.lastSentAt) < taskSnapshotForcedRefreshInterval
	globalTaskSnapshotSync.mu.Unlock()
	if unchanged && fresh {
		return
	}

	// observedAt is deliberately excluded from hashPayload. It refreshes only
	// on a real state change or the two-hour freshness floor, not every minute.
	payload := map[string]interface{}{
		"deviceId":   globalConvexSync.deviceID,
		"observedAt": time.Now().UnixMilli(),
		"tasks":      rows,
	}
	if !globalConvexSync.publishTaskSnapshot(ctx, payload) {
		return
	}
	globalTaskSnapshotSync.mu.Lock()
	globalTaskSnapshotSync.lastHash = h
	globalTaskSnapshotSync.lastSentAt = time.Now()
	globalTaskSnapshotSync.sent = true
	globalTaskSnapshotSync.mu.Unlock()
}

// publishTaskSnapshot uses a first-class Yaver HTTP action. A Yaver session
// bearer is not Convex-native function auth, so posting it to /api/mutation
// either misses the HTTP deployment (.site) or is rejected by Convex (.cloud).
func (s *convexSyncer) publishTaskSnapshot(ctx context.Context, payload map[string]interface{}) bool {
	if convexMutationRecorder != nil {
		// Keep the established recorder name and argument shape so the global
		// Convex privacy walker continues to cover this payload.
		convexMutationRecorder(taskSnapshotRecorderPath, payload)
		return s.recordTaskSnapshotSuccess()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return s.recordTaskSnapshotFailure(fmt.Errorf("marshal payload: %w", err))
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(s.convexURL, "/")+"/task-snapshots",
		bytes.NewReader(body),
	)
	if err != nil {
		return s.recordTaskSnapshotFailure(fmt.Errorf("construct request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+s.authToken)
	req.Header.Set("Content-Type", "application/json")
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return s.recordTaskSnapshotFailure(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)

	if res.StatusCode >= http.StatusBadRequest {
		return s.recordTaskSnapshotFailure(fmt.Errorf("HTTP %d", res.StatusCode))
	}
	return s.recordTaskSnapshotSuccess()
}

func (s *convexSyncer) recordTaskSnapshotFailure(err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCount++
	s.lastError = fmt.Sprintf("task snapshots: %v", err)
	return false
}

func (s *convexSyncer) recordTaskSnapshotSuccess() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successCount++
	s.lastError = ""
	return true
}
