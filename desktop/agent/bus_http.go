package main

// Agent-side HTTP endpoints for the P2P bus. These let web + mobile
// clients subscribe to the bus without running the full transport
// stack themselves — they hit the local agent (or the agent through
// the relay) and get the same event stream a Go peer would.
//
// Mobile specifically benefits: iOS/Android can't keep QUIC streams
// open when backgrounded, so they subscribe to /bus/events only
// while foregrounded and use Convex polling (via the mobile-headless
// client) otherwise.
//
// Endpoints:
//   GET  /bus/status    — counters snapshot
//   GET  /bus/retained  — current retained topics (same shape as bus.Retained())
//   GET  /bus/events    — SSE stream. ?prefix= filters to a topic prefix.
//   POST /bus/publish   — owner-only. Lets the web UI emit events directly.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (s *HTTPServer) handleBusStatus(w http.ResponseWriter, r *http.Request) {
	b := bus()
	if b == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}
	// Also surface LeaderTracker info when the scheduler has wired
	// one up. Read-only snapshot — cheap.
	leader := globalLeader
	status := map[string]interface{}{
		"enabled": true,
		"bus":     b.Status(),
	}
	if leader != nil {
		status["leader"] = leader.Info()
	}
	jsonReply(w, http.StatusOK, status)
}

func (s *HTTPServer) handleBusRetained(w http.ResponseWriter, r *http.Request) {
	b := bus()
	if b == nil {
		jsonReply(w, http.StatusOK, []BusEvent{})
		return
	}
	prefix := r.URL.Query().Get("prefix")
	jsonReply(w, http.StatusOK, b.Retained(prefix))
}

// handleBusEvents — SSE. The intended consumer is the mobile app
// (while foregrounded) or the web dashboard (always). Browsers
// reconnect on connection loss automatically via EventSource.
//
// On connect we first fire the current retained snapshot so
// late subscribers see present state with no cold-start lag. Then
// live events stream until the client disconnects.
//
// Heartbeat comment frames every 15 s keep mobile OS network
// stacks from killing an idle connection.
func (s *HTTPServer) handleBusEvents(w http.ResponseWriter, r *http.Request) {
	b := bus()
	if b == nil {
		http.Error(w, "bus not running", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	prefix := r.URL.Query().Get("prefix")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Replay retained state first.
	for _, evt := range b.Retained(prefix) {
		if err := writeSSEEvent(w, evt); err != nil {
			return
		}
		flusher.Flush()
	}

	ch := make(chan BusEvent, 128)
	unsub := b.Subscribe(prefix, func(evt BusEvent) {
		select {
		case ch <- evt:
		default:
			// Client is slow — drop rather than block bus dispatch.
		}
	})
	defer unsub()

	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			if err := writeSSEEvent(w, evt); err != nil {
				return
			}
			flusher.Flush()
		case <-keep.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleBusPublish — accept an event from the owner (dashboard or
// CLI), inject it into the local bus. Publisher defaults to the
// local deviceId if the body didn't supply one; QoS defaults to 1.
func (s *HTTPServer) handleBusPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b := bus()
	if b == nil {
		http.Error(w, "bus not running", http.StatusServiceUnavailable)
		return
	}

	var in struct {
		Topic     string          `json:"topic"`
		Payload   json.RawMessage `json:"payload"`
		RetainSec int64           `json:"retainSec,omitempty"`
		QoS       byte            `json:"qos,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if in.Topic == "" {
		http.Error(w, "topic required", http.StatusBadRequest)
		return
	}
	// Default to QoS 1 (at-least-once) when unspecified. Retain
	// default 0 (ephemeral) — callers that want retention must ask.
	if in.QoS == 0 {
		in.QoS = 1
	}
	evt, err := b.Publish(r.Context(), in.Topic,
		json.RawMessage(in.Payload), in.RetainSec, in.QoS)
	if err != nil {
		// Partial success — local delivery happened, one or more
		// remote transports failed. Still report 202 Accepted since
		// the bus will retry via its own logic.
		jsonReply(w, http.StatusAccepted, map[string]interface{}{
			"id":      evt.ID,
			"topic":   evt.Topic,
			"warning": err.Error(),
		})
		return
	}
	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"id":    evt.ID,
		"topic": evt.Topic,
	})
}

// writeSSEEvent serialises one BusEvent as a single SSE frame.
// JSON.Marshal on the struct never emits embedded newlines, so a
// single `data: ` line covers the whole payload.
func writeSSEEvent(w io.Writer, evt BusEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", body)
	return err
}

// globalLeader is wired from main.go at startup when the bus is
// ready. Left as a package-global so bus_http.go doesn't have to
// plumb it through HTTPServer — the bus itself is process-global
// and the LeaderTracker's lifetime matches it.
var globalLeader *LeaderTracker
