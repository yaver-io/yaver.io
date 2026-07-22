package main

// Durable, owner-scoped backing store for the runtime-turn queue.
//
// Postmortem notes — each bullet is a real defect this file exists to prevent,
// stated as the false green it produced:
//
//   - "Captured. I'll attach it to the current app." was a LIE across a restart.
//     The queue was a package-level map with no disk backing, so every idea a
//     user spoke into their watch died with the agent process. The surface
//     cheerfully confirmed capture; nothing had been captured. An idea queue
//     that forgets is worse than no idea queue, because the user stops keeping
//     their own notes.
//
//   - Listing was global. runtimeQueue.list() returned every item on the box
//     regardless of who created it, so on a multi-user agent (multiuser.go) one
//     tenant's spoken utterances — raw task input prompts — rendered on another
//     tenant's watch. Items are now stamped with the acting user and filtered on
//     read; an empty owner matches only an empty owner.
//
//   - Polling reordered the list. runtime_turns refreshed every task-backed item
//     on every call, and update() unconditionally bumped UpdatedAt, which is the
//     list's sort key. A phone polling every 2s permanently churned the order and
//     sank `captured` items (which never refresh) to the bottom forever. update()
//     now bumps the clock only when a mutable field actually changed.
//
//   - Unbounded growth. Nothing ever evicted, so a long-lived agent accumulated
//     every utterance ever spoken. Capped, terminal-first.
//
// PRIVACY: this file is LOCAL ONLY and must stay that way. Queue items carry
// `Utterance` (a raw task input prompt) and `Target.WorkDir` (an absolute path
// that leaks the user's home-dir username). Both are on the forbidden list in
// convex_privacy_test.go. Nothing here may ever be routed through
// convexSyncer.callMutation.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// runtimeQueueMaxItems bounds the on-disk queue. Terminal items are evicted
// before live ones so a long backlog can never push out work still in flight.
const runtimeQueueMaxItems = 500

type runtimeQueueStore struct {
	mu     sync.RWMutex
	items  map[string]*RuntimeTurnQueueItem
	path   string
	loaded bool
}

var runtimeQueue = &runtimeQueueStore{items: make(map[string]*RuntimeTurnQueueItem)}

// storePath resolves ~/.yaver/runtime-queue.json. It is deliberately tolerant:
// if the home dir cannot be resolved the queue degrades to in-memory rather
// than failing the user's turn. A spoken idea that survives only until restart
// still beats an error at the microphone.
func (s *runtimeQueueStore) storePath() string {
	if s.path != "" {
		return s.path
	}
	dir, err := yaverDir()
	if err != nil {
		return ""
	}
	s.path = filepath.Join(dir, "runtime-queue.json")
	return s.path
}

// ensureLoaded hydrates the queue from disk exactly once. Callers must hold the
// write lock.
func (s *runtimeQueueStore) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	path := s.storePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var items []RuntimeTurnQueueItem
	if err := json.Unmarshal(data, &items); err != nil {
		// A corrupt store must not wedge the agent. Start clean; the file is
		// overwritten on the next mutation.
		return
	}
	for i := range items {
		item := items[i]
		if item.ItemID == "" {
			continue
		}
		// Work that was mid-flight when the agent died cannot still be running.
		// Reflect that honestly rather than showing a spinner forever.
		if item.State == runtimeQueueStateRunning {
			item.State = runtimeQueueStateQueued
			item.Spoken = "Picked back up after a restart."
		}
		s.items[item.ItemID] = &item
	}
}

