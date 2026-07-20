package mesh

// disco.go — peer-to-peer signalling carried inside the existing relay frames.
//
// THE GAP THIS FILLS. relay/mesh.go is an opaque addressed pump: a frame from A
// tagged dst=B is written to B's stream and the relay "never inspects" the
// payload. It moves WireGuard bytes and nothing else. So two Yaver peers can
// always TALK through the relay and can never TELL EACH OTHER WHERE THEY ARE —
// there is no channel on which a direct path could be negotiated, which is why
// the only direct paths that work today are ones guessed from a heartbeat
// (see docs/architecture/YAVER_MESH_INTEROP_AUDIT.md §10).
//
// Tailscale carries the same thing over DERP and calls it disco: ping/pong with
// a TxID, and CallMeMaybe (peer-reported endpoints).
//
// WHY THIS NEEDS NO WIRE CHANGE. The relay routes on the frame's dst and treats
// the payload as bytes. A WireGuard packet's first byte is its message type and
// is always 1..4 (handshake init/response, cookie reply, transport data). So a
// payload beginning with a magic that cannot be 1..4 is unambiguously not
// WireGuard, and every existing component keeps working:
//
//   - the relay needs NO change and no new version,
//   - an OLD agent that receives a disco frame hands it to wireguard-go, which
//     drops an unparseable packet — the same thing it already does with a
//     corrupt frame. No crash, no misrouting, no downgrade.
//
// That makes a mixed-version fleet safe by construction rather than by rollout
// discipline, which matters because agents update on a jittered 6-12h cadence.
//
// PRIVACY. Candidate endpoints are LAN IPs, which the Convex contract forbids
// (convex_privacy_test.go). They travel here — peer-to-peer, over the user's
// own relay session, never stored — which is precisely why signalling belongs
// on this channel and not in a heartbeat field.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"
)

// DiscoMagic prefixes every signalling payload.
//
// 0x59 ('Y') is the load-bearing byte: WireGuard's first byte is a message type
// in 1..4, so this can never be mistaken for a WG packet in either direction.
// Do not change it to something in 1..4 for any reason.
var DiscoMagic = [2]byte{0x59, 0x44} // "YD"

// DiscoVersion is bumped only for an INCOMPATIBLE change. Receivers ignore
// frames with a version they do not know, so a future version is a no-op on an
// old agent rather than an error.
const DiscoVersion byte = 1

// Disco message types.
const (
	DiscoPing      byte = 1 // are you there — echoed back with the same TxID
	DiscoPong      byte = 2 // I am here, and here is where I saw you from
	DiscoEndpoints byte = 3 // here are my candidate addresses ("CallMeMaybe")
)

// DiscoMsg is one signalling message. JSON body: this is a control channel at
// human timescales (a handful of messages per connect), not a data path, so
// wire compactness matters far less than being able to add a field without a
// version bump.
type DiscoMsg struct {
	Type byte `json:"-"`

	// TxID matches a Pong to its Ping so RTT is measurable and a stale reply
	// cannot be credited to a newer probe.
	TxID string `json:"txid,omitempty"`

	// SrcDevice is who sent this. The relay already tells the receiver the src
	// deviceId; carrying it again lets a receiver detect a relay that is
	// misrouting rather than trusting the envelope blindly.
	SrcDevice string `json:"src,omitempty"`

	// Endpoints are candidate "ip:port" addresses the sender believes it can be
	// reached at: LAN addresses, a STUN-derived reflexive address, a port-mapped
	// address. Ordered by the sender's own preference; the receiver races them
	// and forms its own opinion from RTT.
	Endpoints []string `json:"endpoints,omitempty"`

	// ObservedSrc is where the RESPONDER saw the pinger arrive from. This is how
	// a peer learns its own reflexive address without a STUN server — the same
	// trick STUN performs, for free, on a channel we already have.
	ObservedSrc string `json:"observed,omitempty"`

	// SentAtUnixMs is the sender's clock. Never used for RTT (clocks disagree);
	// only to discard obviously ancient frames.
	SentAtUnixMs int64 `json:"t,omitempty"`
}

// IsDisco reports whether a relay payload is signalling rather than WireGuard.
// Cheap enough to call on every inbound frame on the data path.
func IsDisco(payload []byte) bool {
	return len(payload) >= 4 && payload[0] == DiscoMagic[0] && payload[1] == DiscoMagic[1]
}

// EncodeDisco produces a payload for EncodeDERPFrame.
// Layout: magic[2] | version[1] | type[1] | json body.
func EncodeDisco(m DiscoMsg) ([]byte, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal disco: %w", err)
	}
	out := make([]byte, 0, 4+len(body))
	out = append(out, DiscoMagic[0], DiscoMagic[1], DiscoVersion, m.Type)
	return append(out, body...), nil
}

