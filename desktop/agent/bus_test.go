package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingTransport — no-op Publish, lets tests observe what the
// bus tried to send to the wire.
type recordingTransport struct {
	mu   sync.Mutex
	name string
	out  []BusEvent
	fail bool
}

func (t *recordingTransport) Name() string { return t.name }
func (t *recordingTransport) Publish(ctx context.Context, evt BusEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail {
		return context.DeadlineExceeded
	}
	t.out = append(t.out, evt)
	return nil
}
func (t *recordingTransport) Close() error { return nil }
func (t *recordingTransport) drain() []BusEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]BusEvent(nil), t.out...)
	t.out = t.out[:0]
	return out
}

func newTestBus(t *testing.T, device string) *Bus {
	t.Helper()
	b := NewBus(device, "user-test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.Start(ctx)
	t.Cleanup(b.Stop)
	return b
}

func TestBus_PublishFiresLocalSubscriber(t *testing.T) {
	b := newTestBus(t, "d1")
	var got atomic.Uint64
	b.Subscribe("peer", func(evt BusEvent) {
		got.Add(1)
	})
	_, err := b.Publish(context.Background(), "peer/d1/online", map[string]string{"x": "y"}, 60, 1)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Load() != 1 {
		t.Fatalf("expected 1 local delivery, got %d", got.Load())
	}
}

func TestBus_PublishRoutesToEveryTransport(t *testing.T) {
	b := newTestBus(t, "d1")
	ta, tb := &recordingTransport{name: "a"}, &recordingTransport{name: "b"}
	b.RegisterTransport(ta)
	b.RegisterTransport(tb)
	_, _ = b.Publish(context.Background(), "peer/d1/online", map[string]int{"v": 1}, 60, 1)
	if len(ta.drain()) != 1 || len(tb.drain()) != 1 {
		t.Fatalf("expected each transport to see 1 event")
	}
}

func TestBus_DedupOnIDAcrossTransports(t *testing.T) {
	b := newTestBus(t, "d1")
	var got atomic.Uint64
	b.Subscribe("peer", func(evt BusEvent) { got.Add(1) })

	evt := BusEvent{
		ID:          "e1",
		Topic:       "peer/d2/online",
		Publisher:   "d2", // not us
		PublishedAt: time.Now().UnixMilli(),
		TTL:         60,
		QoS:         1,
	}
	b.Receive(evt) // first delivery
	b.Receive(evt) // duplicate — must NOT re-fire subscribers
	// Give the dispatch loop a tick.
	waitFor(t, 500*time.Millisecond, func() bool { return got.Load() >= 1 })
	time.Sleep(50 * time.Millisecond) // dupe window
	if got.Load() != 1 {
		t.Fatalf("dedup failed: got %d deliveries", got.Load())
	}
	if b.dupes.Load() != 1 {
		t.Fatalf("expected dupes=1, got %d", b.dupes.Load())
	}
}

func TestBus_RetainedReplaysToLateSubscribers(t *testing.T) {
	b := newTestBus(t, "d1")
	_, _ = b.Publish(context.Background(), "peer/d1/online", map[string]int{"v": 42}, 60, 1)

	var payload atomic.Value
	b.Subscribe("peer", func(evt BusEvent) {
		var p map[string]int
		_ = json.Unmarshal(evt.Payload, &p)
		payload.Store(p["v"])
	})

	got := payload.Load()
	if got == nil || got.(int) != 42 {
		t.Fatalf("expected retained event to replay on subscribe; got %v", got)
	}
}

func TestBus_IgnoresOwnBounceback(t *testing.T) {
	b := newTestBus(t, "d1")
	var got atomic.Uint64
	b.Subscribe("peer", func(evt BusEvent) { got.Add(1) })
	evt := BusEvent{
		ID:          "bounce1",
		Topic:       "peer/d1/online",
		Publisher:   "d1", // ourselves — came back through a relay fanout
		PublishedAt: time.Now().UnixMilli(),
	}
	b.Receive(evt)
	time.Sleep(50 * time.Millisecond)
	if got.Load() != 0 {
		t.Fatalf("expected own-event bounceback to be ignored; got %d deliveries", got.Load())
	}
}

func TestBus_TopicMatchRespectsSegments(t *testing.T) {
	cases := []struct {
		topic, prefix string
		want          bool
	}{
		{"peer/abc/online", "peer", true},
		{"peer/abc/online", "peer/", true},
		{"peer/abc/online", "peer/abc", true},
		{"peering/abc", "peer", false},
		{"peer", "peer", true},
		{"anything", "", true},
	}
	for _, c := range cases {
		if got := topicMatches(c.topic, c.prefix); got != c.want {
			t.Errorf("topicMatches(%q,%q) = %v, want %v", c.topic, c.prefix, got, c.want)
		}
	}
}

func TestBus_GCEvictsExpiredRetained(t *testing.T) {
	b := newTestBus(t, "d1")
	fixed := time.Unix(1_700_000_000, 0)
	b.nowFn = func() time.Time { return fixed }

	_, _ = b.Publish(context.Background(), "peer/d1/short", map[string]bool{"ok": true}, 1 /*second*/, 1)
	if got := b.Retained("peer"); len(got) != 1 {
		t.Fatalf("expected 1 retained, got %d", len(got))
	}

	// Jump forward past TTL + run gc once.
	b.nowFn = func() time.Time { return fixed.Add(5 * time.Second) }
	b.gcOnce(b.nowFn())

	if got := b.Retained("peer"); len(got) != 0 {
		t.Fatalf("expected retained eviction after TTL; still have %d", len(got))
	}
}

func TestLeader_DeterministicMin(t *testing.T) {
	lt := NewLeaderTracker("d2")
	fixed := time.Unix(1_700_000_000, 0)
	lt.nowFn = func() time.Time { return fixed }

	// Only self alive — we are the leader.
	if lt.LeaderAt(fixed) != "d2" {
		t.Fatalf("expected self-leader when alone")
	}

	// A smaller deviceId heartbeats → it becomes leader.
	lt.mu.Lock()
	lt.peers["d1"] = PeerPresence{DeviceID: "d1", LastSeenAt: fixed.UnixMilli()}
	lt.peers["d9"] = PeerPresence{DeviceID: "d9", LastSeenAt: fixed.UnixMilli()}
	lt.mu.Unlock()
	if got := lt.LeaderAt(fixed); got != "d1" {
		t.Fatalf("expected leader=d1, got %s", got)
	}

	// d1 falls out of the alive window → we become leader.
	lt.mu.Lock()
	lt.peers["d1"] = PeerPresence{DeviceID: "d1", LastSeenAt: fixed.Add(-10 * time.Minute).UnixMilli()}
	lt.mu.Unlock()
	if got := lt.LeaderAt(fixed); got != "d2" {
		t.Fatalf("expected leader=d2 after d1 expired, got %s", got)
	}
}

func TestLeader_WiredToBusUpdatesOnEvents(t *testing.T) {
	b := newTestBus(t, "d2")
	lt := NewLeaderTracker("d2")
	lt.WireTo(b)

	// Simulate receiving a peer event.
	b.Receive(BusEvent{
		ID:          "e-1",
		Topic:       "peer/d1/online",
		Publisher:   "d1",
		PublishedAt: time.Now().UnixMilli(),
		Payload:     json.RawMessage(`{"deviceId":"d1"}`),
	})
	waitFor(t, 500*time.Millisecond, func() bool {
		return lt.LeaderAt(time.Now()) == "d1"
	})
	if got := lt.LeaderAt(time.Now()); got != "d1" {
		t.Fatalf("expected leader=d1 after event, got %s", got)
	}
}
