package netcapture

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// DNS decoder (UDP/TCP 53). Failures (NXDOMAIN/SERVFAIL) are the first thing to
// break an ERP/cloud link — caught before any TCP is attempted.

func init() {
	registerProto(&protoDecoder{name: "dns", ports: []int{53}, fn: decodeDNS})
}

var dnsRcode = map[byte]string{0: "noerror", 1: "formerr", 2: "servfail", 3: "nxdomain", 5: "refused"}

func decodeDNS(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	// DNS over TCP carries a 2-byte length prefix.
	if p.l4 == 6 {
		if len(b) < 2 {
			return nil
		}
		b = b[2:]
	}
	if len(b) < 12 {
		return nil
	}
	id := binary.BigEndian.Uint16(b[0:2])
	flags := binary.BigEndian.Uint16(b[2:4])
	qr := flags&0x8000 != 0
	rcode := byte(flags & 0x000f)
	qd := binary.BigEndian.Uint16(b[4:6])
	d := a.dnsStats()
	name := ""
	if qd > 0 {
		name = parseDNSName(b, 12)
	}
	key := fmt.Sprintf("dns|%s|%d", f.Key, id)

	if !qr { // query
		d.Queries++
		if name != "" {
			d.ByName[name]++
		}
		a.markReq(key, p.ts)
		return []Event{{
			TS: p.ts, Proto: "dns", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
			Summary: "DNS query " + name,
			Detail:  map[string]interface{}{"name": name, "id": id},
		}}
	}
	// response
	d.Responses++
	lat, _ := a.takeReq(key, p.ts)
	rc := dnsRcode[rcode]
	if rc == "" {
		rc = fmt.Sprintf("rcode%d", rcode)
	}
	sev := "info"
	switch rcode {
	case 3:
		d.NXDomain++
		sev = "warn"
	case 2:
		d.ServFail++
		sev = "warn"
	}
	return []Event{{
		TS: p.ts, Proto: "dns", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: sev,
		Summary: fmt.Sprintf("DNS %s %s (%.0fms)", rc, name, lat),
		Detail:  map[string]interface{}{"name": name, "rcode": rc, "latencyMs": lat},
	}}
}

// parseDNSName reads a (non-compressed-at-start) QNAME; good enough for the
// first question, which never uses compression.
func parseDNSName(b []byte, off int) string {
	var labels []string
	for off < len(b) {
		l := int(b[off])
		if l == 0 {
			break
		}
		if l&0xc0 != 0 { // compression pointer — stop
			break
		}
		off++
		if off+l > len(b) {
			break
		}
		labels = append(labels, string(b[off:off+l]))
		off += l
		if len(labels) > 32 {
			break
		}
	}
	return strings.Join(labels, ".")
}
