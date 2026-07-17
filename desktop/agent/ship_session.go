package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// A ship runs detached, and reports by ID.
//
// It cannot be synchronous. The whole use case is "say it from the couch and go
// outside" — and a mobile deploy alone is a ~45 minute archive, so a request that
// waited for the result would time out long before the barrier finished, on the
// very surface (a phone) the feature exists for. Worse, a dropped connection
// cancelling a ship mid-deploy would race the thaw against the disconnect.
//
// So the request starts the barrier and returns an ID, exactly like autorun. The
// caller polls ship_status, or just waits for the notification — which is what a
// human outside actually does.
//
// In-memory, and that is deliberate rather than lazy: a ship that does not
// survive an agent restart is correct, because the freezes it holds do not
// survive either (autorunGate is in-memory too, and remote gates carry a lease
// that thaws them). Restart kills the coordinator and the fleet un-freezes on its
// own. Persisting the ship would create the one state that must not exist: a
// record of a barrier nobody is holding.

type shipSession struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"` // running | completed | failed
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt time.Time  `json:"finishedAt,omitempty"`
	Result     shipResult `json:"result"`
	cancel     context.CancelFunc
}

type shipSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*shipSession
}

var shipSessions = &shipSessionManager{sessions: map[string]*shipSession{}}

// start launches a ship and returns immediately.
//
// Refuses to start a second concurrent ship. Two ships would fight over the same
// freeze: the second would find the fleet already frozen, not own it, and its
// thaw would lift the first one's barrier mid-deploy. The gate's ownership return
// makes that survivable, but there is no reason to allow it — one deploy at a
// time is the entire point of a barrier.
func (m *shipSessionManager) start(parent context.Context, s *HTTPServer, opts shipOptions) (*shipSession, error) {
	m.mu.Lock()
	for _, existing := range m.sessions {
		if existing.Status == "running" {
			m.mu.Unlock()
			return nil, fmt.Errorf("ship %s is already running; a second ship would thaw the first one's fleet mid-deploy", existing.ID)
		}
	}
	// Detached from the request for the same reason autorun sessions are: the
	// barrier is daemon-owned and must outlive the call that asked for it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	sess := &shipSession{
		ID:        fmt.Sprintf("ship-%d", time.Now().UTC().UnixNano()),
		Status:    "running",
		StartedAt: time.Now().UTC(),
		cancel:    cancel,
	}
	m.sessions[sess.ID] = sess
	m.mu.Unlock()

	go func() {
		res := runShip(ctx, s, opts)
		m.mu.Lock()
		sess.Result = res
		sess.FinishedAt = time.Now().UTC()
		sess.Status = "completed"
		if !res.OK {
			sess.Status = "failed"
		}
		sess.cancel = nil
		m.mu.Unlock()
		cancel()
	}()
	return sess, nil
}

func (m *shipSessionManager) status(id string) ([]shipSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if id != "" {
		s, ok := m.sessions[id]
		if !ok {
			return nil, fmt.Errorf("ship %q not found", id)
		}
		return []shipSession{*s}, nil
	}
	out := make([]shipSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out, nil
}

// stop cancels a running ship. The barrier still thaws: runShip's defer runs on
// a context detached from this one, precisely so that cancelling a ship cannot
// strand a frozen fleet.
func (m *shipSessionManager) stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("ship %q not found", id)
	}
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
