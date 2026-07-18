package main

// Yaver P2P bus — distributed pub/sub over the channels every agent
// already maintains (relay QUIC stream, direct peer connections, LAN
// multicast). No central broker: every topic is published by exactly
// one device, retained locally on every subscriber, redelivered over
// whichever transport has the shortest path. See
// docs/p2p-bus-architecture.md for the full design.
//
// This file is Phase 1: the in-process bus (topic registry, dedup,
// retain, QoS 1 ack/retry). Transports live in bus_*.go and register
// themselves at startup. Tier 1 (LAN multicast) and Tier 3 (direct
// dial) are deferred to Phase 2.
//
// Mobile consideration: iOS/Android restrict long-lived sockets in
// background. The bus is designed so a mobile client can subscribe
// only while foregrounded (via /bus/events SSE on the agent) and
// fall back to Convex polling when backgrounded — no peer sees a
// "missing" mobile device because Convex's device registry remains
// the source of truth for "does this user own that device". The bus
// is the live-presence layer on top.

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func randUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

// ── Types ──────────────────────────────────────────────────────────

// DeviceID is a Convex-registered device identifier. Typed alias for
// clarity at call sites.
type DeviceID = string

// BusEvent is the wire format. Small + JSON-encodable so every
// transport can serialise it trivially.
type BusEvent struct {
	ID          string          `json:"id"`          // per-device monotonic (UUID v7-ish: timestamp+random)
	Topic       string          `json:"topic"`       // e.g. "peer/abc123/online"
	Publisher   DeviceID        `json:"publisher"`
	PublishedAt int64           `json:"publishedAt"` // unix millis
	TTL         int64           `json:"ttl,omitempty"` // retain seconds; 0 = one-shot
	QoS         byte            `json:"qos"`         // 0 fire-forget, 1 at-least-once
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// BusTransport publishes events to remote peers and surfaces incoming
// events back to the bus. Each tier implements one of these. The bus
// fans out every Publish to every registered transport; receiving
// goroutines push to Bus.inbox.
type BusTransport interface {
	Name() string
	Publish(ctx context.Context, evt BusEvent) error
	Close() error
}

// ── Bus ────────────────────────────────────────────────────────────

// Bus is the process-local pub/sub core. One per `yaver serve`.
// Matches the one-supervisor-per-process pattern from healer.go.
type Bus struct {
	mu          sync.RWMutex
	deviceID    DeviceID
	userID      string // from auth; scopes fanout
	transports  []BusTransport
	subs        map[string][]*subscription // key = topic prefix
	retained    map[string]BusEvent        // key = topic — last-value-wins per-publisher
	seen        map[string]time.Time       // key = event.ID — dedup window

	inbox   chan BusEvent
	stop    chan struct{}
	stopOnce sync.Once
	running  atomic.Bool

	// Stats for /bus/status + /self-check
	published atomic.Uint64
	received  atomic.Uint64
	dupes     atomic.Uint64

	nowFn func() time.Time
}

// Subscription filters incoming events. Handler runs synchronously
// on the bus goroutine, so callers that need to block should forward
// to their own goroutine.
type subscription struct {
	id      uint64
	prefix  string // matches events with topic == prefix OR prefix + "/..."
	handler func(BusEvent)
}

var nextSubID atomic.Uint64

// NewBus — the only constructor. Start() wires goroutines + gc.
func NewBus(deviceID DeviceID, userID string) *Bus {
	return &Bus{
		deviceID: deviceID,
		userID:   userID,
		subs:     make(map[string][]*subscription),
		retained: make(map[string]BusEvent),
		seen:     make(map[string]time.Time),
		inbox:    make(chan BusEvent, 1024),
		stop:     make(chan struct{}),
		nowFn:    time.Now,
	}
}

// RegisterTransport adds a transport. Safe to call before or after
// Start(). The transport must not publish until the caller has also
// Start()'d the bus — otherwise incoming events race the inbox.
func (b *Bus) RegisterTransport(t BusTransport) {
	b.mu.Lock()
	b.transports = append(b.transports, t)
	b.mu.Unlock()
}

// Start launches the dispatcher + retain-GC goroutines. Idempotent.
func (b *Bus) Start(ctx context.Context) {
	if !b.running.CompareAndSwap(false, true) {
		return
	}
	go b.dispatchLoop(ctx)
	go b.gcLoop(ctx)
}

// Stop drains the inbox cleanly. Idempotent.
func (b *Bus) Stop() {
	b.stopOnce.Do(func() {
		close(b.stop)
		b.mu.Lock()
		tr := b.transports
		b.transports = nil
		b.mu.Unlock()
		for _, t := range tr {
			_ = t.Close()
		}
	})
}

// ── Publish / Subscribe ────────────────────────────────────────────

// Publish sends an event through every registered transport + fires
// local subscriptions. Mirrors MQTT at the API level: callers supply
// topic + payload, bus handles ID generation and retention.
//
// Topic convention: segments separated by "/". The first segment is
// usually a category ("peer", "leader", "state"), the second is the
// owning device id. Subscribers match by prefix.
//
// retainSec > 0 keeps the event in the retained cache so late
// subscribers see it. Set ttl=0 for ephemeral events.
func (b *Bus) Publish(ctx context.Context, topic string, payload interface{}, retainSec int64, qos byte) (BusEvent, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return BusEvent{}, fmt.Errorf("bus: marshal payload: %w", err)
	}
	evt := BusEvent{
		ID:          generateBusEventID(b.deviceID, b.nowFn()),
		Topic:       topic,
		Publisher:   b.deviceID,
		PublishedAt: b.nowFn().UnixMilli(),
		TTL:         retainSec,
		QoS:         qos,
		Payload:     raw,
	}

	// Fire locally first — a subscriber on this device must never miss
	// its own publish, even if every transport is down.
	b.deliverLocal(evt)

	// Retain before publishing so even a transport error leaves the
	// latest value cached. Late subscribers via .Retained() still see it.
	if retainSec > 0 {
		b.mu.Lock()
		b.retained[topic] = evt
		b.mu.Unlock()
	}

	b.mu.RLock()
	transports := append([]BusTransport(nil), b.transports...)
	b.mu.RUnlock()

	var lastErr error
	for _, t := range transports {
		if err := t.Publish(ctx, evt); err != nil {
			lastErr = err
			log.Printf("[bus] publish via %s failed: %v", t.Name(), err)
		}
	}
	b.published.Add(1)
	// Returning the first error (if any) is informational — caller
	// can log or ignore. The local deliver already succeeded and
	// retained state is updated regardless.
	return evt, lastErr
}