// saveLocked writes the queue atomically. Callers must hold the write lock.
// Atomic because a torn write here loses the whole idea backlog, not one row.
func (s *runtimeQueueStore) saveLocked() {
	path := s.storePath()
	if path == "" {
		return
	}
	items := make([]RuntimeTurnQueueItem, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}

// evictLocked enforces runtimeQueueMaxItems, dropping terminal items (oldest
// first) before touching anything still live.
func (s *runtimeQueueStore) evictLocked() {
	if len(s.items) <= runtimeQueueMaxItems {
		return
	}
	items := make([]*RuntimeTurnQueueItem, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		ti, tj := isRuntimeQueueTerminal(items[i].State), isRuntimeQueueTerminal(items[j].State)
		if ti != tj {
			return ti // terminal items sort first == evicted first
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})
	for i := 0; i < len(items)-runtimeQueueMaxItems; i++ {
		delete(s.items, items[i].ItemID)
	}
}

func isRuntimeQueueTerminal(state string) bool {
	switch state {
	case runtimeQueueStateDone, runtimeQueueStateFailed, runtimeQueueStateCancelled:
		return true
	default:
		return false
	}
}

func (s *runtimeQueueStore) add(item *RuntimeTurnQueueItem) RuntimeTurnQueueItem {
	if item.ItemID == "" {
		item.ItemID = newRuntimeQueueID()
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	cp := *item
	s.items[item.ItemID] = &cp
	s.evictLocked()
	s.saveLocked()
	return cp
}

// update mutates one item in place. UpdatedAt advances only when a mutable
// field actually changed — see the polling-churn note at the top of this file.
func (s *runtimeQueueStore) update(id string, fn func(*RuntimeTurnQueueItem)) (RuntimeTurnQueueItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	item, ok := s.items[id]
	if !ok {
		return RuntimeTurnQueueItem{}, false
	}
	before := runtimeItemFingerprint(item)
	fn(item)
	if runtimeItemFingerprint(item) != before {
		item.UpdatedAt = time.Now().UTC()
		s.saveLocked()
	}
	return *item, true
}

// updateAny mutates an item WITHOUT an owner check. It exists for inbound
// device acknowledgements, which arrive on the black-box event stream and carry
// a device identity rather than a user session — there is no ActorUserID to
// scope by. Safe because the caller cannot choose the item: the correlation id
// was minted by this agent and handed to the device on the way out, so an
// attacker would have to guess a random turn id to reach anything, and the only
// reachable mutation is the reload outcome.
//
// Do NOT use this for anything a user-facing verb can call.
func (s *runtimeQueueStore) updateAny(id string, fn func(*RuntimeTurnQueueItem)) (RuntimeTurnQueueItem, bool) {
	return s.update(id, fn)
}

// runtimeItemFingerprint captures every field a refresh can legitimately
// change. If this string is unchanged, the refresh was a no-op and must not
// disturb the item's position in the list.
func runtimeItemFingerprint(item *RuntimeTurnQueueItem) string {
	tt := ""
	if item.TestTarget != nil {
		tt = fmt.Sprintf("%s|%s|%d", item.TestTarget.State, item.TestTarget.Detail, item.TestTarget.Listeners)
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		item.State, item.Spoken, item.Error, item.TaskID, item.Session, item.Runner, tt)
}

// get returns an item only if owner may see it. Cross-owner reads return
// not-found rather than a permission error: a caller must not be able to probe
// which turn IDs exist on a shared box.
func (s *runtimeQueueStore) get(owner, id string) (RuntimeTurnQueueItem, bool) {
	s.mu.Lock()
	s.ensureLoadedLocked()
	s.mu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok || item.OwnerUserID != owner {
		return RuntimeTurnQueueItem{}, false
	}
	return *item, true
}

func (s *runtimeQueueStore) list(owner string, limit int) []RuntimeTurnQueueItem {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	s.mu.Lock()
	s.ensureLoadedLocked()
	s.mu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]RuntimeTurnQueueItem, 0, len(s.items))
	for _, item := range s.items {
		if item.OwnerUserID != owner {
			continue
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// newRuntimeQueueID pairs the clock with 32 bits of randomness. A bare
// UnixNano() collides on coarse-clock platforms (Windows ticks ~100ns), and a
// collision here silently OVERWRITES someone's queued work rather than erroring.
func newRuntimeQueueID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("rq_%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("rq_%d_%s", time.Now().UTC().UnixNano(), hex.EncodeToString(b[:]))
}
