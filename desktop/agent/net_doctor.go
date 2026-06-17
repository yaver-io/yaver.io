package main

// net_doctor.go — deep internet-connectivity troubleshooting.
//
// The existing network tools (ping, dns_lookup, wifi_info, speed_test, …) each
// answer ONE question. `net_doctor` answers the question a human actually has:
// "my internet is broken — *where* is it broken and what do I do?"
//
// It walks the connectivity stack in dependency order and stops reasoning at the
// FIRST layer that fails — that layer is the root cause, everything downstream
// is a symptom. The classic footguns it pins down:
//
//   link        — no active interface / Wi-Fi associated but no IP (DHCP fail,
//                  self-assigned 169.254.x), hotspot vs Wi-Fi vs ethernet.
//   gateway     — can't even reach the router/hotspot (LAN problem, not ISP).
//   internet    — gateway OK but 1.1.1.1/8.8.8.8 unreachable by IP (ISP / hotspot
//                  out of data) — internet is down *independent of DNS*.
//   dns         — raw internet OK but names don't resolve (the #1 "wifi connected
//                  but nothing loads" cause; bad resolver / captive DNS).
//   captive     — a captive portal (hotel/airport/cafe) is intercepting traffic;
//                  you must sign in in a browser.
//   https       — DNS+IP OK but TLS to the wider web fails (MITM proxy, clock skew).
//   quality     — everything works but latency/jitter/loss/throughput is bad.
//   yaver       — Yaver-specific reachability: local agent port + relay DNS, so
//                  "why can't my phone reach this box" is answered in the same run.
//
// Same engine drives three surfaces: the `yaver net doctor` CLI, the `net_doctor`
// MCP tool (so the AI can self-diagnose its runner), and POST /net/doctor for the
// mobile Connection screen's "Troubleshoot" card.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// NetLayerStatus is the verdict for one layer of the stack.
type NetLayerStatus string

const (
	NetOK   NetLayerStatus = "ok"
	NetWarn NetLayerStatus = "warn"
	NetFail NetLayerStatus = "fail"
	NetSkip NetLayerStatus = "skip"
)

// NetLayer is one rung of the connectivity ladder.
type NetLayer struct {
	Name    string                 `json:"name"`  // stable id: link/gateway/internet/dns/captive/https/quality/yaver
	Title   string                 `json:"title"` // human-readable
	Status  NetLayerStatus         `json:"status"`
	Detail  string                 `json:"detail"`
	Hint    string                 `json:"hint,omitempty"` // what to do if this layer is the problem
	Metrics map[string]interface{} `json:"metrics,omitempty"`
}

// NetDoctorReport is the synthesized result returned to every surface.
type NetDoctorReport struct {
	StartedAt   string         `json:"started_at"`
	DurationMs  int64          `json:"duration_ms"`
	Host        string         `json:"host"`
	Platform    string         `json:"platform"`
	Medium      string         `json:"medium"` // wifi | ethernet | cellular | hotspot-ios | hotspot-android | unknown
	SSID        string         `json:"ssid,omitempty"`
	Status      NetLayerStatus `json:"status"`  // overall = worst layer
	Verdict     string         `json:"verdict"` // one-line plain-English summary
	RootCause   string         `json:"root_cause,omitempty"`
	Layers      []NetLayer     `json:"layers"`
	Remediation []string       `json:"remediation,omitempty"`
}

// NetDoctorOptions tweaks the run.
type NetDoctorOptions struct {
	Throughput bool   // run a small download (off by default: costs time + mobile data)
	Target     string // optional extra host to verify end-to-end (e.g. "github.com")
	SkipYaver  bool   // skip the yaver-reachability layer
}

// ─── Low-level probes ───────────────────────────────────────────────

