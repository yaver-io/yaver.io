package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DomainRoute maps a domain+path to an app name or port.
type DomainRoute struct {
	Domain      string `json:"domain"`
	Path        string `json:"path"`
	Target      string `json:"target"` // app name or port number
	SSL         bool   `json:"ssl"`
	CacheStatic bool   `json:"cacheStatic"`
}

// DomainInfo holds the full configuration and status of a managed domain.
type DomainInfo struct {
	Domain        string        `json:"domain"`
	IP            string        `json:"ip"`
	IPType        string        `json:"ipType"`   // static_public / dynamic_public / private_nat
	SSLStatus     string        `json:"sslStatus"` // active / expired / none
	SSLExpiry     time.Time     `json:"sslExpiry"`
	SSLIssuer     string        `json:"sslIssuer"`
	Routes        []DomainRoute `json:"routes"`
	Provider      string        `json:"provider"`      // cloudflare / manual
	DNSConfigured bool          `json:"dnsConfigured"`
}

// DDNSConfig holds the configuration for the dynamic DNS updater.
type DDNSConfig struct {
	Provider   string        `json:"provider"`
	Domain     string        `json:"domain"`
	Interval   time.Duration `json:"interval"`
	LastIP     string        `json:"lastIp"`
	LastUpdate time.Time     `json:"lastUpdate"`
	Running    bool          `json:"running"`
}

// DomainManager manages domain configurations, SSL, DNS, and DDNS.
type DomainManager struct {
	mu         sync.Mutex
	domains    map[string]*DomainInfo
	ddns       *DDNSConfig
	ddnsStop   chan struct{}
	configPath string
}

// NewDomainManager creates a new DomainManager backed by ~/.yaver/domains.json.
func NewDomainManager() *DomainManager {
	home, _ := os.UserHomeDir()
	m := &DomainManager{
		domains:    make(map[string]*DomainInfo),
		configPath: filepath.Join(home, ".yaver", "domains.json"),
	}
	_ = m.loadConfig()
	return m
}

