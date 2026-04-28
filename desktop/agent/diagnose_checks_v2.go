package main

// diagnose_checks_v2.go — the second tier of yaver diagnose checks:
// external reachability + runner auth. These need the network /
// subprocess tools and are kept separate from diagnose.go so a v1
// "local box" smoke can opt out with --skip=cloudflared,tailscale,
// relay,vpn,convex,runners.
//
// Design: each check is registered in init() into the extraDiagChecks
// slice that RunDiagnose appends to the v1 list. Optional (they
// short-circuit with DiagInfo "skipped" when the relevant tool /
// config is absent — never fail loudly when the user doesn't use
// that subsystem).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// extraDiagChecks is populated by init() below and appended by
// RunDiagnose. Keeps the v1 checks pure while letting v2 extend.
var extraDiagChecks []diagCheck

func init() {
	extraDiagChecks = []diagCheck{
		{Name: "cloudflared", Run: checkCloudflared},
		{Name: "tailscale", Run: checkTailscale},
		{Name: "relay", Run: checkRelay},
		{Name: "vpn", Run: checkVPN},
		{Name: "convex", Run: checkConvex},
		{Name: "runners", Run: checkRunners},
	}
}

// ─── cloudflared ───────────────────────────────────────────────────

func checkCloudflared(ctx context.Context, emit DiagEmit) {
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		emit(DiagEvent{Type: "finding", Check: "cloudflared", Severity: DiagInfo, Message: "cloudflared not installed — skipping (ok if you're not using Cloudflare tunnels)"})
		return
	}
	out, _ := exec.CommandContext(ctx, path, "--version").Output()
	emit(DiagEvent{Type: "finding", Check: "cloudflared", Severity: DiagInfo, Message: strings.TrimSpace(string(out))})

	// Quick status probe — `cloudflared tunnel list` requires auth,
	// so the mere presence of an error is inconclusive. Just list
	// config paths so the user can spot a misconfig.
	configDirs := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		configDirs = append(configDirs,
			home+"/.cloudflared",
			"/etc/cloudflared",
		)
	}
	found := false
	for _, d := range configDirs {
		if entries, err := os.ReadDir(d); err == nil && len(entries) > 0 {
			found = true
			emit(DiagEvent{Type: "finding", Check: "cloudflared", Severity: DiagOK, Message: fmt.Sprintf("config dir %s has %d entries", d, len(entries))})
		}
	}
	if !found {
		emit(DiagEvent{Type: "finding", Check: "cloudflared", Severity: DiagInfo, Message: "No cloudflared config found; tunnel not configured on this box."})
	}
}

// ─── tailscale ─────────────────────────────────────────────────────

type tailscaleStatusV2 struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName    string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online     bool     `json:"Online"`
	} `json:"Self"`
	Health []string `json:"Health"`
}

func checkTailscale(ctx context.Context, emit DiagEmit) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		emit(DiagEvent{Type: "finding", Check: "tailscale", Severity: DiagInfo, Message: "tailscale not installed — skipping"})
		return
	}
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		emit(DiagEvent{Type: "finding", Check: "tailscale", Severity: DiagWarning, Message: fmt.Sprintf("tailscale status failed: %v (you may need to `tailscale up`)", err)})
		return
	}
	var st tailscaleStatusV2
	if err := json.Unmarshal(out, &st); err != nil {
		emit(DiagEvent{Type: "finding", Check: "tailscale", Severity: DiagWarning, Message: "tailscale status JSON unparseable — version drift?"})
		return
	}
	sev := DiagOK
	if !st.Self.Online || st.BackendState != "Running" {
		sev = DiagWarning
	}
	emit(DiagEvent{
		Type:     "finding",
		Check:    "tailscale",
		Severity: sev,
		Message:  fmt.Sprintf("backend=%s self=%s online=%t ips=%v", st.BackendState, st.Self.DNSName, st.Self.Online, st.Self.TailscaleIPs),
		Data:     map[string]interface{}{"backend": st.BackendState, "online": st.Self.Online, "ips": st.Self.TailscaleIPs, "health": st.Health},
	})
	for _, msg := range st.Health {
		emit(DiagEvent{Type: "finding", Check: "tailscale", Severity: DiagWarning, Message: "health: " + msg})
	}
}

// ─── relay ─────────────────────────────────────────────────────────

