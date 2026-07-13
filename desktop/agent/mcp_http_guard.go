package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// guardOutboundHTTPURL vets a URL before the http_request MCP tool shells out to
// curl. Two attacker vectors it closes (audit 2026-07-13, A3):
//
//  1. Cloud-metadata SSRF: http://169.254.169.254/... returns instance metadata.
//     On a Yaver managed box that can expose the broker token / user_data creds
//     → account takeover. Any link-local target (169.254.0.0/16, fe80::/10) is
//     refused; no legitimate http_request ever targets link-local.
//  2. Scheme abuse: curl also speaks file://, gopher://, dict://, scp://, …
//     — file:///etc/passwd is arbitrary local file read, gopher:// is a raw
//     socket. Only http/https are allowed.
//
// Loopback and RFC1918 are intentionally NOT blocked: http_request is an
// owner-only tool (guest scopes exclude /mcp) and `curl localhost:3000` /
// LAN dev is a legitimate, common use. The credential-theft vector is
// link-local, and that is what we cut off.
//
// Residual: curl performs its own DNS resolution, so a name that resolves
// "public" here but "link-local" at curl time (DNS rebinding) is not fully
// defeated for hostnames. Literal-IP metadata fetches — the realistic attack —
// are blocked outright.
func guardOutboundHTTPURL(rawurl string) error {
	raw := strings.TrimSpace(rawurl)
	if raw == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("scheme %q not allowed (only http/https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	blocked := func(ip net.IP) bool {
		return ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
	}

	// Literal IP: check directly.
	if lit := net.ParseIP(host); lit != nil {
		if blocked(lit) {
			return fmt.Errorf("refused: %s is a link-local/metadata address", host)
		}
		return nil
	}
	// Hostname: refuse if ANY resolved address is link-local (catches
	// metadata.google.internal → 169.254.169.254 and friends).
	ips, err := net.LookupIP(host)
	if err != nil {
		// Can't resolve — let curl surface the network error; not a bypass of
		// the metadata block (an unresolvable name reaches nothing).
		return nil
	}
	for _, ip := range ips {
		if blocked(ip) {
			return fmt.Errorf("refused: host %s resolves to a link-local/metadata address", host)
		}
	}
	return nil
}
