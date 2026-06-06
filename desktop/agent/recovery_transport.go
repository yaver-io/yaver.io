package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	urlpkg "net/url"
	"slices"
	"strings"
)

type RecoveryTransportPosture struct {
	Status string `json:"status,omitempty"`
	// NOTE: these two arrays must NOT be `omitempty`, and must be non-nil.
	// Convex's recoveryPostureValidator (backend/convex/devices.ts) requires
	// `mobileApprovedTransports`/`webApprovedTransports` as non-optional
	// arrays. On a fresh box with no approved transports the slices are empty;
	// with `omitempty` they were dropped from the JSON entirely, so EVERY
	// device registration failed the nested validator with
	// "Object is missing the required field `mobileApprovedTransports`" → 500.
	// Always send them (as `[]` when empty) so registration validates.
	MobileApprovedTransports   []string `json:"mobileApprovedTransports"`
	WebApprovedTransports      []string `json:"webApprovedTransports"`
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
	// Coerce nil → empty slice so the JSON carries `[]`, not `null`. A nil
	// slice marshals to `null`, which Convex's `v.array(...)` rejects just as
	// hard as a missing field — both 500 the registration on a fresh box.
	if mobile == nil {
		mobile = []string{}
	}
	if web == nil {
		web = []string{}
	}
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

// recoveryRelayPasswordMatches checks the X-Relay-Password header value
// against the agent's configured relay password (or its cached copy
// from a prior Convex sync) in constant time. Returns false if cfg is
// nil, both stored values are empty, or the input doesn't match either.
//
// We accept a match against either RelayPassword or CachedRelayPassword
// because the live value can be unset on a freshly-installed agent that
// only has the cached one (re-pair scenarios, where the agent has not
// yet refreshed userSettings from Convex).
func recoveryRelayPasswordMatches(cfg *Config, got string) bool {
	if cfg == nil {
		return false
	}
	gotBytes := []byte(got)
	candidates := []string{cfg.RelayPassword, cfg.CachedRelayPassword}
	matched := false
	for _, want := range candidates {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if subtle.ConstantTimeCompare(gotBytes, []byte(want)) == 1 {
			matched = true
		}
	}
	return matched
}

func classifyRecoveryIngress(r *http.Request, cfg *Config) recoveryIngressVerdict {
	if r == nil {
		return recoveryIngressVerdict{Allowed: false, Reason: "missing request context"}
	}
	// Validate the X-Relay-Password header VALUE, not just its presence.
	// The previous "header non-empty == private relay" check was a public
	// bypass (C-5): any unauthenticated caller could send any non-empty
	// value to skip the private-transport gate. Compare against the
	// configured relay password (or its cached counterpart from a prior
	// Convex sync) using subtle.ConstantTimeCompare.
	if got := strings.TrimSpace(r.Header.Get("X-Relay-Password")); got != "" {
		if recoveryRelayPasswordMatches(cfg, got) {
			return recoveryIngressVerdict{Allowed: true, Transport: "relay", Reason: "private relay"}
		}
		// Non-empty but wrong: deliberately fall through to the IP-based
		// classification below (rather than 403) so a misconfigured
		// header doesn't reveal whether the secret guess was close.
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
