package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type IncidentSeverity string

const (
	IncidentSeverityInfo  IncidentSeverity = "info"
	IncidentSeverityWarn  IncidentSeverity = "warn"
	IncidentSeverityError IncidentSeverity = "error"
	IncidentSeverityFatal IncidentSeverity = "fatal"
)

type IncidentEvent struct {
	ID              string                 `json:"id"`
	Timestamp       int64                  `json:"timestamp"`
	Severity        IncidentSeverity       `json:"severity"`
	Category        string                 `json:"category"`
	Code            string                 `json:"code"`
	Source          string                 `json:"source"`
	Title           string                 `json:"title"`
	UserMessage     string                 `json:"userMessage"`
	TechnicalInfo   string                 `json:"technicalInfo,omitempty"`
	SuggestedAction string                 `json:"suggestedAction,omitempty"`
	OperationID     string                 `json:"operationId,omitempty"`
	DeviceID        string                 `json:"deviceId,omitempty"`
	ProjectPath     string                 `json:"projectPath,omitempty"`
	Target          string                 `json:"target,omitempty"`
	LogsAvailable   bool                   `json:"logsAvailable"`
	LogRefs         []string               `json:"logRefs,omitempty"`
	CorrelationID   string                 `json:"correlationId,omitempty"`
	Recoverable     bool                   `json:"recoverable"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Resolved        bool                   `json:"resolved"`
	ResolvedAt      int64                  `json:"resolvedAt,omitempty"`
	ResolutionNote  string                 `json:"resolutionNote,omitempty"`
}

type IncidentFilter struct {
	Category        string
	Severity        string
	Code            string
	DeviceID        string
	ProjectPath     string
	IncludeResolved bool
	Limit           int
}

type IncidentSummary struct {
	Total            int            `json:"total"`
	Open             int            `json:"open"`
	Resolved         int            `json:"resolved"`
	ByCategory       map[string]int `json:"byCategory"`
	BySeverity       map[string]int `json:"bySeverity"`
	TopReasonCodes   []string       `json:"topReasonCodes,omitempty"`
	LastIncidentAt   int64          `json:"lastIncidentAt,omitempty"`
}

type incidentStore struct {
	mu          sync.Mutex
	path        string
	maxEntries  int
	entries     []IncidentEvent
	subscribers map[chan IncidentEvent]struct{}
}

type IncidentKey struct {
	Category    string
	Code        string
	DeviceID    string
	ProjectPath string
	Target      string
}

var (
	incidentStoreOnce sync.Once
	incidentStoreInst *incidentStore
)

const incidentStoreMaxEntries = 2000

func GlobalIncidentStore() *incidentStore {
	incidentStoreOnce.Do(func() {
		base, err := ConfigDir()
		if err != nil {
			incidentStoreInst = &incidentStore{
				maxEntries:  incidentStoreMaxEntries,
				entries:     []IncidentEvent{},
				subscribers: make(map[chan IncidentEvent]struct{}),
			}
			return
		}
		dir := filepath.Join(base, "incidents")
		_ = os.MkdirAll(dir, 0700)
		s := &incidentStore{
			path:        filepath.Join(dir, "incidents.jsonl"),
			maxEntries:  incidentStoreMaxEntries,
			entries:     []IncidentEvent{},
			subscribers: make(map[chan IncidentEvent]struct{}),
		}
		s.load()
		incidentStoreInst = s
	})
	return incidentStoreInst
}

func (s *incidentStore) load() {
	if s == nil || s.path == "" {
		return
	}
	f, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var ev IncidentEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		s.entries = append(s.entries, ev)
	}
	if len(s.entries) > s.maxEntries {
		s.entries = s.entries[len(s.entries)-s.maxEntries:]
	}
}

func (s *incidentStore) Append(ev IncidentEvent) IncidentEvent {
	if s == nil {
		return ev
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if strings.TrimSpace(ev.ID) == "" {
		ev.ID = incidentID(ev.Timestamp, ev.Code, ev.DeviceID)
	}
	if ev.Severity == "" {
		ev.Severity = IncidentSeverityError
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, ev)
	if len(s.entries) > s.maxEntries {
		s.entries = s.entries[len(s.entries)-s.maxEntries:]
	}
	s.appendToDisk(ev)
	for ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev
}

func (s *incidentStore) appendToDisk(ev IncidentEvent) {
	if s.path == "" {
		return
	}
	if info, err := os.Stat(s.path); err == nil && info.Size() > 8*1024*1024 {
		_ = os.Rename(s.path, s.path+".old")
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	if data, err := json.Marshal(ev); err == nil {
		_, _ = f.Write(data)
		_, _ = f.Write([]byte{'\n'})
	}
}

func (s *incidentStore) List(filter IncidentFilter) []IncidentEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]IncidentEvent, 0, 64)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	for i := len(s.entries) - 1; i >= 0; i-- {
		ev := s.entries[i]
		if !filter.IncludeResolved && ev.Resolved {
			continue
		}
		if filter.Category != "" && ev.Category != filter.Category {
			continue
		}
		if filter.Severity != "" && string(ev.Severity) != filter.Severity {
			continue
		}
		if filter.Code != "" && ev.Code != filter.Code {
			continue
		}
		if filter.DeviceID != "" && ev.DeviceID != filter.DeviceID {
			continue
		}
		if filter.ProjectPath != "" && ev.ProjectPath != filter.ProjectPath {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *incidentStore) Get(id string) *IncidentEvent {
	if s == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].ID == id {
			ev := s.entries[i]
			return &ev
		}
	}
	return nil
}

func (s *incidentStore) setResolved(id string, resolved bool, note string) bool {
	if s == nil || strings.TrimSpace(id) == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].ID != id {
			continue
		}
		s.entries[i].Resolved = resolved
		if resolved {
			s.entries[i].ResolvedAt = time.Now().UnixMilli()
			s.entries[i].ResolutionNote = note
		} else {
			s.entries[i].ResolvedAt = 0
			s.entries[i].ResolutionNote = ""
		}
		for ch := range s.subscribers {
			select {
			case ch <- s.entries[i]:
			default:
			}
		}
		return true
	}
	return false
}

func (s *incidentStore) Resolve(id, note string) bool {
	return s.setResolved(id, true, note)
}

func (s *incidentStore) Reopen(id string) bool {
	return s.setResolved(id, false, "")
}

func (s *incidentStore) UpsertOpen(key IncidentKey, ev IncidentEvent) IncidentEvent {
	if s == nil {
		return ev
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if ev.Severity == "" {
		ev.Severity = IncidentSeverityError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		cur := s.entries[i]
		if cur.Resolved {
			continue
		}
		if cur.Category != key.Category || cur.Code != key.Code || cur.DeviceID != key.DeviceID || cur.ProjectPath != key.ProjectPath || cur.Target != key.Target {
			continue
		}
		ev.ID = cur.ID
		ev.Resolved = false
		s.entries[i] = ev
		for ch := range s.subscribers {
			select {
			case ch <- ev:
			default:
			}
		}
		return ev
	}
	if strings.TrimSpace(ev.ID) == "" {
		ev.ID = incidentID(ev.Timestamp, ev.Code, ev.DeviceID)
	}
	s.entries = append(s.entries, ev)
	if len(s.entries) > s.maxEntries {
		s.entries = s.entries[len(s.entries)-s.maxEntries:]
	}
	s.appendToDisk(ev)
	for ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev
}

func (s *incidentStore) ResolveOpenByKey(key IncidentKey, note string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resolvedAny := false
	for i := range s.entries {
		cur := &s.entries[i]
		if cur.Resolved {
			continue
		}
		if cur.Category != key.Category || cur.Code != key.Code || cur.DeviceID != key.DeviceID || cur.ProjectPath != key.ProjectPath || cur.Target != key.Target {
			continue
		}
		cur.Resolved = true
		cur.ResolvedAt = time.Now().UnixMilli()
		cur.ResolutionNote = note
		resolvedAny = true
		for ch := range s.subscribers {
			select {
			case ch <- *cur:
			default:
			}
		}
	}
	return resolvedAny
}

func (s *incidentStore) Summary() IncidentSummary {
	out := IncidentSummary{
		ByCategory: make(map[string]int),
		BySeverity: make(map[string]int),
	}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reasonCounts := make(map[string]int)
	for _, ev := range s.entries {
		out.Total++
		if ev.Resolved {
			out.Resolved++
		} else {
			out.Open++
		}
		if ev.Category != "" {
			out.ByCategory[ev.Category]++
		}
		if ev.Severity != "" {
			out.BySeverity[string(ev.Severity)]++
		}
		if ev.Code != "" {
			reasonCounts[ev.Code]++
		}
		if ev.Timestamp > out.LastIncidentAt {
			out.LastIncidentAt = ev.Timestamp
		}
	}
	for code, count := range reasonCounts {
		if count >= 2 {
			out.TopReasonCodes = append(out.TopReasonCodes, code)
		}
	}
	return out
}

func (s *incidentStore) Subscribe() (<-chan IncidentEvent, []IncidentEvent, func()) {
	ch := make(chan IncidentEvent, 256)
	if s == nil {
		close(ch)
		return ch, nil, func() {}
	}
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	snapshot := make([]IncidentEvent, len(s.entries))
	copy(snapshot, s.entries)
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, snapshot, cancel
}

func incidentID(ts int64, code, deviceID string) string {
	base := strings.TrimSpace(code)
	if base == "" {
		base = "incident"
	}
	base = strings.ReplaceAll(base, ".", "_")
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return "inc_" + base + "_" + time.UnixMilli(ts).UTC().Format("20060102T150405.000")
	}
	return "inc_" + base + "_" + deviceID + "_" + time.UnixMilli(ts).UTC().Format("20060102T150405.000")
}