// Subscribe delivers events whose topic starts with `prefix`. Returns
// an unsubscribe function.
//
// Retained events matching the prefix fire synchronously to the new
// handler before this call returns — late subscribers instantly see
// current state without waiting for the next publish.
func (b *Bus) Subscribe(prefix string, handler func(BusEvent)) func() {
	sub := &subscription{id: nextSubID.Add(1), prefix: prefix, handler: handler}
	b.mu.Lock()
	b.subs[prefix] = append(b.subs[prefix], sub)
	retained := make([]BusEvent, 0, len(b.retained))
	for _, evt := range b.retained {
		if topicMatches(evt.Topic, prefix) {
			retained = append(retained, evt)
		}
	}
	b.mu.Unlock()

	for _, evt := range retained {
		handler(evt)
	}

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[prefix]
		for i, s := range list {
			if s.id == sub.id {
				b.subs[prefix] = append(list[:i], list[i+1:]...)
				break
			}
		}
	}
}

// Retained returns a snapshot of all retained events matching prefix.
// Useful for /bus/state or mobile SSE handshake (send current state
// on connect).
func (b *Bus) Retained(prefix string) []BusEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BusEvent, 0, len(b.retained))
	for _, evt := range b.retained {
		if topicMatches(evt.Topic, prefix) {
			out = append(out, evt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}

// Receive is called by transports when they get an event from a peer.
// Enqueues for dispatch. Drops on full inbox (QoS 0) — QoS 1 relies
// on publisher's ack/retry.
func (b *Bus) Receive(evt BusEvent) {
	if evt.Publisher == b.deviceID {
		// Our own event bouncing back through a relay fan-out. Skip.
		return
	}
	select {
	case b.inbox <- evt:
	default:
		log.Printf("[bus] inbox full — dropping %s from %s (QoS %d)", evt.Topic, evt.Publisher, evt.QoS)
	}
}

// ── internals ──────────────────────────────────────────────────────

// dispatchLoop fans incoming events out to matching subscribers.
func (b *Bus) dispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stop:
			return
		case evt := <-b.inbox:
			b.received.Add(1)
			if !b.markSeen(evt) {
				b.dupes.Add(1)
				continue
			}
			if evt.TTL > 0 {
				b.mu.Lock()
				b.retained[evt.Topic] = evt
				b.mu.Unlock()
			}
			b.deliverLocal(evt)
		}
	}
}

