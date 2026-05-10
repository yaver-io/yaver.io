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

// isCGNATTailscaleIP reports whether ip falls in 100.64.0.0/10 (the
// IANA Carrier-Grade NAT range Tailscale uses). Used by callers that
// need to distinguish a real LAN IP from a Tailscale overlay IP in a
// list that mixes both — e.g. when sorting fallback candidates.
func isCGNATTailscaleIP(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip)).To4()
	if parsed == nil {
		return false
	}
	return parsed[0] == 100 && parsed[1] >= 64 && parsed[1] <= 127
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
			if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
				return true
			}
		}
	}
	return false
}
