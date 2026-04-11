package main

// pubsub.go — in-process topic pubsub + SSE gateway. Solo-dev
// alternative to Ably / Pusher / PartyKit for the "push a live
// update from my server to every connected client" case.
//
// Design:
//
//   - Topics are dynamic strings. Subscribers register an SSE
//     connection per topic and receive every publish.
//   - In-memory only — no durability. Pubsub is for
//     transient "new order arrived" style fan-out, not for
//     workflow events the dev needs to replay. If you need
//     durability, land the event in analytics_events first
//     and subscribe to that.
//   - Fan-out is bounded: each subscriber has a 64-message
//     buffer, and slow subscribers get dropped when the
//     buffer overflows. That's the right trade for a solo-
//     dev context where the dev's own mobile app is the
//     only consumer and missing a frame is OK.
//
// HTTP surface:
//
//   POST /pubsub/publish?topic=<t>       — body = JSON payload
//   GET  /pubsub/subscribe?topic=<t>     — SSE stream of JSON
//                                          "event: message" frames
//   GET  /pubsub/topics                  — list known topics +
//                                          subscriber counts

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// pubsubSubscriber is one connected SSE client.
type pubsubSubscriber struct {
	id    string
	ch    chan string
	topic string
}

// PubSubHub is the in-process topic router.
type PubSubHub struct {
	mu     sync.RWMutex
	topics map[string]map[string]*pubsubSubscriber // topic → id → sub
}

var (
	pubsubHubOnce sync.Once
	pubsubHubInst *PubSubHub
)

func GlobalPubSub() *PubSubHub {
	pubsubHubOnce.Do(func() {
		pubsubHubInst = &PubSubHub{topics: map[string]map[string]*pubsubSubscriber{}}
	})
	return pubsubHubInst
}

// Publish pushes a payload to every subscriber on a topic.
// Returns the number of subscribers that received the message.
func (h *PubSubHub) Publish(topic string, payload []byte) int {
	h.mu.RLock()
	subs := h.topics[topic]
	h.mu.RUnlock()

	delivered := 0
	for _, sub := range subs {
		select {
		case sub.ch <- string(payload):
			delivered++
		default:
			// Subscriber's buffer is full — skip rather than
			// block. Consistent with the "best-effort fan-out"
			// design.
		}
	}
	return delivered
}

// Subscribe registers a subscriber and returns its channel.
// The caller is responsible for calling Unsubscribe on exit so
// the hub doesn't leak.
func (h *PubSubHub) Subscribe(topic string) *pubsubSubscriber {
	sub := &pubsubSubscriber{
		id:    fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(h.topics)),
		ch:    make(chan string, 64),
		topic: topic,
	}
	h.mu.Lock()
	if h.topics[topic] == nil {
		h.topics[topic] = map[string]*pubsubSubscriber{}
	}
	h.topics[topic][sub.id] = sub
	h.mu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber from its topic.
func (h *PubSubHub) Unsubscribe(sub *pubsubSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs := h.topics[sub.topic]; subs != nil {
		delete(subs, sub.id)
		if len(subs) == 0 {
			delete(h.topics, sub.topic)
		}
	}
	close(sub.ch)
}

// Topics returns the current topic/subscriber-count summary
// for the mobile Monitor > Pubsub view.
func (h *PubSubHub) Topics() []map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]map[string]interface{}, 0, len(h.topics))
	for t, subs := range h.topics {
		out = append(out, map[string]interface{}{
			"topic":       t,
			"subscribers": len(subs),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["topic"].(string)
		b, _ := out[j]["topic"].(string)
		return a < b
	})
	return out
}

// --- HTTP -----------------------------------------------------------------

func (s *HTTPServer) handlePubSubPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	topic := r.URL.Query().Get("topic")
	if topic == "" {
		jsonError(w, http.StatusBadRequest, "topic required")
		return
	}
	// Accept either raw JSON (any shape) or a {payload: ...} envelope.
	// We just forward the bytes unchanged so the dev owns the schema.
	buf := make([]byte, 1<<16)
	n, _ := r.Body.Read(buf)
	payload := buf[:n]
	delivered := GlobalPubSub().Publish(topic, payload)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"topic":     topic,
		"delivered": delivered,
	})
}

func (s *HTTPServer) handlePubSubSubscribe(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	if topic == "" {
		http.Error(w, "topic required", http.StatusBadRequest)
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

	sub := GlobalPubSub().Subscribe(topic)
	defer GlobalPubSub().Unsubscribe(sub)

	// Immediate comment to open the stream — some proxies need
	// this before they flush headers.
	bufio.NewWriter(w)
	fmt.Fprintf(w, ": yaver-pubsub connected topic=%s\n\n", topic)
	flusher.Flush()

	// Heartbeat ticker so idle clients don't get killed by
	// intermediate proxies (CF, nginx).
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-sub.ch:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *HTTPServer) handlePubSubTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"topics": GlobalPubSub().Topics(),
	})
}

// Ensure json import stays live on platforms where the pubsub
// handlers don't get exercised — the handlers use jsonReply/
// jsonError which pulls in encoding/json transitively in the
// HTTP server, but keeping an explicit reference here prevents
// lint noise on future refactors.
var _ = json.Marshal