// markSeen returns true if this event is new. Uses event.ID as key;
// the dedup window is bounded by gcLoop.
func (b *Bus) markSeen(evt BusEvent) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.seen[evt.ID]; ok {
		return false
	}
	b.seen[evt.ID] = b.nowFn()
	return true
}

// deliverLocal dispatches the event to every matching handler.
// Handler panics are recovered so one misbehaving subscriber cannot
// take down the dispatch loop.
func (b *Bus) deliverLocal(evt BusEvent) {
	b.mu.RLock()
	var matched []*subscription
	for prefix, subs := range b.subs {
		if topicMatches(evt.Topic, prefix) {
			matched = append(matched, subs...)
		}
	}
	b.mu.RUnlock()
	for _, sub := range matched {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[bus] subscriber %q panicked on %s: %v", sub.prefix, evt.Topic, r)
				}
			}()
			sub.handler(evt)
		}()
	}
}

// gcLoop clears the dedup table and evicts expired retained events.
func (b *Bus) gcLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stop:
			return
		case now := <-t.C:
			b.gcOnce(now)
		}
	}
}

func (b *Bus) gcOnce(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Dedup window — 5 min covers redelivery via redundant transports.
	cutoff := now.Add(-5 * time.Minute)
	for id, seen := range b.seen {
		if seen.Before(cutoff) {
			delete(b.seen, id)
		}
	}
	// Retained eviction — honour per-event TTL.
	for topic, evt := range b.retained {
		if evt.TTL <= 0 {
			continue
		}
		exp := time.UnixMilli(evt.PublishedAt).Add(time.Duration(evt.TTL) * time.Second)
		if now.After(exp) {
			delete(b.retained, topic)
		}
	}
}

// topicMatches — prefix match with segment boundary. "peer" matches
// "peer/abc/online" but NOT "peering". Empty prefix matches everything.
func topicMatches(topic, prefix string) bool {
	if prefix == "" {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return true
	}
	if topic == prefix {
		return true
	}
	n := len(prefix)
	return len(topic) > n && topic[:n] == prefix && topic[n] == '/'
}

// generateBusEventID produces a per-device monotonic ID. Format:
// "<unix-millis-hex>-<deviceId[:6]>-<random4hex>". Locally monotonic
// by construction (timestamp first); device suffix prevents collisions
// across devices; random suffix prevents collisions within a single
// millisecond on one device.
func generateBusEventID(device DeviceID, now time.Time) string {
	shortDev := device
	if len(shortDev) > 6 {
		shortDev = shortDev[:6]
	}
	return fmt.Sprintf("%x-%s-%x", now.UnixMilli(), shortDev, randUint32())
}

// BusStatus exposes counters for /bus/status + /self-check.
type BusStatus struct {
	DeviceID    DeviceID          `json:"deviceId"`
	UserID      string            `json:"userId"`
	Running     bool              `json:"running"`
	Published   uint64            `json:"published"`
	Received    uint64            `json:"received"`
	Dupes       uint64            `json:"dupes"`
	Transports  []string          `json:"transports"`
	Retained    int               `json:"retainedCount"`
	Subs        int               `json:"subscriptionCount"`
}

func (b *Bus) Status() BusStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.transports))
	for _, t := range b.transports {
		names = append(names, t.Name())
	}
	subCount := 0
	for _, s := range b.subs {
		subCount += len(s)
	}
	return BusStatus{
		DeviceID:   b.deviceID,
		UserID:     b.userID,
		Running:    b.running.Load(),
		Published:  b.published.Load(),
		Received:   b.received.Load(),
		Dupes:      b.dupes.Load(),
		Transports: names,
		Retained:   len(b.retained),
		Subs:       subCount,
	}
}

// ── Global singleton + wire-up helpers ─────────────────────────────

var (
	globalBus   *Bus
	globalBusMu sync.Mutex
)

func bus() *Bus {
	globalBusMu.Lock()
	defer globalBusMu.Unlock()
	return globalBus
}

// InitBus wires the process-wide bus. Called from runServe() once
// the supervised ctx is known. Safe to call with empty deviceID /
// userID — the bus still works for local pub/sub but transports
// will refuse to publish cross-device events.
func InitBus(ctx context.Context, deviceID, userID string) *Bus {
	globalBusMu.Lock()
	defer globalBusMu.Unlock()
	if globalBus != nil {
		globalBus.Stop()
	}
	globalBus = NewBus(deviceID, userID)
	globalBus.Start(ctx)
	return globalBus
}
