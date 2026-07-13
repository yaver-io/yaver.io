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
