package main

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

const (
	connPrefDirectLAN    = "direct-lan"
	connPrefTailscale    = "tailscale"
	connPrefHeadscale    = "headscale"
	connPrefOwnVPN       = "own-vpn"
	connPrefHTTPSTunnel  = "https-tunnel"
	connPrefFreeRelay    = "free-relay"
	connPrefPrivateRelay = "private-relay"
)

var validConnectionPreferenceKinds = map[string]bool{
	connPrefDirectLAN:    true,
	connPrefTailscale:    true,
	connPrefHeadscale:    true,
	connPrefOwnVPN:       true,
	connPrefHTTPSTunnel:  true,
	connPrefFreeRelay:    true,
	connPrefPrivateRelay: true,
}

func connectionPreferencesForHeartbeat(cfg *Config, localIps []string, publicEndpoints []string) []ConnectionPreference {
	prefs := map[string]ConnectionPreference{}
	add := func(kind string, active, preferred bool, source string) {
		kind = strings.TrimSpace(strings.ToLower(kind))
		source = strings.TrimSpace(strings.ToLower(source))
		if !validConnectionPreferenceKinds[kind] {
			return
		}
		if source == "" {
			source = "agent-detected"
		}
		if existing, ok := prefs[kind]; ok {
			active = active || existing.Active
			preferred = preferred || existing.Preferred
			if existing.Source == "user-config" {
				source = existing.Source
			}
		}
		prefs[kind] = ConnectionPreference{Kind: kind, Active: active, Preferred: preferred, Source: source}
	}

	if cfg != nil {
		for _, pref := range cfg.ConnectionPreferences {
			add(pref.Kind, pref.Active, pref.Preferred, "user-config")
		}
	}

	hasRFC1918, hasTailnet := classifyHeartbeatIPs(localIps)
	if hasRFC1918 {
		add(connPrefDirectLAN, true, true, "agent-detected")
	}
	if hasTailnet {
		kind := connPrefTailscale
		if existing, ok := prefs[connPrefHeadscale]; ok && (existing.Active || existing.Preferred) {
			kind = connPrefHeadscale
		}
		add(kind, true, true, "agent-detected")
	}
	if hasVPNInterface() {
		add(connPrefOwnVPN, true, false, "agent-detected")
	}
	if hasHTTPSEndpoint(cfg, publicEndpoints) {
		add(connPrefHTTPSTunnel, true, false, "agent-detected")
	}
	if cfg != nil {
		if len(cfg.RelayServers) > 0 {
			add(connPrefPrivateRelay, true, false, "user-config")
		}
		for _, relay := range cfg.CachedRelayServers {
			add(classifyCachedRelayPreference(relay), true, false, "platform-config")
		}
	}

	out := make([]ConnectionPreference, 0, len(prefs))
	for _, pref := range prefs {
		out = append(out, pref)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Preferred != out[j].Preferred {
			return out[i].Preferred
		}
		return connectionPreferenceRank(out[i].Kind) < connectionPreferenceRank(out[j].Kind)
	})
	return out
}

func classifyHeartbeatIPs(ips []string) (hasRFC1918 bool, hasTailnet bool) {
	for _, raw := range ips {
		ip := net.ParseIP(strings.TrimSpace(raw)).To4()
		if ip == nil {
			continue
		}
		if isTailnetIPv4(ip) {
			hasTailnet = true
			continue
		}
		if ip.IsPrivate() {
			hasRFC1918 = true
		}
	}
	return hasRFC1918, hasTailnet
}

func hasHTTPSEndpoint(cfg *Config, publicEndpoints []string) bool {
	for _, raw := range publicEndpoints {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && strings.EqualFold(u.Scheme, "https") && strings.TrimSpace(u.Host) != "" {
			return true
		}
	}
	if cfg != nil {
		for _, tunnel := range cfg.CloudflareTunnels {
			u, err := url.Parse(strings.TrimSpace(tunnel.URL))
			if err == nil && strings.EqualFold(u.Scheme, "https") && strings.TrimSpace(u.Host) != "" {
				return true
			}
		}
	}
	return false
}

func hasVPNInterface() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "tun") ||
			strings.HasPrefix(name, "tap") ||
			strings.HasPrefix(name, "utun") ||
			strings.HasPrefix(name, "wg") ||
			strings.HasPrefix(name, "ppp") ||
			strings.HasPrefix(name, "ipsec") {
			return true
		}
	}
	return false
}

func classifyCachedRelayPreference(relay RelayServerConfig) string {
	text := strings.ToLower(strings.Join([]string{relay.ID, relay.Label, relay.Region}, " "))
	switch {
	case strings.Contains(text, "free"), strings.Contains(text, "public"), strings.Contains(text, "shared"):
		return connPrefFreeRelay
	case strings.Contains(text, "private"), strings.Contains(text, "managed"), strings.Contains(text, "custom"):
		return connPrefPrivateRelay
	default:
		return connPrefFreeRelay
	}
}

func connectionPreferenceRank(kind string) int {
	switch kind {
	case connPrefDirectLAN:
		return 10
	case connPrefHeadscale, connPrefTailscale:
		return 20
	case connPrefOwnVPN:
		return 30
	case connPrefHTTPSTunnel:
		return 40
	case connPrefPrivateRelay:
		return 50
	case connPrefFreeRelay:
		return 60
	default:
		return 100
	}
}
