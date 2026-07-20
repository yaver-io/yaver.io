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
	lastHello  map[string]time.Time // deviceID -> last HELLO sent
	// noHsSince tracks, per peer public-key (lowercase hex), when we first saw
	// it without a live handshake — used to fall a peer over to the DERP relay
	// after a direct-path grace period (H2). Cleared when the peer goes live or
	// leaves. Accessed only under m.mu (applyPeers runs locked).
	noHsSince map[string]time.Time
}

// SetRelayTransport enables the relay-as-DERP fallback: peers with no directly
// reachable endpoint are bridged over the relay through per-peer loopback shims.
// Call before Start.
func (m *Manager) SetRelayTransport(t RelayTransport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.derp = NewDERPManager(m.listenPort, t)
	if m.lastHello == nil {
		m.lastHello = map[string]time.Time{}
	}
	t.SetReceiver(m.derp.DeliverFrame)
}

const (
	// endpointRoamGrace is how recently a peer must have completed a WireGuard
	// handshake for us to treat its live endpoint as authoritative and stop
	// re-asserting the (possibly stale) control-plane endpoint over it.
	endpointRoamGrace = 180 * time.Second
	// derpFalloverAfter is how long we give a peer's DIRECT endpoint to produce
	// a handshake before falling it over to the DERP relay shim. Direct-first,
	// relay-fallback: this is what makes symmetric-NAT pairs actually connect
	// (H2 — previously DERP only engaged for an empty endpoint, which never
	// happened once STUN/public endpoints were published).
	derpFalloverAfter = 20 * time.Second
)

// applyPeers reconciles the desired peer set onto the device:
//   - H1: endpoints WireGuard has roamed to are preserved (a peer that moved
//     WiFi→cellular must not have its live endpoint clobbered by the reconcile).
//   - H2: a non-live peer's direct endpoint gets derpFalloverAfter to produce a
//     handshake; if it doesn't, the peer falls over to its DERP relay shim so
//     symmetric-NAT pairs still connect.
//   - M1: DERP shims for peers that went live or left are reclaimed, so the
//     per-peer loopback socket + goroutine don't leak on churn.
func (m *Manager) applyPeers(dev *Device, peers []Peer) error {
	if m.noHsSince == nil {
		m.noHsSince = map[string]time.Time{}
	}
	live := m.livePeerKeys(dev)
	now := time.Now()
	seen := make(map[string]bool, len(peers)) // peer keys present this round
	derpKeep := map[string]bool{}             // deviceIds that should keep a shim

	for i := range peers {
		hex, err := keyB64ToHex(peers[i].PublicKey)
		lhex := strings.ToLower(hex)
		if err == nil {
			seen[lhex] = true
		}
		if err == nil && live[lhex] {
			// H1: live → WireGuard owns the endpoint (roaming). Blank the
			// control-plane endpoint so renderPeers omits it, and clear the
			// fallover timer. Do NOT DERP this peer — it is reachable now.
			peers[i].Endpoint = ""
			delete(m.noHsSince, lhex)
			continue
		}
		if m.derp == nil || peers[i].DeviceID == "" {
			continue // no relay available / unknown device — leave endpoint as-is
		}
		// Not live. Start (or read) the direct-path grace timer.
		if err == nil {
			if _, ok := m.noHsSince[lhex]; !ok {
				m.noHsSince[lhex] = now
			}
		}
		stale := err == nil && now.Sub(m.noHsSince[lhex]) >= derpFalloverAfter
		if peers[i].Endpoint == "" || stale {
			// No direct candidate at all, or the direct path never handshook —
			// bridge over the relay.
			if ep, derr := m.derp.EndpointFor(peers[i].DeviceID); derr == nil {
				peers[i].Endpoint = ep
				derpKeep[peers[i].DeviceID] = true
				// Falling back to the relay is exactly when a better path is
				// worth looking for, so this is where signalling starts. HELLO
				// tells the peer what overlays we are on; both sides then run
				// the same negotiation and reach the same verdict.
				//
				// Rate-limited per peer: reconcile runs every 20s and a HELLO
				// per peer per cycle would be chatter, not discovery. Failure
				// is ignored on purpose — the relay bridge above already works,
				// and signalling is an OPTIMISATION that must never be able to
				// break the floor it rides on.
				m.maybeSendHello(peers[i].DeviceID, now)
			}
		}
	}

	// GC fallover timers for peers no longer in the set.
	for k := range m.noHsSince {
		if !seen[k] {
			delete(m.noHsSince, k)
		}
	}
	// M1: reclaim shims for peers that went live or left this round.
	if m.derp != nil {
		m.derp.ReconcilePeers(derpKeep)
	}
	return dev.SetPeers(peers)
}

