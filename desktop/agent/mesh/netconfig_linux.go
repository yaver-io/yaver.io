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

// cleanStaleArtifacts is a no-op on Linux: DNS wiring is a no-op here, and the
// forwarding rules are interface-scoped and removed on graceful down.
func cleanStaleArtifacts() {}

// ensureRule adds an iptables rule only if it isn't already present (the `-C`
// check). This makes enableForwarding idempotent across `yaver serve` restarts
// — previously the FORWARD `-A` rules had no guard and duplicated on every
// restart, leaking a pair each time (M4).
func ensureRule(table, chain string, spec ...string) {
	check := append([]string{"-t", table, "-C", chain}, spec...)
	if runCmd("iptables", check...) == nil {
		return // already present
	}
	add := append([]string{"-t", table, "-A", chain}, spec...)
	_ = runCmd("iptables", add...)
}

func deleteRule(table, chain string, spec ...string) {
	del := append([]string{"-t", table, "-D", chain}, spec...)
	_ = runCmd("iptables", del...)
}

// enableForwarding turns this node into a subnet router / exit node: enable IP
// forwarding and masquerade mesh-sourced traffic leaving for non-mesh
// destinations. Needs CAP_NET_ADMIN.
//
// Forwarding is scoped to MESH-SOURCED traffic (`-s meshCIDR`) with stateful
// return only — NOT a blanket `-i/-o iface ACCEPT`. That stops the box from
// forwarding arbitrary non-mesh packets that happen to arrive on the interface,
// and combined with per-user mesh isolation keeps this from acting as an open
// relay (only this user's own devices + explicitly-granted guests are peers).
func enableForwarding(iface, meshCIDR string) error {
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	ensureRule("nat", "POSTROUTING", "-s", meshCIDR, "!", "-d", meshCIDR, "-j", "MASQUERADE")
	ensureRule("filter", "FORWARD", "-i", iface, "-s", meshCIDR, "-j", "ACCEPT")
	ensureRule("filter", "FORWARD", "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	return nil
}

func disableForwarding(iface, meshCIDR string) error {
	deleteRule("nat", "POSTROUTING", "-s", meshCIDR, "!", "-d", meshCIDR, "-j", "MASQUERADE")
	deleteRule("filter", "FORWARD", "-i", iface, "-s", meshCIDR, "-j", "ACCEPT")
	deleteRule("filter", "FORWARD", "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	return nil
}
