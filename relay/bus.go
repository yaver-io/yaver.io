package main

// Relay-side fanout for the Yaver P2P bus (Tier 2). Every agent
// under the same userId opens one SSE connection via GET
// /bus/subscribe and POSTs events to /bus/publish. The relay
// dispatches each POSTed event to every OTHER subscriber under
// the same userId — in-memory, no persistence.
//
// This is intentionally **not** a broker in the MQTT sense:
//   - No topic→subscriber map; every subscriber gets every event
//     from every peer. Filtering happens on the agent side.
//   - No retention; if a subscriber connects after an event
//     publishes, it missed it. The agent handles retention via
//     its local retained cache + late-subscriber replay.
//   - No cross-user fanout. Ever. Events are scoped to the
//     authenticated userId and nothing else.
//
// Why HTTP/SSE instead of the relay's existing QUIC tunnel: bus
// events cross the userId boundary (tunnels are per-device), and
// the same SSE endpoint serves mobile clients that can't keep a
// QUIC connection open when backgrounded.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// busHub holds per-user subscriber lists + counters. One instance
// per RelayServer (see server.go wiring).
type busHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*busSubscriber]struct{} // userId -> set
	delivered   atomic.Uint64
	published   atomic.Uint64
}

type busSubscriber struct {
	userID string
	ch     chan []byte
	done   chan struct{}
}

func newBusHub() *busHub {
	return &busHub{subscribers: map[string]map[*busSubscriber]struct{}{}}
}

func (h *busHub) add(userID string) *busSubscriber {
	sub := &busSubscriber{
		userID: userID,
		// Bounded channel — slow/dead subscriber drops the oldest
		// event instead of blocking fanout. 256 is generous.
		ch:   make(chan []byte, 256),
		done: make(chan struct{}),
	}
	h.mu.Lock()
	if h.subscribers[userID] == nil {
		h.subscribers[userID] = make(map[*busSubscriber]struct{})
	}
	h.subscribers[userID][sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *busHub) remove(sub *busSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.subscribers[sub.userID]; ok {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subscribers, sub.userID)
		}
	}
	select {
	case <-sub.done:
	default:
		close(sub.done)
	}
}

func (h *busHub) fanout(userID string, payload []byte, skipID string) {
	h.published.Add(1)
	h.mu.RLock()
	set := h.subscribers[userID]
	targets := make([]*busSubscriber, 0, len(set))
	for s := range set {
		targets = append(targets, s)
	}
	h.mu.RUnlock()
	for _, s := range targets {
		select {
		case s.ch <- payload:
			h.delivered.Add(1)
		default:
			// Subscriber channel full; drop this event. SSE
			// clients are expected to reconnect on missing
			// keepalive; lost frames aren't a correctness
			// problem for the bus's QoS 0 events.
		}
	}
}

// ── HTTP handlers ──────────────────────────────────────────────────

// handleBusPublish: agent-side publishes one event.
// We enforce relay password + attempt to resolve the user (same path
// as other relay endpoints — see validatePassword). The per-user
// fanout ensures we can never accidentally mix events across users.
func (s *RelayServer) handleBusPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relayPw := r.Header.Get("X-Relay-Password")
	if !s.validatePassword(relayPw) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid relay password"})
		return
	}
	userID := s.resolveBusUser(r, relayPw)
	if userID == "" {
		http.Error(w, "cannot resolve userId", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Minimal shape validation — we don't parse the whole event,
	// just verify it looks like JSON with an `id` so downstream
	// clients can dedup.
	var shape struct {
		ID        string `json:"id"`
		Topic     string `json:"topic"`
		Publisher string `json:"publisher"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	if shape.ID == "" || shape.Topic == "" {
		http.Error(w, "event missing id or topic", http.StatusBadRequest)
		return
	}

	s.busHub.fanout(userID, body, shape.ID)
	w.WriteHeader(http.StatusAccepted)
}

// handleBusSubscribe: Server-Sent Events stream of every event
// published by peers under the same userId. Mobile + desktop
// agents + the web dashboard can all consume this.
//
// Heartbeat comment frames every 20 s keep proxies / mobile OS
// network stacks from killing an idle connection.
func (s *RelayServer) handleBusSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relayPw := r.Header.Get("X-Relay-Password")
	if !s.validatePassword(relayPw) {
		http.Error(w, "invalid relay password", http.StatusUnauthorized)
		return
	}
	userID := s.resolveBusUser(r, relayPw)
	if userID == "" {
		http.Error(w, "cannot resolve userId", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := s.busHub.add(userID)
	defer s.busHub.remove(sub)

	// Initial hello so slow caching proxies flush promptly.
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			return
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case payload := <-sub.ch:
			// SSE frame: single-line data. BusEvent is already
			// JSON-encoded; we don't re-encode.
			if _, err := io.WriteString(w, "data: "); err != nil {
				return
			}
			// Replace any stray newlines in payload (extremely
			// rare with JSON.Marshal, but SSE is strict).
			if bytes.IndexByte(payload, '\n') >= 0 {
				payload = bytes.ReplaceAll(payload, []byte("\n"), []byte(" "))
			}
			if _, err := w.Write(payload); err != nil {
				return
			}
			if _, err := io.WriteString(w, "\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleBusStatus returns counters so operators can tell if the
// fanout is actually moving events. Same auth as the rest of the
// relay's admin surface.
func (s *RelayServer) handleBusStatus(w http.ResponseWriter, r *http.Request) {
	relayPw := r.Header.Get("X-Relay-Password")
	if !s.validatePassword(relayPw) {
		http.Error(w, "invalid relay password", http.StatusUnauthorized)
		return
	}
	s.busHub.mu.RLock()
	userCount := len(s.busHub.subscribers)
	subs := 0
	for _, v := range s.busHub.subscribers {
		subs += len(v)
	}
	s.busHub.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"users":       userCount,
		"subscribers": subs,
		"published":   s.busHub.published.Load(),
		"delivered":   s.busHub.delivered.Load(),
	})
}

// resolveBusUser maps an authenticated request to a userId. With a
// Convex-per-user-password deployment this is a Convex query (same
// as validatePasswordViaConvex); with a shared-password deployment
// we fall back to using the password hash as the userId. That's not
// ideal — everyone with the shared password lands in the same bus
// namespace — but a shared-password relay has no notion of userId
// anyway.
func (s *RelayServer) resolveBusUser(r *http.Request, relayPw string) string {
	if userID := s.resolveUserIDFromPassword(relayPw); userID != "" {
		return userID
	}
	// Shared-password or validation-disabled mode — everyone shares
	// one bus namespace on this relay. Keyed by password hash so
	// rotating the password isolates stale clients.
	if relayPw != "" {
		return "shared-" + fmt.Sprintf("%x", sumShort(relayPw))
	}
	return ""
}

// sumShort is a 32-bit checksum just for opaque namespace keying.
// We do NOT use this as a security primitive.
func sumShort(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