// livePeerKeys returns the set of peer public keys (lowercase hex) that have a
// handshake within endpointRoamGrace — i.e. currently reachable on their live
// endpoint. Best-effort: on any read error it returns an empty set (all peers
// treated as needing a control-plane endpoint), which is the safe pre-H1
// behavior.
func (m *Manager) livePeerKeys(dev *Device) map[string]bool {
	live := map[string]bool{}
	stats, err := dev.Stats()
	if err != nil {
		return live
	}
	cutoff := time.Now().Add(-endpointRoamGrace).Unix()
	for _, s := range stats {
		if s.LastHandshakeUnix > 0 && s.LastHandshakeUnix >= cutoff {
			live[strings.ToLower(s.PublicKeyHex)] = true
		}
	}
	return live
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

	// Port-level ACLs: compile and apply the inbound matcher. FAIL-CLOSED —
	// if the rules can't be loaded (control-plane outage at boot) we install a
	// deny-all matcher rather than leaving the device unfiltered (nil matcher =
	// pass-through). The reconcile loop retries every interval and swaps in the
	// real matcher on the first successful fetch. A successful fetch of zero
	// rules is still default-allow (NewMatcher(nil)); only a FAILURE denies.
	if m.aclSource != nil {
		if matcher, aerr := m.aclSource(); aerr == nil {
			dev.SetMatcher(matcher)
		} else {
			dev.SetMatcher(DenyAllMatcher())
			m.lastErr = "acl load failed, fail-closed (deny-all until next fetch): " + aerr.Error()
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

	// Pass the current stop channel by value so this goroutine always selects
	// on its OWN channel. If Stop() then Start() reassigns m.stop, an old loop
	// still parked in m.source() won't wake on the new (open) channel and leak
	// — it dies on the close of the channel it captured (M2).
	go m.reconcileLoop(m.stop)
	return nil
}

// reconcileLoop re-pulls peers periodically and re-applies them, so revoked
// shares drop out of the data plane and new peers/endpoints come in.
func (m *Manager) reconcileLoop(stop chan struct{}) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
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

// helloEvery bounds how often we re-introduce ourselves to one peer. Reconcile
// runs every 20s; re-sending a HELLO each cycle would be chatter rather than
// discovery, and the information in it changes on the order of minutes.
const helloEvery = 2 * time.Minute

// maybeSendHello introduces this device to a peer over the relay, at most once
// per helloEvery.
//
// Best-effort by design: the relay bridge is already carrying the peer when
// this runs, so a failure here costs a slower path and never a lost one.
func (m *Manager) maybeSendHello(deviceID string, now time.Time) {
	if m.derp == nil || deviceID == "" {
		return
	}
	if m.lastHello == nil {
		m.lastHello = map[string]time.Time{}
	}
	if last, ok := m.lastHello[deviceID]; ok && now.Sub(last) < helloEvery {
		return
	}
	m.lastHello[deviceID] = now
	// SrcDevice is left empty on purpose: the relay already tells the receiver
	// which device a frame came from, and carrying an unverified second copy
	// would invite trusting the body over the envelope.
	_ = m.derp.SendDisco(deviceID, DiscoMsg{
		Type:         DiscoHello,
		SentAtUnixMs: now.UnixMilli(),
	})
}
