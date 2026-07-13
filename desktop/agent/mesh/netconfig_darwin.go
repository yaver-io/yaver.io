//go:build darwin

package mesh

import (
	"fmt"
	"os"
)

// macOS: utun interfaces are point-to-point. Assign the overlay IP as both
// local and peer address, bring it up, then route the whole mesh subnet at the
// interface. Needs root (ifconfig/route).

func defaultTUNName() string { return "utun" }

// meshResolverFile makes macOS route *.mesh DNS queries to our overlay
// responder automatically (no /etc/hosts surgery). scutil picks it up live.
const meshResolverFile = "/etc/resolver/mesh"

func registerMeshDNS(selfIP string) error {
	content := fmt.Sprintf("nameserver %s\nport 53\n", selfIP)
	return os.WriteFile(meshResolverFile, []byte(content), 0o644)
}

func unregisterMeshDNS() error {
	if err := os.Remove(meshResolverFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cleanStaleArtifacts drops a resolver entry a crashed agent may have left
// pointing at a dead overlay responder (M3).
func cleanStaleArtifacts() { _ = unregisterMeshDNS() }

// enableForwarding turns on IP forwarding. macOS NAT is done with pf; we enable
// forwarding here and leave the pf nat anchor to a documented manual step (a
// safe automatic pf rewrite is risky on a user's Mac). Servers acting as exit
// nodes should run Linux.
func enableForwarding(iface, meshCIDR string) error {
	return runCmd("sysctl", "-w", "net.inet.ip.forwarding=1")
}

func disableForwarding(iface, meshCIDR string) error {
	return runCmd("sysctl", "-w", "net.inet.ip.forwarding=0")
}

func configureInterface(name, selfIPv4, meshCIDR string) error {
	if err := runCmd("ifconfig", name, "inet", selfIPv4, selfIPv4, "up"); err != nil {
		return err
	}
	// -q quiets "route already in table"; harmless on re-up.
	return runCmd("route", "-q", "-n", "add", "-inet", "-net", meshCIDR, "-interface", name)
}
