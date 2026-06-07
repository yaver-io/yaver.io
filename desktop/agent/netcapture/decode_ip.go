package netcapture

import (
	"encoding/binary"
	"fmt"
	"net"
)

// packet is the decoded L3/L4 view of one captured frame, handed to the
// Analyzer. Pure-Go, no libpcap.
type packet struct {
	ts      int64
	l4      byte // 6=TCP 17=UDP 1=ICMP 58=ICMPv6 0=other
	srcIP   string
	dstIP   string
	srcPort int
	dstPort int
	flags   byte // TCP flags
	seq     uint32
	ack     uint32
	payload []byte
	size    int // wire size (orig len)
}

// TCP flag bits.
const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpPSH = 0x08
	tcpACK = 0x10
)

// link-layer types we understand (libpcap DLT_*).
const (
	linkNull     = 0   // BSD loopback (lo0 on macOS): 4-byte AF header
	linkEthernet = 1   // standard NIC
	linkRaw      = 101 // raw IP
	linkLinuxSLL = 113 // "any" device, classic cooked
	linkLinuxSLL2 = 276 // "any" device, cooked v2 (newer libpcap)
)

// decodePacket turns one captured record into a packet. ok=false means "not an
// IP/TCP/UDP/ICMP frame we care about".
func decodePacket(linkType int, data []byte, ts int64) (packet, bool) {
	l3, ok := stripLink(linkType, data)
	if !ok || len(l3) < 1 {
		return packet{}, false
	}
	p := packet{ts: ts, size: len(data)}
	ver := l3[0] >> 4
	var l4proto byte
	var l4 []byte
	switch ver {
	case 4:
		if len(l3) < 20 {
			return packet{}, false
		}
		ihl := int(l3[0]&0x0f) * 4
		if ihl < 20 || len(l3) < ihl {
			return packet{}, false
		}
		total := int(binary.BigEndian.Uint16(l3[2:4]))
		if total < ihl || total > len(l3) {
			total = len(l3)
		}
		l4proto = l3[9]
		p.srcIP = net.IP(l3[12:16]).String()
		p.dstIP = net.IP(l3[16:20]).String()
		l4 = l3[ihl:total]
	case 6:
		if len(l3) < 40 {
			return packet{}, false
		}
		l4proto = l3[6] // next header; extension headers not chased (rare on PLC links)
		p.srcIP = net.IP(l3[8:24]).String()
		p.dstIP = net.IP(l3[24:40]).String()
		l4 = l3[40:]
	default:
		return packet{}, false
	}
	p.l4 = l4proto
	switch l4proto {
	case 6: // TCP
		if len(l4) < 20 {
			return p, true
		}
		p.srcPort = int(binary.BigEndian.Uint16(l4[0:2]))
		p.dstPort = int(binary.BigEndian.Uint16(l4[2:4]))
		p.seq = binary.BigEndian.Uint32(l4[4:8])
		p.ack = binary.BigEndian.Uint32(l4[8:12])
		off := int(l4[12]>>4) * 4
		p.flags = l4[13]
		if off >= 20 && off <= len(l4) {
			p.payload = l4[off:]
		}
	case 17: // UDP
		if len(l4) < 8 {
			return p, true
		}
		p.srcPort = int(binary.BigEndian.Uint16(l4[0:2]))
		p.dstPort = int(binary.BigEndian.Uint16(l4[2:4]))
		ulen := int(binary.BigEndian.Uint16(l4[4:6]))
		if ulen >= 8 && ulen <= len(l4) {
			p.payload = l4[8:ulen]
		} else {
			p.payload = l4[8:]
		}
	case 1, 58: // ICMP / ICMPv6
		p.payload = l4
	}
	return p, true
}

// stripLink returns the L3 (IP) bytes for the given link-layer type.
func stripLink(linkType int, data []byte) ([]byte, bool) {
	switch linkType {
	case linkEthernet:
		if len(data) < 14 {
			return nil, false
		}
		et := binary.BigEndian.Uint16(data[12:14])
		off := 14
		if et == 0x8100 || et == 0x88a8 { // VLAN tag
			if len(data) < 18 {
				return nil, false
			}
			et = binary.BigEndian.Uint16(data[16:18])
			off = 18
		}
		if et != 0x0800 && et != 0x86dd {
			return nil, false
		}
		return data[off:], true
	case linkNull:
		if len(data) < 4 {
			return nil, false
		}
		return data[4:], true
	case linkRaw:
		return data, true
	case linkLinuxSLL:
		if len(data) < 16 {
			return nil, false
		}
		return data[16:], true
	case linkLinuxSLL2:
		if len(data) < 20 {
			return nil, false
		}
		return data[20:], true
	default:
		// Best effort: many edge captures are raw IP.
		return data, true
	}
}

// flowKey canonicalizes a 5-tuple so request and response share one flow,
// ordering endpoints lexicographically.
func (p *packet) flowKey() (key, src, dst, proto string) {
	proto = "ip"
	switch p.l4 {
	case 6:
		proto = "tcp"
	case 17:
		proto = "udp"
	case 1, 58:
		proto = "icmp"
	}
	a := fmt.Sprintf("%s:%d", p.srcIP, p.srcPort)
	b := fmt.Sprintf("%s:%d", p.dstIP, p.dstPort)
	src, dst = a, b
	if a <= b {
		key = a + ">" + b + "/" + proto
	} else {
		key = b + ">" + a + "/" + proto
	}
	return
}

func (p *packet) srcIPPort() string { return fmt.Sprintf("%s:%d", p.srcIP, p.srcPort) }
func (p *packet) dstIPPort() string { return fmt.Sprintf("%s:%d", p.dstIP, p.dstPort) }

// flagStr renders TCP flags compactly for summaries.
func flagStr(f byte) string {
	s := ""
	if f&tcpSYN != 0 {
		s += "S"
	}
	if f&tcpACK != 0 {
		s += "A"
	}
	if f&tcpFIN != 0 {
		s += "F"
	}
	if f&tcpRST != 0 {
		s += "R"
	}
	if f&tcpPSH != 0 {
		s += "P"
	}
	if s == "" {
		s = "-"
	}
	return s
}
