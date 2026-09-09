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
	"context"
	"net"
	"os/exec"
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

// firstDialableSameSubnetLanIP keeps the "LAN first" policy honest: a
// same-/24 address is only better than Tailscale if ssh can actually open TCP.
// Without this, stale LAN rows make `yaver ssh` hang on OpenSSH's long timeout
// before the resolver ever reaches a working overlay route.
func firstDialableSameSubnetLanIP(candidates []string, port string, timeout time.Duration) string {
	locals, err := localInterfacePrivateIPv4s()
	if err != nil || len(locals) == 0 {
		return ""
	}
	for _, raw := range candidates {
		ip := strings.TrimSpace(raw)
		parsed := net.ParseIP(ip).To4()
		if parsed == nil || !isPrivateLanIPv4(parsed) || isLikelyDockerBridgeIP(ip) {
			continue
		}
		sameSubnet := false
		for _, local := range locals {
			if sameIPv4Slash24(parsed, local) {
				sameSubnet = true
				break
			}
		}
		if !sameSubnet {
			continue
		}
		if tcpPortDialable(ip, port, timeout) {
			return ip
		}
	}
	return ""
}

// firstDialableTailscaleIP returns a device-row Tailscale address only after
// proving ssh's TCP port answers. A 100.x interface on this host means the route
// can exist; it does not prove the target accepts ssh.
func firstDialableTailscaleIP(candidates []string, port string, timeout time.Duration) string {
	if !localTailscaleUp() {
		return ""
	}
	for _, raw := range candidates {
		ip := strings.TrimSpace(raw)
		if !isCGNATTailscaleIP(ip) {
			continue
		}
		if tcpPortDialable(ip, port, timeout) {
			return ip
		}
	}
	return ""
}

func tcpPortDialable(host, port string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// firstDialableHost tries address-family candidates in their advertised order.
// This is deliberately family-neutral: on an IPv6-only caller an older cached
// IPv4 endpoint fails immediately and the next IPv6 endpoint can still win.
func firstDialableHost(candidates []string, port string, timeout time.Duration) string {
	for _, raw := range candidates {
		host := strings.TrimSpace(raw)
		if host != "" && tcpPortDialable(host, port, timeout) {
			return host
		}
	}
	return ""
}

// firstDialableHostConcurrent probes all inferred routes within ONE wall-clock
// budget and returns the earliest candidate (preference order) that answered.
// Serial per-address timeouts make a fallback ladder slower as it becomes more
// robust; concurrency keeps added routes from taxing the user when they are
// stale. The channel is fully buffered so timed-out probe goroutines can finish
// without being retained by the caller.
func firstDialableHostConcurrent(candidates []string, port string, timeout time.Duration) string {
	type result struct {
		index int
		ok    bool
	}
	unique := make([]string, 0, len(candidates))
	seen := make(map[string]bool)
	for _, raw := range candidates {
		host := strings.TrimSpace(raw)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		unique = append(unique, host)
	}
	if len(unique) == 0 {
		return ""
	}
	results := make(chan result, len(unique))
	for i, host := range unique {
		go func(index int, candidate string) {
			results <- result{index: index, ok: sshHandshakeDialable(candidate, port, timeout)}
		}(i, host)
	}
	timer := time.NewTimer(timeout + 50*time.Millisecond)
	defer timer.Stop()
	best := len(unique)
	for range unique {
		select {
		case r := <-results:
			if r.ok && r.index < best {
				best = r.index
			}
		case <-timer.C:
			if best < len(unique) {
				return unique[best]
			}
			return ""
		}
	}
	if best < len(unique) {
		return unique[best]
	}
	return ""
}

// sshHandshakeDialable proves that a listener is actually serving SSH, not
// merely accepting TCP. A resource-starved sshd can complete the three-way
// handshake and then never deliver its identification banner; treating that as
// healthy recreates the exact multi-second stall this selector exists to avoid.
// RFC 4253 permits informational lines before the SSH identification, so scan a
// small bounded prefix rather than assuming the first read begins with "SSH-".
func sshHandshakeDialable(host, port string, timeout time.Duration) bool {
	host = bareHostNoPort(host)
	if host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	buf := make([]byte, 512)
	prefix := make([]byte, 0, 4096)
	for len(prefix) < 4096 {
		n, readErr := conn.Read(buf)
		if n > 0 {
			prefix = append(prefix, buf[:n]...)
			for _, line := range strings.Split(string(prefix), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "SSH-2.0-") || strings.HasPrefix(line, "SSH-1.99-") {
					return true
				}
			}
		}
		if readErr != nil {
			return false
		}
	}
	return false
}

