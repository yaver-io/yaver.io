package mesh

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

// buildTCPPacket fabricates a minimal IPv4+TCP packet for src->dst:dstPort.
func buildTCPPacket(src, dst netip.Addr, dstPort uint16) []byte {
	pkt := make([]byte, 24)
	pkt[0] = 0x45 // IPv4, IHL=5
	pkt[9] = 6    // TCP
	s := src.As4()
	d := dst.As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	binary.BigEndian.PutUint16(pkt[20:22], 12345)   // src port
	binary.BigEndian.PutUint16(pkt[22:24], dstPort) // dst port
	return pkt
}

func TestMatcher_defaultAllowWhenNoRules(t *testing.T) {
	m := NewMatcher(nil)
	if !m.Allow(netip.MustParseAddr("100.96.0.1"), netip.MustParseAddr("100.96.0.2"), 6, 22) {
		t.Error("empty ruleset must default-allow")
	}
}

func TestMatcher_implicitDenyOnceRulesExist(t *testing.T) {
	// One accept rule for :22 only. Anything else is denied (Tailscale semantics).
	m := NewMatcher([]IPRule{{
		Src:    []netip.Prefix{mustPrefix("100.96.0.1/32")},
		Dst:    []netip.Prefix{mustPrefix("100.96.0.2/32")},
		Ports:  []PortRange{{22, 22}},
		Action: ACLAccept,
	}})
	a := netip.MustParseAddr("100.96.0.1")
	b := netip.MustParseAddr("100.96.0.2")
	if !m.Allow(a, b, 6, 22) {
		t.Error("allowed port 22 should pass")
	}
	if m.Allow(a, b, 6, 80) {
		t.Error("port 80 should be denied by implicit default-deny")
	}
	if m.Allow(b, a, 6, 22) {
		t.Error("reverse direction not in rules → denied")
	}
}

func TestMatcher_dropRuleBeatsLaterAccept(t *testing.T) {
	a := netip.MustParseAddr("100.96.0.1")
	b := netip.MustParseAddr("100.96.0.2")
	m := NewMatcher([]IPRule{
		{Src: []netip.Prefix{mustPrefix("100.96.0.1/32")}, Dst: []netip.Prefix{mustPrefix("100.96.0.2/32")}, Ports: []PortRange{{22, 22}}, Action: ACLDrop},
		{Action: ACLAccept}, // any->any
	})
	if m.Allow(a, b, 6, 22) {
		t.Error("first-match drop should win over later any-accept")
	}
	if !m.Allow(a, b, 6, 80) {
		t.Error("port 80 should hit the any-accept rule")
	}
}

func TestMatcher_portRange(t *testing.T) {
	m := NewMatcher([]IPRule{{Ports: []PortRange{{8000, 8100}}, Action: ACLAccept}})
	any := netip.MustParseAddr("100.96.0.9")
	if !m.Allow(any, any, 6, 8050) {
		t.Error("8050 in range should pass")
	}
	if m.Allow(any, any, 6, 9000) {
		t.Error("9000 out of range should be denied")
	}
}

func TestParseIPv4_extractsTuple(t *testing.T) {
	src := netip.MustParseAddr("100.96.0.5")
	dst := netip.MustParseAddr("100.96.0.6")
	info, ok := parseIPv4(buildTCPPacket(src, dst, 443))
	if !ok {
		t.Fatal("parse failed")
	}
	if info.src != src || info.dst != dst || info.proto != 6 || info.dstPort != 443 {
		t.Fatalf("bad parse: %+v", info)
	}
}

func TestAllowPacket_failOpenForNonIPv4(t *testing.T) {
	m := NewMatcher([]IPRule{{Action: ACLDrop}}) // drop-all
	if !m.allowPacket([]byte{0x60, 0x00}) {      // looks like IPv6/garbage
		t.Error("unparseable/non-IPv4 packets must fail-open, not be dropped")
	}
}
