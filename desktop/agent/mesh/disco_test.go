package mesh

import (
	"testing"
	"time"
)

func hello(dev string, ms ...OverlayMembership) HelloBody {
	return HelloBody{DeviceID: dev, Overlays: ms}
}
func ts(nid, addr string) OverlayMembership {
	return OverlayMembership{Kind: OverlayTailscale, NetworkID: nid, Addr: addr, Reachable: true}
}
func ym(addr string) OverlayMembership {
	return OverlayMembership{Kind: OverlayYaverMesh, NetworkID: "yaver", Addr: addr, Reachable: true}
}

// The disco magic must never look like WireGuard, or the relay's opaque pump
// would hand signalling to wireguard-go and vice versa.
func TestDiscoNeverCollidesWithWireGuard(t *testing.T) {
	for wgType := byte(1); wgType <= 4; wgType++ {
		wg := []byte{wgType, 0, 0, 0, 0xde, 0xad}
		if IsDisco(wg) {
			t.Fatalf("WireGuard message type %d misread as disco — signalling would be fed to the tunnel", wgType)
		}
	}
	enc, err := EncodeDisco(DiscoMsg{Type: DiscoPing, TxID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !IsDisco(enc) {
		t.Fatal("our own disco frame not recognised")
	}
	if enc[0] >= 1 && enc[0] <= 4 {
		t.Fatalf("magic byte %#x is inside WireGuard's type range", enc[0])
	}
}

func TestUnknownVersionIsIgnoredNotAnError(t *testing.T) {
	enc, _ := EncodeDisco(DiscoMsg{Type: DiscoPing})
	enc[2] = 99 // a peer from the future
	m, err := DecodeDisco(enc)
	if err != nil || m != nil {
		t.Fatalf("a future version must be a silent no-op, got msg=%v err=%v", m, err)
	}
}

func TestRoundTrip(t *testing.T) {
	in := DiscoMsg{Type: DiscoEndpoints, SrcDevice: "A", Endpoints: []string{"10.0.0.2:51820"}, ObservedSrc: "1.2.3.4:9"}
	enc, err := EncodeDisco(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeDisco(enc)
	if err != nil || out == nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Type != DiscoEndpoints || out.ObservedSrc != "1.2.3.4:9" || len(out.Endpoints) != 1 {
		t.Fatalf("round trip lost data: %+v", out)
	}
}

// The four topologies, and the one that used to be wrong.
func TestNegotiateTopologies(t *testing.T) {
	cases := []struct {
		name      string
		a, b      HelloBody
		wantVia   OverlayKind
		wantRelay bool
	}{
		{"both same tailnet", hello("A", ts("net1", "100.1.1.1")), hello("B", ts("net1", "100.1.1.2")), OverlayTailscale, false},
		{"DIFFERENT tailnets is not a shared overlay", hello("A", ts("net1", "100.1.1.1")), hello("B", ts("net2", "100.2.2.2")), "", true},
		{"hybrid: one on tailscale, one not", hello("A", ts("net1", "100.1.1.1")), hello("B"), "", true},
		{"neither on anything", hello("A"), hello("B"), "", true},
		{"both yaver mesh only", hello("A", ym("100.96.0.1")), hello("B", ym("100.96.0.2")), OverlayYaverMesh, false},
		{"both same third-party vpn",
			hello("A", OverlayMembership{Kind: OverlayOtherVPN, NetworkID: "corp", Addr: "10.9.0.1", Reachable: true}),
			hello("B", OverlayMembership{Kind: OverlayOtherVPN, NetworkID: "corp", Addr: "10.9.0.2", Reachable: true}),
			OverlayOtherVPN, false},
		{"incumbent beats yaver mesh when both shared",
			hello("A", ts("net1", "100.1.1.1"), ym("100.96.0.1")),
			hello("B", ts("net1", "100.1.1.2"), ym("100.96.0.2")),
			OverlayTailscale, false},
	}
	for _, c := range cases {
		got := NegotiateTransport(c.a, c.b)
		if got.Via != c.wantVia || got.UseRelay() != c.wantRelay {
			t.Errorf("%s: via=%q relay=%v, want via=%q relay=%v (%s)", c.name, got.Via, got.UseRelay(), c.wantVia, c.wantRelay, got.Reason)
		}
		// Symmetry: both ends must reach the same verdict independently, or
		// they route at each other over different paths.
		rev := NegotiateTransport(c.b, c.a)
		if rev.Via != got.Via {
			t.Errorf("%s: NOT SYMMETRIC — A says %q, B says %q", c.name, got.Via, rev.Via)
		}
	}
}

func TestUnusableSharedOverlayIsNotChosen(t *testing.T) {
	down := OverlayMembership{Kind: OverlayTailscale, NetworkID: "net1", Addr: "100.1.1.2", Reachable: false}
	got := NegotiateTransport(hello("A", ts("net1", "100.1.1.1")), hello("B", down))
	if !got.UseRelay() {
		t.Fatalf("a shared-but-down overlay must fall to relay, not be routed into: %+v", got)
	}
}

func TestFallbackIsAlwaysDownwardAndStopsAtTheFloor(t *testing.T) {
	if n, _ := NextAfterFailure(TierDirect); n != TierOverlay {
		t.Fatalf("direct should fall to overlay, got %v", n)
	}
	if n, _ := NextAfterFailure(TierOverlay); n != TierRelay {
		t.Fatalf("overlay should fall to relay, got %v", n)
	}
	if n, _ := NextAfterFailure(TierRelay); n != TierRelay {
		t.Fatalf("the floor must not fall further, got %v", n)
	}
}

func TestUpgradeIsDamped(t *testing.T) {
	// A better tier is taken immediately.
	if !ShouldUpgrade(TierRelay, 50*time.Millisecond, 0, TierDirect, 60*time.Millisecond) {
		t.Fatal("a better tier must win even if slower")
	}
	// Same tier, marginally faster, inside the trust window: refuse (flapping).
	if ShouldUpgrade(TierDirect, 50*time.Millisecond, time.Second, TierDirect, 40*time.Millisecond) {
		t.Fatal("must not flap inside the trust window")
	}
	// Same tier, faster by a real margin, after the window: take it.
	if !ShouldUpgrade(TierDirect, 50*time.Millisecond, PathTrustWindow+time.Second, TierDirect, 20*time.Millisecond) {
		t.Fatal("a materially faster path after the window should win")
	}
	// Never downgrade tier on RTT alone.
	if ShouldUpgrade(TierDirect, 90*time.Millisecond, time.Hour, TierRelay, 1*time.Millisecond) {
		t.Fatal("a faster RELAY must never displace a working direct path")
	}
}

// Bill discipline: a stable device must produce ZERO writes.
func TestStatusOnlyWritesOnRealChange(t *testing.T) {
	prev := DeviceConnStatus{Intent: IntentOnline, Tier: "direct", OnTailnet: true, AtUnixMs: 1000}
	same := prev
	same.AtUnixMs = 999999 // only the clock moved
	if same.Changed(&prev) {
		t.Fatal("a timestamp-only difference must NOT trigger a Convex write — that is every heartbeat")
	}
	moved := prev
	moved.Tier = "relay"
	if !moved.Changed(&prev) {
		t.Fatal("a real tier change must be published")
	}
	if !(DeviceConnStatus{}).Changed(nil) {
		t.Fatal("first publish must always write")
	}
}

func TestIntentDrivesPeerBehaviour(t *testing.T) {
	now := time.Now().UnixMilli()
	fresh := func(i ConnIntent) DeviceConnStatus { return DeviceConnStatus{Intent: i, AtUnixMs: now} }

	if !fresh(IntentWantsPeers).ShouldAnswerProbes(now) {
		t.Fatal("a peer that lost its path and is seeking must be helped")
	}
	if fresh(IntentGoingDown).ShouldAnswerProbes(now) {
		t.Fatal("a deliberate shutdown must stop peers retrying — it is not a fault")
	}
	if fresh(IntentRelayOnly).ShouldAnswerProbes(now) {
		t.Fatal("relay-only means direct probes are waste")
	}
	old := DeviceConnStatus{Intent: IntentWantsPeers, AtUnixMs: now - IntentStaleAfter.Milliseconds() - 1}
	if old.ShouldAnswerProbes(now) {
		t.Fatal("a stale intent must be treated as unknown, not acted on")
	}
}
