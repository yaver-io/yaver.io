package main

// Deterministic leader election. No voting round, no Raft — just a
// pure function over the current bus peer view. Matches the design
// in docs/p2p-bus-architecture.md §5.
//
//     leader_at(now) = min(deviceId among peers whose latest heartbeat
//                           was within the last aliveWindow)
//
// Works because every subscriber has the same peer view (within bus
// convergence latency, ~1 tick = 1 min). Split-brain risk is bounded:
// if the mesh partitions, both halves run their own leader, which is
// safe for idempotent work. Work that requires strict single-writer
// semantics must gate on a Convex-backed lease token in addition.

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

const (
	leaderAliveWindow = 5 * time.Minute
)

// PeerPresence is the payload on peer/{id}/online | ping events.
// Kept minimal so heartbeat traffic stays tiny.
type PeerPresence struct {
	DeviceID   DeviceID `json:"deviceId"`
	Hostname   string   `json:"hostname,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	Version    string   `json:"version,omitempty"`
	StartedAt  int64    `json:"startedAt,omitempty"`  // unix millis
	LastSeenAt int64    `json:"lastSeenAt,omitempty"` // updated locally on each event
}

// LeaderTracker maintains the per-device cache of peer presence
// derived from bus events. Exposes LeaderAt(now) which callers use
// to decide "is it me that runs the daily cleanup".
type LeaderTracker struct {
	mu    sync.RWMutex
	peers map[DeviceID]PeerPresence
	self  DeviceID
	nowFn func() time.Time
}

func NewLeaderTracker(selfID DeviceID) *LeaderTracker {
	return &LeaderTracker{
		peers: map[DeviceID]PeerPresence{},
		self:  selfID,
		nowFn: time.Now,
	}
}

// WireTo subscribes to peer/* and updates the cache on each event.
// Returns an unsubscribe fn. Typically called once at boot.
func (l *LeaderTracker) WireTo(b *Bus) func() {
	return b.Subscribe("peer", func(evt BusEvent) {
		var p PeerPresence
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return
		}
		// Publisher is authoritative for its own presence; ignore
		// payload.DeviceID in case of drift.
		p.DeviceID = evt.Publisher
		p.LastSeenAt = evt.PublishedAt
		l.mu.Lock()
		l.peers[evt.Publisher] = p
		l.mu.Unlock()
	})
}

// Peers returns a snapshot of currently-known peers, filtered to
// those whose last heartbeat falls within the alive window.
func (l *LeaderTracker) Peers() []PeerPresence {
	now := l.nowFn()
	cutoff := now.Add(-leaderAliveWindow).UnixMilli()
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]PeerPresence, 0, len(l.peers))
	for _, p := range l.peers {
		if p.LastSeenAt >= cutoff {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

// LeaderAt computes the leader deterministically: the smallest
// DeviceID among live peers (plus ourselves if alive). Returns ""
// when the mesh is empty and self is not alive — which shouldn't
// happen since the caller is typically the self-device asking.
func (l *LeaderTracker) LeaderAt(now time.Time) DeviceID {
	cutoff := now.Add(-leaderAliveWindow).UnixMilli()
	l.mu.RLock()
	defer l.mu.RUnlock()
	best := DeviceID("")
	consider := func(id DeviceID) {
		if best == "" || id < best {
			best = id
		}
	}
	// We always consider ourselves alive — it's this very process
	// running the check. Avoids the edge case where no `peer/self/*`
	// event has been received yet (we only see our own retained
	// events via deliverLocal, which fires in its own path).
	consider(l.self)
	for id, p := range l.peers {
		if p.LastSeenAt >= cutoff {
			consider(id)
		}
	}
	return best
}

// IAmLeader is a convenience wrapper for guard-style checks:
//
//     if leaderTracker.IAmLeader() {
//         doTheSingleton()
//     }
func (l *LeaderTracker) IAmLeader() bool {
	return l.LeaderAt(l.nowFn()) == l.self
}

// LeaderInfo is a snapshot surfaceable via /bus/status. Contains
// enough context to debug a "why isn't my box the leader" question.
type LeaderInfo struct {
	Self      DeviceID       `json:"self"`
	Leader    DeviceID       `json:"leader"`
	AmLeader  bool           `json:"amLeader"`
	Alive     []PeerPresence `json:"alivePeers"`
}

func (l *LeaderTracker) Info() LeaderInfo {
	now := l.nowFn()
	return LeaderInfo{
		Self:     l.self,
		Leader:   l.LeaderAt(now),
		AmLeader: l.LeaderAt(now) == l.self,
		Alive:    l.Peers(),
	}
}

// ── Heartbeat emitter ──────────────────────────────────────────────

// StartPeerHeartbeat publishes peer/{self}/ping every interval onto
// the bus. Non-aggressive: 1-min default cadence per the architecture
// doc. Also publishes peer/{self}/online once at start, and registers
// a best-effort peer/{self}/offline at shutdown.
//
// The event-driven re-announcements (on network recovery, on state
// changes) live in their own call sites — this function is only the
// steady-state keepalive.
func StartPeerHeartbeat(ctx context.Context, b *Bus, info PeerPresence, interval time.Duration) {
	if interval < 15*time.Second {
		interval = 60 * time.Second
	}
	info.StartedAt = time.Now().UnixMilli()

	// Initial online event — retained 15 min so late subscribers see it.
	_, _ = b.Publish(ctx, "peer/"+b.deviceID+"/online", info, 900, 1)

	// SupervisedGo so the bus ticker inherits TaskSupervisor's panic
	// recovery + /self-check reporting rather than being a bare go().
	SupervisedGo("bus-heartbeat", interval, false, func(ctx context.Context) error {
		_, err := b.Publish(ctx, "peer/"+b.deviceID+"/ping", info, 120, 0)
		return err
	})
}

// AnnounceOffline publishes peer/{self}/offline synchronously. Call
// from the signal-handling shutdown path so peers see the transition
// before the process exits.
func AnnounceOffline(ctx context.Context, b *Bus) {
	if b == nil {
		return
	}
	// TTL 0 — we want the event delivered once, then forgotten. The
	// 15-min online-retention expiration is the long-tail fallback.
	_, _ = b.Publish(ctx, "peer/"+b.deviceID+"/offline", map[string]interface{}{
		"deviceId": b.deviceID,
		"ts":       time.Now().UnixMilli(),
	}, 0, 1)
}

// ReAnnouncePresence fires a fresh peer/{self}/online whenever the
// agent decides network state has changed (sleep→wake, Wi-Fi→cellular,
// relay reconnected after outage). Callers supply the current state
// snapshot; this never runs on a ticker — it runs on events.
func ReAnnouncePresence(ctx context.Context, b *Bus, info PeerPresence) {
	if b == nil {
		return
	}
	info.StartedAt = time.Now().UnixMilli()
	_, _ = b.Publish(ctx, "peer/"+b.deviceID+"/online", info, 900, 1)
}
