package main

import (
	"net"
	"testing"
)

func TestIsPrivateLanIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		// RFC1918 — true.
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.111.25", true},
		// Outside RFC1918 — false. 100.x is Tailscale CGNAT, not LAN.
		{"100.64.0.1", false},
		{"100.89.155.25", false},
		{"100.127.255.255", false},
		// Public.
		{"8.8.8.8", false},
		// Just-outside-172/12.
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		// Loopback / link-local.
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		// Empty / garbage.
		{"", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		got := isPrivateLanIPv4(net.ParseIP(tc.ip))
		if got != tc.want {
			t.Errorf("isPrivateLanIPv4(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIsCGNATTailscaleIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.89.155.25", true},
		{"100.127.255.255", true},
		// Just outside 100.64/10.
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		// RFC1918.
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		// Public.
		{"100.0.0.1", false},
		// Garbage.
		{"", false},
		{"100.64", false},
	}
	for _, tc := range cases {
		got := isCGNATTailscaleIP(tc.ip)
		if got != tc.want {
			t.Errorf("isCGNATTailscaleIP(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestSameIPv4Slash24(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"192.168.111.25", "192.168.111.8", true},
		{"192.168.111.25", "192.168.112.8", false},
		{"10.0.0.1", "10.0.0.99", true},
		{"10.0.0.1", "10.0.1.1", false},
		{"172.16.0.5", "172.16.0.99", true},
		// IPv6 — always false (we only compare /24 of v4).
		{"::1", "192.168.1.1", false},
	}
	for _, tc := range cases {
		got := sameIPv4Slash24(net.ParseIP(tc.a), net.ParseIP(tc.b))
		if got != tc.want {
			t.Errorf("sameIPv4Slash24(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestPickReachableLanIP_NoLocalsReturnsEmpty shoves an empty locals
// list at the picker by stubbing the interface scan would be ideal,
// but we can't easily — so instead verify the candidate-side filters
// rule out non-RFC1918 IPs even when a local match would exist. The
// no-locals case is exercised implicitly: if the test host has no
// RFC1918 interfaces (CI inside Docker), pickReachableLanIP returns
// "" regardless of candidate quality.
func TestPickReachableLanIP_RejectsNonLanCandidates(t *testing.T) {
	// All of these should be rejected even on a host with active LAN.
	for _, bad := range []string{
		"100.64.0.5",   // Tailscale CGNAT
		"100.89.155.25", // Tailscale CGNAT (the regression IP)
		"8.8.8.8",      // public
		"127.0.0.1",    // loopback
		"::1",          // ipv6 loopback
		"169.254.1.1",  // link-local
		"",             // empty
		"garbage",      // unparseable
		"172.17.0.1",   // docker bridge default
	} {
		if got := pickReachableLanIP([]string{bad}); got != "" {
			t.Errorf("pickReachableLanIP(%q) = %q, want \"\"", bad, got)
		}
	}
}

// TestPickReachableLanIP_PicksFirstSubnetMatch verifies preference
// ordering when multiple LAN candidates are on different subnets:
// the first one whose /24 matches a local interface wins. We discover
// the host's actual local /24 first, then craft candidates around it.
func TestPickReachableLanIP_PicksFirstSubnetMatch(t *testing.T) {
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		t.Skip("no RFC1918 local interface — can't test subnet matching")
	}
	local := locals[0].To4()
	// Off-subnet first, on-subnet second — picker must skip the
	// off-subnet one and return the on-subnet one.
	off := net.IPv4(10, 99, 99, 99).String()
	on := net.IPv4(local[0], local[1], local[2], 200).String()
	got := pickReachableLanIP([]string{off, on})
	if got != on {
		t.Errorf("pickReachableLanIP([%q,%q]) = %q, want %q", off, on, got, on)
	}
}

// TestLocalTailscaleUp_ConsistentWithInterfaces is a sanity check
// that the helper agrees with what net.Interfaces actually shows us.
// We can't assert true/false because it depends on the host running
// the test, but we can confirm the function doesn't panic and
// returns the same answer twice in a row.
func TestLocalTailscaleUp_StableAcrossCalls(t *testing.T) {
	a := localTailscaleUp()
	b := localTailscaleUp()
	if a != b {
		t.Fatalf("localTailscaleUp returned different values across calls: %v then %v", a, b)
	}
}

func TestTailscaleStateLabel(t *testing.T) {
	if got := tailscaleStateLabel(true); got == "" {
		t.Fatalf("tailscaleStateLabel(true) returned empty label")
	}
	if got := tailscaleStateLabel(false); got == "" {
		t.Fatalf("tailscaleStateLabel(false) returned empty label")
	}
}
