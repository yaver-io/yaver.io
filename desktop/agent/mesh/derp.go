package mesh

// derp.go (Phase 2 completion) — relay-as-DERP fallback for peers that can't
// establish a direct UDP path (symmetric NAT). Rather than reimplement
// wireguard-go's conn.Bind, we bridge through LOOPBACK UDP:
//
//   - For a relay-only peer P we open a UDP socket on 127.0.0.1:<portP> and
//     configure P's WireGuard endpoint to that loopback address.
//   - WireGuard then sends P's (already-encrypted) packets to 127.0.0.1:<portP>;
//     our reader forwards them over the RelayTransport to P's agent.
//   - Frames arriving from P over the relay are written from the SAME loopback
//     socket back to WireGuard's listen port, so WireGuard sees them as coming
//     from 127.0.0.1:<portP> — i.e. from P — and (via endpoint roaming) keeps
//     replying through the shim.
//
// This needs no privileged calls and no Bind surgery, and the bridge logic is
// unit-testable end to end over loopback with a fake transport.

import (
	"fmt"
	"net"
	"sync"
)

// RelayTransport carries opaque WireGuard frames to a peer agent by deviceId and
// delivers inbound frames back. The agent plugs in a QUIC-relay-backed
// implementation; tests use an in-memory one.
type RelayTransport interface {
	// SendFrame forwards an encrypted WG packet to dstDeviceID via the relay.
	SendFrame(dstDeviceID string, payload []byte) error
	// SetReceiver registers the callback invoked for each inbound frame
	// (srcDeviceID, payload). The DERPManager wires this to DeliverFrame.
	SetReceiver(func(srcDeviceID string, payload []byte))
}

// DERPManager bridges relay-only WireGuard peers over a RelayTransport using
// per-peer loopback UDP sockets.
type DERPManager struct {
	wgListenPort int
	transport    RelayTransport

	mu     sync.Mutex
	peers  map[string]*derpPeer // deviceId -> shim
	closed bool

	// onDisco receives signalling frames (see disco.go). Separate from the
	// WireGuard path on purpose: disco is how peers find each other, so it
	// MUST work before any peer shim exists.
	onDisco func(srcDeviceID string, msg *DiscoMsg)
}

type derpPeer struct {
	deviceID string
	conn     *net.UDPConn
	loopback *net.UDPAddr // 127.0.0.1:<portP> — used as the WG endpoint
	wgAddr   *net.UDPAddr // 127.0.0.1:<wgListenPort> — inject target
	stop     chan struct{}
}

// NewDERPManager builds a manager that injects inbound frames into the local
// WireGuard device listening on wgListenPort.
func NewDERPManager(wgListenPort int, transport RelayTransport) *DERPManager {
	return &DERPManager{
		wgListenPort: wgListenPort,
		transport:    transport,
		peers:        map[string]*derpPeer{},
	}
}

// EndpointFor ensures a loopback shim exists for peerDeviceID and returns the
// "host:port" to configure as that peer's WireGuard endpoint. Idempotent.
func (m *DERPManager) EndpointFor(peerDeviceID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("derp manager closed")
	}
	if p, ok := m.peers[peerDeviceID]; ok {
		return p.loopback.String(), nil
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return "", fmt.Errorf("derp: open loopback socket: %w", err)
	}
	p := &derpPeer{
		deviceID: peerDeviceID,
		conn:     conn,
		loopback: conn.LocalAddr().(*net.UDPAddr),
		wgAddr:   &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: m.wgListenPort},
		stop:     make(chan struct{}),
	}
	m.peers[peerDeviceID] = p
	go m.pumpOutbound(p)
	return p.loopback.String(), nil
}

// pumpOutbound reads WireGuard's packets for this peer off the loopback socket
// and forwards them over the relay.
func (m *DERPManager) pumpOutbound(p *derpPeer) {
	buf := make([]byte, 1500)
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		n, _, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-p.stop:
				return
			default:
				continue
			}
		}
		if m.transport != nil {
			pkt := make([]byte, n)
			copy(pkt, buf[:n])
			_ = m.transport.SendFrame(p.deviceID, pkt)
		}
	}
}

// DeliverFrame injects a relay-received frame from srcDeviceID into the local
// WireGuard device by writing it from that peer's loopback socket. Called by the
// transport's receive loop. A frame for an unknown peer is dropped (no shim yet).
func (m *DERPManager) DeliverFrame(srcDeviceID string, payload []byte) {
	// Disco is checked FIRST, before the peer lookup, and the order is
	// load-bearing. The lookup below drops frames from a device with no shim
	// yet — but signalling is precisely what arrives BEFORE a shim exists: it
	// is how the two sides discover each other and decide to build one.
	// Checking disco after the lookup would silently drop every first contact
	// and leave the protocol working only between peers that were already
	// talking, which is the one case it is not needed for.
	if IsDisco(payload) {
		m.mu.Lock()
		h := m.onDisco
		m.mu.Unlock()
		if h == nil {
			return
		}
		msg, err := DecodeDisco(payload)
		if err != nil || msg == nil {
			// Unparseable, or a version from the future: ignore. Never feed a
			// disco frame to WireGuard as a fallback — that is exactly the
			// confusion the magic exists to prevent.
			return
		}
		h(srcDeviceID, msg)
		return
	}

	m.mu.Lock()
	p, ok := m.peers[srcDeviceID]
	m.mu.Unlock()
	if !ok {
		return
	}
	_, _ = p.conn.WriteToUDP(payload, p.wgAddr)
}

// SetDiscoHandler registers the callback for inbound signalling frames.
func (m *DERPManager) SetDiscoHandler(fn func(srcDeviceID string, msg *DiscoMsg)) {
	m.mu.Lock()
	m.onDisco = fn
	m.mu.Unlock()
}

// SendDisco sends a signalling message to a peer over the relay.
//
// Needs no peer shim and no WireGuard state: it rides the same addressed relay
// stream the data path uses, which is why signalling survives exactly as long
// as the relay session — the floor under every topology.
func (m *DERPManager) SendDisco(dstDeviceID string, msg DiscoMsg) error {
	m.mu.Lock()
	t := m.transport
	m.mu.Unlock()
	if t == nil {
		return fmt.Errorf("no relay transport: signalling needs the relay session")
	}
	payload, err := EncodeDisco(msg)
	if err != nil {
		return err
	}
	return t.SendFrame(dstDeviceID, payload)
}

// RemovePeer tears down a peer's shim (e.g. it became directly reachable or was
// revoked).
func (m *DERPManager) RemovePeer(peerDeviceID string) {
	m.mu.Lock()
	p, ok := m.peers[peerDeviceID]
	if ok {
		delete(m.peers, peerDeviceID)
	}
	m.mu.Unlock()
	if ok {
		close(p.stop)
		_ = p.conn.Close()
	}
}

// ReconcilePeers tears down shims for any device not in keep — a peer that
// became directly reachable or left the peer set. Without this the per-peer
// loopback socket + pumpOutbound goroutine leak on every churn (M1).
func (m *DERPManager) ReconcilePeers(keep map[string]bool) {
	m.mu.Lock()
	var drop []string
	for id := range m.peers {
		if !keep[id] {
			drop = append(drop, id)
		}
	}
	m.mu.Unlock()
	for _, id := range drop {
		m.RemovePeer(id)
	}
}

// Close tears down all shims.
func (m *DERPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, p := range m.peers {
		close(p.stop)
		_ = p.conn.Close()
		delete(m.peers, id)
	}
}
