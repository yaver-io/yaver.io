package main

// auto_public_ip.go — best-effort detection of the agent's externally-
// routable IPv4 so the device record carries at least one public
// reachability candidate even when the user hasn't configured a
// Cloudflare tunnel and the relay's published URL has rotted.
//
// We probe a small set of independent "echo my IP" services in
// parallel with tight timeouts and accept the first valid response.
// The result is cached for autoPublicIPCacheTTL so steady-state
// heartbeats don't burn external requests. Disabled by
// `disable_auto_public_ip: true` in config.json — defaults ON, because
// Yaver's wedge for remote primaries is "user reaches the box from
// their phone, no SSH". A device with zero working publicEndpoints is
// invisible to the phone the moment any one published path rots.
//
// Privacy: the detected IP is appended as a publicEndpoint and lives
// on the Convex device row, visible to that user's other signed-in
// surfaces. It is NOT a customer-derived value the way an LAN IP
// would be — this IS the IP of the box the user owns. The opt-out
// covers users who route exclusively through Cloudflare and don't
// want the bare host:port advertised even to themselves.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	autoPublicIPCacheTTL     = 5 * time.Minute
	autoPublicIPProbeTimeout = 3 * time.Second
)

// IPv4-echo services. We pick well-known, no-auth endpoints that have
// been stable for years and don't impose rate limits at our cadence
// (cached at 5-min granularity). Run in parallel — first valid IPv4
// wins, the others are abandoned.
var autoPublicIPProbeURLs = []string{
	"https://api.ipify.org",
	"https://icanhazip.com",
	"https://ifconfig.me/ip",
}

type autoPublicIPCacheState struct {
	mu sync.Mutex
	ip string
	ts time.Time
}

var autoPublicIPCache autoPublicIPCacheState

// detectAutoPublicIP returns the agent's externally-visible IPv4.
// Cached per autoPublicIPCacheTTL. Returns "" on unreachable/timeout;
// callers should treat that as "no auto-IP this round" rather than
// fatal — the agent stays useful via the user-configured publicEndpoints
// (Cloudflare tunnels, manual IPs) and the relay-assigned URL.
func detectAutoPublicIP(ctx context.Context) string {
	autoPublicIPCache.mu.Lock()
	if autoPublicIPCache.ip != "" && time.Since(autoPublicIPCache.ts) < autoPublicIPCacheTTL {
		ip := autoPublicIPCache.ip
		autoPublicIPCache.mu.Unlock()
		return ip
	}
	autoPublicIPCache.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, autoPublicIPProbeTimeout)
	defer cancel()

	resCh := make(chan string, len(autoPublicIPProbeURLs))
	for _, url := range autoPublicIPProbeURLs {
		go func(u string) {
			ip := probeOneIPEcho(probeCtx, u)
			if ip != "" {
				resCh <- ip
			}
		}(url)
	}
	for i := 0; i < len(autoPublicIPProbeURLs); i++ {
		select {
		case <-probeCtx.Done():
			return ""
		case ip := <-resCh:
			autoPublicIPCache.mu.Lock()
			autoPublicIPCache.ip = ip
			autoPublicIPCache.ts = time.Now()
			autoPublicIPCache.mu.Unlock()
			return ip
		}
	}
	return ""
}

func probeOneIPEcho(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	return parseIPv4FromEchoBody(string(body))
}

// parseIPv4FromEchoBody extracts a usable public IPv4 from an echo
// service body. Tolerates surrounding whitespace and multi-line bodies
// (some services emit headers + IP). Rejects loopback / link-local /
// multicast / unspecified — those are signs the probe got intercepted
// by a captive portal or local proxy.
func parseIPv4FromEchoBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		ip := net.ParseIP(token)
		if ip == nil {
			continue
		}
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		if v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsMulticast() || v4.IsUnspecified() {
			continue
		}
		return v4.String()
	}
	return ""
}

// autoPublicEndpoint returns "http://<ip>:<port>" when auto-IP detection
// is enabled in cfg AND we successfully probed an external IP. Returns
// "" when disabled, when probing failed, or when port is invalid. The
// caller appends to its publicEndpoints list at the desired position
// (currently lowest priority, after relay-assigned URLs).
func autoPublicEndpoint(cfg *Config, port int) string {
	if cfg == nil || cfg.DisableAutoPublicIP {
		return ""
	}
	if port <= 0 || port > 65535 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), autoPublicIPProbeTimeout+time.Second)
	defer cancel()
	ip := detectAutoPublicIP(ctx)
	if ip == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", ip, port)
}

// resetAutoPublicIPCache is exported only for tests — clears the
// in-process cache so a follow-up call performs a fresh probe.
func resetAutoPublicIPCache() {
	autoPublicIPCache.mu.Lock()
	autoPublicIPCache.ip = ""
	autoPublicIPCache.ts = time.Time{}
	autoPublicIPCache.mu.Unlock()
}

// publicEndpointsWithAutoIP returns configuredPublicEndpoints(cfg)
// with the auto-detected http://<ip>:<port> appended at the end (lowest
// priority) when the auto-IP feature is enabled and detection succeeds.
// De-duplicates against any user-configured entry that already covers
// the same host. Used by the registration + heartbeat paths so the
// device row always carries the freshest reachability hint.
func publicEndpointsWithAutoIP(cfg *Config, port int) []string {
	endpoints := configuredPublicEndpoints(cfg)
	auto := autoPublicEndpoint(cfg, port)
	if auto == "" {
		return endpoints
	}
	for _, ep := range endpoints {
		if normalizedEndpointMatches(ep, auto) {
			return endpoints
		}
	}
	return append(endpoints, auto)
}

// normalizedEndpointMatches treats two endpoint strings as the "same"
// reachability hint if they resolve to the same host (port-aware
// where both carry one). Lets the user's manual `198.51.100.20` entry
// suppress the auto-detected `http://198.51.100.20:18080` so the
// device row doesn't show two entries for the same box.
func normalizedEndpointMatches(a, b string) bool {
	hostA := stripSchemeAndPath(a)
	hostB := stripSchemeAndPath(b)
	if hostA == "" || hostB == "" {
		return false
	}
	if hostA == hostB {
		return true
	}
	// Treat a bare host (no port) as matching a host:port form for the
	// same host — that's what lets a manual `198.51.100.20` config
	// suppress an auto-detected `http://198.51.100.20:18080`. Differing
	// explicit ports (e.g. :18080 vs :8443) stay distinct because the
	// user opted into both as separate reachability candidates.
	hasPortA := strings.LastIndex(hostA, ":") > strings.LastIndex(hostA, "]")
	hasPortB := strings.LastIndex(hostB, ":") > strings.LastIndex(hostB, "]")
	if hasPortA && hasPortB {
		return false
	}
	bareA := stripPort(hostA)
	bareB := stripPort(hostB)
	return bareA != "" && bareA == bareB
}

func stripSchemeAndPath(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
