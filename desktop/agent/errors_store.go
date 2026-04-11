package main

// errors_store.go — cross-device error aggregation for the mobile
// Errors tab. BlackBox already captures individual error events
// per-device; this store flattens them into a per-fingerprint
// ledger so the dev can see "this error hit 12 devices, 47 times
// in the last 2 hours" on a single mobile screen.
//
// Deliberately file-based (~/.yaver/errors/store.json) so
// scheduler subprocess ticks and `yaver serve` restarts see the
// same data. Ring-bounded to the last 500 distinct fingerprints
// so a runaway broken build can't fill the disk.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrorRecord is one deduped error entry. Occurrences accumulate
// across every SDK session that hits the same fingerprint.
type ErrorRecord struct {
	Fingerprint  string        `json:"fingerprint"`  // sha256 of message + first stack line
	Message      string        `json:"message"`
	FirstFrame   string        `json:"firstFrame,omitempty"`
	Stack        []string      `json:"stack,omitempty"`
	FirstSeenAt  string        `json:"firstSeenAt"`
	LastSeenAt   string        `json:"lastSeenAt"`
	Count        int           `json:"count"`
	DeviceIDs    []string      `json:"deviceIds"` // unique devices hit
	Fatal        bool          `json:"fatal,omitempty"`
	Resolved     bool          `json:"resolved,omitempty"`
	ResolvedAt   string        `json:"resolvedAt,omitempty"`
	ResolvedNote string        `json:"resolvedNote,omitempty"`
	Recent       []ErrorSample `json:"recent,omitempty"` // last 10 raw events for context
}

// ErrorSample is a trimmed BlackBoxEvent retained alongside the
// dedup record so the Errors tab can show recent occurrences
// without pulling the full per-device BlackBox stream.
type ErrorSample struct {
	DeviceID  string            `json:"deviceId"`
	Timestamp int64             `json:"timestamp"`
	Message   string            `json:"message"`
	Route     string            `json:"route,omitempty"`
	Source    string            `json:"source,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ErrorStore is the flat, fingerprint-keyed ledger.
type ErrorStore struct {
	mu      sync.Mutex
	path    string
	records map[string]*ErrorRecord // fingerprint → record
}

const errorStoreMaxRecords = 500

// NewErrorStore opens or creates the on-disk ledger.
func NewErrorStore() (*ErrorStore, error) {
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "errors")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &ErrorStore{
		path:    filepath.Join(dir, "store.json"),
		records: make(map[string]*ErrorRecord),
	}
	_ = s.load()
	return s, nil
}

// Record ingests one error event from a BlackBox session,
// bumping the matching fingerprint or creating a new one.
func (s *ErrorStore) Record(deviceID string, ev BlackBoxEvent) {
	if ev.Type != "error" && !ev.IsFatal {
		return
	}
	fp := errorFingerprint(ev)
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[fp]
	now := time.Now().UTC().Format(time.RFC3339)
	if !ok {
		rec = &ErrorRecord{
			Fingerprint: fp,
			Message:     ev.Message,
			FirstFrame:  firstStackLine(ev.Stack),
			Stack:       ev.Stack,
			FirstSeenAt: now,
			LastSeenAt:  now,
			Count:       0,
			DeviceIDs:   []string{},
			Fatal:       ev.IsFatal,
		}
		s.records[fp] = rec
	}
	rec.Count++
	rec.LastSeenAt = now
	if ev.IsFatal {
		rec.Fatal = true
	}
	if !containsString(rec.DeviceIDs, deviceID) {
		rec.DeviceIDs = append(rec.DeviceIDs, deviceID)
	}
	// Retain the last 10 raw occurrences so the detail view
	// has real per-device context.
	sample := ErrorSample{
		DeviceID:  deviceID,
		Timestamp: ev.Timestamp,
		Message:   ev.Message,
		Route:     ev.Route,
		Source:    ev.Source,
	}
	if len(ev.Metadata) > 0 {
		sample.Metadata = map[string]string{}
		for k, v := range ev.Metadata {
			if sv, ok := v.(string); ok {
				sample.Metadata[k] = sv
			}
		}
	}
	rec.Recent = append(rec.Recent, sample)
	if len(rec.Recent) > 10 {
		rec.Recent = rec.Recent[len(rec.Recent)-10:]
	}

	// Cap at errorStoreMaxRecords — evict the oldest resolved
	// records first, then oldest by LastSeenAt.
	if len(s.records) > errorStoreMaxRecords {
		s.evictOldestLocked()
	}

	_ = s.saveLocked()
}

// MarkResolved flips a record's resolved flag. Callable from the
// mobile app via /errors/resolve; leaves the record in place so
// history survives.
func (s *ErrorStore) MarkResolved(fingerprint, note string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[fingerprint]
	if !ok {
		return false
	}
	rec.Resolved = true
	rec.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	rec.ResolvedNote = note
	_ = s.saveLocked()
	return true
}

// Reopen clears the resolved flag. Rarely needed but useful if a
// "fixed" error starts firing again.
func (s *ErrorStore) Reopen(fingerprint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[fingerprint]
	if !ok {
		return false
	}
	rec.Resolved = false
	rec.ResolvedAt = ""
	rec.ResolvedNote = ""
	_ = s.saveLocked()
	return true
}

// List returns every record, newest first by last-seen.
// `includeResolved=false` filters out fixed errors.
func (s *ErrorStore) List(includeResolved bool) []*ErrorRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ErrorRecord, 0, len(s.records))
	for _, r := range s.records {
		if !includeResolved && r.Resolved {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeenAt > out[j].LastSeenAt
	})
	return out
}

// Get returns a single record by fingerprint.
func (s *ErrorStore) Get(fingerprint string) *ErrorRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[fingerprint]
}

// Stats summarises the store for the mobile card header:
// total open, total resolved, last 24h count.
func (s *ErrorStore) Stats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	open, resolved, last24h := 0, 0, 0
	cutoff := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	for _, r := range s.records {
		if r.Resolved {
			resolved++
			continue
		}
		open++
		if r.LastSeenAt > cutoff {
			last24h += r.Count
		}
	}
	return map[string]int{
		"open":          open,
		"resolved":      resolved,
		"openLast24h":   last24h,
		"totalDistinct": len(s.records),
	}
}

// --- internal ------------------------------------------------------------

func (s *ErrorStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Records map[string]*ErrorRecord `json:"records"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Records != nil {
		s.records = payload.Records
	}
	return nil
}

