package main

// Relay tunnel auto-heal: detect the "service reports connected but
// traffic blackholes" failure mode and force the QUIC tunnel to redial.
//
// The trigger for this code path was a macOS Tailscale incident where
// the network extension marked the VPN as "Connected" after the user
// daemon had already died. DNS still resolved (MagicDNS interception
// remained installed), so applications believed the network was fine,
// but every TCP connect to a public host timed out at the kernel.
// Yaver's existing /health check was happy because it polled the relay
// over HTTP via the local agent's network stack — which was the same
// stack that was silently blackholing — and a deadlocked stack returns
// a perfectly polite "context deadline exceeded" rather than anything
// the consumer code distinguishes from "relay is down."
//
// This file adds three small primitives that together let the agent
// notice split-brain quickly and recover without user intervention:
//
//   - probeRelayPath: L4 dial probe that separates DNS health from
//     TCP-handshake health. The combination "DNS fast, TCP timeout"
//     is the fingerprint of a half-dead tunnel; the existing /health
//     probe collapses both into one Boolean and can't tell them apart.
//   - detectVPNInterference: enumerate UP tunnel-class interfaces
//     (utun*/tun*/ppp*/ipsec*/wg*) with non-link-local addresses so
//     we can hint at the likely culprit when split-brain fires.
//   - splitBrainStreaks: count consecutive split-brain detections per
//     relay so we only act after a second confirmation. Single-shot
//     blackholes happen during real network flaps and re-dialing on
//     the first miss would hurt rather than help.
//
// Triggering the actual redial (ForceReconnect) lives on relayManager
// in main.go — that's where the per-tunnel cancel functions are
// tracked. This file is pure-detection.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RelayPathProbe is one snapshot of the path between this device and a
// relay's HTTP endpoint, broken into DNS-layer and TCP-layer timings.
// Both numbers are exported into RelayHealthStatus so `yaver doctor`
// can show them and the user can tell at a glance whether their
// resolver is slow, their socket is blackholed, or both.
type RelayPathProbe struct {
	DNSLatencyMs int64
	TCPLatencyMs int64
	// Kind is one of: "ok", "dns-fail", "tcp-timeout", "tcp-refused",
	// "tcp-error", "url-invalid". Anything other than "ok" populates Err.
	Kind string
	Err  string
}

// IsSplitBrain returns true for the specific failure where DNS
// resolution succeeded (proving the resolver is reachable) but the
// subsequent TCP handshake timed out. That combination is what a dead
// userspace tunnel produces; a genuinely-down relay returns
// "tcp-refused" or a fast network error instead.
func (p RelayPathProbe) IsSplitBrain() bool {
	return p.Kind == "tcp-timeout" && p.DNSLatencyMs >= 0 && p.Err != ""
}

// probeRelayPath measures DNS-then-TCP latency to the host:port
// extracted from a relay HTTP URL. The dial is L4-only: we close as
// soon as the 3-way handshake completes so we don't conflate TLS
// handshake or HTTP response timings with raw reachability. The
// caller picks the TCP timeout; 5 s is the operational default,
// short enough to recover within one health-check tick.
func probeRelayPath(httpURL string, tcpTimeout time.Duration) RelayPathProbe {
	p := RelayPathProbe{}
	u, err := url.Parse(httpURL)
	if err != nil || u.Host == "" {
		p.Kind = "url-invalid"
		p.Err = fmt.Sprintf("parse %q: %v", httpURL, err)
		return p
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		default:
			port = "443"
		}
	}

	// DNS probe gets its own short deadline so a hung resolver can't
	// eat the caller's whole budget. 3 s is generous; healthy macOS /
	// systemd-resolved answers in <50 ms.
	dnsCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dnsStart := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(dnsCtx, host)
	p.DNSLatencyMs = time.Since(dnsStart).Milliseconds()
	if err != nil || len(addrs) == 0 {
		p.Kind = "dns-fail"
		if err != nil {
			p.Err = err.Error()
		} else {
			p.Err = "no addresses returned"
		}
		return p
	}

	dialer := &net.Dialer{Timeout: tcpTimeout}
	tcpStart := time.Now()
	conn, err := dialer.Dial("tcp", net.JoinHostPort(addrs[0], port))
	p.TCPLatencyMs = time.Since(tcpStart).Milliseconds()
	if err != nil {
		es := err.Error()
		switch {
		case strings.Contains(es, "i/o timeout"), strings.Contains(es, "deadline exceeded"):
			p.Kind = "tcp-timeout"
		case strings.Contains(es, "refused"):
			p.Kind = "tcp-refused"
		default:
			p.Kind = "tcp-error"
		}
		p.Err = es
		return p
	}
	_ = conn.Close()
	p.Kind = "ok"
	return p
}

