package main

// ssh_resolve_lan.go — LAN-IP-preferred SSH resolution.
//
// `yaver ssh <alias>` used to walk Tailscale first, which meant a
// device with both a Tailscale CGNAT IP (100.64/10) AND a real LAN IP
// (192.168.x / 10.x) on our subnet would always SSH over the overlay
// — slower than the LAN, dependent on Tailscale being up, and
// surprising to users who can `ssh user@<lan-ip>` by hand and have
// it just work.
//
// pickReachableLanIP filters a list of candidate IPs (typically the
// `localIps` field of a DeviceInfo row) for the first RFC1918 address
// that shares a /24 prefix with one of our local interface IPs.
// Same-/24 is the cheap "are we on the same subnet" heuristic — wrong
// for /29 home setups and /16 enterprise networks, but right for the
// 99% case (Wi-Fi /24, NAT /24). When wrong, we fall through to the
// existing Tailscale path — strictly no worse than today.

import (
	"net"
	"strings"
	"time"
)

// pickReachableLanIP returns the first candidate that is RFC1918,
// non-Docker, and shares a /24 prefix with a local interface IP.
// Empty string when nothing matches — caller falls back to Tailscale.
func pickReachableLanIP(candidates []string) string {
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		return ""
	}
	for _, raw := range candidates {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip).To4()
		if parsed == nil {
			continue
		}
		if !isPrivateLanIPv4(parsed) {
			continue
		}
		if isLikelyDockerBridgeIP(ip) {
			continue
		}
		for _, local := range locals {
			if sameIPv4Slash24(parsed, local) {
				return ip
			}
		}
	}
	return ""
}

// firstDialablePrivateIP returns the first RFC1918 candidate that
// accepts a TCP connection on port within timeout. This is the bridge
// for the case `pickReachableLanIP` can't see: a private LAN IP we do
// NOT share a /24 with but can still reach through a route — a Tailscale
// subnet router advertising 10.0.0.0/24, a WireGuard/utun tunnel, a
// corporate VPN. Those make `ssh user@10.0.0.45` work by hand even
// though 10.0.0.45 isn't on any local interface's subnet, so the SSH
// resolver should prefer them over a public HTTP endpoint (not an ssh
// host) or a relay PTY. Reachability-gated with a short dial so an
// unreachable address costs ~timeout, never OpenSSH's 30 s connect hang.
// Docker bridge gateways and loopback are skipped. Empty when none
// answer — caller falls through to the existing Tailscale/public paths.
func firstDialablePrivateIP(candidates []string, port string, timeout time.Duration) string {
	for _, raw := range candidates {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip).To4()
		if parsed == nil || !isPrivateLanIPv4(parsed) {
			continue
		}
		if isLikelyDockerBridgeIP(ip) {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, port), timeout)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return ip
	}
	return ""
}

// localInterfacePrivateIPv4s collects RFC1918 IPv4 addresses on every
// non-loopback interface that's UP. Used as the "what subnet am I
// on?" probe for pickReachableLanIP.
func localInterfacePrivateIPv4s() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			if !isPrivateLanIPv4(v4) {
				continue
			}
			if isLikelyDockerBridgeIP(v4.String()) {
				continue
			}
			out = append(out, v4)
		}
	}
	return out, nil
}

// isPrivateLanIPv4 matches RFC1918 only — 10/8, 172.16/12, 192.168/16.
// 100.64/10 (Tailscale CGNAT) is intentionally excluded so the LAN
// preference never accidentally classifies the overlay as a LAN.
func isPrivateLanIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}

// isMeshOverlayIPv4 reports whether ip falls in Yaver's mesh overlay
// range 100.96.0.0/12 (second octet 96–111). This block is deliberately
// a SUBSET of Tailscale's 100.64/10 CGNAT range, so anything that needs
// to tell a Yaver mesh IP apart from a Tailscale IP must check this
// FIRST — isCGNATTailscaleIP below explicitly excludes the mesh subnet
// so the two never both claim the same address.
func isMeshOverlayIPv4(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip)).To4()
	if parsed == nil {
		return false
	}
	return parsed[0] == 100 && parsed[1] >= 96 && parsed[1] <= 111
}

// isCGNATTailscaleIP reports whether ip falls in 100.64.0.0/10 (the
// IANA Carrier-Grade NAT range Tailscale uses) but NOT in Yaver's mesh
// sub-range 100.96/12. Used by callers that need to distinguish a real
// LAN IP from a Tailscale overlay IP in a list that mixes both — e.g.
// when sorting fallback candidates. Excluding the mesh subnet lets the
// SSH resolver gate Tailscale and Yaver-mesh addresses independently:
// a user who dropped Tailscale but runs `yaver mesh up` still reaches
// peers over the overlay.
func isCGNATTailscaleIP(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip)).To4()
	if parsed == nil {
		return false
	}
	if parsed[0] != 100 || parsed[1] < 64 || parsed[1] > 127 {
		return false
	}
	// 100.96–111 is the Yaver mesh overlay, handled on its own path.
	if parsed[1] >= 96 && parsed[1] <= 111 {
		return false
	}
	return true
}

// sameIPv4Slash24 returns true when a and b share the first three
// octets — the cheap same-subnet heuristic.
func sameIPv4Slash24(a, b net.IP) bool {
	a4 := a.To4()
	b4 := b.To4()
	if a4 == nil || b4 == nil {
		return false
	}
	return a4[0] == b4[0] && a4[1] == b4[1] && a4[2] == b4[2]
}

// localTailscaleUp reports whether this host has at least one 100.64/10
// IP on a non-loopback interface. False means tailscaled is stopped,
// not installed, or not authenticated — in which case any 100.x IPs
// in a device row are unreachable from us and shouldn't be returned
// by the SSH resolver. Without this gate the resolver would happily
// hand back a Tailscale CGNAT IP and ssh would block for 30 s on
// "Operation timed out" before the user found out why.
func localTailscaleUp() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			// Tailscale CGNAT, but NOT the Yaver mesh sub-range — a
			// host can be mesh-up while Tailscale-down, and counting a
			// 100.96 mesh address as "Tailscale up" would wrongly send
			// the SSH resolver down the Tailscale CLI paths.
			if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 && !(v4[1] >= 96 && v4[1] <= 111) {
				return true
			}
		}
	}
	return false
}

// localMeshUp reports whether this host has a Yaver mesh overlay IP
// (100.96/12) on a non-loopback interface — i.e. `yaver mesh up` has
// brought the WireGuard TUN online. Distinct from localTailscaleUp:
// the ranges overlap, so this is what lets the SSH resolver prefer the
// overlay route with no dependency on Tailscale being installed or up.
func localMeshUp() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			if v4[0] == 100 && v4[1] >= 96 && v4[1] <= 111 {
				return true
			}
		}
	}
	return false
}
