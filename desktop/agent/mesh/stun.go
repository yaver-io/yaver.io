package mesh

// stun.go (Phase 2) — minimal STUN client (RFC 5389) for NAT traversal. To make
// a direct WireGuard path possible between two peers behind NAT, each node needs
// to learn and advertise its PUBLIC endpoint. We query a STUN server for the
// public address; combined with WireGuard's persistent keepalive (25s) and its
// endpoint-roaming (it adopts the real source endpoint of the first packet it
// receives from a peer), this establishes direct connectivity across
// port-preserving cone NATs — the common home-router case.
//
// Symmetric NATs that remap the port per-destination still need the relay-DERP
// fallback (the remaining Phase 2 piece); see meshDeriveDirectEndpoint's caller
// which keeps the relay path as a backstop.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// DefaultStunServers are public STUN servers used to discover the node's public
// IP. Tried in order until one answers.
var DefaultStunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

const stunMagicCookie uint32 = 0x2112A442

// DiscoverPublicIP queries STUN servers and returns the node's public IP as seen
// from the internet. It uses an ephemeral socket (so it never contends with the
// WireGuard listener), which reliably yields the public IP; the public port is
// assumed to be port-preserved for WireGuard's UDP port (true for most home
// NATs, and self-correcting via WireGuard roaming once any packet flows).
func DiscoverPublicIP(timeout time.Duration) (netip.Addr, error) {
	var lastErr error
	for _, server := range DefaultStunServers {
		addr, err := stunQuery(server, timeout)
		if err == nil {
			return addr.Addr(), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no STUN servers configured")
	}
	return netip.Addr{}, lastErr
}

// stunQuery sends one binding request to a STUN server and parses the mapped
// address from the response.
func stunQuery(server string, timeout time.Duration) (netip.AddrPort, error) {
	conn, err := net.Dial("udp", server)
	if err != nil {
		return netip.AddrPort{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req, txID := buildBindingRequest()
	if _, err := conn.Write(req); err != nil {
		return netip.AddrPort{}, err
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return parseBindingResponse(buf[:n], txID)
}

// buildBindingRequest builds a 20-byte STUN binding request with a random
// transaction ID.
func buildBindingRequest() ([]byte, [12]byte) {
	var txID [12]byte
	_, _ = rand.Read(txID[:])
	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(msg[2:4], 0)      // length: no attributes
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	copy(msg[8:20], txID[:])
	return msg, txID
}

// parseBindingResponse extracts the mapped address from a STUN response,
// preferring XOR-MAPPED-ADDRESS (0x0020) and falling back to MAPPED-ADDRESS
// (0x0001). It validates the magic cookie and transaction ID.
func parseBindingResponse(resp []byte, txID [12]byte) (netip.AddrPort, error) {
	if len(resp) < 20 {
		return netip.AddrPort{}, fmt.Errorf("STUN response too short: %d bytes", len(resp))
	}
	if binary.BigEndian.Uint32(resp[4:8]) != stunMagicCookie {
		return netip.AddrPort{}, fmt.Errorf("STUN magic cookie mismatch")
	}
	var gotTx [12]byte
	copy(gotTx[:], resp[8:20])
	if gotTx != txID {
		return netip.AddrPort{}, fmt.Errorf("STUN transaction ID mismatch")
	}

	msgLen := int(binary.BigEndian.Uint16(resp[2:4]))
	if 20+msgLen > len(resp) {
		msgLen = len(resp) - 20
	}
	attrs := resp[20 : 20+msgLen]

	var mapped, xorMapped netip.AddrPort
	var haveMapped, haveXor bool
	for len(attrs) >= 4 {
		atype := binary.BigEndian.Uint16(attrs[0:2])
		alen := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+alen > len(attrs) {
			break
		}
		val := attrs[4 : 4+alen]
		switch atype {
		case 0x0020: // XOR-MAPPED-ADDRESS
			if ap, ok := parseXorMappedAddress(val); ok {
				xorMapped, haveXor = ap, true
			}
		case 0x0001: // MAPPED-ADDRESS
			if ap, ok := parseMappedAddress(val); ok {
				mapped, haveMapped = ap, true
			}
		}
		// Attributes are padded to 4-byte boundaries.
		advance := 4 + alen
		if pad := alen % 4; pad != 0 {
			advance += 4 - pad
		}
		if advance > len(attrs) {
			break
		}
		attrs = attrs[advance:]
	}
	if haveXor {
		return xorMapped, nil
	}
	if haveMapped {
		return mapped, nil
	}
	return netip.AddrPort{}, fmt.Errorf("STUN response had no mapped address")
}

// parseMappedAddress parses a plain MAPPED-ADDRESS attribute value.
func parseMappedAddress(val []byte) (netip.AddrPort, bool) {
	if len(val) < 8 || val[1] != 0x01 { // family 0x01 = IPv4
		return netip.AddrPort{}, false
	}
	port := binary.BigEndian.Uint16(val[2:4])
	ip := netip.AddrFrom4([4]byte{val[4], val[5], val[6], val[7]})
	return netip.AddrPortFrom(ip, port), true
}

// parseXorMappedAddress parses an XOR-MAPPED-ADDRESS value, un-XOR-ing the port
// and IPv4 address with the magic cookie.
func parseXorMappedAddress(val []byte) (netip.AddrPort, bool) {
	if len(val) < 8 || val[1] != 0x01 { // family 0x01 = IPv4
		return netip.AddrPort{}, false
	}
	port := binary.BigEndian.Uint16(val[2:4]) ^ uint16(stunMagicCookie>>16)
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], binary.BigEndian.Uint32(val[4:8])^stunMagicCookie)
	ip := netip.AddrFrom4(raw)
	return netip.AddrPortFrom(ip, port), true
}
