package mesh

// acl.go (Phase 4) — port-level ACL enforcement on the overlay. This is the
// Tailscale-style "src → dst:ports" rule engine. The control plane (Convex
// meshAcls) stores rules by tag/device/user; the agent resolves those to overlay
// IP prefixes and compiles them into a Matcher. The Matcher is consulted on the
// INBOUND path (packets a peer sends us) — the authoritative enforcement point,
// matching sdk/js/src/acl.ts's "Convex composes, agent enforces".
//
// Everything here is pure (no I/O), so the matching logic is fully unit-tested.

import (
	"encoding/binary"
	"net/netip"
)

// ACLAction is accept or drop.
type ACLAction int

const (
	ACLAccept ACLAction = iota
	ACLDrop
)

// PortRange is an inclusive port span. An empty Ports slice on a rule means "all
// ports".
type PortRange struct {
	Lo uint16
	Hi uint16
}

func (p PortRange) contains(port uint16) bool { return port >= p.Lo && port <= p.Hi }

// IPRule is an ACL rule already resolved to overlay IP prefixes.
type IPRule struct {
	Src    []netip.Prefix
	Dst    []netip.Prefix
	Ports  []PortRange // empty = all ports
	Protos []uint8     // empty = all protocols; else e.g. 6 (TCP), 17 (UDP)
	Action ACLAction
}

// Matcher evaluates packets against an ordered rule list. With NO rules it is
// "default allow" — enabling the mesh must not silently break connectivity;
// users opt into restriction by authoring rules.
type Matcher struct {
	rules        []IPRule
	defaultAllow bool
}

// NewMatcher builds a matcher. An empty rule set is default-allow.
func NewMatcher(rules []IPRule) *Matcher {
	return &Matcher{rules: rules, defaultAllow: len(rules) == 0}
}

// DenyAllMatcher blocks all modeled (IPv4 TCP/UDP) inbound traffic. It is the
// fail-CLOSED default installed when ACL rules cannot be loaded yet (e.g. a
// control-plane outage at boot), so the overlay never comes up unfiltered.
// Non-IPv4 / unmodeled packets still pass (see allowPacket) so the WireGuard
// tunnel and IPv6 ND keep working; only modeled peer traffic is denied until a
// real rule set loads.
func DenyAllMatcher() *Matcher {
	return &Matcher{rules: nil, defaultAllow: false}
}

// Allow reports whether a packet src->dst proto/port is permitted. Rules are
// evaluated in order; the first whose src/dst/proto/port all match decides
// (accept or drop). If none match, the default applies: allow when there are no
// rules, otherwise deny (implicit default-deny once any rule exists — Tailscale
// semantics).
func (m *Matcher) Allow(src, dst netip.Addr, proto uint8, dstPort uint16) bool {
	if m == nil {
		return true
	}
	for _, r := range m.rules {
		if !prefixesContain(r.Src, src) || !prefixesContain(r.Dst, dst) {
			continue
		}
		if len(r.Protos) > 0 && !containsU8(r.Protos, proto) {
			continue
		}
		if len(r.Ports) > 0 && !portsContain(r.Ports, dstPort) {
			continue
		}
		return r.Action == ACLAccept
	}
	return m.defaultAllow
}

func prefixesContain(prefixes []netip.Prefix, ip netip.Addr) bool {
	if len(prefixes) == 0 {
		return true // unconstrained side ("any")
	}
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func portsContain(ports []PortRange, port uint16) bool {
	for _, pr := range ports {
		if pr.contains(port) {
			return true
		}
	}
	return false
}

func containsU8(s []uint8, v uint8) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// packetInfo is the 4-tuple the matcher needs from an IPv4 packet.
type packetInfo struct {
	src     netip.Addr
	dst     netip.Addr
	proto   uint8
	dstPort uint16
}

// parseIPv4 extracts src/dst/proto/dstPort from an IPv4 packet. Returns ok=false
// for non-IPv4 or truncated packets (those are passed through unfiltered — we
// only enforce on traffic we understand). dstPort is 0 for non-TCP/UDP.
func parseIPv4(pkt []byte) (packetInfo, bool) {
	if len(pkt) < 20 {
		return packetInfo{}, false
	}
	if pkt[0]>>4 != 4 {
		return packetInfo{}, false // not IPv4 (IPv6 handled separately later)
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return packetInfo{}, false
	}
	proto := pkt[9]
	src := netip.AddrFrom4([4]byte{pkt[12], pkt[13], pkt[14], pkt[15]})
	dst := netip.AddrFrom4([4]byte{pkt[16], pkt[17], pkt[18], pkt[19]})

	var dstPort uint16
	switch proto {
	case 6, 17: // TCP, UDP — destination port is bytes 2..4 of the L4 header
		if len(pkt) >= ihl+4 {
			dstPort = binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])
		}
	}
	return packetInfo{src: src, dst: dst, proto: proto, dstPort: dstPort}, true
}

// allowPacket parses a packet and applies the matcher. Unparseable/non-IPv4
// packets are allowed (fail-open for traffic we don't model, so the mesh never
// silently drops e.g. IPv6 ND).
func (m *Matcher) allowPacket(pkt []byte) bool {
	info, ok := parseIPv4(pkt)
	if !ok {
		return true
	}
	return m.Allow(info.src, info.dst, info.proto, info.dstPort)
}
