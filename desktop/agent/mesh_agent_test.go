package main

import (
	"net"
	"testing"
)

func TestPickPeerEndpoint_prefersSameLAN(t *testing.T) {
	local := []net.IP{net.ParseIP("192.168.1.50")}
	endpoints := []string{"192.168.1.20:51820", "203.0.113.7:51820"}
	if got := pickPeerEndpoint(local, endpoints); got != "192.168.1.20:51820" {
		t.Fatalf("expected same-LAN endpoint, got %q", got)
	}
}

func TestPickPeerEndpoint_fallsBackToPublic(t *testing.T) {
	// We are on a different LAN than the peer's private IP, so the public
	// (STUN-discovered) endpoint is the reachable one.
	local := []net.IP{net.ParseIP("10.0.0.5")}
	endpoints := []string{"192.168.1.20:51820", "203.0.113.7:51820"}
	if got := pickPeerEndpoint(local, endpoints); got != "203.0.113.7:51820" {
		t.Fatalf("expected public endpoint, got %q", got)
	}
}

func TestPickPeerEndpoint_lastResortFirst(t *testing.T) {
	local := []net.IP{net.ParseIP("10.0.0.5")}
	endpoints := []string{"192.168.1.20:51820"} // only an unreachable LAN IP
	if got := pickPeerEndpoint(local, endpoints); got != "192.168.1.20:51820" {
		t.Fatalf("expected first endpoint as last resort, got %q", got)
	}
}

func TestPickPeerEndpoint_empty(t *testing.T) {
	if got := pickPeerEndpoint(nil, nil); got != "" {
		t.Fatalf("expected empty string for no endpoints, got %q", got)
	}
}

func TestSameIPv24(t *testing.T) {
	if !sameIPv24(net.ParseIP("192.168.1.5"), net.ParseIP("192.168.1.250")) {
		t.Error("same /24 should be true")
	}
	if sameIPv24(net.ParseIP("192.168.1.5"), net.ParseIP("192.168.2.5")) {
		t.Error("different /24 should be false")
	}
}
