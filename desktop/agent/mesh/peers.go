package mesh

// peers.go (Phase 1) — peer reconciliation. The control plane (Convex
// mesh:listMeshPeers) tells the agent who the peers are; this renders that into
// a WireGuard UAPI config and applies it to the live device. Peer sets are
// applied with replace_peers=true so a removed/revoked peer disappears from the
// data plane on the next reconcile — sharing revocation takes effect without a
// restart.

import (
	"fmt"
	"strconv"
	"strings"
)

// Peer is one WireGuard peer derived from a meshNodes row the caller can see.
type Peer struct {
	// DeviceID identifies the peer for relay-as-DERP routing (empty for peers
	// that are only ever reached directly).
	DeviceID string
	// PublicKey is the peer's base64 WireGuard public key.
	PublicKey string
	// Endpoint is the best host:port UDP candidate (may be empty if the peer
	// is only reachable once it initiates / via a later DERP path).
	Endpoint string
	// AllowedIPs are the CIDRs routed to this peer — at minimum its /32 overlay
	// IP, plus any advertised subnet routes (Phase 5).
	AllowedIPs []string
	// KeepaliveSeconds keeps NAT bindings alive; 25s is the WireGuard norm for
	// peers behind NAT.
	KeepaliveSeconds int
}

// renderPeers builds a replace_peers UAPI block from a peer list.
func renderPeers(peers []Peer) (string, error) {
	var b strings.Builder
	b.WriteString("replace_peers=true\n")
	for _, p := range peers {
		pubHex, err := keyB64ToHex(p.PublicKey)
		if err != nil {
			return "", fmt.Errorf("peer %q: %w", p.PublicKey, err)
		}
		b.WriteString("public_key=" + pubHex + "\n")
		if ep := strings.TrimSpace(p.Endpoint); ep != "" {
			b.WriteString("endpoint=" + ep + "\n")
		}
		if p.KeepaliveSeconds > 0 {
			b.WriteString("persistent_keepalive_interval=" + strconv.Itoa(p.KeepaliveSeconds) + "\n")
		}
		b.WriteString("replace_allowed_ips=true\n")
		for _, cidr := range p.AllowedIPs {
			if c := strings.TrimSpace(cidr); c != "" {
				b.WriteString("allowed_ip=" + c + "\n")
			}
		}
	}
	return b.String(), nil
}

// SetPeers replaces the device's full peer set.
func (d *Device) SetPeers(peers []Peer) error {
	cfg, err := renderPeers(peers)
	if err != nil {
		return err
	}
	if err := d.dev.IpcSet(cfg); err != nil {
		return fmt.Errorf("apply peers: %w", err)
	}
	return nil
}

// PeerStat is a per-peer liveness snapshot parsed from the device's UAPI.
type PeerStat struct {
	PublicKeyHex      string
	Endpoint          string
	LastHandshakeUnix int64
	RxBytes           int64
	TxBytes           int64
}

// Stats reads the device's current per-peer handshake/throughput counters.
func (d *Device) Stats() ([]PeerStat, error) {
	raw, err := d.dev.IpcGet()
	if err != nil {
		return nil, fmt.Errorf("read device state: %w", err)
	}
	return parsePeerStats(raw), nil
}

// parsePeerStats turns a UAPI get response into per-peer stats. The UAPI streams
// flat key=value lines; a `public_key=` line starts a new peer block.
func parsePeerStats(uapi string) []PeerStat {
	var out []PeerStat
	var cur *PeerStat
	for _, line := range strings.Split(uapi, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			out = append(out, PeerStat{PublicKeyHex: v})
			cur = &out[len(out)-1]
		case "endpoint":
			if cur != nil {
				cur.Endpoint = v
			}
		case "last_handshake_time_sec":
			if cur != nil {
				cur.LastHandshakeUnix, _ = strconv.ParseInt(v, 10, 64)
			}
		case "rx_bytes":
			if cur != nil {
				cur.RxBytes, _ = strconv.ParseInt(v, 10, 64)
			}
		case "tx_bytes":
			if cur != nil {
				cur.TxBytes, _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	return out
}