func checkRelay(ctx context.Context, emit DiagEmit) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagInfo, Message: "no config — skipping relay probe"})
		return
	}

	// Prefer the live platform-config relay list so we see what Convex
	// believes is current. Falls back to offline-only when Convex is
	// unreachable or the agent has no token yet.
	var relays []struct {
		ID       string `json:"id"`
		HTTPURL  string `json:"httpUrl"`
		QuicAddr string `json:"quicAddr"`
		Priority int    `json:"priority"`
	}
	if cfg.ConvexSiteURL != "" {
		req, _ := http.NewRequestWithContext(ctx, "GET", cfg.ConvexSiteURL+"/config", nil)
		client := &http.Client{Timeout: 5 * time.Second}
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var body struct {
					RelayServers []struct {
						ID       string `json:"id"`
						HTTPURL  string `json:"httpUrl"`
						QuicAddr string `json:"quicAddr"`
						Priority int    `json:"priority"`
					} `json:"relayServers"`
				}
				raw, _ := io.ReadAll(resp.Body)
				if json.Unmarshal(raw, &body) == nil {
					for _, r := range body.RelayServers {
						relays = append(relays, struct {
							ID       string `json:"id"`
							HTTPURL  string `json:"httpUrl"`
							QuicAddr string `json:"quicAddr"`
							Priority int    `json:"priority"`
						}(r))
					}
				}
			} else {
				emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagWarning, Message: fmt.Sprintf("platform /config returned HTTP %d", resp.StatusCode)})
			}
		} else {
			emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagWarning, Message: fmt.Sprintf("platform /config unreachable: %v", err)})
		}
	}

	if len(relays) == 0 {
		emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagInfo, Message: "no relay servers configured (or Convex unreachable)"})
		return
	}

	for _, r := range relays {
		httpClient := &http.Client{Timeout: 3 * time.Second}
		start := time.Now()
		u := strings.TrimRight(r.HTTPURL, "/") + "/health"
		resp, err := httpClient.Get(u)
		if err != nil {
			emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagWarning, Message: fmt.Sprintf("%s HTTP unreachable: %v", r.HTTPURL, err)})
			continue
		}
		resp.Body.Close()
		sev := DiagOK
		if resp.StatusCode >= 500 {
			sev = DiagFailure
		} else if resp.StatusCode >= 400 {
			sev = DiagWarning
		}
		emit(DiagEvent{
			Type:     "finding",
			Check:    "relay",
			Severity: sev,
			Message:  fmt.Sprintf("%s /health HTTP %d (%dms)", r.HTTPURL, resp.StatusCode, time.Since(start).Milliseconds()),
			Data:     map[string]interface{}{"id": r.ID, "httpUrl": r.HTTPURL, "status": resp.StatusCode},
		})

		// QUIC reachability: just try a UDP socket connect (won't
		// complete a handshake, but proves the UDP path isn't
		// filtered between us and the relay edge).
		if r.QuicAddr != "" {
			host, port, err := net.SplitHostPort(r.QuicAddr)
			if err == nil {
				udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, port))
				if err != nil {
					emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagWarning, Message: fmt.Sprintf("%s UDP resolve failed: %v", r.QuicAddr, err)})
				} else if udpConn, err := net.DialUDP("udp", nil, udpAddr); err != nil {
					emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagWarning, Message: fmt.Sprintf("%s UDP dial failed: %v", r.QuicAddr, err)})
				} else {
					udpConn.Close()
					emit(DiagEvent{Type: "finding", Check: "relay", Severity: DiagOK, Message: fmt.Sprintf("%s UDP path ok", r.QuicAddr)})
				}
			}
		}
	}
}

// ─── vpn detection ────────────────────────────────────────────────

func checkVPN(ctx context.Context, emit DiagEmit) {
	ifaces, err := net.Interfaces()
	if err != nil {
		emit(DiagEvent{Type: "finding", Check: "vpn", Severity: DiagWarning, Message: fmt.Sprintf("interface enumeration failed: %v", err)})
		return
	}
	var vpnIfaces []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		// Heuristic: tun*, tap*, utun*, wg*, ppp*, ipsec* usually = VPN.
		if strings.HasPrefix(name, "tun") ||
			strings.HasPrefix(name, "tap") ||
			strings.HasPrefix(name, "utun") ||
			strings.HasPrefix(name, "wg") ||
			strings.HasPrefix(name, "ppp") ||
			strings.HasPrefix(name, "ipsec") {
			vpnIfaces = append(vpnIfaces, iface.Name)
		}
	}
	if len(vpnIfaces) == 0 {
		emit(DiagEvent{Type: "finding", Check: "vpn", Severity: DiagInfo, Message: "No VPN-like interfaces up."})
		return
	}
	emit(DiagEvent{
		Type:     "finding",
		Check:    "vpn",
		Severity: DiagInfo,
		Message:  fmt.Sprintf("VPN interfaces up: %v (if reload is broken, try toggling VPN off)", vpnIfaces),
		Data:     map[string]interface{}{"interfaces": vpnIfaces},
	})
}

