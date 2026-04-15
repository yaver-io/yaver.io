package main

import (
	"container/list"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LogStream is a named, in-memory log channel that fans out appended
// lines to any number of SSE subscribers and keeps a small history
// buffer so late subscribers see context. Designed for streaming the
// progress of long-running CLI operations (e.g. `yaver autodev`) to
// the mobile app and the web dashboard simultaneously.
//
// Every operation is non-blocking from the producer's perspective:
// Append never waits on a slow consumer. If a subscriber's buffer is
// full, the publisher drops further deliveries to that subscriber
// rather than back-pressuring the autodev loop.
type LogStream struct {
	name        string
	mu          sync.Mutex
	history     *list.List // values are string lines, capped at historyMax
	historyMax  int
	subscribers map[chan string]struct{}
	closed      bool
}

func newLogStream(name string) *LogStream {
	return &LogStream{
		name:        name,
		history:     list.New(),
		historyMax:  500,
		subscribers: make(map[chan string]struct{}),
	}
}

// Append publishes a single line to history and to every live
// subscriber. Slow subscribers drop the line silently.
func (s *LogStream) Append(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.history.PushBack(line)
	for s.history.Len() > s.historyMax {
		s.history.Remove(s.history.Front())
	}
	for ch := range s.subscribers {
		select {
		case ch <- line:
		default: // subscriber too slow — drop
		}
	}
}

// Subscribe returns a channel that will receive every line appended
// after subscription, plus a snapshot of recent history delivered
// before any new lines. The caller must invoke the returned cancel
// to release resources when done.
func (s *LogStream) Subscribe() (<-chan string, []string, func()) {
	ch := make(chan string, 256)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	snapshot := make([]string, 0, s.history.Len())
	for e := s.history.Front(); e != nil; e = e.Next() {
		snapshot = append(snapshot, e.Value.(string))
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

// LogStreamRegistry holds named LogStreams keyed by name. Streams are
// created on first reference (either Append or Subscribe). Empty
// streams are cheap; we don't garbage-collect them today since the
// total set is bounded by the number of named long-running operations
// the user has invoked in the daemon's lifetime.
type LogStreamRegistry struct {
	mu      sync.Mutex
	streams map[string]*LogStream
}

func NewLogStreamRegistry() *LogStreamRegistry {
	return &LogStreamRegistry{streams: make(map[string]*LogStream)}
}

func (r *LogStreamRegistry) Get(name string) *LogStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[name]; ok {
		return s
	}
	s := newLogStream(name)
	r.streams[name] = s
	return s
}

func (r *LogStreamRegistry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.streams))
	for k := range r.streams {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleStreams routes /streams (GET — list active streams) and
// /streams/{name}/append (POST — append a line; producer-side).
func (s *HTTPServer) handleStreams(w http.ResponseWriter, r *http.Request) {
	if s.streams == nil {
		jsonError(w, http.StatusServiceUnavailable, "streams not enabled")
		return
	}
	if r.Method == http.MethodGet {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"streams": s.streams.Names(),
		})
		return
	}
	jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// handleStreamByName routes /streams/{name} (GET = SSE subscribe) and
// /streams/{name}/append (POST = append one line). The append form
// accepts either {"line":"..."} or {"lines":["...","..."]}.
func (s *HTTPServer) handleStreamByName(w http.ResponseWriter, r *http.Request) {
	if s.streams == nil {
		jsonError(w, http.StatusServiceUnavailable, "streams not enabled")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/streams/")
	if rest == "" {
		jsonError(w, http.StatusBadRequest, "missing stream name")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	stream := s.streams.Get(name)

	switch action {
	case "":
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET to subscribe")
			return
		}
		s.streamSSE(w, r, stream)
	case "append":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST to append")
			return
		}
		var body struct {
			Line  string   `json:"line"`
			Lines []string `json:"lines"`
		}
		_ = decodeJSONBody(r, &body)
		if body.Line != "" {
			stream.Append(body.Line)
		}
		for _, l := range body.Lines {
			stream.Append(l)
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown stream action")
	}
}

func (s *HTTPServer) streamSSE(w http.ResponseWriter, r *http.Request, stream *LogStream) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, snapshot, cancel := stream.Subscribe()
	defer cancel()

	for _, line := range snapshot {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type": "line",
			"text": line,
		}))
	}
	flusher.Flush()

	ctx := r.Context()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			// SSE comment line keeps idle connections alive through
			// proxies / mobile NAT.
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
				"type": "line",
				"text": line,
			}))
			flusher.Flush()
		}
	}
}

// decodeJSONBody is a tiny tolerant decoder: empty body is fine,
// malformed JSON returns nil too — callers default to empty struct.
func decodeJSONBody(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}
