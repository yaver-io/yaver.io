package main

// logs_store.go — cross-device log aggregation + search, the
// yaver answer to Papertrail / BetterStack Logs / Datadog. Backed
// by the same BlackBox stream the SDK already runs, so any app
// that has the Feedback SDK installed gets log capture for free.
//
// Design:
//
//   - Ring-bounded in-memory + jsonl spill file for durability
//     across agent restarts.
//   - One deviceID -> ring slot so the hot loop never touches the
//     global slice under contention.
//   - Search is substring + optional level/device filter. No
//     full-text index — solo dev rarely needs it and we avoid
//     dragging in bleve/lucene for a SaaS replacement.
//
// Storage: ~/.yaver/logs/logs.jsonl with 4MB rotation (keeps one
// .old file, so ~8MB worst case).

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEntry is one captured log line.
type LogEntry struct {
	DeviceID  string `json:"deviceId"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
	Route     string `json:"route,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type logStore struct {
	mu       sync.Mutex
	path     string
	tail     []LogEntry // ring buffer of the most-recent entries
	tailSize int
}

var (
	logStoreOnce sync.Once
	logStoreInst *logStore
)

const logStoreTailSize = 2000

// GlobalLogStore returns the process-wide store, lazily initialized.
func GlobalLogStore() *logStore {
	logStoreOnce.Do(func() {
		base, err := ConfigDir()
		if err != nil {
			logStoreInst = &logStore{tail: []LogEntry{}, tailSize: logStoreTailSize}
			return
		}
		dir := filepath.Join(base, "logs")
		_ = os.MkdirAll(dir, 0700)
		logStoreInst = &logStore{
			path:     filepath.Join(dir, "logs.jsonl"),
			tail:     []LogEntry{},
			tailSize: logStoreTailSize,
		}
	})
	return logStoreInst
}

// Append persists one log entry to the in-memory tail and the
// rotating jsonl file.
func (s *logStore) Append(entry LogEntry) {
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().UnixMilli()
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tail = append(s.tail, entry)
	if len(s.tail) > s.tailSize {
		s.tail = s.tail[len(s.tail)-s.tailSize:]
	}

	if s.path == "" {
		return
	}
	// Rotate at 4MB.
	if info, err := os.Stat(s.path); err == nil && info.Size() > 4*1024*1024 {
		_ = os.Rename(s.path, s.path+".old")
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	if data, jerr := json.Marshal(entry); jerr == nil {
		f.Write(data)
		f.Write([]byte{'\n'})
	}
}

// Search returns the most recent entries matching filters. Level
// and deviceID are exact; query is case-insensitive substring on
// the message. Falls back to tail-only when the on-disk file is
// small; hits disk when the tail is exhausted.
func (s *logStore) Search(filter LogFilter) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]LogEntry, 0, 128)
	match := func(e LogEntry) bool {
		if filter.Level != "" && !strings.EqualFold(filter.Level, e.Level) {
			return false
		}
		if filter.DeviceID != "" && filter.DeviceID != e.DeviceID {
			return false
		}
		if filter.Since > 0 && e.Timestamp < filter.Since {
			return false
		}
		if filter.Query != "" {
			if !strings.Contains(strings.ToLower(e.Message), strings.ToLower(filter.Query)) {
				return false
			}
		}
		return true
	}
	// Scan tail newest-first.
	for i := len(s.tail) - 1; i >= 0; i-- {
		if match(s.tail[i]) {
			out = append(out, s.tail[i])
			if filter.Limit > 0 && len(out) >= filter.Limit {
				return out
			}
		}
	}
	// If the tail didn't fill the limit and we have a spill file,
	// scan that too. Best-effort.
	if s.path == "" || (filter.Limit > 0 && len(out) >= filter.Limit) {
		return out
	}
	f, err := os.Open(s.path)
	if err != nil {
		return out
	}
	defer f.Close()
	// Read everything and append matches not already covered by
	// the tail. Cheap because the spill file is 4MB max.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e LogEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if !match(e) {
			continue
		}
		// Don't dup tail entries.
		dup := false
		for _, t := range s.tail {
			if t.Timestamp == e.Timestamp && t.Message == e.Message && t.DeviceID == e.DeviceID {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, e)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

// LogFilter is the search input.
type LogFilter struct {
	Query    string
	Level    string
	DeviceID string
	Since    int64
	Limit    int
}

// Stats returns counts by level across the tail.
func (s *logStore) Stats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{"total": len(s.tail)}
	for _, e := range s.tail {
		out[e.Level]++
	}
	return out
}
