package mesh

import "testing"

// twoManagers wires A and B through the in-memory fake relay, exactly as the
// real relay forwards frames between two agents.
func twoManagers(t *testing.T) (a, b *DERPManager) {
	t.Helper()
	ta := &fakeTransport{peers: map[string]*DERPManager{}, selfID: "A"}
	tb := &fakeTransport{peers: map[string]*DERPManager{}, selfID: "B"}
	a = NewDERPManager(0, ta)
	b = NewDERPManager(0, tb)
	ta.peers["B"] = b
	tb.peers["A"] = a
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

// THE test for this wiring. Signalling is how peers discover each other, so it
// must arrive before any shim exists — the state in which the WireGuard path
// deliberately drops frames. Checking disco after the peer lookup would make
// first contact impossible while appearing to work between peers already
// talking, which is the one case it is not needed for.
func TestDiscoDeliveredWithNoPeerShim(t *testing.T) {
	a, b := twoManagers(t)

	got := make(chan *DiscoMsg, 1)
	gotSrc := make(chan string, 1)
	b.SetDiscoHandler(func(src string, m *DiscoMsg) { gotSrc <- src; got <- m })

	// No EndpointFor() anywhere: neither side has a shim, exactly like first
	// contact between two boxes that have never spoken.
	if err := a.SendDisco("B", DiscoMsg{Type: DiscoHello, SrcDevice: "A"}); err != nil {
		t.Fatalf("SendDisco: %v", err)
	}
	select {
	case m := <-got:
		if m.Type != DiscoHello {
			t.Fatalf("wrong type %d", m.Type)
		}
		if src := <-gotSrc; src != "A" {
			t.Fatalf("wrong src %q", src)
		}
	default:
		t.Fatal("disco dropped for a peer with no shim — first contact could never work")
	}
}

func TestWireGuardFramesNeverReachTheDiscoHandler(t *testing.T) {
	_, b := twoManagers(t)
	called := false
	b.SetDiscoHandler(func(string, *DiscoMsg) { called = true })
	for wgType := byte(1); wgType <= 4; wgType++ {
		b.DeliverFrame("A", []byte{wgType, 0, 0, 0, 0xbe, 0xef})
	}
	if called {
		t.Fatal("a WireGuard frame was routed to the disco handler")
	}
}

func TestCorruptDiscoIsDroppedNotForwarded(t *testing.T) {
	_, b := twoManagers(t)
	called := false
	b.SetDiscoHandler(func(string, *DiscoMsg) { called = true })
	// Correct magic, garbage body — must not reach the handler, and must never
	// fall through to WireGuard either.
	b.DeliverFrame("A", []byte{DiscoMagic[0], DiscoMagic[1], DiscoVersion, DiscoPing, '{', '!'})
	if called {
		t.Fatal("a corrupt disco frame must not reach the handler")
	}
}

// A full HELLO exchange over the relay, both sides reaching the same verdict.
func TestHelloExchangeAgreesOnTransport(t *testing.T) {
	a, b := twoManagers(t)

	selfA := HelloBody{DeviceID: "A", Overlays: []OverlayMembership{
		{Kind: OverlayTailscale, NetworkID: "net1", Addr: "100.1.1.1", Reachable: true},
	}}
	selfB := HelloBody{DeviceID: "B", Overlays: []OverlayMembership{
		{Kind: OverlayTailscale, NetworkID: "net1", Addr: "100.1.1.2", Reachable: true},
	}}

	bGot := make(chan *DiscoMsg, 1)
	b.SetDiscoHandler(func(_ string, m *DiscoMsg) { bGot <- m })
	aGot := make(chan *DiscoMsg, 1)
	a.SetDiscoHandler(func(_ string, m *DiscoMsg) { aGot <- m })

	if err := a.SendDisco("B", DiscoMsg{Type: DiscoHello, SrcDevice: "A"}); err != nil {
		t.Fatal(err)
	}
	if len(bGot) != 1 {
		t.Fatal("B never received A's HELLO")
	}
	if err := b.SendDisco("A", DiscoMsg{Type: DiscoHello, SrcDevice: "B"}); err != nil {
		t.Fatal(err)
	}
	if len(aGot) != 1 {
		t.Fatal("A never received B's HELLO")
	}

	// Both ends negotiate independently and must agree, or they route at each
	// other over different paths.
	fromA := NegotiateTransport(selfA, selfB)
	fromB := NegotiateTransport(selfB, selfA)
	if fromA.Via != OverlayTailscale || fromB.Via != OverlayTailscale {
		t.Fatalf("same tailnet should win: A=%q B=%q", fromA.Via, fromB.Via)
	}
	if fromA.UseRelay() || fromB.UseRelay() {
		t.Fatal("a shared, up overlay must not fall to relay")
	}
}