// netReachTCP opens a TCP connection as a root-free reachability + RTT probe.
// Returns (reachable, latencyMs). latencyMs is -1 when unreachable.
func netReachTCP(host string, port, timeoutMs int) (bool, int64) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", addr, time.Duration(timeoutMs)*time.Millisecond)
	if err != nil {
		return false, -1
	}
	conn.Close()
	return true, time.Since(t0).Milliseconds()
}

// netDefaultGateway returns (gatewayIP, interface) using the OS routing table.
// `route -n get default` (darwin) / `ip route show default` (linux) are the
// cleanest, parse-stable sources — no netstat column guessing.
func netDefaultGateway() (gw string, iface string) {
	switch runtime.GOOS {
	case "darwin":
		out, err := runCmd("route", "-n", "get", "default")
		if err != nil || out == "" {
			// Daemon PATH may omit /sbin.
			out, err = runCmd("/sbin/route", "-n", "get", "default")
		}
		if err != nil {
			return "", ""
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "gateway:") {
				gw = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
			} else if strings.HasPrefix(line, "interface:") {
				iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
			}
		}
	case "linux":
		out, err := runCmd("sh", "-c", "ip route show default 2>/dev/null | head -1")
		if err != nil {
			return "", ""
		}
		// default via 192.168.1.1 dev wlan0 proto dhcp ...
		f := strings.Fields(out)
		for i := 0; i < len(f)-1; i++ {
			if f[i] == "via" {
				gw = f[i+1]
			}
			if f[i] == "dev" {
				iface = f[i+1]
			}
		}
	}
	return gw, iface
}

var netLossRe = regexp.MustCompile(`([\d.]+)% packet loss`)
var netRttRe = regexp.MustCompile(`=\s*([\d.]+)/([\d.]+)/([\d.]+)`)

// netPingStats runs ICMP ping and parses loss + min/avg/max RTT. ok is false
// when ping couldn't run or saw 100% loss.
func netPingStats(host string, count int) (ok bool, lossPct float64, avgMs float64, raw string) {
	if count <= 0 {
		count = 4
	}
	out, _ := runCmd("ping", "-c", strconv.Itoa(count), "-t", "8", host)
	if out == "" {
		// linux uses -W for timeout, not -t; retry without -t.
		out, _ = runCmd("ping", "-c", strconv.Itoa(count), host)
	}
	raw = out
	lossPct = 100
	if m := netLossRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			lossPct = v
		}
	}
	if m := netRttRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[2], 64); err == nil {
			avgMs = v
		}
	}
	ok = lossPct < 100 && strings.Contains(out, "packets transmitted")
	return ok, lossPct, avgMs, raw
}

// netMediumFromGateway pins iOS/Android hotspots by their fixed gateway subnets —
// the most reliable "you're tethered" signal we can read locally. portHint, when
// non-empty, is the OS's authoritative interface type ("wifi"/"ethernet") and
// wins over name-prefix guessing (on macOS en0 is Wi-Fi, not ethernet).
func netMediumFromGateway(gw, iface, ssid, portHint string) string {
	switch {
	case strings.HasPrefix(gw, "172.20.10."):
		return "hotspot-ios" // iOS Personal Hotspot always hands out 172.20.10.0/28
	case gw == "192.168.43.1" || strings.HasPrefix(gw, "192.168.43."):
		return "hotspot-android" // Android Wi-Fi tether default
	}
	li := strings.ToLower(iface)
	if strings.Contains(li, "iphone") || strings.Contains(li, "usb") || strings.Contains(li, "rndis") {
		return "hotspot-usb"
	}
	if portHint == "wifi" || portHint == "ethernet" {
		return portHint
	}
	if ssid != "" {
		return "wifi"
	}
	if strings.HasPrefix(li, "wl") || strings.HasPrefix(li, "wi") {
		return "wifi"
	}
	if strings.HasPrefix(li, "en") || strings.HasPrefix(li, "eth") {
		return "ethernet"
	}
	return "unknown"
}

