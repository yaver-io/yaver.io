package mesh

// peerroutes.go — install a /32 per peer instead of claiming the whole mesh
// subnet, so Yaver Mesh can coexist with an incumbent VPN instead of standing
// down whenever one exists.
//
// THE PROBLEM. Yaver Mesh routes 100.96.0.0/12, which is entirely inside
// Tailscale's 100.64.0.0/10. Tailscale installs one route for its whole /10, so
// the /12 cannot be claimed without fighting it — and mesh correctly refuses to
// start rather than break someone else's VPN. Correct, and catastrophic in
// reach: mesh becomes unavailable to every Tailscale user, including for peers
// Tailscale cannot carry at all (a phone that is not on the tailnet).
//
// THE FIX. A /32 is the longest possible prefix. It wins for exactly one
// address and touches nothing else, so:
//   - no conflict with Tailscale's /10, hence no reason to stand down,
//   - a route exists only for peers we DECIDED to serve, and
//   - the start-order blackhole disappears: mesh never owns a range wide enough
//     to swallow a Tailscale address it has never heard of.
//
// SAFETY. Two rules, both load-bearing, both enforced below.
//
//  1. NEVER route an address the incumbent can reach. That is deferral, and
//     violating it is precisely how you break a user's Tailscale.
//  2. NEVER route an address we have not VALIDATED. An unvalidated /32 is a
//     blackhole: traffic leaves for an interface that cannot deliver it, and
//     the failure looks like the peer is down rather than like a bad route.
//     Falling back to the relay is always available and always better.

import (
	"fmt"
	"sort"
	"sync"
)

// PeerRoute is one peer we have decided to carry over Yaver Mesh.
type PeerRoute struct {
	DeviceID string
	MeshIP   string // the peer's address on the Yaver Mesh overlay
}

// PeerRouteTable reconciles the set of installed /32s against the set we want.
//
// Reconciliation rather than add/remove calls at call sites: the desired set is
// derived fresh from peer state, and a table that diffs cannot leak a route
// when a peer disappears between two events.
type PeerRouteTable struct {
	mu        sync.Mutex
	iface     string
	installed map[string]string // deviceID -> meshIP currently routed

	// addRoute/delRoute are seams so the reconciler is testable without
	// touching a real routing table. A test that has to be root is a test
	// nobody runs.
	addRoute func(iface, ip string) error
	delRoute func(ip string) error
}

func NewPeerRouteTable(iface string) *PeerRouteTable {
	return &PeerRouteTable{
		iface:     iface,
		installed: map[string]string{},
		addRoute:  addPeerHostRoute,
		delRoute:  delPeerHostRoute,
	}
}

// Reconcile installs routes for `want` and removes every route not in it.
//
// Returns the applied changes for logging — a route table that changes silently
// is one nobody can debug when traffic goes missing.
func (t *PeerRouteTable) Reconcile(want []PeerRoute) (added, removed []string, errs []error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	desired := map[string]string{}
	for _, r := range want {
		if r.DeviceID == "" || r.MeshIP == "" {
			continue
		}
		desired[r.DeviceID] = r.MeshIP
	}

	// Remove first. If a peer's address CHANGED, deleting the stale route
	// before adding the new one keeps us from briefly owning two routes for the
	// same peer, where longest-prefix ties are resolved by the kernel rather
	// than by us.
	for dev, ip := range t.installed {
		if newIP, ok := desired[dev]; ok && newIP == ip {
			continue
		}
		if err := t.delRoute(ip); err != nil {
			errs = append(errs, fmt.Errorf("remove /32 for %s (%s): %w", dev, ip, err))
			continue
		}
		delete(t.installed, dev)
		removed = append(removed, ip)
	}

	for dev, ip := range desired {
		if cur, ok := t.installed[dev]; ok && cur == ip {
			continue
		}
		if err := t.addRoute(t.iface, ip); err != nil {
			errs = append(errs, fmt.Errorf("install /32 for %s (%s): %w", dev, ip, err))
			continue
		}
		t.installed[dev] = ip
		added = append(added, ip)
	}

	sort.Strings(added)
	sort.Strings(removed)
	return added, removed, errs
}

// Installed returns the currently routed peers, for status output.
func (t *PeerRouteTable) Installed() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]string, len(t.installed))
	for k, v := range t.installed {
		out[k] = v
	}
	return out
}

// Clear removes every route this table installed. Called on mesh shutdown so a
// stopped agent does not leave the kernel pointing at a dead interface.
func (t *PeerRouteTable) Clear() []error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	for dev, ip := range t.installed {
		if err := t.delRoute(ip); err != nil {
			errs = append(errs, fmt.Errorf("clear /32 for %s (%s): %w", dev, ip, err))
			continue
		}
		delete(t.installed, dev)
	}
	return errs
}

// PeerRouteDecision is the per-peer verdict feeding Reconcile.
type PeerRouteDecision struct {
	DeviceID string
	MeshIP   string
	// IncumbentReaches is true when an existing VPN already carries this peer.
	IncumbentReaches bool
	// Validated is true when a path check has actually succeeded on MeshIP.
	Validated bool
}

// SelectPeerRoutes applies the two safety rules and explains every exclusion.
//
// The reasons are returned, not logged internally, so the caller can surface
// them where a human will look. "Mesh isn't routing this peer" with no stated
// cause is the failure mode this whole audit exists to remove.
func SelectPeerRoutes(decisions []PeerRouteDecision) (routes []PeerRoute, skipped map[string]string) {
	skipped = map[string]string{}
	for _, d := range decisions {
		switch {
		case d.DeviceID == "" || d.MeshIP == "":
			continue
		case d.IncumbentReaches:
			// Rule 1: defer. The incumbent already carries this pair, and
			// competing for it is how you break a user's VPN.
			skipped[d.DeviceID] = "an existing VPN already reaches this peer — deferring to it"
		case !d.Validated:
			// Rule 2: an unvalidated /32 is a blackhole. The relay is carrying
			// this peer meanwhile, which is a worse path but a working one.
			skipped[d.DeviceID] = "no validated path to this peer's mesh address yet — staying on the relay"
		default:
			routes = append(routes, PeerRoute{DeviceID: d.DeviceID, MeshIP: d.MeshIP})
		}
	}
	return routes, skipped
}
