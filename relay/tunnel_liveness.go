package main

// Tunnel liveness: evict tunnels that are registered but no longer forwarding.
//
// A QUIC tunnel can go ZOMBIE — registered here, believed healthy by the agent,
// and yet nothing sent down it ever arrives. Both ends miss it:
//
//   - The relay's "up 1h4m" is just now−connectedAt. It is not liveness, and
//     nothing here ever tested delivery.
//   - OpenStreamSync succeeds against a dead peer, because QUIC creates the
//     stream locally without a round-trip. So the per-request path opens a
//     stream happily and then blocks reading a response that never comes.
//   - The agent sits in AcceptStream on a connection that never errors, so its
//     tunnel counter stays live and it keeps publishing relayConnected=true.
//
// The victim is the phone: it has no LAN and no VPN path, so the relay is its
// ONLY route to a machine. Observed 2026-07-14 on an always-on Mac mini —
// registered for over an hour, every request from the phone timing out, both
// ends reporting a healthy tunnel. It came back the instant the agent restarted.
//
// Worse, a zombie BLOCKS ITS OWN REPLACEMENT: registration refuses a duplicate
// deviceId while the stale conn's context has not fired, which for a zombie it
// never does.
//
// So probe delivery, and when it fails, evict the tunnel AND close the
// connection — closing it is what makes the agent's serve loop return and redial.
// This works against agents of any version, which matters because the fleet
// upgrades slowly and the stranded box is exactly the one you cannot reach to
// upgrade.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"
)

const (
	// How often to test that the tunnel can still carry a request.
	tunnelProbeInterval = 30 * time.Second
	// A probe hits the agent's local /health, so it answers in milliseconds
	// when alive. 10s is pure headroom for a loaded box.
	tunnelProbeTimeout = 10 * time.Second
	// Consecutive failures before eviction. Two filters a single flake while
	// still recovering the tunnel inside ~1 minute.
	tunnelProbeFailuresBeforeEvict = 2
)

// watchTunnelLiveness probes a tunnel until it dies or stops forwarding.
func (s *RelayServer) watchTunnelLiveness(t *agentTunnel) {
	fails := 0
	for {
		select {
		case <-t.conn.Context().Done():
			return // conn closed normally; the registration cleanup path handles it
		case <-time.After(tunnelProbeInterval):
		}

		if err := s.probeTunnel(t); err != nil {
			fails++
			if fails < tunnelProbeFailuresBeforeEvict {
				continue
			}
			log.Printf("[RELAY] tunnel %s registered but not forwarding (%v) — evicting so the agent redials",
				shortID(t.deviceID), err)
			s.mu.Lock()
			if cur, ok := s.tunnels[t.deviceID]; ok && cur.conn == t.conn {
				delete(s.tunnels, t.deviceID)
			}
			s.mu.Unlock()
			// Closing the connection is the part that actually heals it: the
			// agent's AcceptStream returns, its serve loop exits, and it redials
			// into a registration slot we have just freed.
			t.conn.CloseWithError(0, "tunnel not forwarding")
			return
		}
		fails = 0
	}
}

// probeTunnel forwards a synthetic GET /health down the tunnel and waits for the
// agent to answer. Deliberately a REAL proxied request rather than a new ping
// message type: it exercises the same path a phone uses, and needs no support
// from the agent, so it detects zombies on agents that predate this code.
func (s *RelayServer) probeTunnel(t *agentTunnel) error {
	ctx, cancel := context.WithTimeout(context.Background(), tunnelProbeTimeout)
	defer cancel()

	stream, err := t.conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.CancelRead(0)

	req := TunnelRequest{
		ID:      fmt.Sprintf("liveness-%d", time.Now().UnixNano()),
		Method:  "GET",
		Path:    "/health",
		Headers: map[string]string{},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal probe: %w", err)
	}
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("write probe: %w", err)
	}
	stream.Close() // half-close: we are done writing

	// The agent replies as soon as its own /health answers, so the FIRST byte is
	// the liveness signal. We deliberately do not read the whole body: a healthy
	// agent proves itself with one byte, and a zombie never produces even that.
	//
	// (This is also why per-request reads have no deadline and must not get one:
	// a real request can legitimately take minutes to produce its first byte —
	// Metro bundling, hermesc — so slowness there is not evidence of death. A
	// /health probe has no such excuse.)
	_ = stream.SetReadDeadline(time.Now().Add(tunnelProbeTimeout))
	var first [1]byte
	if _, err := io.ReadFull(stream, first[:]); err != nil {
		return fmt.Errorf("no response to /health probe: %w", err)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
