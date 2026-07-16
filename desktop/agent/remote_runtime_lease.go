package main

// remote_runtime_lease.go — P5 concurrency arbitration.
//
// Today every viewer's control POST wins last-writer-wins. On the
// n2n loop that means phone + TV would fight the moment they both
// try to drive one session — precisely the "phone + TV at once"
// case the design targets.
//
// The lease: at any moment ≤1 client holds *controller* role for a
// session; the rest are *viewers*. Any client can `take` the lease
// (either it's free, held by someone else past a soft timeout, or
// the caller passes `force=true`); the holder can `release` it.
// Every mutation is broadcast to all peers via live.sendEventJSON so
// the picker UI + control UI stay in sync without polling.
//
// A control POST goes through checkAndRefresh(clientID): if the
// caller is not the holder AND a holder exists, the request is
// rejected with a clear "someone else is driving" error carrying
// the holder id — the viewer can prompt the user to take over.
//
// Deliberately in-process only: the goal is arbitration among the
// clients this agent is serving, not a distributed lock. Relay-bus
// registry (also P5) is what makes the lease *visible* across a
// user's fleet; that lands separately.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ControlLease is a single-writer lease per remote-runtime session.
// idleTimeout: how long the current holder can go without a control
// call before another caller can steal the lease without --force.
type ControlLease struct {
	mu           sync.Mutex
	holderID     string
	holderLabel  string
	lastActivity time.Time
	idleTimeout  time.Duration
}

// LeaseSnapshot is the serialisable view broadcast to peers.
type LeaseSnapshot struct {
	SessionID    string `json:"sessionId"`
	HolderID     string `json:"holderId,omitempty"`
	HolderLabel  string `json:"holderLabel,omitempty"`
	LastActivity string `json:"lastActivity,omitempty"`
	Held         bool   `json:"held"`
}

const defaultControlLeaseIdle = 60 * time.Second

// TakeControl assigns the lease to clientID. Succeeds when:
//   * the lease is free
//   * force=true
//   * the current holder has been idle > idleTimeout
//
// Otherwise returns an error naming the current holder so the UI can
// prompt "TV is driving — take over?".
func (l *ControlLease) TakeControl(clientID, clientLabel string, force bool, now time.Time) (LeaseSnapshot, error) {
	if strings.TrimSpace(clientID) == "" {
		return LeaseSnapshot{}, fmt.Errorf("clientId is required to take control")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holderID == "" || l.holderID == clientID || force || (l.idleTimeout > 0 && now.Sub(l.lastActivity) > l.idleTimeout) {
		l.holderID = clientID
		l.holderLabel = strings.TrimSpace(clientLabel)
		l.lastActivity = now
		return l.snapshotLocked(), nil
	}
	return l.snapshotLocked(), fmt.Errorf("session is controlled by %s — retry with force=true to override", labelOrID(l.holderID, l.holderLabel))
}

// ReleaseControl clears the holder when clientID matches (or force).
func (l *ControlLease) ReleaseControl(clientID string, force bool) LeaseSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holderID == clientID || force || l.holderID == "" {
		l.holderID = ""
		l.holderLabel = ""
		l.lastActivity = time.Time{}
	}
	return l.snapshotLocked()
}

// CheckAndRefresh is the gate every ExecuteControl call goes through.
// Empty clientID = anonymous (legacy web viewer) — allowed only when
// the lease is free (backward compat with viewers that don't send an
// id yet). A held lease rejects strangers.
func (l *ControlLease) CheckAndRefresh(clientID string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holderID == "" {
		if strings.TrimSpace(clientID) != "" {
			l.holderID = clientID
			l.lastActivity = now
		}
		return nil
	}
	if strings.TrimSpace(clientID) == "" {
		return fmt.Errorf("session is controlled by %s — send clientId to take over", labelOrID(l.holderID, l.holderLabel))
	}
	if l.holderID != clientID {
		if l.idleTimeout > 0 && now.Sub(l.lastActivity) > l.idleTimeout {
			l.holderID = clientID
			l.lastActivity = now
			return nil
		}
		return fmt.Errorf("session is controlled by %s — take control first", labelOrID(l.holderID, l.holderLabel))
	}
	l.lastActivity = now
	return nil
}

// Snapshot returns the current lease view without any mutation.
func (l *ControlLease) Snapshot() LeaseSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

func (l *ControlLease) snapshotLocked() LeaseSnapshot {
	snap := LeaseSnapshot{
		HolderID:    l.holderID,
		HolderLabel: l.holderLabel,
		Held:        l.holderID != "",
	}
	if !l.lastActivity.IsZero() {
		snap.LastActivity = l.lastActivity.UTC().Format(time.RFC3339Nano)
	}
	return snap
}

func labelOrID(id, label string) string {
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	if strings.TrimSpace(id) == "" {
		return "another client"
	}
	return id
}