// DecodeDisco parses a payload IsDisco() accepted.
//
// Returns (nil, nil) — not an error — for a version this build does not know.
// An unknown version is a peer from the future, not a fault, and must not be
// logged as one or a fleet mid-upgrade fills its logs with noise.
func DecodeDisco(payload []byte) (*DiscoMsg, error) {
	if !IsDisco(payload) {
		return nil, fmt.Errorf("not a disco payload")
	}
	if payload[2] != DiscoVersion {
		return nil, nil
	}
	var m DiscoMsg
	if err := json.Unmarshal(payload[4:], &m); err != nil {
		return nil, fmt.Errorf("unmarshal disco: %w", err)
	}
	m.Type = payload[3]
	return &m, nil
}

// NewTxID returns a probe id. Not cryptographic: it only has to be unique
// across the handful of probes in flight for one peer at one moment.
func NewTxID(seed uint64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], seed)
	return fmt.Sprintf("%x", b)
}

// ── HELLO: who is on what, and therefore how we talk ────────────────────────
//
// Before candidates are worth exchanging, the two peers have to agree on WHICH
// overlay they share. That is a four-way question and the answer differs per
// PAIR, not per host:
//
//   both on the same tailnet      -> use it; Yaver Mesh installs nothing
//   only one on a tailnet         -> the tailnet cannot carry this pair -> relay,
//                                    then upgrade to mesh/direct if we can
//   neither, both run Yaver Mesh  -> Yaver Mesh
//   both on the same third-party  -> use it, same rule as tailnet
//
// Both sides run the SAME function over the SAME two HELLOs, so they always
// reach the same verdict without a round of agreement. A negotiation that can
// disagree with itself is a negotiation that will.

const DiscoHello byte = 4

// OverlayKind names a membership a peer can hold.
type OverlayKind string

const (
	OverlayTailscale OverlayKind = "tailscale"
	OverlayYaverMesh OverlayKind = "yaver-mesh"
	OverlayOtherVPN  OverlayKind = "other-vpn"
)

// OverlayMembership is one overlay this peer is on.
//
// NetworkID is what makes "both on Tailscale" meaningful: two devices on
// DIFFERENT tailnets share a technology and no route, and treating that as a
// match is a blackhole. It is a HASH of the tailnet/VPN identity, never the
// name — the peer only needs to compare it, and a hash cannot leak an employer
// or a household from a captured frame.
type OverlayMembership struct {
	Kind      OverlayKind `json:"kind"`
	NetworkID string      `json:"nid,omitempty"`
	// Addr is this peer's address ON that overlay. Peer-to-peer only; this is
	// exactly the field that may never reach Convex.
	Addr string `json:"addr,omitempty"`
	// Reachable is the sender's own read of whether that overlay is usable for
	// it right now (e.g. tailscaled Running). Advisory: the receiver still
	// validates a path before trusting it.
	Reachable bool `json:"up,omitempty"`
}

// HelloBody is carried as the JSON body of a DiscoHello message.
type HelloBody struct {
	DeviceID   string              `json:"device"`
	Overlays   []OverlayMembership `json:"overlays,omitempty"`
	MeshPubKey string              `json:"meshpk,omitempty"`
	// NATClass is coarse on purpose: "easy" (endpoint-independent mapping) or
	// "hard" (endpoint-dependent). RFC 8489 dropped RFC 3489's finer NAT-type
	// discovery because classification by probing is unreliable.
	NATClass string `json:"nat,omitempty"`
}

// TransportChoice is the negotiated answer for one pair.
type TransportChoice struct {
	Via    OverlayKind // empty = no shared overlay; use the relay
	Addr   string      // peer address on the chosen overlay
	Reason string      // always populated — a silent choice cannot be debugged
}

// UseRelay reports whether this pair has no shared overlay and must ride the
// relay. Note this is never a failure: the relay is the floor under every
// topology, and 10-25% of pairs live there permanently.
func (c TransportChoice) UseRelay() bool { return c.Via == "" }

