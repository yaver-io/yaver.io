//go:build !darwin && !linux && !windows

package mesh

import "fmt"

// Platforms without a wired-up interface configurator (e.g. *BSD). The TUN
// device may still create, but we can't assign the address automatically, so we
// fail loudly rather than bring up a half-configured interface.

func defaultTUNName() string { return "yaver-wg0" }

func configureInterface(name, selfIPv4, meshCIDR string) error {
	return fmt.Errorf("mesh: automatic interface configuration is not implemented on this platform; "+
		"assign %s on %s and route %s manually", selfIPv4, name, meshCIDR)
}

func registerMeshDNS(selfIP string) error { return nil }
func unregisterMeshDNS() error            { return nil }
func cleanStaleArtifacts()                {}

func enableForwarding(iface, meshCIDR string) error  { return nil }
func disableForwarding(iface, meshCIDR string) error { return nil }

func addPeerHostRoute(iface, peerIP string) error {
	return fmt.Errorf("mesh: per-peer host routes are not implemented on this platform; route %s via %s manually", peerIP, iface)
}

func delPeerHostRoute(peerIP string) error {
	return fmt.Errorf("mesh: per-peer host routes are not implemented on this platform; remove the route for %s manually", peerIP)
}