// orderedSSHRouteCandidates translates one device row into direct SSH
// candidates. It does no I/O; the concurrent capability probe above is the
// only authority. Order preserves policy: same-LAN, mesh, routed private,
// Tailscale, public SSH, then any remaining advertised address.
func orderedSSHRouteCandidates(dev *DeviceInfo, tailscaleUp, meshUp bool) []string {
	if dev == nil {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(dev.LocalIps)+len(dev.PublicEndpoints)+1)
	add := func(raw string) {
		host := strings.TrimSpace(raw)
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, host)
	}
	locals, _ := localInterfacePrivateIPv4s()
	for _, raw := range dev.LocalIps {
		ip := net.ParseIP(strings.TrimSpace(raw)).To4()
		if ip == nil || !isPrivateLanIPv4(ip) || isLikelyDockerBridgeIP(raw) {
			continue
		}
		for _, local := range locals {
			if sameIPv4Slash24(ip, local) {
				add(raw)
				break
			}
		}
	}
	if meshUp {
		for _, ip := range dev.LocalIps {
			if isMeshOverlayIPv4(ip) {
				add(ip)
			}
		}
	}
	for _, raw := range dev.LocalIps {
		ip := net.ParseIP(strings.TrimSpace(raw)).To4()
		if ip != nil && isPrivateLanIPv4(ip) && !isLikelyDockerBridgeIP(raw) {
			add(raw)
		}
	}
	if tailscaleUp {
		for _, ip := range dev.LocalIps {
			if isCGNATTailscaleIP(ip) {
				add(ip)
			}
		}
	}
	for _, raw := range dev.PublicEndpoints {
		ep := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "https://"), "http://")
		if slash := strings.IndexByte(ep, '/'); slash >= 0 {
			if strings.HasPrefix(ep[slash:], "/d/") {
				continue
			}
			ep = ep[:slash]
		}
		ep = bareHostNoPort(ep)
		if ep != "" && !isYaverHTTPRelayHost(ep) {
			add(ep)
		}
	}
	for _, ip := range dev.LocalIps {
		if ip == "" || strings.HasPrefix(ip, "127.") || ip == "::1" || isLikelyDockerBridgeIP(ip) {
			continue
		}
		if (!tailscaleUp && isCGNATTailscaleIP(ip)) || (!meshUp && isMeshOverlayIPv4(ip)) {
			continue
		}
		add(ip)
	}
	if host := strings.TrimSpace(dev.QuicHost); host != "" && host != "0.0.0.0" && !isLikelyDockerBridgeIP(host) {
		if !(!tailscaleUp && isCGNATTailscaleIP(host)) && !(!meshUp && isMeshOverlayIPv4(host)) {
			add(host)
		}
	}
	return out
}

// firstDialableSSHConfigHint preserves real ~/.ssh/config aliases without
// trusting their presence. `ssh -G` only reads the merged config; the resolved
// HostName/Port must also accept TCP before the alias can outrank the relay.
// OpenSSH echoes an unknown hint as `hostname <hint>`, which is deliberately
// rejected so a registered device name cannot become an unbounded DNS/overlay
// fallback after every inferred route already failed.
func firstDialableSSHConfigHint(sshPath string, hints []string, defaultPort string, timeout time.Duration) string {
	seen := make(map[string]bool)
	for _, raw := range hints {
		hint := strings.TrimSpace(raw)
		if hint == "" || seen[hint] {
			continue
		}
		seen[hint] = true
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, err := exec.CommandContext(ctx, sshPath, "-G", hint).Output()
		cancel()
		if err != nil {
			continue
		}
		host, port := parseSSHGOutput(string(out))
		if host == "" || strings.EqualFold(host, hint) {
			continue
		}
		if port == "" {
			port = defaultPort
		}
		if sshHandshakeDialable(host, port, timeout) {
			return hint // retain the alias so OpenSSH applies the full stanza
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
