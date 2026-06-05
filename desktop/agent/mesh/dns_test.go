package mesh

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// buildAQuery builds a minimal single-question A query for name.
func buildAQuery(id uint16, name string) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:2], id)
	msg[2] = 0x01                           // RD
	binary.BigEndian.PutUint16(msg[4:6], 1) // QDCOUNT
	for _, label := range splitLabels(name) {
		msg = append(msg, byte(len(label)))
		msg = append(msg, []byte(label)...)
	}
	msg = append(msg, 0x00)       // root
	msg = append(msg, 0x00, 0x01) // QTYPE A
	msg = append(msg, 0x00, 0x01) // QCLASS IN
	return msg
}

func splitLabels(name string) []string {
	var out []string
	cur := ""
	for _, r := range name {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestBuildDNSResponse_resolvesMesh(t *testing.T) {
	want := netip.AddrFrom4([4]byte{100, 96, 0, 5})
	resolve := func(name string) (netip.Addr, bool) {
		if name == "mybox.mesh" {
			return want, true
		}
		return netip.Addr{}, false
	}
	query := buildAQuery(0x1234, "mybox.mesh")
	resp := buildDNSResponse(query, resolve)
	if resp == nil {
		t.Fatal("nil response")
	}
	// ID echoed, QR set, ANCOUNT=1.
	if binary.BigEndian.Uint16(resp[0:2]) != 0x1234 {
		t.Error("response ID mismatch")
	}
	if resp[2]&0x80 == 0 {
		t.Error("QR bit not set")
	}
	if binary.BigEndian.Uint16(resp[6:8]) != 1 {
		t.Fatalf("expected ANCOUNT=1, got %d", binary.BigEndian.Uint16(resp[6:8]))
	}
	// Last 4 bytes are the A record RDATA.
	got := netip.AddrFrom4([4]byte{resp[len(resp)-4], resp[len(resp)-3], resp[len(resp)-2], resp[len(resp)-1]})
	if got != want {
		t.Fatalf("resolved %v, want %v", got, want)
	}
}

func TestBuildDNSResponse_nxdomainForUnknown(t *testing.T) {
	resolve := func(string) (netip.Addr, bool) { return netip.Addr{}, false }
	resp := buildDNSResponse(buildAQuery(1, "ghost.mesh"), resolve)
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp[3]&0x0f != 0x03 {
		t.Errorf("expected NXDOMAIN rcode 3, got %d", resp[3]&0x0f)
	}
	if binary.BigEndian.Uint16(resp[6:8]) != 0 {
		t.Error("ANCOUNT should be 0 for NXDOMAIN")
	}
}

func TestBuildDNSResponse_ignoresNonMeshZone(t *testing.T) {
	called := false
	resolve := func(string) (netip.Addr, bool) { called = true; return netip.Addr{}, false }
	resp := buildDNSResponse(buildAQuery(1, "example.com"), resolve)
	if resp == nil {
		t.Fatal("nil response")
	}
	if called {
		t.Error("resolver should not be consulted for non-.mesh names")
	}
}

func TestParseDNSQuestion(t *testing.T) {
	name, qtype, _, ok := parseDNSQuestion(buildAQuery(1, "a.b.mesh"))
	if !ok || name != "a.b.mesh" || qtype != 1 {
		t.Fatalf("parse failed: name=%q qtype=%d ok=%v", name, qtype, ok)
	}
}
