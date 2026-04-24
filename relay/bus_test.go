package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Wire up a real relay server with an HTTP test server pointing at
// its mux, then drive publish + subscribe end-to-end. No Convex,
// no QUIC — just the HTTP fan-out plane. Everything else already
// has its own tests.
func newRelayForTest(t *testing.T) *httptest.Server {
	t.Helper()
	rs := NewRelayServer(0, 0, "test-pw", "", "") // no Convex, shared pw
	mux := http.NewServeMux()
	mux.HandleFunc("/bus/publish", rs.handleBusPublish)
	mux.HandleFunc("/bus/subscribe", rs.handleBusSubscribe)
	mux.HandleFunc("/bus/status", rs.handleBusStatus)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestBus_PublishFanoutToSubscriber(t *testing.T) {
	srv := newRelayForTest(t)

	// Open subscriber first. Uses a separate goroutine because SSE
	// blocks until the connection dies.
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()

	seen := make(chan string, 2)
	subReady := make(chan struct{})

	go func() {
		req, _ := http.NewRequestWithContext(subCtx, http.MethodGet, srv.URL+"/bus/subscribe", nil)
		req.Header.Set("X-Relay-Password", "test-pw")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		close(subReady)

		buf := make([]byte, 4096)
		acc := []byte{}
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				acc = append(acc, buf[:n]...)
				for {
					idx := bytes.IndexByte(acc, '\n')
					if idx < 0 {
						break
					}
					line := string(bytes.TrimRight(acc[:idx], "\r"))
					acc = acc[idx+1:]
					if strings.HasPrefix(line, "data: ") {
						select {
						case seen <- line[len("data: "):]:
						default:
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait for the subscriber's SSE handshake to complete before
	// publishing, otherwise fanout runs into an empty subscriber set.
	select {
	case <-subReady:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never connected")
	}
	// One extra beat so handleBusSubscribe has added itself to the hub.
	time.Sleep(50 * time.Millisecond)

	// Publish.
	body, _ := json.Marshal(map[string]interface{}{
		"id":        "e1",
		"topic":     "peer/d1/online",
		"publisher": "d1",
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/bus/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("publish expected 202, got %d", resp.StatusCode)
	}

	select {
	case got := <-seen:
		if !strings.Contains(got, `"topic":"peer/d1/online"`) {
			t.Fatalf("unexpected payload: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never received the event")
	}
}

func TestBus_RejectsBadPassword(t *testing.T) {
	srv := newRelayForTest(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/bus/publish", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Password", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad password, got %d", resp.StatusCode)
	}
}

func TestBus_StatusCountersIncrement(t *testing.T) {
	srv := newRelayForTest(t)

	var received atomic.Uint64
	// Start a subscriber so "delivered" actually increments.
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	subReady := make(chan struct{})
	go func() {
		req, _ := http.NewRequestWithContext(subCtx, http.MethodGet, srv.URL+"/bus/subscribe", nil)
		req.Header.Set("X-Relay-Password", "test-pw")
		resp, _ := http.DefaultClient.Do(req)
		if resp == nil {
			return
		}
		defer resp.Body.Close()
		close(subReady)
		io.Copy(io.Discard, resp.Body)
	}()
	select {
	case <-subReady:
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe timeout")
	}
	time.Sleep(50 * time.Millisecond)

	// Publish two events.
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(map[string]interface{}{
			"id":        "e-" + string(rune('1'+i)),
			"topic":     "peer/d1/ping",
			"publisher": "d1",
		})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/bus/publish", bytes.NewReader(body))
		req.Header.Set("X-Relay-Password", "test-pw")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		received.Add(1)
	}

	// Wait for fanout to drain.
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/bus/status", nil)
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer resp.Body.Close()
	var got map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if pub, _ := got["published"].(float64); pub < float64(received.Load()) {
		t.Fatalf("status.published < events published: got %v, want ≥ %d", pub, received.Load())
	}
	if del, _ := got["delivered"].(float64); del < float64(received.Load()) {
		t.Fatalf("status.delivered < events delivered: got %v, want ≥ %d", del, received.Load())
	}
}