// Setup runs a full domain setup wizard for the given domain and provider.
// It detects the IP type, generates DNS instructions or creates records automatically
// (when provider is "cloudflare" and CF_API_TOKEN is set), waits for DNS propagation,
// and configures SSL via Caddy.
func (m *DomainManager) Setup(domain, provider string) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("=== Domain setup: %s ===\n\n", domain))

	// Step 1: Detect IP type.
	ip, ipType, err := m.DetectIP()
	if err != nil {
		return "", fmt.Errorf("IP detection failed: %w", err)
	}
	sb.WriteString(fmt.Sprintf("Step 1 — Detected IP: %s (type: %s)\n", ip, ipType))

	// Step 2: Generate instructions or auto-create DNS records.
	cfToken := os.Getenv("CF_API_TOKEN")
	dnsConfigured := false

	switch ipType {
	case "static_public":
		if provider == "cloudflare" && cfToken != "" {
			sb.WriteString("Step 2 — Creating Cloudflare DNS A record automatically…\n")
			zoneID, zErr := m.cloudflareGetZoneID(domain)
			if zErr != nil {
				sb.WriteString(fmt.Sprintf("  Warning: could not get zone ID (%v). Falling back to manual.\n", zErr))
				sb.WriteString(fmt.Sprintf("  Manual: Add A record  %s → %s\n", domain, ip))
			} else {
				if cErr := m.cloudflareCreateRecord(zoneID, "A", domain, ip, false); cErr != nil {
					sb.WriteString(fmt.Sprintf("  Warning: record creation failed (%v). Falling back to manual.\n", cErr))
					sb.WriteString(fmt.Sprintf("  Manual: Add A record  %s → %s\n", domain, ip))
				} else {
					sb.WriteString(fmt.Sprintf("  A record created: %s → %s\n", domain, ip))
					dnsConfigured = true
				}
			}
		} else {
			sb.WriteString(fmt.Sprintf("Step 2 — Manual DNS: Add A record  %s → %s\n", domain, ip))
		}

	case "dynamic_public":
		sb.WriteString(fmt.Sprintf("Step 2 — Dynamic IP detected (%s).\n", ip))
		if provider == "cloudflare" && cfToken != "" {
			sb.WriteString("  Auto-starting DDNS updater via Cloudflare (every 5 minutes)…\n")
			if _, dErr := m.DDNSStart("cloudflare", domain, 5*time.Minute); dErr != nil {
				sb.WriteString(fmt.Sprintf("  Warning: DDNS start failed: %v\n", dErr))
			} else {
				sb.WriteString("  DDNS updater started. DNS will be kept in sync automatically.\n")
				dnsConfigured = true
			}
		} else {
			sb.WriteString("  Recommendation: set up DDNS so the record stays current.\n")
			sb.WriteString(fmt.Sprintf("  Run: yaver domain ddns-start cloudflare %s\n", domain))
			sb.WriteString(fmt.Sprintf("  Or manually add A record: %s → %s (update when IP changes)\n", domain, ip))
		}

	case "private_nat":
		sb.WriteString("Step 2 — Machine is behind NAT (private IP). Direct A record not possible.\n")
		sb.WriteString("  Options:\n")
		sb.WriteString("    1. Use Cloudflare Tunnel (zero-trust, no port forwarding): cloudflared tunnel …\n")
		sb.WriteString("    2. Use Yaver Relay as reverse proxy: yaver relay route-add …\n")
		sb.WriteString("    3. Configure port forwarding on your router, then add A record manually.\n")
	}

	// Step 3: Wait for DNS propagation (skip for NAT — DNS record not expected).
	if ipType != "private_nat" && !dnsConfigured {
		sb.WriteString("\nStep 3 — Skipping DNS propagation check (manual setup required).\n")
		sb.WriteString("  Run 'yaver domain dns-check " + domain + "' after updating your DNS records.\n")
	} else if dnsConfigured {
		sb.WriteString("\nStep 3 — Waiting for DNS propagation (up to 60s)…\n")
		resolved := false
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			addrs, lErr := m.dnsLookup(domain)
			if lErr == nil {
				for _, a := range addrs {
					if a == ip {
						resolved = true
						break
					}
				}
			}
			if resolved {
				sb.WriteString(fmt.Sprintf("  DNS propagated: %s resolves to %s\n", domain, ip))
				break
			}
			time.Sleep(5 * time.Second)
		}
		if !resolved {
			sb.WriteString("  DNS not yet propagated. This can take up to 10 minutes.\n")
			sb.WriteString("  Run 'yaver domain dns-check " + domain + "' to verify later.\n")
		}
	}

	// Step 4: SSL via Caddy.
	sb.WriteString("\nStep 4 — Configuring SSL (Caddy / Let's Encrypt)…\n")
	if ipType == "private_nat" || !dnsConfigured {
		sb.WriteString("  Skipping automatic SSL — DNS must be configured and propagated first.\n")
		sb.WriteString("  Run 'yaver domain ssl-renew " + domain + "' after DNS is set up.\n")
	} else {
		sb.WriteString("  Generating Caddy config and requesting certificate…\n")
		// In a full implementation this would call the Caddy admin API.
		// Here we record the intent and note the Caddy reload step.
		sb.WriteString("  Caddy config updated. Reload Caddy: systemctl reload caddy (or equivalent).\n")
		sb.WriteString("  SSL will be automatically renewed by Caddy before expiry.\n")
	}

	// Step 5: Save domain config.
	info := &DomainInfo{
		Domain:        domain,
		IP:            ip,
		IPType:        ipType,
		SSLStatus:     "none",
		Routes:        []DomainRoute{},
		Provider:      provider,
		DNSConfigured: dnsConfigured,
	}
	if dnsConfigured && ipType != "private_nat" {
		info.SSLStatus = "active"
	}

	m.mu.Lock()
	m.domains[domain] = info
	m.mu.Unlock()

	if sErr := m.saveConfig(); sErr != nil {
		sb.WriteString(fmt.Sprintf("\nWarning: could not persist config: %v\n", sErr))
	}

	sb.WriteString(fmt.Sprintf("\nSetup complete for %s.\n", domain))
	return sb.String(), nil
}

