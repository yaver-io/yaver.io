//go:build linux

package mesh

// Linux: assign the overlay IP with the mesh prefix length so the kernel
// installs the subnet route automatically, then bring the link up. Needs
// CAP_NET_ADMIN (root). Uses iproute2 (`ip`), present on every modern distro.

func defaultTUNName() string { return "yaver-wg0" }

func configureInterface(name, selfIPv4, meshCIDR string) error {
	addr := selfIPv4 + "/" + cidrPrefix(meshCIDR)
	// `replace` is idempotent on re-up.
	if err := runCmd("ip", "addr", "replace", addr, "dev", name); err != nil {
		return err
	}
	return runCmd("ip", "link", "set", name, "up")
}

// registerMeshDNS: the .mesh responder runs on the overlay IP; wiring it into
// systemd-resolved/resolv.conf varies too much across distros to do safely
// here, so this is a no-op. Direct queries to the overlay IP still resolve.
func registerMeshDNS(selfIP string) error { return nil }
func unregisterMeshDNS() error            { return nil }

// enableForwarding turns this node into a subnet router / exit node: enable IP
// forwarding and masquerade mesh-sourced traffic leaving for non-mesh
// destinations. Needs CAP_NET_ADMIN.
func enableForwarding(iface, meshCIDR string) error {
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	// Masquerade mesh traffic egressing to the wider internet/LAN.
	_ = runCmd("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", meshCIDR, "!", "-d", meshCIDR, "-j", "MASQUERADE")
	if err := runCmd("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", meshCIDR, "!", "-d", meshCIDR, "-j", "MASQUERADE"); err != nil {
		return err
	}
	_ = runCmd("iptables", "-A", "FORWARD", "-i", iface, "-j", "ACCEPT")
	_ = runCmd("iptables", "-A", "FORWARD", "-o", iface, "-j", "ACCEPT")
	return nil
}

func disableForwarding(iface, meshCIDR string) error {
	_ = runCmd("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", meshCIDR, "!", "-d", meshCIDR, "-j", "MASQUERADE")
	_ = runCmd("iptables", "-D", "FORWARD", "-i", iface, "-j", "ACCEPT")
	_ = runCmd("iptables", "-D", "FORWARD", "-o", iface, "-j", "ACCEPT")
	return nil
}