// NegotiateTransport picks the path for THIS pair from both HELLOs.
//
// Deterministic and symmetric: same inputs, same answer on both ends.
//
// Order is deliberate. An incumbent third-party overlay wins over Yaver Mesh
// whenever both peers are on the same one, because Yaver Mesh is a fallback
// overlay and not a competitor — fighting an incumbent for a pair it already
// carries is strictly worse than deferring, and it is how you break someone
// else's VPN. Yaver Mesh is used exactly where nothing else reaches.
func NegotiateTransport(self, peer HelloBody) TransportChoice {
	shared := func(kind OverlayKind) (selfM, peerM *OverlayMembership) {
		for i := range self.Overlays {
			if self.Overlays[i].Kind != kind {
				continue
			}
			for j := range peer.Overlays {
				if peer.Overlays[j].Kind != kind {
					continue
				}
				// Same technology is not enough — it must be the same NETWORK.
				if self.Overlays[i].NetworkID != "" &&
					self.Overlays[i].NetworkID != peer.Overlays[j].NetworkID {
					continue
				}
				return &self.Overlays[i], &peer.Overlays[j]
			}
		}
		return nil, nil
	}

	for _, kind := range []OverlayKind{OverlayTailscale, OverlayOtherVPN, OverlayYaverMesh} {
		s, p := shared(kind)
		if s == nil || p == nil {
			continue
		}
		if !s.Reachable || !p.Reachable {
			// Both are members but at least one says it is not usable right
			// now. Do NOT pick it — an unusable shared overlay is how a pair
			// ends up routed into a blackhole instead of onto the relay.
			continue
		}
		if p.Addr == "" {
			continue
		}
		return TransportChoice{
			Via:    kind,
			Addr:   p.Addr,
			Reason: fmt.Sprintf("both peers are on the same %s and report it up", kind),
		}
	}

	switch {
	case len(self.Overlays) == 0 && len(peer.Overlays) == 0:
		return TransportChoice{Reason: "neither peer is on any overlay — relay, then try to upgrade"}
	default:
		return TransportChoice{Reason: "no overlay is shared by BOTH peers — relay carries this pair"}
	}
}

// ── Fallback ladder, timeouts, and published intent ─────────────────────────
//
// Every path can fail, and the failure of a path must never be the failure of
// the CONNECTION. The ladder is therefore relay-first: a pair is usable at
// t≈1 RTT and only ever gets faster.
//
//   RELAY (floor, always attempted first, never abandoned)
//     └─ upgrade → shared overlay (tailnet / other VPN), if HELLO agreed one
//          └─ upgrade → direct (mesh /32 or LAN), if candidates validate
//
// Downgrade is immediate and silent; upgrade is deliberate and damped. That
// asymmetry is the whole design: losing a fast path costs latency, losing the
// floor costs the session.

// Timeouts. Named, not sprinkled as literals, because every one of them is a
// promise to the user about how long they may stare at a spinner.
const (
	// HelloTimeout bounds the who-is-on-what exchange. The relay session is
	// already up when this runs, so this is one round trip through a server we
	// are connected to — generous at 5s.
	HelloTimeout = 5 * time.Second

	// CandidateProbeTimeout bounds a single direct-path probe. Deliberately
	// short: a candidate that has not answered in 3s is not the fast path we
	// are upgrading FOR, and the relay is already carrying traffic meanwhile.
	CandidateProbeTimeout = 3 * time.Second

	// UpgradeWindow is how long we keep trying to leave the relay for a better
	// path before settling. Past this the pair simply stays on the relay —
	// which is a supported steady state (10-25% of pairs live there), not a
	// failure to report.
	UpgradeWindow = 30 * time.Second

	// PathTrustWindow damps flapping. A challenger path must beat the incumbent
	// by PathBetterMargin for this long before it displaces it. Without it a
	// racer oscillates, and oscillation is worse than a stable slower path.
	PathTrustWindow  = 20 * time.Second
	PathBetterMargin = 15 * time.Millisecond

	// PathDeadAfter is how long without a response before a path is considered
	// gone and we fall back. Shorter than the relay's own keepalive so we
	// retreat to the floor before the user notices.
	PathDeadAfter = 8 * time.Second

	// IntentStaleAfter is how long a published intent stays meaningful. A peer
	// reading an older one must treat it as unknown rather than acting on it.
	IntentStaleAfter = 5 * time.Minute
)

// PathTier ranks the ladder. Higher is better; ties are broken on RTT.
type PathTier int

const (
	TierNone    PathTier = iota
	TierRelay            // the floor
	TierOverlay          // shared tailnet / other VPN
	TierDirect           // mesh /32 or LAN
)

func (t PathTier) String() string {
	switch t {
	case TierRelay:
		return "relay"
	case TierOverlay:
		return "overlay"
	case TierDirect:
		return "direct"
	}
	return "none"
}

