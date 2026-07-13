package main

import (
	"strings"
	"testing"
)

// TestFilterAdvertisedRoutes locks in the cross-tenant route-validation fixes:
// a malicious peer must not be able to (a) bypass the exit-node gate with a
// default-route split, (b) claim overlay addresses to source-spoof other nodes,
// or (c) smuggle in unparseable CIDRs.
func TestFilterAdvertisedRoutes(t *testing.T) {
	const peer = "peerDeviceA"

	cases := []struct {
		name        string
		routes      []string
		useExitNode string
		want        []string
	}{
		{
			name:        "default route honored only for chosen exit node",
			routes:      []string{"0.0.0.0/0"},
			useExitNode: peer,
			want:        []string{"0.0.0.0/0"},
		},
		{
			name:        "default route dropped when peer is not the exit node",
			routes:      []string{"0.0.0.0/0"},
			useExitNode: "",
			want:        []string{},
		},
		{
			name:        "default-route SPLIT bypass is gated like a default route",
			routes:      []string{"0.0.0.0/1", "128.0.0.0/1"},
			useExitNode: "", // not chosen → both halves dropped
			want:        []string{},
		},
		{
			name:        "overlay range cannot be claimed",
			routes:      []string{"100.96.0.0/12"},
			useExitNode: peer,
			want:        []string{},
		},
		{
			name:        "another node's overlay /32 cannot be claimed",
			routes:      []string{"100.111.5.5/32"},
			useExitNode: peer,
			want:        []string{},
		},
		{
			name:        "legit LAN subnet route allowed",
			routes:      []string{"192.168.1.0/24"},
			useExitNode: "",
			want:        []string{"192.168.1.0/24"},
		},
		{
			name:        "unparseable CIDR dropped",
			routes:      []string{"not-a-cidr", "10.0.0.0/8"},
			useExitNode: "",
			want:        []string{"10.0.0.0/8"},
		},
		{
			// Tailscale interop: the tailnet-minus-overlay CIDRs must survive the
			// overlap filter (raw 100.64/10 would NOT — it contains the overlay).
			name:        "tailnet interop routes accepted",
			routes:      tailnetInteropRoutes,
			useExitNode: "",
			want:        []string{"100.64.0.0/11", "100.112.0.0/12"},
		},
		{
			name:        "raw tailnet 100.64/10 rejected (would capture overlay)",
			routes:      []string{"100.64.0.0/10"},
			useExitNode: "",
			want:        []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAdvertisedRoutes(tc.routes, peer, tc.useExitNode)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("routes=%v exit=%q → got %v, want %v",
					tc.routes, tc.useExitNode, got, tc.want)
			}
		})
	}
}
