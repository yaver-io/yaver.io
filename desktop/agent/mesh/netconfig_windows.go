//go:build windows

package mesh

import "net"

// Windows: wintun adapters are configured via netsh. Assign the overlay IP with
// the mesh subnet mask (which installs the on-link route) and bring it up. Needs
// an elevated (Administrator) process.

func defaultTUNName() string { return "yaver-mesh" }

func configureInterface(name, selfIPv4, meshCIDR string) error {
	mask := maskFromCIDR(meshCIDR)
	return runCmd("netsh", "interface", "ip", "set", "address",
		"name="+name, "static", selfIPv4, mask)
}

// maskFromCIDR converts "100.96.0.0/12" to a dotted netmask "255.240.0.0".
func maskFromCIDR(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || len(ipnet.Mask) != 4 {
		return "255.240.0.0" // /12 default
	}
	m := ipnet.Mask
	return net.IPv4(m[0], m[1], m[2], m[3]).String()
}

// registerMeshDNS: NRPT-rule wiring for the .mesh zone is left to a later pass;
// the responder still answers direct queries on the overlay IP.
func registerMeshDNS(selfIP string) error { return nil }
func unregisterMeshDNS() error            { return nil }
func cleanStaleArtifacts()                {}

// enableForwarding is a no-op on Windows for now (exit-node/subnet-router hosts
// should run Linux); the data plane still works as a client.
func enableForwarding(iface, meshCIDR string) error  { return nil }
func disableForwarding(iface, meshCIDR string) error { return nil }