// ConnIntent is what a device PUBLISHES about itself so other devices can act
// without probing it.
//
// This is the "connection dropped, tell everyone what I want" channel. A peer
// that reads `IntentWantsPeers` on a box that just lost its path knows to
// answer a hole-punch rather than assume the box is gone; a peer that reads
// `IntentGoingDown` stops retrying immediately instead of running its backoff
// to exhaustion against a machine that is deliberately leaving.
type ConnIntent string

const (
	IntentOnline     ConnIntent = "online"      // reachable, no help needed
	IntentWantsPeers ConnIntent = "wants-peers" // lost a path, actively seeking — please respond to probes
	IntentRelayOnly  ConnIntent = "relay-only"  // direct is impossible here; do not waste probes
	IntentGoingDown  ConnIntent = "going-down"  // deliberate shutdown — stop retrying, this is not a fault
	IntentDegraded   ConnIntent = "degraded"    // reachable but on the floor; upgrades welcome
)

// DeviceConnStatus is the per-device status published to Convex.
//
// BILL DISCIPLINE — this is the whole reason the struct is this small.
// Convex charges per function call, so the rules are:
//  1. Ride the EXISTING heartbeat. No new table, no new mutation, no poller.
//  2. Publish ONLY on change (see Changed) — a stable device costs nothing
//     beyond the beat it was already sending.
//  3. Scalars only. No endpoint lists, no candidate sets, no paths: those are
//     volatile AND forbidden by the privacy contract. Exact addresses travel
//     peer-to-peer over the relay; Convex holds only the SHAPE.
//
// A device that is up and unchanged must generate ZERO additional writes.
type DeviceConnStatus struct {
	Intent    ConnIntent `json:"intent"`
	Tier      string     `json:"tier"` // best path tier right now
	OnTailnet bool       `json:"onTailnet,omitempty"`
	MeshOK    bool       `json:"meshOk,omitempty"` // mesh data plane usable (not deferring, not conflicted)
	NATClass  string     `json:"nat,omitempty"`    // "easy" | "hard" | ""
	AtUnixMs  int64      `json:"at,omitempty"`
}

// Changed reports whether this status differs from the last published one in a
// way worth a write. AtUnixMs is deliberately excluded: a timestamp always
// differs, and comparing it would make every beat a write — which is exactly
// the bill this design exists to avoid.
func (s DeviceConnStatus) Changed(prev *DeviceConnStatus) bool {
	if prev == nil {
		return true
	}
	return s.Intent != prev.Intent ||
		s.Tier != prev.Tier ||
		s.OnTailnet != prev.OnTailnet ||
		s.MeshOK != prev.MeshOK ||
		s.NATClass != prev.NATClass
}

// Stale reports whether a status read from Convex is too old to act on.
func (s DeviceConnStatus) Stale(nowUnixMs int64) bool {
	if s.AtUnixMs == 0 {
		return true
	}
	return nowUnixMs-s.AtUnixMs > IntentStaleAfter.Milliseconds()
}

// ShouldAnswerProbes reports whether a peer reading this status should spend
// effort helping that device reconnect. This is the payoff of publishing
// intent: the responder decides from a fact rather than from a guess.
func (s DeviceConnStatus) ShouldAnswerProbes(nowUnixMs int64) bool {
	if s.Stale(nowUnixMs) {
		return false
	}
	switch s.Intent {
	case IntentWantsPeers, IntentDegraded:
		return true
	case IntentGoingDown, IntentRelayOnly:
		// Deliberately leaving, or known to be relay-only: probing is waste in
		// both cases, and in the first it is also noise against a box that is
		// not coming back.
		return false
	default:
		return false
	}
}

// NextAfterFailure returns the tier to fall back to when `failed` dies.
//
// Downgrade is immediate and never optional: there is no state in which we
// prefer a dead fast path to a live slow one. Reaching TierRelay is the end of
// the ladder — the floor does not fall back, it retries in place.
func NextAfterFailure(failed PathTier) (PathTier, string) {
	switch failed {
	case TierDirect:
		return TierOverlay, "direct path died — falling back to the shared overlay"
	case TierOverlay:
		return TierRelay, "overlay path died — falling back to the relay floor"
	default:
		return TierRelay, "relay is the floor — retrying in place, not falling further"
	}
}

// ShouldUpgrade damps path changes. A challenger must be a better TIER, or the
// same tier and faster by a real margin, and only after the incumbent's trust
// window has elapsed.
func ShouldUpgrade(cur PathTier, curRTT time.Duration, held time.Duration, cand PathTier, candRTT time.Duration) bool {
	if cand > cur {
		return true // a better tier is worth taking immediately
	}
	if cand < cur {
		return false
	}
	if held < PathTrustWindow {
		return false
	}
	return candRTT+PathBetterMargin < curRTT
}
