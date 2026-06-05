package mesh

// extreme_test.go — adversarial / edge-case coverage for the mesh data plane.
// These are the "extreme" tests: malformed packets, boundary ports, IPv6,
// truncated frames, key validation, ACL ordering, and fuzz-style round-trips.
// They run with the normal `go test ./mesh/` (no root/TUN needed) — the
// privileged data-plane path is covered by scripts/test-mesh-e2e.sh.

import (
	"bytes"
	"encoding/base64"
	"math/rand"
	"net/netip"
	"testing"
)

// --- ACL matcher: ordering, overlap, boundaries ---

func TestMatcher_firstMatchWins(t *testing.T) {
	a := netip.MustParseAddr("100.96.0.1")
	b := netip.MustParseAddr("100.96.0.2")
	m := NewMatcher([]IPRule{
		{Src: []netip.Prefix{mustPrefix("100.96.0.1/32")}, Dst: []netip.Prefix{mustPrefix("100.96.0.2/32")}, Ports: []PortRange{{1, 65535}}, Action: ACLAccept},
		{Action: ACLDrop}, // any->any drop, but never reached for the pair above
	})
	if !m.Allow(a, b, 6, 22) {
		t.Error("first accept rule must win over later drop-all")
	}
	// A pair not covered by rule 1 hits the drop-all.
	if m.Allow(b, a, 6, 22) {
		t.Error("uncovered pair should hit the drop-all")
	}
}

func TestMatcher_portBoundaries(t *testing.T) {
	any := netip.MustParseAddr("100.96.0.9")
	m := NewMatcher([]IPRule{{Ports: []PortRange{{1024, 1024}}, Action: ACLAccept}})
	if !m.Allow(any, any, 6, 1024) {
		t.Error("exact boundary 1024 must match")
	}
	for _, p := range []uint16{0, 1023, 1025, 65535} {
		if m.Allow(any, any, 6, p) {
			t.Errorf("port %d outside [1024,1024] must be denied", p)
		}
	}
}

func TestMatcher_protoConstraint(t *testing.T) {
	any := netip.MustParseAddr("100.96.0.9")
	m := NewMatcher([]IPRule{{Protos: []uint8{6}, Action: ACLAccept}}) // TCP only
	if !m.Allow(any, any, 6, 80) {
		t.Error("TCP should match proto-constrained accept")
	}
	if m.Allow(any, any, 17, 80) {
		t.Error("UDP must not match a TCP-only rule (implicit deny)")
	}
}

func TestMatcher_multiPrefixSrc(t *testing.T) {
	m := NewMatcher([]IPRule{{
		Src:    []netip.Prefix{mustPrefix("100.96.0.0/24"), mustPrefix("10.0.0.0/8")},
		Action: ACLAccept,
	}})
	if !m.Allow(netip.MustParseAddr("10.5.5.5"), netip.MustParseAddr("1.1.1.1"), 6, 1) {
		t.Error("src in second prefix should match")
	}
	if m.Allow(netip.MustParseAddr("172.16.0.1"), netip.MustParseAddr("1.1.1.1"), 6, 1) {
		t.Error("src in neither prefix should be denied")
	}
}

// --- packet parsing: malformed / IPv6 / truncated L4 ---

func TestParseIPv4_rejects(t *testing.T) {
	cases := map[string][]byte{
		"empty":          {},
		"too short":      {0x45, 0x00, 0x00},
		"ipv6":           append([]byte{0x60}, make([]byte, 40)...),
		"bad ihl":        append([]byte{0x40}, make([]byte, 20)...), // IHL=0
		"ihl beyond len": {0x4f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8},
	}
	for name, pkt := range cases {
		if _, ok := parseIPv4(pkt); ok {
			t.Errorf("%s: expected parse failure", name)
		}
	}
}

func TestParseIPv4_nonTCPUDPHasZeroPort(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 1 // ICMP
	copy(pkt[12:16], []byte{100, 96, 0, 1})
	copy(pkt[16:20], []byte{100, 96, 0, 2})
	info, ok := parseIPv4(pkt)
	if !ok || info.proto != 1 || info.dstPort != 0 {
		t.Fatalf("ICMP should parse with dstPort 0: %+v ok=%v", info, ok)
	}
}

func TestParseIPv4_truncatedL4PortStaysZero(t *testing.T) {
	// IPv4 header says TCP but no L4 bytes present.
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 6 // TCP, but no ports follow
	info, ok := parseIPv4(pkt)
	if !ok || info.dstPort != 0 {
		t.Fatalf("truncated L4 must leave dstPort 0, got %+v", info)
	}
}

