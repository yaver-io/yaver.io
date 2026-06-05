package mesh

// manager.go (Phase 1) — the long-lived owner of the mesh data plane. It lives
// inside the `yaver serve` daemon (NOT a one-shot CLI invocation) so the TUN
// stays up. Start() brings up the device, configures the interface, and applies
// the initial peer set; a background loop re-applies peers on an interval so
// added/revoked shares converge without a restart.
//
// The Manager is deliberately decoupled from Convex: it pulls peers through a
// PeerSource callback, so the agent wires in the real listMeshPeers query while
// tests can inject a static source.

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// PeerSource returns this node's assigned overlay IP and the current peer set.
type PeerSource func() (selfIPv4 string, peers []Peer, err error)

// NameSource returns the alias -> overlay-IP map for MagicDNS (keys are bare
// aliases, e.g. "mybox"; the DNS server appends the .mesh suffix).
type NameSource func() (map[string]netip.Addr, error)

// Manager owns the WireGuard device + reconcile loop.
type Manager struct {
	privKeyB64 string
	source     PeerSource
	listenPort int
	mtu        int
	interval   time.Duration

	mu         sync.Mutex
	dev        *Device
	selfIP     string
	stop       chan struct{}
	running    bool
	lastErr    string
	nameSource NameSource
	names      map[string]netip.Addr
	dns        *DNSServer
	aclSource  ACLSource
	forwarding bool
	derp       *DERPManager
}

// SetRelayTransport enables the relay-as-DERP fallback: peers with no directly
// reachable endpoint are bridged over the relay through per-peer loopback shims.
// Call before Start.
func (m *Manager) SetRelayTransport(t RelayTransport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.derp = NewDERPManager(m.listenPort, t)
	t.SetReceiver(m.derp.DeliverFrame)
}

// applyPeers substitutes a DERP shim endpoint for any peer that has no direct
// endpoint (symmetric-NAT fallback), then applies the peer set to the device.
func (m *Manager) applyPeers(dev *Device, peers []Peer) error {
	if m.derp != nil {
		for i := range peers {
			if peers[i].Endpoint == "" && peers[i].DeviceID != "" {
				if ep, err := m.derp.EndpointFor(peers[i].DeviceID); err == nil {
					peers[i].Endpoint = ep
				}
			}
		}
	}
	return dev.SetPeers(peers)
}

// SetForwarding marks this node as a subnet router / exit node so Start enables
// IP forwarding + NAT for mesh-sourced traffic. Call before Start.
func (m *Manager) SetForwarding(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forwarding = enabled
}

// ACLSource returns the compiled inbound ACL matcher (nil = default-allow).
type ACLSource func() (*Matcher, error)

// SetACLSource enables port-level ACL enforcement. The manager loads the matcher
// at Start and refreshes it each reconcile. Call before Start.
func (m *Manager) SetACLSource(src ACLSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aclSource = src
}

// SetNameSource enables MagicDNS: the manager starts a .mesh DNS responder on
// the overlay IP and refreshes the alias map each reconcile. Call before Start.
func (m *Manager) SetNameSource(src NameSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nameSource = src
}

// resolveName backs the DNS server: looks up "<alias>.mesh" in the cached map.
func (m *Manager) resolveName(fqdn string) (netip.Addr, bool) {
	host := strings.TrimSuffix(fqdn, MeshDNSSuffix)
	m.mu.Lock()
	defer m.mu.Unlock()
	addr, ok := m.names[host]
	return addr, ok
}

// NewManager builds a manager. privKeyB64 is the device's WireGuard private key
// (base64); source yields the overlay IP + peers (typically backed by Convex).
func NewManager(privKeyB64 string, source PeerSource) *Manager {
	return &Manager{
		privKeyB64: privKeyB64,
		source:     source,
		listenPort: DefaultListenPort,
		mtu:        DefaultMTU,
		interval:   20 * time.Second,
	}
}

