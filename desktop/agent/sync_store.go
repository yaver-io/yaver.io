package main

// sync_store.go — generic local sync primitive with timestamps +
// tombstones + origin tracking. No Convex, no central server —
// devices peer-merge over the existing P2P relay.
//
// Use case: a solo dev on a laptop + phone + maybe a Hetzner box
// who wants env vars, feature flags, uptime monitors, and other
// mutable state to stay coherent across all three without
// storing any of it in a vendor.
//
// Data model: each item is a `SyncItem{Key, Value, UpdatedAt,
// UpdatedBy, Deleted}`. Merge rule is last-write-wins by
// UpdatedAt with lexicographic UpdatedBy as the tiebreaker. The
// same file format works for every synced kind (env, flags,
// monitors, …) — a `kind` parameter keeps them in separate
// files under ~/.yaver/sync/<kind>.json.
//
// Wire protocol (both endpoints scoped by `kind`):
//
//   GET  /sync/<kind>?since=<ts>       — every item with
//                                        UpdatedAt >= since
//   POST /sync/<kind>                  — {items: [...]}, merged
//                                        into the local store
//
// A peer pulls with `since = last_synced_at`, then POSTs its
// local-only items. Both sides converge after one full round.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SyncItem is the LWW record shared across all synced kinds.
type SyncItem struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"` // opaque blob; kind-specific decoders cast this
	UpdatedAt int64           `json:"updatedAt"`       // unix ms
	UpdatedBy string          `json:"updatedBy"`       // origin device ID
	Deleted   bool            `json:"deleted,omitempty"`
}

// SyncStore is a file-backed LWW map keyed by Key, scoped to a
// single `kind` (env, flags-sync, monitors-sync, ...). Safe for
// concurrent reads + writes.
type SyncStore struct {
	mu    sync.Mutex
	kind  string
	path  string
	items map[string]SyncItem
}

// syncOriginID is the stable per-agent identifier baked into
// every write. Reuses the device ID from the agent's config when
// available, otherwise a persisted random UUID so sync history
// survives re-auth.
var (
	syncOriginIDOnce sync.Once
	syncOriginIDVal  string
)

// syncOrigin returns this agent's origin ID for sync writes.
func syncOrigin() string {
	syncOriginIDOnce.Do(func() {
		// Prefer the device ID from the config — same identity
		// the rest of the agent uses, which means peer lookups
		// through Convex / relay already agree on "who wrote
		// this."
		if cfg, err := LoadConfig(); err == nil && cfg != nil && cfg.DeviceID != "" {
			syncOriginIDVal = cfg.DeviceID
			return
		}
		// Fall back to a persisted local UUID so the origin
		// doesn't change every agent restart (which would look
		// like a split-brain on the remote side).
		base, err := ConfigDir()
		if err != nil {
			syncOriginIDVal = "local"
			return
		}
		dir := filepath.Join(base, "sync")
		_ = os.MkdirAll(dir, 0700)
		origPath := filepath.Join(dir, ".origin")
		if data, rerr := os.ReadFile(origPath); rerr == nil {
			syncOriginIDVal = strings.TrimSpace(string(data))
			if syncOriginIDVal != "" {
				return
			}
		}
		syncOriginIDVal = uuid.New().String()
		_ = os.WriteFile(origPath, []byte(syncOriginIDVal), 0600)
	})
	return syncOriginIDVal
}

