package mesh

// dns.go (Phase 3) — MagicDNS. A tiny authoritative DNS responder bound to the
// node's overlay IP that answers A queries for "<alias>.mesh" with the peer's
// overlay IP. This makes device aliases network-real: `ssh mybox.mesh` resolves
// to the peer's mesh address instead of being a CLI-only convenience.
//
// We hand-roll a single-question A responder rather than pull in a DNS library —
// the query shape we serve is narrow and fully testable via buildDNSResponse.

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
)

// MeshDNSSuffix is the DNS zone MagicDNS is authoritative for.
const MeshDNSSuffix = ".mesh"

// dnsResolver maps a fully-qualified lower-case name (e.g. "mybox.mesh") to an
// overlay IP.
type dnsResolver func(name string) (netip.Addr, bool)

// DNSServer is a UDP DNS responder for the .mesh zone.
type DNSServer struct {
	resolve dnsResolver
	conn    *net.UDPConn
	stopped chan struct{}
	wg      sync.WaitGroup
}

// NewDNSServer builds a server that answers via resolve.
func NewDNSServer(resolve dnsResolver) *DNSServer {
	return &DNSServer{resolve: resolve, stopped: make(chan struct{})}
}

// Start binds UDP :53 on listenIP (the overlay address) and serves until Stop.
func (s *DNSServer) Start(listenIP string) error {
	ip := net.ParseIP(listenIP)
	if ip == nil {
		return fmt.Errorf("mesh dns: invalid listen IP %q", listenIP)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: 53})
	if err != nil {
		return fmt.Errorf("mesh dns: bind %s:53: %w", listenIP, err)
	}
	s.conn = conn
	s.wg.Add(1)
	go s.loop()
	return nil
}

// Stop tears the responder down.
func (s *DNSServer) Stop() {
	select {
	case <-s.stopped:
		return
	default:
		close(s.stopped)
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.wg.Wait()
}

func (s *DNSServer) loop() {
	defer s.wg.Done()
	buf := make([]byte, 1500)
	for {
		select {
		case <-s.stopped:
			return
		default:
		}
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.stopped:
				return
			default:
				continue
			}
		}
		resp := buildDNSResponse(buf[:n], s.resolve)
		if resp != nil {
			_, _ = s.conn.WriteToUDP(resp, addr)
		}
	}
}

// parseDNSQuestion extracts the first question's name (lower-cased, no trailing
// dot), qtype, and the byte offset just past the question.
func parseDNSQuestion(query []byte) (name string, qtype uint16, end int, ok bool) {
	if len(query) < 12 {
		return "", 0, 0, false
	}
	qd := binary.BigEndian.Uint16(query[4:6])
	if qd < 1 {
		return "", 0, 0, false
	}
	var labels []string
	off := 12
	for off < len(query) {
		l := int(query[off])
		off++
		if l == 0 {
			break
		}
		if l > 63 || off+l > len(query) {
			return "", 0, 0, false
		}
		labels = append(labels, strings.ToLower(string(query[off:off+l])))
		off += l
	}
	if off+4 > len(query) {
		return "", 0, 0, false
	}
	qtype = binary.BigEndian.Uint16(query[off : off+2])
	off += 4 // qtype + qclass
	return strings.Join(labels, "."), qtype, off, true
}

// buildDNSResponse answers a single-question A query for the .mesh zone. Returns
// nil for malformed input. Non-.mesh or unresolved names get an authoritative
// NXDOMAIN so the OS resolver falls through to its other nameservers cleanly.
func buildDNSResponse(query []byte, resolve dnsResolver) []byte {
	name, qtype, qend, ok := parseDNSQuestion(query)
	if !ok {
		return nil
	}

	// Header: copy ID, set QR=1, AA=1, RD (copied), RA=0.
	resp := make([]byte, qend)
	copy(resp, query[:qend])
	rd := query[2] & 0x01
	resp[2] = 0x84 | rd // QR=1, AA=1, Opcode=0, RD copied
	resp[3] = 0x00
	binary.BigEndian.PutUint16(resp[4:6], 1) // QDCOUNT
	binary.BigEndian.PutUint16(resp[6:8], 0) // ANCOUNT (set below)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)

	var addr netip.Addr
	var found bool
	if qtype == 1 && strings.HasSuffix(name, MeshDNSSuffix) { // A query in our zone
		addr, found = resolve(name)
	}
	if !found || !addr.Is4() {
		resp[3] = 0x03 // RCODE=3 NXDOMAIN
		return resp
	}

	// One answer: NAME pointer to the question (0xC00C), A/IN, TTL 60, RDATA.
	ans := make([]byte, 0, 16)
	ans = append(ans, 0xC0, 0x0C)
	ans = append(ans, 0x00, 0x01)             // TYPE A
	ans = append(ans, 0x00, 0x01)             // CLASS IN
	ans = append(ans, 0x00, 0x00, 0x00, 0x3C) // TTL 60
	ans = append(ans, 0x00, 0x04)             // RDLENGTH 4
	v4 := addr.As4()
	ans = append(ans, v4[0], v4[1], v4[2], v4[3])

	binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT=1
	return append(resp, ans...)
}