func TestAllowPacket_dropMatchedTCP(t *testing.T) {
	m := NewMatcher([]IPRule{{
		Dst:    []netip.Prefix{mustPrefix("100.96.0.6/32")},
		Ports:  []PortRange{{22, 22}},
		Action: ACLDrop,
	}, {Action: ACLAccept}})
	deny := buildTCPPacket(netip.MustParseAddr("100.96.0.5"), netip.MustParseAddr("100.96.0.6"), 22)
	allow := buildTCPPacket(netip.MustParseAddr("100.96.0.5"), netip.MustParseAddr("100.96.0.6"), 80)
	if m.allowPacket(deny) {
		t.Error(":22 should be dropped")
	}
	if !m.allowPacket(allow) {
		t.Error(":80 should be allowed by the catch-all")
	}
}

// --- DERP framing: truncation, large payloads, binary safety ---

func TestDERPFrame_truncatedDecodeFails(t *testing.T) {
	var buf bytes.Buffer
	_ = EncodeDERPFrame(&buf, "dev", []byte{1, 2, 3, 4, 5})
	full := buf.Bytes()
	for cut := 1; cut < len(full); cut++ {
		if _, _, err := DecodeDERPFrame(bytes.NewReader(full[:cut])); err == nil {
			t.Errorf("truncated frame at %d bytes should fail to decode", cut)
		}
	}
}

func TestDERPFrame_binarySafeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		payload := make([]byte, rng.Intn(1500))
		rng.Read(payload)
		id := make([]byte, rng.Intn(40))
		rng.Read(id)
		var buf bytes.Buffer
		if err := EncodeDERPFrame(&buf, string(id), payload); err != nil {
			t.Fatalf("encode: %v", err)
		}
		gotID, gotPayload, err := DecodeDERPFrame(&buf)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if gotID != string(id) || !bytes.Equal(gotPayload, payload) {
			t.Fatalf("round-trip mismatch at i=%d", i)
		}
	}
}

func TestDERPFrame_oversizeRejected(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeDERPFrame(&buf, "id", make([]byte, maxDERPFrame+1)); err == nil {
		t.Error("payload over max should be rejected")
	}
}

// --- STUN parsing: bad family, short attrs, MAPPED fallback ---

func TestParseBindingResponse_shortAttrIgnored(t *testing.T) {
	txID := [12]byte{7, 7, 7}
	resp := buildXorMappedResponse(txID, [4]byte{198, 51, 100, 9}, 4242)
	// Truncate one byte off the end → the attribute is incomplete; parser must
	// not panic and must report "no mapped address".
	if _, err := parseBindingResponse(resp[:len(resp)-1], txID); err == nil {
		t.Error("expected error on truncated attribute")
	}
}

func TestParseXorMappedAddress_rejectsIPv6Family(t *testing.T) {
	val := make([]byte, 20)
	val[1] = 0x02 // family 0x02 = IPv6 (we only handle IPv4)
	if _, ok := parseXorMappedAddress(val); ok {
		t.Error("IPv6 family should be rejected by the IPv4-only parser")
	}
}

// --- keys: clamping invariants over many samples, public determinism ---

func TestKeys_clampInvariantMany(t *testing.T) {
	for i := 0; i < 500; i++ {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		raw, _ := base64.StdEncoding.DecodeString(kp.PrivateKey)
		if len(raw) != 32 || raw[0]&7 != 0 || raw[31]&0x80 != 0 || raw[31]&0x40 == 0 {
			t.Fatalf("clamp invariant violated at i=%d: %x", i, raw)
		}
		pub, err := PublicFromPrivate(kp.PrivateKey)
		if err != nil || pub != kp.PublicKey {
			t.Fatalf("public non-deterministic at i=%d", i)
		}
	}
}

// --- renderPeers: empty allowed-ips, multiple peers ordering ---

func TestRenderPeers_multiplePeersAllPresent(t *testing.T) {
	k1, _ := GenerateKeyPair()
	k2, _ := GenerateKeyPair()
	cfg, err := renderPeers([]Peer{
		{PublicKey: k1.PublicKey, AllowedIPs: []string{"100.96.0.2/32"}},
		{PublicKey: k2.PublicKey, Endpoint: "1.2.3.4:51820", AllowedIPs: []string{"100.96.0.3/32"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Both peers' hex keys present; replace_peers set once at the top.
	if bytes.Count([]byte(cfg), []byte("replace_peers=true")) != 1 {
		t.Error("replace_peers should appear exactly once")
	}
	if bytes.Count([]byte(cfg), []byte("public_key=")) != 2 {
		t.Error("both peers should be rendered")
	}
}