func (s *ErrorStore) saveLocked() error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"records": s.records,
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

func (s *ErrorStore) evictOldestLocked() {
	// Collect fingerprints sorted by LastSeenAt asc (oldest first).
	// Prefer evicting resolved ones even if they're newer.
	type candidate struct {
		fp       string
		resolved bool
		lastSeen string
	}
	cands := make([]candidate, 0, len(s.records))
	for fp, r := range s.records {
		cands = append(cands, candidate{fp, r.Resolved, r.LastSeenAt})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].resolved != cands[j].resolved {
			return cands[i].resolved
		}
		return cands[i].lastSeen < cands[j].lastSeen
	})
	// Drop enough to bring us back under the cap.
	overflow := len(s.records) - errorStoreMaxRecords + 1
	for i := 0; i < overflow && i < len(cands); i++ {
		delete(s.records, cands[i].fp)
	}
}

func errorFingerprint(ev BlackBoxEvent) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(ev.Message)))
	h.Write([]byte{'|'})
	if len(ev.Stack) > 0 {
		h.Write([]byte(ev.Stack[0]))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func firstStackLine(stack []string) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[0]
}

func containsString(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// errorStoreSingleton is lazily initialized on first access. The
// blackbox session pipe calls GlobalErrorStore().Record for every
// error-typed event, and the HTTP handlers in errors_http.go read
// from the same singleton.
var (
	errorStoreOnce sync.Once
	errorStoreInst *ErrorStore
)

func GlobalErrorStore() *ErrorStore {
	errorStoreOnce.Do(func() {
		s, err := NewErrorStore()
		if err != nil {
			// Fall back to an in-memory store so BlackBox ingest
			// never panics; the dev just loses persistence.
			errorStoreInst = &ErrorStore{
				records: make(map[string]*ErrorRecord),
			}
			return
		}
		errorStoreInst = s
	})
	return errorStoreInst
}