// Start brings up the data plane. Idempotent: a second call while running is a
// no-op. Returns a descriptive error (often "elevated privilege required") if
// the TUN or interface configuration fails.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	selfIP, peers, err := m.source()
	if err != nil {
		return fmt.Errorf("fetch peers: %w", err)
	}
	if selfIP == "" {
		return fmt.Errorf("no overlay IP assigned yet — run `yaver mesh up` first")
	}
	dev, err := NewDevice(defaultTUNName(), m.privKeyB64, m.listenPort, m.mtu)
	if err != nil {
		return err
	}
	if err := dev.ConfigureNetwork(selfIP, MeshSubnetCIDR); err != nil {
		dev.Close()
		return fmt.Errorf("configure %s: %w", dev.Name(), err)
	}
	if err := m.applyPeers(dev, peers); err != nil {
		dev.Close()
		return fmt.Errorf("apply initial peers: %w", err)
	}
	m.dev = dev
	m.selfIP = selfIP
	m.stop = make(chan struct{})
	m.running = true

	// Exit node / subnet router: enable forwarding + NAT for mesh traffic.
	if m.forwarding {
		if ferr := enableForwarding(dev.Name(), MeshSubnetCIDR); ferr != nil {
			m.lastErr = "forwarding: " + ferr.Error()
		}
	}

	// Port-level ACLs: compile and apply the inbound matcher (default-allow
	// until the user authors rules).
	if m.aclSource != nil {
		if matcher, aerr := m.aclSource(); aerr == nil {
			dev.SetMatcher(matcher)
		}
	}

	// MagicDNS: prime the alias map and start the .mesh responder on the overlay
	// IP. Best-effort — a DNS bind failure must not take the data plane down.
	if m.nameSource != nil {
		if names, err := m.nameSource(); err == nil {
			m.names = names
		}
		m.dns = NewDNSServer(m.resolveName)
		if derr := m.dns.Start(selfIP); derr != nil {
			m.lastErr = "magicdns: " + derr.Error()
			m.dns = nil
		} else {
			_ = registerMeshDNS(selfIP)
		}
	}

	go m.reconcileLoop()
	return nil
}

// reconcileLoop re-pulls peers periodically and re-applies them, so revoked
// shares drop out of the data plane and new peers/endpoints come in.
func (m *Manager) reconcileLoop() {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			_, peers, err := m.source()
			m.mu.Lock()
			switch {
			case err != nil:
				m.lastErr = err.Error()
			case m.dev != nil:
				if serr := m.applyPeers(m.dev, peers); serr != nil {
					m.lastErr = serr.Error()
				} else {
					m.lastErr = ""
				}
			}
			src := m.nameSource
			aclSrc := m.aclSource
			dev := m.dev
			m.mu.Unlock()
			// Refresh the MagicDNS alias map (peers may have joined/left).
			if src != nil {
				if names, nerr := src(); nerr == nil {
					m.mu.Lock()
					m.names = names
					m.mu.Unlock()
				}
			}
			// Refresh ACLs so rule edits / revocations take effect live.
			if aclSrc != nil && dev != nil {
				if matcher, aerr := aclSrc(); aerr == nil {
					dev.SetMatcher(matcher)
				}
			}
		}
	}
}

// Stop tears the data plane down. Idempotent.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil
	}
	close(m.stop)
	m.running = false
	if m.dns != nil {
		m.dns.Stop()
		m.dns = nil
		_ = unregisterMeshDNS()
	}
	if m.derp != nil {
		m.derp.Close()
		m.derp = nil
	}
	var err error
	if m.dev != nil {
		if m.forwarding {
			_ = disableForwarding(m.dev.Name(), MeshSubnetCIDR)
		}
		err = m.dev.Close()
		m.dev = nil
	}
	return err
}

// Running reports whether the data plane is currently up.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Status is a snapshot of the data plane for /mesh/status.
type Status struct {
	Running   bool       `json:"running"`
	IfaceName string     `json:"ifaceName,omitempty"`
	SelfIP    string     `json:"selfIp,omitempty"`
	Peers     []PeerStat `json:"peers,omitempty"`
	LastErr   string     `json:"lastError,omitempty"`
}

// Status returns the current data-plane snapshot, including live per-peer
// handshake counters read from the device.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Running: m.running, SelfIP: m.selfIP, LastErr: m.lastErr}
	if m.dev != nil {
		st.IfaceName = m.dev.Name()
		if stats, err := m.dev.Stats(); err == nil {
			st.Peers = stats
		}
	}
	return st
}