// vpnInterfacePrefixes are name prefixes assigned to tunnel-class
// network interfaces on the platforms the agent runs on (macOS, Linux,
// Windows). The list is intentionally broad — false positives here
// only produce informational warnings, while a false negative would
// silently miss the macOS-Tailscale failure that motivated this file.
var vpnInterfacePrefixes = []string{
	"utun", // macOS userspace tunnels (Tailscale, OpenVPN, WireGuard, Cloudflare WARP)
	"tun",  // Linux generic tunnel
	"tap",  // Linux/Windows TAP devices (OpenVPN bridged mode)
	"ppp",  // Point-to-point (PPTP/L2TP)
	"ipsec", // IPsec virtual interfaces
	"wg",   // WireGuard kernel module
}

// detectVPNInterference returns the names of UP, non-loopback,
// tunnel-class interfaces that carry at least one non-link-local
// address. The presence of such an interface alone proves nothing —
// many users run a VPN that is genuinely healthy — but combined with a
// split-brain probe failure it is the single most common cause and
// worth surfacing to the user.
func detectVPNInterference() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var hits []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifc.Name)
		var matched bool
		for _, prefix := range vpnInterfacePrefixes {
			if strings.HasPrefix(name, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// Filter out utun* interfaces that exist on macOS but only
		// hold a link-local IPv6 address — those are stale templates,
		// not active tunnels.
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			s := a.String()
			if strings.HasPrefix(s, "fe80:") || strings.HasPrefix(s, "169.254.") {
				continue
			}
			hits = append(hits, ifc.Name)
			break
		}
	}
	return hits
}

// splitBrainHealThreshold is the number of consecutive split-brain
// probes required before the health loop calls ForceReconnect. Two is
// the smallest value that filters single-tick network flaps while
// still recovering within ~2 minutes at the default 60 s probe
// cadence.
const splitBrainHealThreshold = 2

var (
	splitBrainStreaksMu sync.Mutex
	splitBrainStreaks   = make(map[string]int)
)

// noteProbeOutcome updates the per-relay streak counter and returns
// the streak length. Pass an "ok" probe to reset. The streak is keyed
// by the relay's HTTP URL so a multi-relay configuration heals each
// path independently.
func noteProbeOutcome(httpURL string, p RelayPathProbe) int {
	splitBrainStreaksMu.Lock()
	defer splitBrainStreaksMu.Unlock()
	if p.Kind == "ok" {
		splitBrainStreaks[httpURL] = 0
		return 0
	}
	if !p.IsSplitBrain() {
		// Non-split-brain failures (dns-fail, tcp-refused) do not
		// trigger forced re-dial — they indicate a problem the redial
		// can't fix (resolver down, relay actually offline).
		return splitBrainStreaks[httpURL]
	}
	splitBrainStreaks[httpURL]++
	return splitBrainStreaks[httpURL]
}

// resetSplitBrainStreak clears the streak for httpURL. Used by
// ForceReconnect after triggering a redial so we don't immediately
// re-trigger on the next tick if recovery takes more than one cycle.
func resetSplitBrainStreak(httpURL string) {
	splitBrainStreaksMu.Lock()
	defer splitBrainStreaksMu.Unlock()
	splitBrainStreaks[httpURL] = 0
}