// netIfaceType returns the OS's authoritative type for an interface
// ("wifi"/"ethernet"/""). On macOS en0 is Wi-Fi, so name prefixes lie — we ask
// `networksetup` which hardware port owns the device.
func netIfaceType(iface string) string {
	if iface == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := runCmd("networksetup", "-listallhardwareports")
		if err != nil || out == "" {
			out, err = runCmd("/usr/sbin/networksetup", "-listallhardwareports")
		}
		if err != nil {
			return ""
		}
		// Blocks: "Hardware Port: Wi-Fi\nDevice: en0\n..."
		port := ""
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Hardware Port:") {
				port = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
			} else if strings.HasPrefix(line, "Device:") {
				if strings.TrimSpace(strings.TrimPrefix(line, "Device:")) == iface {
					lp := strings.ToLower(port)
					switch {
					case strings.Contains(lp, "wi-fi") || strings.Contains(lp, "airport"):
						return "wifi"
					case strings.Contains(lp, "iphone") || strings.Contains(lp, "usb"):
						return "hotspot-usb"
					case strings.Contains(lp, "ethernet") || strings.Contains(lp, "lan") || strings.Contains(lp, "thunderbolt"):
						return "ethernet"
					}
				}
			}
		}
	case "linux":
		// /sys/class/net/<iface>/wireless exists only for Wi-Fi devices.
		if _, err := os.Stat("/sys/class/net/" + iface + "/wireless"); err == nil {
			return "wifi"
		}
		if strings.HasPrefix(iface, "en") || strings.HasPrefix(iface, "eth") {
			return "ethernet"
		}
	}
	return ""
}

// ─── Engine ─────────────────────────────────────────────────────────