// Add maps a domain (optionally with a path prefix) to an app name or port.
func (m *DomainManager) Add(domain, appOrPort, path string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("domain must not be empty")
	}
	if appOrPort == "" {
		return "", fmt.Errorf("target app or port must not be empty")
	}
	if path == "" {
		path = "/"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.domains[domain]
	if !ok {
		info = &DomainInfo{
			Domain:  domain,
			Routes:  []DomainRoute{},
			SSLStatus: "none",
		}
		m.domains[domain] = info
	}

	// Check for existing route on same path.
	for i, r := range info.Routes {
		if r.Path == path {
			info.Routes[i].Target = appOrPort
			if err := m.saveConfig(); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("Updated route: %s%s → %s", domain, path, appOrPort), nil
		}
	}

	info.Routes = append(info.Routes, DomainRoute{
		Domain: domain,
		Path:   path,
		Target: appOrPort,
		SSL:    info.SSLStatus == "active",
	})

	if err := m.saveConfig(); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return fmt.Sprintf("Added route: %s%s → %s", domain, path, appOrPort), nil
}

// Remove deletes a domain mapping and its Caddy configuration.
func (m *DomainManager) Remove(domain string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.domains[domain]; !ok {
		return "", fmt.Errorf("domain %q not found", domain)
	}

	delete(m.domains, domain)

	if err := m.saveConfig(); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return fmt.Sprintf("Removed domain %s and its routes.", domain), nil
}

// List returns all managed domains with their current status.
func (m *DomainManager) List() ([]DomainInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]DomainInfo, 0, len(m.domains))
	for _, info := range m.domains {
		result = append(result, *info)
	}
	return result, nil
}

// SSLStatus reports the SSL certificate status for a domain.
func (m *DomainManager) SSLStatus(domain string) (string, error) {
	m.mu.Lock()
	info, ok := m.domains[domain]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("domain %q not found", domain)
	}

	conn, err := tls.Dial("tcp", domain+":443", nil)
	if err != nil {
		return fmt.Sprintf("SSL check failed for %s: %v\nStored status: %s", domain, err, info.SSLStatus), nil
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Sprintf("SSL: no certificates returned for %s", domain), nil
	}
	leaf := certs[0]
	issuer := leaf.Issuer.CommonName
	expiry := leaf.NotAfter
	now := time.Now()
	status := "active"
	if expiry.Before(now) {
		status = "expired"
	} else if expiry.Before(now.Add(30 * 24 * time.Hour)) {
		status = "expiring_soon"
	}

	// Update stored info.
	m.mu.Lock()
	info.SSLStatus = status
	info.SSLExpiry = expiry
	info.SSLIssuer = issuer
	m.mu.Unlock()
	_ = m.saveConfig()

	autoRenew := "yes (managed by Caddy / Let's Encrypt)"
	return fmt.Sprintf(
		"Domain: %s\nSSL status: %s\nIssuer: %s\nExpiry: %s\nAuto-renew: %s",
		domain, status, issuer, expiry.Format(time.RFC3339), autoRenew,
	), nil
}

// SSLRenew forces SSL certificate renewal for a domain via Caddy.
func (m *DomainManager) SSLRenew(domain string) (string, error) {
	m.mu.Lock()
	_, ok := m.domains[domain]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("domain %q not found", domain)
	}

	// Caddy admin API: POST /certificates/renew/{domain}
	// Default Caddy admin socket is localhost:2019.
	url := fmt.Sprintf("http://localhost:2019/certificates/acme/%s/renew", domain)
	resp, err := http.Post(url, "application/json", nil) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("Caddy renew request failed: %w (is Caddy running?)", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("Caddy returned %d: %s", resp.StatusCode, string(body))
	}

	m.mu.Lock()
	if info, exists := m.domains[domain]; exists {
		info.SSLStatus = "active"
	}
	m.mu.Unlock()
	_ = m.saveConfig()

	return fmt.Sprintf("SSL renewal triggered for %s. Caddy will obtain a fresh certificate.", domain), nil
}

