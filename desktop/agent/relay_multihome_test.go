package main

import (
	"context"
	"testing"
)

// Multi-relay failover is the product's only defence against "one relay reboots
// and every phone loses every machine" — from LTE there is no LAN path and no
// VPN path, so the relay is the ONLY route to a box.
//
// It works today for one reason and one reason only: the agent opens a tunnel to
// EVERY configured relay, so a device is reachable through any of them, and the
// client (buildRemoteAgentCandidates) emits a /d/<deviceID> candidate per relay.
// Nobody wrote that down and nothing tested it, which makes it exactly the kind
// of property a plausible refactor deletes by accident — "why dial three relays
// when one is enough? just pick the healthiest" is a very reasonable-sounding
// change that would silently reduce the fleet to a single point of failure and
// not fail a single test.
//
// So: assert it. If you are here because this test failed, you did not break a
// test — you broke relay redundancy.
func TestApplyRelayServersTunnelsToEveryConfiguredRelay(t *testing.T) {
	// A cancelled parent makes runRelayTunnel's goroutine return on its first
	// select instead of dialling the network; applyRelayServers still records
	// the tunnel, which is what we are asserting on.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rm := &relayManager{
		parentCtx:     ctx,
		deviceID:      "dev-multihome",
		activeTunnels: make(map[string]context.CancelFunc),
		healthStatus:  make(map[string]*RelayHealthStatus),
	}

	servers := []RelayServerInfo{
		{ID: "eu-1", QuicAddr: "relay-a.example.com:4433", HttpURL: "https://relay-a.example.com"},
		{ID: "eu-2", QuicAddr: "relay-b.example.com:4433", HttpURL: "https://relay-b.example.com"},
		{ID: "us-1", QuicAddr: "relay-c.example.com:4433", HttpURL: "https://relay-c.example.com"},
	}
	rm.applyRelayServers(servers, map[string]string{})

	if got, want := len(rm.activeTunnels), len(servers); got != want {
		t.Fatalf("agent opened %d tunnel(s) for %d configured relays — redundancy is gone; "+
			"a device is only reachable through relays it is tunnelled to", got, want)
	}
	for _, rs := range servers {
		if _, ok := rm.activeTunnels[rs.QuicAddr]; !ok {
			t.Errorf("no tunnel to %s — that relay cannot serve this device", rs.QuicAddr)
		}
	}
}

// Removing a relay from config must tear its tunnel down (otherwise a
// decommissioned relay keeps a registration alive and clients keep probing a
// host that will never answer).
func TestApplyRelayServersDropsRemovedRelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rm := &relayManager{
		parentCtx:     ctx,
		deviceID:      "dev-multihome",
		activeTunnels: make(map[string]context.CancelFunc),
		healthStatus:  make(map[string]*RelayHealthStatus),
	}

	both := []RelayServerInfo{
		{ID: "eu-1", QuicAddr: "relay-a.example.com:4433", HttpURL: "https://relay-a.example.com"},
		{ID: "eu-2", QuicAddr: "relay-b.example.com:4433", HttpURL: "https://relay-b.example.com"},
	}
	rm.applyRelayServers(both, map[string]string{})
	if len(rm.activeTunnels) != 2 {
		t.Fatalf("setup: expected 2 tunnels, got %d", len(rm.activeTunnels))
	}

	rm.applyRelayServers(both[:1], map[string]string{})
	if len(rm.activeTunnels) != 1 {
		t.Fatalf("expected the removed relay's tunnel to be torn down, still have %d", len(rm.activeTunnels))
	}
	if _, ok := rm.activeTunnels["relay-b.example.com:4433"]; ok {
		t.Error("relay-b was removed from config but its tunnel is still active")
	}
}