// RunNetDoctor walks the connectivity stack and returns a synthesized report.
func RunNetDoctor(ctx context.Context, opts NetDoctorOptions) NetDoctorReport {
	start := time.Now()
	host, _ := os.Hostname()
	rep := NetDoctorReport{
		StartedAt: start.UTC().Format(time.RFC3339),
		Host:      host,
		Platform:  runtime.GOOS,
		Status:    NetOK,
	}

	add := func(l NetLayer) { rep.Layers = append(rep.Layers, l) }

	// ── Layer 1: link (interface + IP + medium) ──────────────────────
	gw, iface := netDefaultGateway()
	localIP := netPrimaryLocalIP()
	ssid := ""
	if wi, ok := mcpWiFiInfo().(map[string]interface{}); ok {
		if s, _ := wi["ssid"].(string); s != "" {
			ssid = s
		}
	}
	rep.SSID = ssid
	rep.Medium = netMediumFromGateway(gw, iface, ssid, netIfaceType(iface))

	link := NetLayer{Name: "link", Title: "Network interface & IP", Metrics: map[string]interface{}{
		"interface": iface, "gateway": gw, "local_ip": localIP, "ssid": ssid, "medium": rep.Medium,
	}}
	switch {
	case gw == "" && localIP == "":
		link.Status = NetFail
		link.Detail = "No active network interface or default route — not connected to any network."
		link.Hint = "Turn Wi-Fi on (or plug in ethernet / enable your phone's hotspot), then re-run."
	case strings.HasPrefix(localIP, "169.254.") || localIP == "":
		link.Status = NetFail
		link.Detail = fmt.Sprintf("Interface %s is up but has a self-assigned address (%s) — DHCP failed.", iface, localIP)
		link.Hint = "Toggle Wi-Fi off/on or renew DHCP. On a hotspot, re-join it. The router never gave you an IP."
	case gw == "":
		link.Status = NetWarn
		link.Detail = fmt.Sprintf("Have IP %s but no default gateway — limited to the local subnet.", localIP)
		link.Hint = "No route to the internet is configured. Reconnect to the network."
	default:
		link.Status = NetOK
		link.Detail = fmt.Sprintf("%s up · IP %s · gateway %s%s", iface, localIP, gw, netMediumNote(rep.Medium, ssid))
	}
	add(link)

	// ── Layer 2: gateway reachability ────────────────────────────────
	gwLayer := NetLayer{Name: "gateway", Title: "Router / hotspot reachable"}
	if link.Status == NetFail || gw == "" {
		gwLayer.Status = NetSkip
		gwLayer.Detail = "Skipped — no gateway to test."
	} else {
		ok, loss, avg, _ := netPingStats(gw, 3)
		gwLayer.Metrics = map[string]interface{}{"gateway": gw, "loss_pct": loss, "rtt_ms": avg}
		if ok {
			gwLayer.Status = NetOK
			gwLayer.Detail = fmt.Sprintf("Gateway %s responds (%.0f ms, %.0f%% loss).", gw, avg, loss)
		} else {
			gwLayer.Status = NetFail
			gwLayer.Detail = fmt.Sprintf("Gateway %s is not responding — the LAN/hotspot link itself is broken (this is local, not your ISP).", gw)
			gwLayer.Hint = "Move closer to the router/phone, forget & rejoin the network, or restart the router/hotspot."
		}
	}
	add(gwLayer)

	// ── Layer 3: internet by IP (DNS-independent) ────────────────────
	inet := NetLayer{Name: "internet", Title: "Internet reachable (by IP)"}
	// 1.1.1.1 and 8.8.8.8 over TCP/443 — works through ICMP-blocking networks.
	cf, cfMs := netReachTCP("1.1.1.1", 443, 4000)
	goog, googMs := netReachTCP("8.8.8.8", 443, 4000)
	inet.Metrics = map[string]interface{}{
		"cloudflare_1111": cf, "cloudflare_ms": cfMs, "google_8888": goog, "google_ms": googMs,
	}
	switch {
	case cf || goog:
		inet.Status = NetOK
		best := cfMs
		if best < 0 || (googMs >= 0 && googMs < best) {
			best = googMs
		}
		inet.Detail = fmt.Sprintf("Reached public internet by IP (%d ms) — raw connectivity is up.", best)
	case gwLayer.Status == NetOK:
		inet.Status = NetFail
		inet.Detail = "Gateway is reachable but the wider internet is not (1.1.1.1 & 8.8.8.8 both unreachable by IP)."
		inet.Hint = "Your router is fine but its upstream is down: ISP outage, or a hotspot that's out of mobile data / has no signal."
	default:
		inet.Status = NetFail
		inet.Detail = "Cannot reach the public internet by IP."
		inet.Hint = "Fix the gateway/link first."
	}
	add(inet)

	// ── Layer 4: DNS resolution ──────────────────────────────────────
	dns := NetLayer{Name: "dns", Title: "DNS resolution"}
	dt0 := time.Now()
	addrs, dnsErr := net.DefaultResolver.LookupHost(ctx, "cloudflare.com")
	dnsMs := time.Since(dt0).Milliseconds()
	dns.Metrics = map[string]interface{}{"resolved": len(addrs), "dns_ms": dnsMs}
	switch {
	case dnsErr == nil && len(addrs) > 0:
		dns.Status = NetOK
		dns.Detail = fmt.Sprintf("cloudflare.com → %s (%d ms).", addrs[0], dnsMs)
		if dnsMs > 800 {
			dns.Status = NetWarn
			dns.Detail = fmt.Sprintf("DNS resolves but is slow (%d ms) — consider 1.1.1.1 / 8.8.8.8.", dnsMs)
			dns.Hint = "Set your DNS to 1.1.1.1 or 8.8.8.8 for faster, more reliable lookups."
		}
	case inet.Status == NetOK:
		dns.Status = NetFail
		dns.Detail = fmt.Sprintf("Internet is up by IP but DNS fails: %v. This is the classic \"connected but nothing loads\".", dnsErr)
		dns.Hint = "Change your DNS server to 1.1.1.1 or 8.8.8.8. On a captive/hotel network you may need to sign in first."
	default:
		dns.Status = NetFail
		dns.Detail = "DNS cannot resolve (no upstream internet)."
		dns.Hint = "Fix internet reachability first."
	}
	add(dns)

	// ── Layer 5: captive portal ──────────────────────────────────────
	cap := netCheckCaptivePortal()
	add(cap)

	// ── Layer 6: HTTPS to the wider web ──────────────────────────────
	https := NetLayer{Name: "https", Title: "HTTPS / TLS to the web"}
	pub, country, httpsErr := netHTTPSTrace()
	if httpsErr == nil && pub != "" {
		https.Status = NetOK
		https.Detail = fmt.Sprintf("HTTPS OK · public IP %s%s.", pub, netCountrySuffix(country))
		https.Metrics = map[string]interface{}{"public_ip": pub, "country": country}
	} else if cap.Status == NetFail {
		https.Status = NetSkip
		https.Detail = "Skipped — a captive portal is in the way."
	} else if dns.Status == NetOK && inet.Status == NetOK {
		https.Status = NetWarn
		https.Detail = fmt.Sprintf("DNS+IP work but an HTTPS fetch failed: %v (possible TLS-intercepting proxy or clock skew).", httpsErr)
		https.Hint = "Check the system clock, and any corporate proxy / VPN that may be intercepting TLS."
	} else {
		https.Status = NetSkip
		https.Detail = "Skipped — earlier layer failed."
	}
	add(https)

	// ── Layer 7: connection quality ──────────────────────────────────
	qual := NetLayer{Name: "quality", Title: "Connection quality"}
	if inet.Status == NetOK {
		ok, loss, avg, _ := netPingStats("1.1.1.1", 5)
		qual.Metrics = map[string]interface{}{"loss_pct": loss, "rtt_ms": avg, "icmp_ok": ok}
		var dlMbps float64 = -1
		if opts.Throughput {
			if st, ok := mcpSpeedTest().(map[string]interface{}); ok {
				if s, _ := st["download_mbps"].(string); s != "" {
					if v, err := strconv.ParseFloat(s, 64); err == nil {
						dlMbps = v
						qual.Metrics["download_mbps"] = dlMbps
					}
				}
			}
		}
		var issues []string
		if avg > 250 {
			issues = append(issues, fmt.Sprintf("high latency %.0f ms", avg))
		}
		if loss > 2 && loss < 100 {
			issues = append(issues, fmt.Sprintf("%.0f%% packet loss", loss))
		}
		if dlMbps >= 0 && dlMbps < 3 {
			issues = append(issues, fmt.Sprintf("low throughput %.1f Mbps", dlMbps))
		}
		if len(issues) == 0 {
			qual.Status = NetOK
			d := fmt.Sprintf("Latency %.0f ms, %.0f%% loss", avg, loss)
			if dlMbps >= 0 {
				d += fmt.Sprintf(", %.1f Mbps down", dlMbps)
			}
			qual.Detail = d + "."
		} else {
			qual.Status = NetWarn
			qual.Detail = "Working but degraded: " + strings.Join(issues, ", ") + "."
			qual.Hint = "On a hotspot this is normal under weak signal; move to better coverage or switch to Wi-Fi/ethernet."
		}
	} else {
		qual.Status = NetSkip
		qual.Detail = "Skipped — no internet to measure."
	}
	add(qual)

	// ── Optional: end-to-end target host ─────────────────────────────
	if t := strings.TrimSpace(opts.Target); t != "" {
		add(netCheckTarget(ctx, t))
	}

	// ── Layer 8: Yaver reachability ──────────────────────────────────
	if !opts.SkipYaver {
		add(netCheckYaver(rep.Medium))
	}

	// ── Synthesize ───────────────────────────────────────────────────
	rep.DurationMs = time.Since(start).Milliseconds()
	netSynthesize(&rep)
	return rep
}

