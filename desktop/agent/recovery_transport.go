package main

import (
	"net"
	"net/http"
	urlpkg "net/url"
	"slices"
	"strings"
)

type RecoveryTransportPosture struct {
	Status                     string   `json:"status,omitempty"`
	MobileApprovedTransports   []string `json:"mobileApprovedTransports,omitempty"`
	WebApprovedTransports      []string `json:"webApprovedTransports,omitempty"`
	HasPrivateTransport        bool     `json:"hasPrivateTransport"`
	HasBrowserTransport        bool     `json:"hasBrowserTransport"`
	PublicDirectRecoveryClosed bool     `json:"publicDirectRecoveryClosed"`
	Summary                    string   `json:"summary,omitempty"`
}

type recoveryIngressVerdict struct {
	Allowed   bool
	Transport string
	Reason    string
}

func computeRecoveryTransportPosture(cfg *Config) RecoveryTransportPosture {
	mobile := approvedMobileRecoveryTransports(cfg)
	web := approvedWebRecoveryTransports(cfg)
	posture := RecoveryTransportPosture{
		MobileApprovedTransports:   mobile,
		WebApprovedTransports:      web,
		HasPrivateTransport:        len(mobile) > 0,
		HasBrowserTransport:        len(web) > 0,
		PublicDirectRecoveryClosed: cfg != nil && cfg.RequirePrivateRecoveryTransport,
	}
	if !posture.PublicDirectRecoveryClosed {
		posture.Status = "open"
		posture.Summary = "Direct public HTTP recovery is allowed on this machine (default). Mobile can use " + formatRecoveryTransports(mobile) + "; web can use " + formatRecoveryTransports(web) + ". Set require-private-recovery=true to restrict /auth/recover to private paths only."
		return posture
	}
	if len(mobile) == 0 {
		posture.Status = "missing"
		posture.Summary = "Private-only recovery is enabled, but no approved remote recovery transport is configured. Add Tailscale for mobile recovery, and a private relay or HTTPS Cloudflare Tunnel for browser recovery."
		return posture
	}
	if len(web) == 0 {
		posture.Status = "mobile-only"
		posture.Summary = "Private-only recovery is enabled. Mobile recovery is ready via " + formatRecoveryTransports(mobile) + ", but the web dashboard still needs a private relay or HTTPS Cloudflare Tunnel."
		return posture
	}
	posture.Status = "private-ready"
	posture.Summary = "Private-only recovery is enabled. Mobile can use " + formatRecoveryTransports(mobile) + "; web can use " + formatRecoveryTransports(web) + ". Direct public HTTP recovery is blocked."
	return posture
}

func approvedMobileRecoveryTransports(cfg *Config) []string {
	var transports []string
	ts := DetectTailscale()
	if ts != nil && ts.Running && ts.Self != nil && len(ts.Self.Addrs) > 0 {
		transports = append(transports, "tailscale")
	}
	if hasConfiguredRecoveryRelay(cfg) {
		transports = append(transports, "relay")
	}
	if hasSecureCloudflareTunnel(cfg) {
		transports = append(transports, "cloudflare")
	}
	return transports
}

func approvedWebRecoveryTransports(cfg *Config) []string {
	var transports []string
	if hasConfiguredRecoveryRelay(cfg) {
		transports = append(transports, "relay")
	}
	if hasSecureCloudflareTunnel(cfg) {
		transports = append(transports, "cloudflare")
	}
	return transports
}

func hasConfiguredRecoveryRelay(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return len(cfg.RelayServers) > 0 || len(cfg.CachedRelayServers) > 0
}

func hasSecureCloudflareTunnel(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	for _, tunnel := range cfg.CloudflareTunnels {
		u, err := urlpkg.Parse(strings.TrimSpace(tunnel.URL))
		if err != nil || u == nil {
			continue
		}
		if strings.EqualFold(u.Scheme, "https") && strings.TrimSpace(u.Host) != "" {
			return true
		}
	}
	return false
}

func classifyRecoveryIngress(r *http.Request, cfg *Config) recoveryIngressVerdict {
	if r == nil {
		return recoveryIngressVerdict{Allowed: false, Reason: "missing request context"}
	}
	if strings.TrimSpace(r.Header.Get("X-Relay-Password")) != "" {
		return recoveryIngressVerdict{Allowed: true, Transport: "relay", Reason: "private relay"}
	}
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return recoveryIngressVerdict{Allowed: false, Reason: "could not determine caller IP"}
	}
	switch {
	case ip.IsLoopback():
		return recoveryIngressVerdict{Allowed: true, Transport: "loopback", Reason: "local loopback"}
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return recoveryIngressVerdict{Allowed: true, Transport: "lan", Reason: "private LAN"}
	case tailscaleCGNAT.Contains(ip):
		return recoveryIngressVerdict{Allowed: true, Transport: "tailscale", Reason: "tailscale overlay"}
	default:
		if cfg == nil || !cfg.RequirePrivateRecoveryTransport {
			return recoveryIngressVerdict{Allowed: true, Transport: "public", Reason: "public ingress allowed by config"}
		}
		posture := computeRecoveryTransportPosture(cfg)
		if posture.HasPrivateTransport {
			return recoveryIngressVerdict{
				Allowed: false,
				Reason:  "remote recovery is blocked on direct public HTTP. Use Tailscale, a private relay, or an HTTPS Cloudflare Tunnel.",
			}
		}
		return recoveryIngressVerdict{
			Allowed: false,
			Reason:  "remote recovery is blocked on direct public HTTP. No approved private recovery transport is configured on this machine.",
		}
	}
}

func formatRecoveryTransports(transports []string) string {
	if len(transports) == 0 {
		return "none configured yet"
	}
	labels := make([]string, 0, len(transports))
	for _, transport := range transports {
		switch transport {
		case "tailscale":
			labels = append(labels, "Tailscale")
		case "relay":
			labels = append(labels, "private relay")
		case "cloudflare":
			labels = append(labels, "HTTPS Cloudflare Tunnel")
		default:
			labels = append(labels, transport)
		}
	}
	slices.Sort(labels)
	return strings.Join(labels, ", ")
}