// OpenSyncStore opens or creates a sync store for the given kind.
func OpenSyncStore(kind string) (*SyncStore, error) {
	if strings.ContainsAny(kind, "/\\") || kind == "" {
		return nil, fmt.Errorf("invalid sync kind %q", kind)
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "sync")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &SyncStore{
		kind:  kind,
		path:  filepath.Join(dir, kind+".json"),
		items: map[string]SyncItem{},
	}
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SyncStore) loadLocked() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Items map[string]SyncItem `json:"items"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Items != nil {
		s.items = payload.Items
	}
	return nil
}

func (s *SyncStore) saveLocked() error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"items":     s.items,
		"kind":      s.kind,
		"updatedAt": time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Set upserts a key with the origin + current timestamp. Returns
// the final stored item (which may be the previous value if this
// write lost an LWW race).
func (s *SyncStore) Set(key string, value json.RawMessage) (SyncItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	item := SyncItem{
		Key:       key,
		Value:     value,
		UpdatedAt: now,
		UpdatedBy: syncOrigin(),
	}
	s.items[key] = item
	if err := s.saveLocked(); err != nil {
		return item, err
	}
	return item, nil
}

// Delete marks a key as a tombstone at the current timestamp.
// Tombstones are retained so a peer that was offline when the
// delete happened learns about it on the next sync.
func (s *SyncStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.items[key]
	s.items[key] = SyncItem{
		Key:       key,
		UpdatedAt: time.Now().UnixMilli(),
		UpdatedBy: syncOrigin(),
		Deleted:   true,
		// Preserve prior value as null — saves space.
		Value: nil,
	}
	_ = existing
	return s.saveLocked()
}

// Get returns the live (non-tombstoned) value for a key, or
// (empty, false) if absent or deleted.
func (s *SyncStore) Get(key string) (SyncItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[key]
	if !ok || it.Deleted {
		return SyncItem{}, false
	}
	return it, true
}

// List returns every live item sorted by key.
func (s *SyncStore) List() []SyncItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SyncItem, 0, len(s.items))
	for _, it := range s.items {
		if !it.Deleted {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ListSince returns every record (including tombstones) with
// UpdatedAt >= since. Used by the sync pull endpoint.
func (s *SyncStore) ListSince(since int64) []SyncItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SyncItem, 0, len(s.items))
	for _, it := range s.items {
		if it.UpdatedAt >= since {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return out
}

// Merge integrates a batch of items from a peer. For each item:
//
//   - if we don't have the key → store the peer's version
//   - if we do and peer's UpdatedAt is newer → replace
//   - if UpdatedAt is equal → lexicographic UpdatedBy wins
//
// Returns the count of items that were actually applied.
func (s *SyncStore) Merge(incoming []SyncItem) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	applied := 0
	for _, it := range incoming {
		if it.Key == "" {
			continue
		}
		existing, have := s.items[it.Key]
		if !have || it.UpdatedAt > existing.UpdatedAt ||
			(it.UpdatedAt == existing.UpdatedAt && it.UpdatedBy > existing.UpdatedBy) {
			s.items[it.Key] = it
			applied++
		}
	}
	if applied > 0 {
		_ = s.saveLocked()
	}
	return applied
}

// Latest returns the highest UpdatedAt across every known item.
// Peers store this locally as their "last synced" mark so the
// next pull asks for only new records.
func (s *SyncStore) Latest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest int64
	for _, it := range s.items {
		if it.UpdatedAt > latest {
			latest = it.UpdatedAt
		}
	}
	return latest
}

// --- HTTP ----------------------------------------------------------

// syncKindAllowList pins the set of kinds the HTTP endpoint will
// expose. Opting in here is a deliberate security choice —
// arbitrary kind names shouldn't be able to create arbitrary
// files under ~/.yaver/sync.
var syncKindAllowList = map[string]bool{
	"env":      true,
	"flags":    true,
	"monitors": true,
	"presets":  true,
}

// handleSyncList serves GET /sync/<kind>?since=<ts>.
func (s *HTTPServer) handleSync(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/sync/")
	path = strings.Trim(path, "/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "missing sync kind")
		return
	}
	parts := strings.SplitN(path, "/", 2)
	kind := parts[0]
	if !syncKindAllowList[kind] {
		jsonError(w, http.StatusNotFound, "unknown sync kind")
		return
	}
	store, err := OpenSyncStore(kind)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		items := store.ListSince(since)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"kind":     kind,
			"items":    items,
			"origin":   syncOrigin(),
			"latestAt": store.Latest(),
		})
	case http.MethodPost:
		var body struct {
			Items []SyncItem `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		applied := store.Merge(body.Items)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"kind":     kind,
			"applied":  applied,
			"latestAt": store.Latest(),
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}