// netPrimaryLocalIP returns the host's primary outbound IPv4 (no traffic sent).
func netPrimaryLocalIP() string {
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		// Fall back to first non-loopback interface address.
		ifaces, _ := net.InterfaceAddrs()
		for _, a := range ifaces {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
		}
		return ""
	}
	defer conn.Close()
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP.String()
	}
	return ""
}

// netCheckCaptivePortal hits the Apple + Google captive-check endpoints WITHOUT
// following redirects. A captive portal answers 200-with-wrong-body or a 3xx
// redirect instead of the expected sentinel.
func netCheckCaptivePortal() NetLayer {
	l := NetLayer{Name: "captive", Title: "Captive portal"}
	client := &http.Client{
		Timeout: 6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // surface 3xx instead of chasing it
		},
	}
	// Apple: expects 200 with body "<HTML><HEAD><TITLE>Success</TITLE>...".
	resp, err := client.Get("http://captive.apple.com/hotspot-detect.html")
	if err != nil {
		// Can't even reach it — that's an internet problem, not a portal. Stay neutral.
		l.Status = NetSkip
		l.Detail = "Could not run captive-portal check (no HTTP path out)."
		return l
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	body = append(body, buf[:n]...)
	bs := string(body)
	l.Metrics = map[string]interface{}{"status_code": resp.StatusCode}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		l.Status = NetFail
		l.Detail = fmt.Sprintf("Captive portal detected — traffic is being redirected (%d → %s).", resp.StatusCode, loc)
		l.Hint = "Open a browser to any http:// site and complete the network's sign-in page."
		l.Metrics["redirect"] = loc
	case resp.StatusCode == 200 && !strings.Contains(bs, "Success"):
		l.Status = NetFail
		l.Detail = "Captive portal detected — a sign-in page is being served instead of real traffic."
		l.Hint = "Open a browser to any http:// site and complete the network's sign-in page."
	case resp.StatusCode == 200:
		l.Status = NetOK
		l.Detail = "No captive portal — traffic passes through cleanly."
	default:
		l.Status = NetWarn
		l.Detail = fmt.Sprintf("Unexpected captive-check response (HTTP %d).", resp.StatusCode)
	}
	return l
}

