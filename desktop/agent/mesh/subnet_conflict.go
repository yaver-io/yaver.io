package mesh

import (
	"fmt"
	"net"
	"net/netip"
)

// SubnetRouteConflict reports whether some interface other than our own mesh
// device already carries an address inside a range that covers MeshSubnetCIDR.
//
// Why this exists: Yaver Mesh's 100.96.0.0/12 sits INSIDE Tailscale's
// 100.64.0.0/10 (see the MeshSubnetCIDR comment — the old claim that it was
// "outside" was false). A host running Tailscale installs one route for the
// whole /10, so bringing mesh up there means two things want the same
// addresses. Detecting that is the difference between "mesh is off, and here
// is why" and a silent routing fight on a machine the user depends on.
//
// Deliberately conservative in BOTH directions:
//   - It reports, it does not remediate. Tailscale is the user's own software
//     and usually predates Yaver; we never tear down their route to win.
//   - It only flags an interface holding an address in CGNAT space that is NOT
//     ours. A plain RFC1918 LAN cannot collide with our overlay, so a laptop on
//     192.168.x is never flagged.
func SubnetRouteConflict(meshIfaceName string) (*Conflict, error) {
	ours, err := netip.ParsePrefix(MeshSubnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse mesh subnet: %w", err)
	}
	cgnat, err := netip.ParsePrefix(TailscaleCGNATCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse cgnat range: %w", err)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Name == meshIfaceName {
			continue // our own device is not a conflict
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipNet.IP.To4()
			if v4 == nil {
				continue
			}
			addr, ok := netip.AddrFromSlice(v4)
			if !ok {
				continue
			}
			// Only CGNAT space can collide with our overlay.
			if !cgnat.Contains(addr) {
				continue
			}
			return &Conflict{
				Interface: iface.Name,
				Address:   addr.String(),
				// Whether the address is inside OUR /12 or merely inside the
				// wider /10 changes how bad it is, so say which.
				OverlapsMeshSubnet: ours.Contains(addr),
			}, nil
		}
	}
	return nil, nil
}

// Conflict describes another interface occupying CGNAT space we would claim.
type Conflict struct {
	Interface string
	Address   string
	// OverlapsMeshSubnet is true when the address falls inside 100.96.0.0/12
	// itself (a direct clash), false when it is elsewhere in 100.64.0.0/10
	// (same route, still contested, but not the same addresses).
	OverlapsMeshSubnet bool
}

// Reason renders a user-facing explanation. Phrased so the user can act on it
// without knowing what CGNAT is, and never implies Yaver will fix it for them
// by removing their VPN.
func (c *Conflict) Reason() string {
	if c == nil {
		return ""
	}
	what := "shares the address range Yaver Mesh uses"
	if c.OverlapsMeshSubnet {
		what = "is using an address inside Yaver Mesh's own range"
	}
	return fmt.Sprintf(
		"interface %s (%s) %s (%s overlaps %s). Yaver Mesh stays off so it does not fight an existing VPN for routes; connections use your LAN or relay instead.",
		c.Interface, c.Address, what, TailscaleCGNATCIDR, MeshSubnetCIDR,
	)
}
