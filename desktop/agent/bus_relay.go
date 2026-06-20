package main

// Tier-2 bus transport: publish + subscribe via the user's relay.
//
// The relay is a dumb per-user fanout. It holds no topic state; it
// only forwards BusEvent frames between agents authenticated under
// the same userId. That matches the "not a broker" principle from
// docs/p2p-bus-architecture.md — the relay is an Ethernet-switch
// analogue for our wire format.
//
// Transport protocol: HTTP.
//   POST /bus/publish  — agent → relay (one event per call)
//   GET  /bus/subscribe — SSE stream, agent ← relay (every event
//                         published by a peer on the same userId)
//
// Chose HTTP over a bespoke QUIC frame because:
//   - Every relay Yaver ships already speaks HTTP/SSE.
//   - Web + mobile clients can consume the exact same SSE stream
//     directly when foregrounded, with no custom client logic
//     (see /bus/events on the agent for that use case).
//   - Debuggability is trivial (`curl -N` tails live events).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type relayBusTransport struct {
	relayURL  string // e.g. https://public.yaver.io
	authToken string // user bearer

	pwMu          sync.Mutex // guards relayPassword (publish + subscribe race)
	relayPassword string

	b      *Bus
	client *http.Client
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// currentPassword returns the live relay password (may have been refreshed
// after a Convex-side rotation; see refreshPasswordIfStale).
func (t *relayBusTransport) currentPassword() string {
	t.pwMu.Lock()
	defer t.pwMu.Unlock()
	return t.relayPassword
}

// refreshPasswordIfStale mirrors the relay-tunnel recovery: when the relay
// rejects us with an invalid-password error (Convex rotated the per-user
// password server-side, our cached one is dead), refetch the fresh password
// from /settings and swap it in. Without this the bus loops forever on 401
// (observed in the field: `[bus-relay] subscribe lost: HTTP 401: invalid relay
// password` every 30s while the QUIC tunnel — which DOES refetch — was fine).
// Returns true when the password actually changed.
func (t *relayBusTransport) refreshPasswordIfStale(ctx context.Context, err error) bool {
	if !looksLikeStaleRelayPassword(err) {
		return false
	}
	fresh := refreshRelayPasswordFromConvex(ctx)
	if fresh == "" {
		return false
	}
	t.pwMu.Lock()
	changed := fresh != t.relayPassword
	t.relayPassword = fresh
	t.pwMu.Unlock()
	if changed {
		fmt.Printf("[bus-relay] refetched fresh relay password from Convex /settings; retrying\n")
	}
	return changed
}

// NewRelayBusTransport constructs the transport but does not start
// the subscriber goroutine until Start() is called.
func NewRelayBusTransport(relayURL, relayPassword, authToken string, b *Bus) *relayBusTransport {
	return &relayBusTransport{
		relayURL:      strings.TrimRight(relayURL, "/"),
		relayPassword: relayPassword,
		authToken:     authToken,
		b:             b,
		client:        &http.Client{Timeout: 0}, // no overall deadline — long-poll SSE
	}
}

func (t *relayBusTransport) Name() string { return "relay" }

// Start opens the SSE subscription goroutine. The caller is
// responsible for RegisterTransport-ing + keeping this alive for
// the lifetime of the bus.
func (t *relayBusTransport) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	t.wg.Add(1)
	go t.subscribeLoop(ctx)
}

func (t *relayBusTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
	return nil
}

// Publish sends one event to the relay via POST. Fire-and-forget
// at the transport level — the bus's own retain + dedup handles
// redelivery.
func (t *relayBusTransport) Publish(ctx context.Context, evt BusEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.relayURL+"/bus/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	}
	if pw := t.currentPassword(); pw != "" {
		req.Header.Set("X-Relay-Password", pw)
	}
	// Tight timeout — this is not the SSE call.
	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(pubCtx)
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		pubErr := fmt.Errorf("bus/publish: HTTP %d: %s", resp.StatusCode, string(msg))
		// Heal a rotated relay password so the next publish/subscribe succeeds.
		t.refreshPasswordIfStale(ctx, pubErr)
		return pubErr
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// subscribeLoop maintains a long-lived SSE connection to the relay.
// On disconnect, reconnects with exponential backoff up to 30 s —
// matches the existing relay reconnection pattern elsewhere in the
// agent. Each successful reconnect triggers a local re-announce of
// retained peer/{self}/* events so peers that reconnected in the
// meantime see our state immediately without waiting for the next
// heartbeat tick.
func (t *relayBusTransport) subscribeLoop(ctx context.Context) {
	defer t.wg.Done()
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := t.subscribeOnce(ctx); err != nil && ctx.Err() == nil {
			// Log once per loss, not per retry.
			fmt.Printf("[bus-relay] subscribe lost: %v (retry in %s)\n", err, backoff)
			// Recover from a Convex-rotated relay password instead of
			// looping forever on 401. On success, retry immediately.
			if t.refreshPasswordIfStale(ctx, err) {
				backoff = time.Second
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// subscribeOnce opens one SSE connection and blocks until it dies.
// Each line that starts with `data: ` is parsed as a BusEvent and
// pushed into the bus. A network hiccup or the relay restarting
// returns us to subscribeLoop for reconnect.
func (t *relayBusTransport) subscribeOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		t.relayURL+"/bus/subscribe", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if t.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	}
	if pw := t.currentPassword(); pw != "" {
		req.Header.Set("X-Relay-Password", pw)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("bus/subscribe: HTTP %d: %s", resp.StatusCode, string(msg))
	}

	// Read line-oriented SSE. Only `data:` frames carry payloads we
	// care about; other SSE directives (`event:`, `id:`, `:` comments)
	// are skipped silently.
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := string(bytes.TrimRight(buf[:idx], "\r"))
				buf = buf[idx+1:]
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				raw := line[len("data: "):]
				var evt BusEvent
				if err := json.Unmarshal([]byte(raw), &evt); err != nil {
					continue
				}
				t.b.Receive(evt)
			}
		}
		if err != nil {
			return err
		}
	}
}
