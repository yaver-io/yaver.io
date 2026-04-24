package main

import (
	"encoding/json"
	"sync"
	"time"
)

type OperationState struct {
	ID          string                 `json:"id"`
	Kind        string                 `json:"kind"`
	Status      string                 `json:"status"`
	Phase       string                 `json:"phase,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Progress    float64                `json:"progress,omitempty"`
	DeviceID    string                 `json:"deviceId,omitempty"`
	ProjectPath string                 `json:"projectPath,omitempty"`
	StartedAt   int64                  `json:"startedAt"`
	UpdatedAt   int64                  `json:"updatedAt"`
	IncidentIDs []string               `json:"incidentIds,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type OperationFilter struct {
	Kind        string
	Status      string
	DeviceID    string
	ProjectPath string
	Limit       int
}

type operationStore struct {
	mu          sync.Mutex
	maxEntries  int
	order       []string
	items       map[string]OperationState
	subscribers map[chan OperationState]struct{}
}

var (
	operationStoreOnce sync.Once
	operationStoreInst *operationStore
)

const operationStoreMaxEntries = 500

func GlobalOperationStore() *operationStore {
	operationStoreOnce.Do(func() {
		operationStoreInst = &operationStore{
			maxEntries:  operationStoreMaxEntries,
			order:       []string{},
			items:       make(map[string]OperationState),
			subscribers: make(map[chan OperationState]struct{}),
		}
	})
	return operationStoreInst
}

func (s *operationStore) Upsert(op OperationState) OperationState {
	if s == nil {
		return op
	}
	now := time.Now().UnixMilli()
	if op.StartedAt == 0 {
		op.StartedAt = now
	}
	op.UpdatedAt = now
	if op.ID == "" {
		op.ID = "op_" + op.Kind + "_" + time.UnixMilli(now).UTC().Format("20060102T150405.000")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[op.ID]; !ok {
		s.order = append(s.order, op.ID)
	}
	s.items[op.ID] = op
	if len(s.order) > s.maxEntries {
		drop := s.order[0]
		s.order = s.order[1:]
		delete(s.items, drop)
	}
	for ch := range s.subscribers {
		select {
		case ch <- op:
		default:
		}
	}
	return op
}

func (s *operationStore) List(filter OperationFilter) []OperationState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]OperationState, 0, 32)
	for i := len(s.order) - 1; i >= 0; i-- {
		op := s.items[s.order[i]]
		if filter.Kind != "" && op.Kind != filter.Kind {
			continue
		}
		if filter.Status != "" && op.Status != filter.Status {
			continue
		}
		if filter.DeviceID != "" && op.DeviceID != filter.DeviceID {
			continue
		}
		if filter.ProjectPath != "" && op.ProjectPath != filter.ProjectPath {
			continue
		}
		out = append(out, op)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *operationStore) Subscribe() (<-chan OperationState, []OperationState, func()) {
	ch := make(chan OperationState, 256)
	if s == nil {
		close(ch)
		return ch, nil, func() {}
	}
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	snapshot := make([]OperationState, 0, len(s.order))
	for _, id := range s.order {
		snapshot = append(snapshot, s.items[id])
	}
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

func (s *operationStore) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.List(OperationFilter{Limit: s.maxEntries}))
}
