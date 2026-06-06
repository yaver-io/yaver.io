package main

import "testing"

// The Yaver mesh overlay (100.96/12) is a subset of Tailscale's CGNAT
// block (100.64/10). These tests pin the classification split so the
// SSH resolver can gate mesh and Tailscale routes independently — a
// user who dropped Tailscale but ran `yaver mesh up` must keep
// overlay-SSH, and a 100.96 address must never be claimed as Tailscale.
func TestMeshVsTailscaleClassification(t *testing.T) {
	tests := []struct {
		ip       string
		mesh     bool
		tailscale bool
	}{
		// Yaver mesh overlay 100.96.0.0/12 → 100.96–111.x.y
		{ip: "100.96.0.2", mesh: true, tailscale: false},
		{ip: "100.96.5.17", mesh: true, tailscale: false},
		{ip: "100.111.255.254", mesh: true, tailscale: false},
		// Tailscale CGNAT outside the mesh sub-range.
		{ip: "100.64.0.5", mesh: false, tailscale: true},
		{ip: "100.95.255.255", mesh: false, tailscale: true},
		{ip: "100.112.0.1", mesh: false, tailscale: true},
		{ip: "100.127.255.255", mesh: false, tailscale: true},
		// Real LAN / public — neither.
		{ip: "192.168.1.10", mesh: false, tailscale: false},
		{ip: "10.0.0.4", mesh: false, tailscale: false},
		{ip: "100.63.0.1", mesh: false, tailscale: false}, // just below CGNAT
		{ip: "100.128.0.1", mesh: false, tailscale: false}, // just above CGNAT
		{ip: "8.8.8.8", mesh: false, tailscale: false},
		{ip: "not-an-ip", mesh: false, tailscale: false},
		{ip: "", mesh: false, tailscale: false},
	}
	for _, tc := range tests {
		if got := isMeshOverlayIPv4(tc.ip); got != tc.mesh {
			t.Errorf("isMeshOverlayIPv4(%q) = %v, want %v", tc.ip, got, tc.mesh)
		}
		if got := isCGNATTailscaleIP(tc.ip); got != tc.tailscale {
			t.Errorf("isCGNATTailscaleIP(%q) = %v, want %v", tc.ip, got, tc.tailscale)
		}
		// A mesh IP and a Tailscale IP are mutually exclusive — the two
		// gates must never both fire for the same address.
		if isMeshOverlayIPv4(tc.ip) && isCGNATTailscaleIP(tc.ip) {
			t.Errorf("%q classified as BOTH mesh and Tailscale", tc.ip)
		}
	}
}