// ─── convex reachability ──────────────────────────────────────────

func checkConvex(ctx context.Context, emit DiagEmit) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.ConvexSiteURL == "" {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagInfo, Message: "No convex_site_url — skipping"})
		return
	}
	u, err := url.Parse(cfg.ConvexSiteURL)
	if err != nil {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagFailure, Message: fmt.Sprintf("convex_site_url unparseable: %v", err)})
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	resp, err := client.Get(u.String() + "/config")
	if err != nil {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagFailure, Message: fmt.Sprintf("%s unreachable: %v", u.Host, err)})
		return
	}
	defer resp.Body.Close()
	sev := DiagOK
	if resp.StatusCode >= 500 {
		sev = DiagFailure
	} else if resp.StatusCode >= 400 {
		sev = DiagWarning
	}
	emit(DiagEvent{
		Type:     "finding",
		Check:    "convex",
		Severity: sev,
		Message:  fmt.Sprintf("%s /config HTTP %d (%dms)", u.Host, resp.StatusCode, time.Since(start).Milliseconds()),
		Data:     map[string]interface{}{"host": u.Host, "status": resp.StatusCode},
	})

	// Auth freshness: if we have a token and it was rejected, the
	// agent will spend requests in retry loops. Surface clearly.
	if cfg.AuthToken == "" {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagWarning, Message: "auth_token empty — run `yaver auth`"})
		return
	}
	if err := ValidateToken(cfg.ConvexSiteURL, cfg.AuthToken); err != nil {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagFailure, Message: fmt.Sprintf("auth token expired / rejected: %v", err)})
	} else {
		emit(DiagEvent{Type: "finding", Check: "convex", Severity: DiagOK, Message: "auth token valid"})
	}
}

// ─── runner auth ─────────────────────────────────────────────────

type runnerProbe struct {
	Name      string
	Command   string
	ProbeArgs []string
	AuthHint  string
}

func checkRunners(ctx context.Context, emit DiagEmit) {
	runners := []runnerProbe{
		{Name: "claude", Command: "claude", ProbeArgs: []string{"--version"}, AuthHint: "run `claude /login` to re-auth"},
		{Name: "codex", Command: "codex", ProbeArgs: []string{"--version"}, AuthHint: "run `codex login` to re-auth"},
		{Name: "opencode", Command: "opencode", ProbeArgs: []string{"--version"}, AuthHint: "`opencode auth login` or set provider API key"},
	}
	anyPresent := false
	for _, r := range runners {
		path, err := exec.LookPath(r.Command)
		if err != nil {
			emit(DiagEvent{Type: "finding", Check: "runners", Severity: DiagInfo, Message: fmt.Sprintf("%s not installed", r.Name)})
			continue
		}
		anyPresent = true
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := exec.CommandContext(probeCtx, path, r.ProbeArgs...).CombinedOutput()
		cancel()
		text := strings.TrimSpace(string(out))
		if err != nil {
			sev := DiagWarning
			lower := strings.ToLower(text + " " + err.Error())
			if strings.Contains(lower, "auth") ||
				strings.Contains(lower, "login") ||
				strings.Contains(lower, "unauthori") ||
				strings.Contains(lower, "api key") {
				sev = DiagFailure
			}
			emit(DiagEvent{
				Type:     "finding",
				Check:    "runners",
				Severity: sev,
				Message:  fmt.Sprintf("%s probe failed: %v — %s", r.Name, err, r.AuthHint),
				Data:     map[string]interface{}{"runner": r.Name, "output": diagFirstLine(text), "authHint": r.AuthHint},
			})
			continue
		}
		emit(DiagEvent{
			Type:     "finding",
			Check:    "runners",
			Severity: DiagOK,
			Message:  fmt.Sprintf("%s — %s", r.Name, diagFirstLine(text)),
			Data:     map[string]interface{}{"runner": r.Name, "version": diagFirstLine(text)},
		})
	}
	if !anyPresent {
		emit(DiagEvent{
			Type:     "finding",
			Check:    "runners",
			Severity: DiagWarning,
			Message:  "No AI coding runners on PATH. Install claude-code, codex, or opencode (e.g. `yaver install claude-code`).",
		})
	}
	_ = runtime.GOOS // keep import stable across refactors
}

func diagFirstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		return s[:idx]
	}
	return s
}