// netHTTPSTrace does a full TLS GET to Cloudflare's trace endpoint and returns
// the public IP + country it reports.
func netHTTPSTrace() (ip, country string, err error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("https://1.1.1.1/cdn-cgi/trace")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	return traceField(body, "ip"), traceField(body, "loc"), nil
}

// netCheckTarget verifies a specific host end-to-end (DNS → TCP 443 → optional).
func netCheckTarget(ctx context.Context, host string) NetLayer {
	l := NetLayer{Name: "target", Title: "Target: " + host}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		l.Status = NetFail
		l.Detail = fmt.Sprintf("%s does not resolve (%v).", host, err)
		l.Hint = "Name resolution for this host failed — check DNS, or the host may be down."
		return l
	}
	ok, ms := netReachTCP(host, 443, 5000)
	l.Metrics = map[string]interface{}{"resolved": addrs[0], "reachable": ok, "ms": ms}
	if ok {
		l.Status = NetOK
		l.Detail = fmt.Sprintf("%s → %s, TCP/443 reachable (%d ms).", host, addrs[0], ms)
	} else {
		l.Status = NetFail
		l.Detail = fmt.Sprintf("%s resolves to %s but TCP/443 is unreachable.", host, addrs[0])
		l.Hint = "Host resolves but won't connect — it may be down, firewalled, or geo/IP-blocked."
	}
	return l
}