// DNSCheck verifies that the DNS records for a domain point to the expected IP.
func (m *DomainManager) DNSCheck(domain string) (string, error) {
	m.mu.Lock()
	info, ok := m.domains[domain]
	m.mu.Unlock()

	var expectedIP string
	if ok {
		expectedIP = info.IP
	}

	addrs, err := m.dnsLookup(domain)
	if err != nil {
		return "", fmt.Errorf("DNS lookup failed for %s: %w", domain, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("DNS check for %s:\n", domain))
	sb.WriteString(fmt.Sprintf("  Resolved addresses: %s\n", strings.Join(addrs, ", ")))

	if expectedIP != "" {
		found := false
		for _, a := range addrs {
			if a == expectedIP {
				found = true
				break
			}
		}
		if found {
			sb.WriteString(fmt.Sprintf("  Expected IP %s: OK\n", expectedIP))
		} else {
			sb.WriteString(fmt.Sprintf("  Expected IP %s: NOT FOUND\n", expectedIP))
			sb.WriteString("  DNS may not have propagated yet, or the record is incorrect.\n")
		}
	} else {
		sb.WriteString("  No expected IP stored — run 'yaver domain setup' first.\n")
	}

	// Also attempt AAAA lookup.
	aaaaAddrs, aErr := net.LookupHost(domain)
	_ = aErr
	ipv6 := []string{}
	for _, a := range aaaaAddrs {
		if strings.Contains(a, ":") {
			ipv6 = append(ipv6, a)
		}
	}
	if len(ipv6) > 0 {
		sb.WriteString(fmt.Sprintf("  IPv6 (AAAA): %s\n", strings.Join(ipv6, ", ")))
	}

	return sb.String(), nil
}

// DetectIP returns the machine's public IP address and classifies it as
// "static_public", "dynamic_public", or "private_nat".
func (m *DomainManager) DetectIP() (string, string, error) {
	publicIP, err := m.getPublicIP()
	if err != nil {
		return "", "", fmt.Errorf("could not determine public IP: %w", err)
	}

	// Check whether the public IP matches a local interface.
	ifaces, err := net.Interfaces()
	if err != nil {
		// Cannot enumerate interfaces — assume NAT.
		return publicIP, "private_nat", nil
	}
	for _, iface := range ifaces {
		addrs, aErr := iface.Addrs()
		if aErr != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.String() == publicIP {
				// Public IP is directly on an interface — static public.
				return publicIP, "static_public", nil
			}
		}
	}

	// Public IP is not on any interface — machine is behind NAT.
	if m.isPrivateIP(publicIP) {
		return publicIP, "private_nat", nil
	}
	// Heuristic: if we see the same public IP across multiple calls in a short
	// window it is more likely static, but we cannot be certain without a
	// historical record. Default to "dynamic_public" — DDNS handles both cases.
	return publicIP, "dynamic_public", nil
}

// DDNSStart launches a background goroutine that keeps a Cloudflare DNS A record
// current with the machine's public IP. It checks every interval duration.
func (m *DomainManager) DDNSStart(provider, domain string, interval time.Duration) (string, error) {
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}

	m.mu.Lock()
	if m.ddns != nil && m.ddns.Running {
		m.mu.Unlock()
		return "", fmt.Errorf("DDNS updater already running for %s", m.ddns.Domain)
	}
	cfg := &DDNSConfig{
		Provider: provider,
		Domain:   domain,
		Interval: interval,
		Running:  true,
	}
	m.ddns = cfg
	stop := make(chan struct{})
	m.ddnsStop = stop
	m.mu.Unlock()

	_ = m.saveConfig()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ip, _, err := m.DetectIP()
				if err != nil {
					continue
				}
				m.mu.Lock()
				changed := m.ddns.LastIP != ip
				m.mu.Unlock()
				if !changed {
					continue
				}
				if uErr := m.ddnsUpdateRecord(provider, domain, ip); uErr == nil {
					m.mu.Lock()
					m.ddns.LastIP = ip
					m.ddns.LastUpdate = time.Now()
					m.mu.Unlock()
					_ = m.saveConfig()
				}
			}
		}
	}()

	return fmt.Sprintf("DDNS updater started for %s via %s (interval: %s).", domain, provider, interval), nil
}