// netCheckYaver answers "can Yaver itself talk over this network" — the local
// agent HTTP port plus relay-host DNS. Informational: it never downgrades the
// overall internet verdict, but pins Yaver-specific reachability in one place.
func netCheckYaver(medium string) NetLayer {
	l := NetLayer{Name: "yaver", Title: "Yaver reachability"}
	agentUp, _ := netReachTCP("127.0.0.1", 18080, 1500)
	l.Metrics = map[string]interface{}{"agent_http_18080": agentUp}

	var notes []string
	if agentUp {
		notes = append(notes, "local agent on :18080 up")
	} else {
		notes = append(notes, "local agent on :18080 not listening (run `yaver serve`)")
	}

	// Relay DNS/TCP, best-effort from config.
	relayState := "unknown"
	if cfg, err := LoadConfig(); err == nil {
		relays := cfg.RelayServers
		if len(relays) == 0 {
			relays = cfg.CachedRelayServers
		}
		if len(relays) > 0 && relays[0].QuicAddr != "" {
			rhost := relays[0].QuicAddr
			if h, _, e := net.SplitHostPort(rhost); e == nil {
				rhost = h
			}
			if ips, e := net.DefaultResolver.LookupHost(context.Background(), rhost); e == nil && len(ips) > 0 {
				relayState = "resolves (" + ips[0] + ")"
			} else {
				relayState = "DNS failed"
			}
			l.Metrics["relay_host"] = rhost
		}
	}
	l.Metrics["relay"] = relayState
	notes = append(notes, "relay "+relayState)

	if strings.HasPrefix(medium, "hotspot") {
		notes = append(notes, "on a hotspot — phone↔box direct LAN won't work; Yaver will use the relay")
		l.Status = NetWarn
		l.Hint = "On a hotspot, devices are often isolated from each other. Pair via the relay (Convex-known IP), or put both devices on the same Wi-Fi/LAN for direct mode."
	} else if agentUp {
		l.Status = NetOK
	} else {
		l.Status = NetWarn
	}
	l.Detail = strings.Join(notes, " · ") + "."
	return l
}

// netSynthesize fills Status / Verdict / RootCause / Remediation from the layers.
func netSynthesize(rep *NetDoctorReport) {
	worst := NetOK
	rank := map[NetLayerStatus]int{NetOK: 0, NetSkip: 0, NetWarn: 1, NetFail: 2}
	var firstFail *NetLayer
	var warnings []NetLayer
	for i := range rep.Layers {
		l := &rep.Layers[i]
		// "yaver" layer is advisory — never drives the overall internet verdict.
		if l.Name == "yaver" {
			continue
		}
		if rank[l.Status] > rank[worst] {
			worst = l.Status
		}
		if l.Status == NetFail && firstFail == nil {
			firstFail = l
		}
		if l.Status == NetWarn {
			warnings = append(warnings, *l)
		}
	}
	rep.Status = worst

	switch {
	case firstFail != nil:
		rep.RootCause = firstFail.Name
		rep.Verdict = firstFail.Detail
		if firstFail.Hint != "" {
			rep.Remediation = append(rep.Remediation, firstFail.Hint)
		}
	case worst == NetWarn && len(warnings) > 0:
		rep.Verdict = "Online, but: " + warnings[0].Detail
		for _, w := range warnings {
			if w.Hint != "" {
				rep.Remediation = append(rep.Remediation, w.Hint)
			}
		}
	default:
		rep.Verdict = "All connectivity layers healthy — you're fully online."
	}

	// Always append any Yaver-layer hint so pairing advice survives a healthy run.
	for i := range rep.Layers {
		if rep.Layers[i].Name == "yaver" && rep.Layers[i].Hint != "" {
			rep.Remediation = append(rep.Remediation, rep.Layers[i].Hint)
		}
	}
}

// ─── small format helpers ───────────────────────────────────────────

func netMediumNote(medium, ssid string) string {
	switch medium {
	case "hotspot-ios":
		return " · iPhone Personal Hotspot"
	case "hotspot-android":
		return " · Android hotspot"
	case "hotspot-usb":
		return " · USB tether"
	case "wifi":
		if ssid != "" {
			return " · Wi-Fi “" + ssid + "”"
		}
		return " · Wi-Fi"
	case "ethernet":
		return " · ethernet"
	}
	return ""
}

func netCountrySuffix(country string) string {
	if country == "" {
		return ""
	}
	return " (" + country + ")"
}