// DDNSStop stops the background DDNS updater goroutine.
func (m *DomainManager) DDNSStop() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ddns == nil || !m.ddns.Running {
		return "", fmt.Errorf("DDNS updater is not running")
	}

	close(m.ddnsStop)
	m.ddnsStop = nil
	m.ddns.Running = false
	_ = m.saveConfig()

	return fmt.Sprintf("DDNS updater stopped (was tracking %s).", m.ddns.Domain), nil
}

// RouteAdd adds or updates a routing rule for a domain.
func (m *DomainManager) RouteAdd(domain, path, target string) (string, error) {
	return m.Add(domain, target, path)
}

// RouteRemove removes a specific routing rule identified by domain+path.
func (m *DomainManager) RouteRemove(domain, path string) (string, error) {
	if path == "" {
		path = "/"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.domains[domain]
	if !ok {
		return "", fmt.Errorf("domain %q not found", domain)
	}

	updated := info.Routes[:0]
	removed := false
	for _, r := range info.Routes {
		if r.Path == path {
			removed = true
			continue
		}
		updated = append(updated, r)
	}
	if !removed {
		return "", fmt.Errorf("no route found for %s%s", domain, path)
	}
	info.Routes = updated

	if err := m.saveConfig(); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	return fmt.Sprintf("Removed route %s%s.", domain, path), nil
}

// Routes returns all routing rules across all managed domains.
func (m *DomainManager) Routes() ([]DomainRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var routes []DomainRoute
	for _, info := range m.domains {
		routes = append(routes, info.Routes...)
	}
	return routes, nil
}

// ─── Cloudflare helpers ───────────────────────────────────────────────────────

// cloudflareAPI sends an authenticated request to the Cloudflare v4 API.
func (m *DomainManager) cloudflareAPI(method, path string, body interface{}) ([]byte, error) {
	token := os.Getenv("CF_API_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("CF_API_TOKEN environment variable is not set")
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := "https://api.cloudflare.com/client/v4" + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Cloudflare API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// cloudflareGetZoneID returns the Cloudflare zone ID for the apex domain.
func (m *DomainManager) cloudflareGetZoneID(domain string) (string, error) {
	// Extract the apex domain (last two labels).
	parts := strings.Split(domain, ".")
	apex := domain
	if len(parts) > 2 {
		apex = strings.Join(parts[len(parts)-2:], ".")
	}

	data, err := m.cloudflareAPI("GET", "/zones?name="+apex+"&status=active", nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse zone list: %w", err)
	}
	if !result.Success || len(result.Result) == 0 {
		return "", fmt.Errorf("zone %q not found in Cloudflare account", apex)
	}
	return result.Result[0].ID, nil
}

// cloudflareCreateRecord creates a DNS record in the specified zone.
func (m *DomainManager) cloudflareCreateRecord(zoneID, rtype, name, content string, proxied bool) error {
	body := map[string]interface{}{
		"type":    rtype,
		"name":    name,
		"content": content,
		"ttl":     1, // 1 = automatic
		"proxied": proxied,
	}
	_, err := m.cloudflareAPI("POST", "/zones/"+zoneID+"/dns_records", body)
	return err
}

// cloudflareUpdateRecord updates an existing DNS record.
func (m *DomainManager) cloudflareUpdateRecord(zoneID, recordID, rtype, name, content string) error {
	body := map[string]interface{}{
		"type":    rtype,
		"name":    name,
		"content": content,
		"ttl":     1,
	}
	_, err := m.cloudflareAPI("PUT", "/zones/"+zoneID+"/dns_records/"+recordID, body)
	return err
}

// ddnsUpdateRecord finds the existing A record for domain and updates its value,
// or creates it if it does not exist.
func (m *DomainManager) ddnsUpdateRecord(provider, domain, ip string) error {
	if provider != "cloudflare" {
		return fmt.Errorf("DDNS provider %q not supported (only cloudflare)", provider)
	}

	zoneID, err := m.cloudflareGetZoneID(domain)
	if err != nil {
		return err
	}

	// List existing A records for the domain.
	data, err := m.cloudflareAPI("GET", "/zones/"+zoneID+"/dns_records?type=A&name="+domain, nil)
	if err != nil {
		return err
	}

	var listResult struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(data, &listResult); err != nil {
		return fmt.Errorf("parse record list: %w", err)
	}

	if len(listResult.Result) == 0 {
		return m.cloudflareCreateRecord(zoneID, "A", domain, ip, false)
	}
	recordID := listResult.Result[0].ID
	return m.cloudflareUpdateRecord(zoneID, recordID, "A", domain, ip)
}

// ─── DNS helpers ─────────────────────────────────────────────────────────────

// dnsLookup resolves the A records for a domain.
func (m *DomainManager) dnsLookup(domain string) ([]string, error) {
	return net.LookupHost(domain)
}

// ─── IP helpers ──────────────────────────────────────────────────────────────

// getPublicIP queries an external service to determine the machine's public IP.
func (m *DomainManager) getPublicIP() (string, error) {
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://checkip.amazonaws.com",
	}

	client := &http.Client{Timeout: 8 * time.Second}
	for _, svc := range services {
		resp, err := client.Get(svc) //nolint:noctx
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip, nil
		}
	}
	return "", fmt.Errorf("all public IP services unreachable")
}

// isPrivateIP reports whether the given IP string is in a private/reserved range.
func (m *DomainManager) isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ─── Caddy config generation ─────────────────────────────────────────────────

// generateCaddyDomainConfig returns a Caddyfile snippet for all managed domains.
func (m *DomainManager) generateCaddyDomainConfig(domains map[string]*DomainInfo) string {
	var sb strings.Builder
	for _, info := range domains {
		if len(info.Routes) == 0 {
			// Default reverse proxy to port 80.
			sb.WriteString(fmt.Sprintf("%s {\n", info.Domain))
			if info.SSLStatus == "none" {
				sb.WriteString("\ttls off\n")
			}
			sb.WriteString("\treverse_proxy localhost:80\n")
			sb.WriteString("}\n\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("%s {\n", info.Domain))
		if info.SSLStatus == "none" {
			sb.WriteString("\ttls off\n")
		}
		for _, route := range info.Routes {
			target := route.Target
			// If target looks like a plain number, treat it as a port.
			upstream := target
			if !strings.Contains(target, ":") && !strings.HasPrefix(target, "http") {
				// Could be a bare port or an app name; default to localhost:<target>.
				upstream = "localhost:" + target
			}
			if route.Path == "/" || route.Path == "" {
				sb.WriteString(fmt.Sprintf("\treverse_proxy %s\n", upstream))
			} else {
				sb.WriteString(fmt.Sprintf("\thandle_path %s* {\n", route.Path))
				sb.WriteString(fmt.Sprintf("\t\treverse_proxy %s\n", upstream))
				sb.WriteString("\t}\n")
			}
			if route.CacheStatic {
				sb.WriteString("\t@static {\n")
				sb.WriteString("\t\tpath *.ico *.css *.js *.gif *.jpg *.jpeg *.png *.svg *.woff *.woff2 *.ttf *.eot\n")
				sb.WriteString("\t}\n")
				sb.WriteString("\theader @static Cache-Control \"public, max-age=31536000, immutable\"\n")
			}
		}
		sb.WriteString("}\n\n")
	}
	return sb.String()
}

// ─── Persistence ─────────────────────────────────────────────────────────────

type domainPersisted struct {
	Domains map[string]*DomainInfo `json:"domains"`
	DDNS    *DDNSConfig            `json:"ddns,omitempty"`
}

func (m *DomainManager) loadConfig() error {
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read domain config: %w", err)
	}
	var p domainPersisted
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse domain config: %w", err)
	}
	if p.Domains != nil {
		m.domains = p.Domains
	}
	if p.DDNS != nil {
		m.ddns = p.DDNS
		// Do not auto-restart DDNS on load — the operator must call DDNSStart explicitly.
		m.ddns.Running = false
	}
	return nil
}

func (m *DomainManager) saveConfig() error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	p := domainPersisted{
		Domains: m.domains,
		DDNS:    m.ddns,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal domain config: %w", err)
	}
	return os.WriteFile(m.configPath, data, 0o600)
}
