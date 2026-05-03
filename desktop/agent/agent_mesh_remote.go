package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type RemoteAgentCandidate struct {
	DeviceID string
	BaseURL  string
	Kind     string
	Label    string
	Headers  map[string]string
}

var remoteAgentLastGood sync.Map // deviceID -> baseURL
var remoteAgentHealth sync.Map   // deviceID|baseURL -> *remoteAgentHealthState
var remoteAgentProbe sync.Map    // deviceID|baseURL -> *remoteAgentProbeState

type remoteAgentHealthState struct {
	LastSuccess time.Time
	LastFailure time.Time
	Successes   int
	Failures    int
}

type remoteAgentProbeState struct {
	CheckedAt time.Time
	Healthy   bool
	Latency   time.Duration
}

func remoteAgentHealthKey(deviceID, baseURL string) string {
	return strings.TrimSpace(deviceID) + "|" + strings.TrimSpace(baseURL)
}

func transportKindRank(kind string) int {
	switch kind {
	case "same-lan":
		return 0
	case "tailscale":
		return 1
	case "direct":
		return 2
	case "cloudflare-tunnel":
		return 3
	case "hostname":
		return 4
	case "relay":
		return 5
	default:
		return 6
	}
}

func loadRemoteAgentHealth(deviceID, baseURL string) remoteAgentHealthState {
	if v, ok := remoteAgentHealth.Load(remoteAgentHealthKey(deviceID, baseURL)); ok {
		if st, ok := v.(*remoteAgentHealthState); ok && st != nil {
			return *st
		}
	}
	return remoteAgentHealthState{}
}

func loadRemoteAgentProbe(deviceID, baseURL string) remoteAgentProbeState {
	if v, ok := remoteAgentProbe.Load(remoteAgentHealthKey(deviceID, baseURL)); ok {
		if st, ok := v.(*remoteAgentProbeState); ok && st != nil {
			return *st
		}
	}
	return remoteAgentProbeState{}
}

func recordRemoteAgentProbe(deviceID, baseURL string, healthy bool, latency time.Duration, checkedAt time.Time) {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(baseURL) == "" {
		return
	}
	remoteAgentProbe.Store(remoteAgentHealthKey(deviceID, baseURL), &remoteAgentProbeState{
		CheckedAt: checkedAt,
		Healthy:   healthy,
		Latency:   latency,
	})
}

func recordRemoteAgentSuccess(deviceID, baseURL string, now time.Time) {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(baseURL) == "" {
		return
	}
	key := remoteAgentHealthKey(deviceID, baseURL)
	state := loadRemoteAgentHealth(deviceID, baseURL)
	state.LastSuccess = now
	state.Successes++
	if state.Failures > 0 {
		state.Failures--
	}
	remoteAgentHealth.Store(key, &state)
}

func recordRemoteAgentFailure(deviceID, baseURL string, now time.Time) {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(baseURL) == "" {
		return
	}
	key := remoteAgentHealthKey(deviceID, baseURL)
	state := loadRemoteAgentHealth(deviceID, baseURL)
	state.LastFailure = now
	state.Failures++
	remoteAgentHealth.Store(key, &state)
}

func remoteAgentCandidateScore(c RemoteAgentCandidate, now time.Time) int {
	score := transportKindRank(c.Kind) * 100
	st := loadRemoteAgentHealth(c.DeviceID, c.BaseURL)
	score += st.Failures * 40
	score -= st.Successes * 5
	if !st.LastFailure.IsZero() && now.Sub(st.LastFailure) < 2*time.Minute {
		score += 200
	}
	if !st.LastSuccess.IsZero() && now.Sub(st.LastSuccess) < 10*time.Minute {
		score -= 80
	}
	probe := loadRemoteAgentProbe(c.DeviceID, c.BaseURL)
	if !probe.CheckedAt.IsZero() && now.Sub(probe.CheckedAt) < 30*time.Second {
		if probe.Healthy {
			score -= 60
			score += min(80, int(probe.Latency/(25*time.Millisecond)))
		} else {
			score += 160
		}
	}
	return score
}

func orderRemoteAgentCandidates(candidates []RemoteAgentCandidate) {
	now := time.Now()
	sort.SliceStable(candidates, func(i, j int) bool {
		iLast, iOK := remoteAgentLastGood.Load(strings.TrimSpace(candidates[i].DeviceID))
		jLast, jOK := remoteAgentLastGood.Load(strings.TrimSpace(candidates[j].DeviceID))
		if iOK && strings.TrimSpace(fmt.Sprint(iLast)) == candidates[i].BaseURL {
			if !(jOK && strings.TrimSpace(fmt.Sprint(jLast)) == candidates[j].BaseURL) {
				return true
			}
		}
		if jOK && strings.TrimSpace(fmt.Sprint(jLast)) == candidates[j].BaseURL {
			if !(iOK && strings.TrimSpace(fmt.Sprint(iLast)) == candidates[i].BaseURL) {
				return false
			}
		}
		iScore := remoteAgentCandidateScore(candidates[i], now)
		jScore := remoteAgentCandidateScore(candidates[j], now)
		if iScore != jScore {
			return iScore < jScore
		}
		return candidates[i].BaseURL < candidates[j].BaseURL
	})
}

func probeRemoteAgentCandidate(candidate RemoteAgentCandidate, timeout time.Duration) {
	if strings.TrimSpace(candidate.BaseURL) == "" {
		return
	}
	client := remoteHTTPClient(timeout)
	start := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(candidate.BaseURL, "/")+"/health", nil)
	if err != nil {
		recordRemoteAgentProbe(candidate.DeviceID, candidate.BaseURL, false, 0, time.Now())
		return
	}
	for k, v := range candidate.Headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		recordRemoteAgentProbe(candidate.DeviceID, candidate.BaseURL, false, 0, time.Now())
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	recordRemoteAgentProbe(candidate.DeviceID, candidate.BaseURL, resp.StatusCode < 500, time.Since(start), time.Now())
}

func maybeRefreshRemoteAgentProbes(candidates []RemoteAgentCandidate) {
	if len(candidates) <= 1 {
		return
	}
	now := time.Now()
	const maxProbe = 4
	probeList := make([]RemoteAgentCandidate, 0, min(maxProbe, len(candidates)))
	for _, candidate := range candidates {
		probe := loadRemoteAgentProbe(candidate.DeviceID, candidate.BaseURL)
		if !probe.CheckedAt.IsZero() && now.Sub(probe.CheckedAt) < 30*time.Second {
			continue
		}
		probeList = append(probeList, candidate)
		if len(probeList) >= maxProbe {
			break
		}
	}
	var wg sync.WaitGroup
	for _, candidate := range probeList {
		wg.Add(1)
		go func(c RemoteAgentCandidate) {
			defer wg.Done()
			probeRemoteAgentCandidate(c, 1500*time.Millisecond)
		}(candidate)
	}
	wg.Wait()
}

func remoteAgentBaseAndToken(deviceHint string) (string, string, error) {
	candidates, token, err := resolveRemoteAgentCandidates(deviceHint)
	if err != nil {
		return "", "", err
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("device %q has no reachable transport candidates", deviceHint)
	}
	return candidates[0].BaseURL, token, nil
}

func resolveRemoteAgentCandidates(deviceHint string) ([]RemoteAgentCandidate, string, error) {
	deviceHint = normalizeDeviceHint(deviceHint)
	if strings.TrimSpace(deviceHint) == "" {
		return nil, "", fmt.Errorf("remote device id required")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, "", fmt.Errorf("missing auth token")
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return nil, "", fmt.Errorf("missing convex site url")
	}

	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, "", fmt.Errorf("list devices: %w", err)
	}
	var target *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if strings.HasPrefix(d.DeviceID, deviceHint) ||
			strings.EqualFold(d.Name, deviceHint) ||
			strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(deviceHint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.EqualFold(d.Alias, deviceHint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.HasPrefix(strings.ToLower(d.Alias), strings.ToLower(deviceHint))) {
			target = d
			break
		}
	}
	if target == nil {
		return nil, "", fmt.Errorf("device %q not found", deviceHint)
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return nil, "", err
	}
	if len(candidates) == 0 {
		if !target.IsOnline {
			return nil, "", fmt.Errorf("device %q is offline", target.Name)
		}
		return nil, "", fmt.Errorf("device %q has no reachable host", target.Name)
	}
	return candidates, cfg.AuthToken, nil
}

func buildRemoteAgentCandidates(cfg *Config, target *DeviceInfo) ([]RemoteAgentCandidate, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required")
	}
	if target == nil {
		return nil, fmt.Errorf("target device required")
	}
	seen := make(map[string]bool)
	candidates := make([]RemoteAgentCandidate, 0, 8)
	add := func(c RemoteAgentCandidate) {
		c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
		if c.BaseURL == "" || seen[c.BaseURL] {
			return
		}
		seen[c.BaseURL] = true
		candidates = append(candidates, c)
	}

	for _, direct := range directAgentBaseCandidates(target) {
		headers, _ := transportHeadersForBase(cfg, direct)
		add(RemoteAgentCandidate{
			DeviceID: target.DeviceID,
			BaseURL:  direct,
			Kind:     classifyRemoteBaseKind(direct),
			Label:    "direct",
			Headers:  headers,
		})
	}
	for _, publicBase := range publicAgentBaseCandidates(target) {
		headers, _ := transportHeadersForBase(cfg, publicBase)
		add(RemoteAgentCandidate{
			DeviceID: target.DeviceID,
			BaseURL:  publicBase,
			Kind:     classifyRemoteBaseKind(publicBase),
			Label:    "public",
			Headers:  headers,
		})
	}

	relayInfos := collectRemoteRelayConfigs(cfg)
	sort.SliceStable(relayInfos, func(i, j int) bool {
		if relayInfos[i].Priority != relayInfos[j].Priority {
			if relayInfos[i].Priority == 0 {
				return false
			}
			if relayInfos[j].Priority == 0 {
				return true
			}
			return relayInfos[i].Priority < relayInfos[j].Priority
		}
		return relayInfos[i].HttpURL < relayInfos[j].HttpURL
	})
	for _, relay := range relayInfos {
		if strings.TrimSpace(relay.HttpURL) == "" {
			continue
		}
		base := strings.TrimRight(relay.HttpURL, "/") + "/d/" + target.DeviceID
		headers, _ := transportHeadersForBase(cfg, base)
		add(RemoteAgentCandidate{
			DeviceID: target.DeviceID,
			BaseURL:  base,
			Kind:     "relay",
			Label:    firstNonEmpty(strings.TrimSpace(relay.Label), strings.TrimSpace(relay.ID), strings.TrimSpace(relay.Region), "relay"),
			Headers:  headers,
		})
	}

	orderRemoteAgentCandidates(candidates)
	maybeRefreshRemoteAgentProbes(candidates)
	orderRemoteAgentCandidates(candidates)

	return candidates, nil
}

func directAgentBaseCandidates(target *DeviceInfo) []string {
	if target == nil {
		return nil
	}
	port := target.QuicPort
	if port <= 0 {
		port = 18080
	}
	var hosts []string
	if host := strings.TrimSpace(target.QuicHost); host != "" {
		hosts = append(hosts, host)
	}
	for _, host := range target.LocalIps {
		host = strings.TrimSpace(host)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(base string) {
		base = strings.TrimSpace(base)
		if base == "" || seen[base] {
			return
		}
		seen[base] = true
		out = append(out, base)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		if ip != nil {
			if ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			if ip.To4() != nil {
				add(fmt.Sprintf("http://%s:%d", host, port))
			} else {
				add(fmt.Sprintf("http://[%s]:%d", host, port))
			}
			continue
		}
		if strings.HasSuffix(strings.ToLower(host), ".local") {
			add(fmt.Sprintf("http://%s:%d", host, port))
			continue
		}
		add(fmt.Sprintf("https://%s", host))
		add(fmt.Sprintf("http://%s:%d", host, port))
	}
	return out
}

func publicAgentBaseCandidates(target *DeviceInfo) []string {
	if target == nil || len(target.PublicEndpoints) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(target.PublicEndpoints))
	add := func(base string) {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" || seen[base] {
			return
		}
		seen[base] = true
		out = append(out, base)
	}
	port := target.QuicPort
	if port <= 0 {
		port = 18080
	}
	for _, endpoint := range target.PublicEndpoints {
		base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if base == "" {
			continue
		}
		// Already a fully-qualified URL — pass through.
		if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
			add(base)
			continue
		}
		// Bare host (e.g. "198.51.100.20" set via config.public_endpoints
		// for SSH discovery). Synthesize the agent's default HTTP URL so
		// remote callers actually have something to dial. The web UI's
		// SSH copy still works against the same bare-host string because
		// it does its own URL stripping on the device-list payload.
		add(fmt.Sprintf("http://%s:%d", base, port))
	}
	return out
}

func classifyRemoteBaseKind(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "unknown"
	}
	host := u.Hostname()
	if host == "" {
		return "unknown"
	}
	if strings.Contains(u.Path, "/d/") {
		return "relay"
	}
	if strings.Contains(strings.ToLower(host), "trycloudflare.com") {
		return "cloudflare-tunnel"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if strings.HasSuffix(strings.ToLower(host), ".local") {
			return "same-lan"
		}
		return "hostname"
	}
	switch {
	case tailscaleCGNAT.Contains(ip):
		return "tailscale"
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return "same-lan"
	default:
		return "direct"
	}
}

func collectRemoteRelayConfigs(cfg *Config) []RelayServerConfig {
	var out []RelayServerConfig
	seen := make(map[string]bool)
	add := func(in []RelayServerConfig) {
		for _, relay := range in {
			key := strings.TrimRight(strings.TrimSpace(relay.HttpURL), "/")
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, relay)
		}
	}
	add(cfg.RelayServers)
	add(cfg.CachedRelayServers)
	if strings.TrimSpace(cfg.ConvexSiteURL) != "" {
		if relays, err := FetchRelayServers(cfg.ConvexSiteURL); err == nil {
			tmp := make([]RelayServerConfig, 0, len(relays))
			for _, relay := range relays {
				tmp = append(tmp, RelayServerConfig{
					ID:       relay.ID,
					QuicAddr: relay.QuicAddr,
					HttpURL:  relay.HttpURL,
					Region:   relay.Region,
					Priority: relay.Priority,
				})
			}
			add(tmp)
		}
	}
	return out
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func relayPasswordForBase(baseURL string) (string, error) {
	headers, err := transportHeadersForBase(nil, baseURL)
	if err != nil {
		return "", err
	}
	return headers["X-Relay-Password"], nil
}

func transportHeadersForBase(cfg *Config, baseURL string) (map[string]string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return map[string]string{}, nil
	}
	if cfg == nil {
		var err error
		cfg, err = LoadConfig()
		if err != nil || cfg == nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse remote url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") && !isLoopbackHost(u.Hostname()) && !strings.EqualFold(u.Scheme, "http") {
		return nil, fmt.Errorf("unsupported remote url scheme %q", u.Scheme)
	}
	headers := make(map[string]string)
	origin := strings.TrimRight(u.Scheme+"://"+u.Host, "/")

	for _, tunnel := range cfg.CloudflareTunnels {
		if strings.TrimRight(strings.TrimSpace(tunnel.URL), "/") != origin {
			continue
		}
		if tunnel.CFAccessClientId != "" {
			headers["CF-Access-Client-Id"] = tunnel.CFAccessClientId
		}
		if tunnel.CFAccessClientSecret != "" {
			headers["CF-Access-Client-Secret"] = tunnel.CFAccessClientSecret
		}
		break
	}

	if !strings.Contains(baseURL, "/d/") {
		return headers, nil
	}
	if !strings.EqualFold(u.Scheme, "https") && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("refusing insecure relay url %q", baseURL)
	}
	for _, relay := range cfg.RelayServers {
		if strings.TrimRight(relay.HttpURL, "/") == origin {
			if relay.Password != "" {
				headers["X-Relay-Password"] = relay.Password
				return headers, nil
			}
			if cfg.RelayPassword != "" {
				headers["X-Relay-Password"] = cfg.RelayPassword
				return headers, nil
			}
			return nil, fmt.Errorf("missing relay password for %s", origin)
		}
	}
	for _, relay := range cfg.CachedRelayServers {
		if strings.TrimRight(relay.HttpURL, "/") == origin {
			if relay.Password != "" {
				headers["X-Relay-Password"] = relay.Password
				return headers, nil
			}
			if cfg.CachedRelayPassword != "" {
				headers["X-Relay-Password"] = cfg.CachedRelayPassword
				return headers, nil
			}
			return nil, fmt.Errorf("missing relay password for %s", origin)
		}
	}
	if cfg.ConvexSiteURL != "" {
		if relays, err := FetchRelayServers(cfg.ConvexSiteURL); err == nil {
			for _, relay := range relays {
				if strings.TrimRight(relay.HttpURL, "/") != origin {
					continue
				}
				if cfg.CachedRelayPassword != "" {
					headers["X-Relay-Password"] = cfg.CachedRelayPassword
					return headers, nil
				}
				if cfg.RelayPassword != "" {
					headers["X-Relay-Password"] = cfg.RelayPassword
					return headers, nil
				}
				return nil, fmt.Errorf("missing relay password for %s", origin)
			}
		}
	}
	return nil, fmt.Errorf("relay origin %s is not trusted", origin)
}

func remoteHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func doRemoteAgentRequest(ctx context.Context, candidates []RemoteAgentCandidate, token, method, path string, bodyJSON []byte, timeout time.Duration) (RemoteAgentCandidate, int, []byte, error) {
	if len(candidates) == 0 {
		return RemoteAgentCandidate{}, 0, nil, fmt.Errorf("no remote transport candidates")
	}
	if method == "" {
		method = http.MethodPost
	}
	client := remoteHTTPClient(timeout)
	orderRemoteAgentCandidates(candidates)
	var errs []string
	for _, candidate := range candidates {
		var reader io.Reader
		if len(bodyJSON) > 0 {
			reader = bytes.NewReader(bodyJSON)
		}
		url := strings.TrimRight(candidate.BaseURL, "/") + path
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate.BaseURL, err))
			continue
		}
		if strings.TrimSpace(token) != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range candidate.Headers {
			if strings.TrimSpace(v) != "" {
				req.Header.Set(k, v)
			}
		}
		if len(bodyJSON) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Yaver-Proxied-By", localDeviceID())
		req.Header.Set("X-Yaver-Proxied-Tool", firstNonEmpty(req.Header.Get("X-Yaver-Proxied-Tool"), "remote-request"))

		resp, err := client.Do(req)
		if err != nil {
			recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
			errs = append(errs, fmt.Sprintf("%s: %v", candidate.BaseURL, err))
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
			errs = append(errs, fmt.Sprintf("%s: read response: %v", candidate.BaseURL, readErr))
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode == http.StatusServiceUnavailable {
			recordRemoteAgentFailure(candidate.DeviceID, candidate.BaseURL, time.Now())
			msg := strings.TrimSpace(string(raw))
			if msg == "" {
				msg = http.StatusText(resp.StatusCode)
			}
			errs = append(errs, fmt.Sprintf("%s: HTTP %d: %s", candidate.BaseURL, resp.StatusCode, msg))
			continue
		}
		if strings.TrimSpace(candidate.DeviceID) != "" {
			remoteAgentLastGood.Store(candidate.DeviceID, candidate.BaseURL)
		}
		recordRemoteAgentSuccess(candidate.DeviceID, candidate.BaseURL, time.Now())
		return candidate, resp.StatusCode, raw, nil
	}
	return RemoteAgentCandidate{}, 0, nil, fmt.Errorf("remote %s %s failed across %d candidate(s): %s", method, path, len(candidates), strings.Join(errs, " | "))
}

func remoteAgentJSON(ctx context.Context, baseURL, token, method, path string, body any, out any) error {
	var bodyJSON []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyJSON = data
	}
	headers, err := transportHeadersForBase(nil, baseURL)
	if err != nil {
		return err
	}
	candidate := RemoteAgentCandidate{
		DeviceID: "",
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Kind:     classifyRemoteBaseKind(baseURL),
		Headers:  headers,
	}
	_, status, raw, err := doRemoteAgentRequest(ctx, []RemoteAgentCandidate{candidate}, token, method, path, bodyJSON, 60*time.Second)
	if err != nil {
		return err
	}
	if status >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(status)
		}
		return fmt.Errorf("remote %s %s failed: HTTP %d: %s", method, path, status, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func remoteAgentJSONForDevice(ctx context.Context, deviceHint, method, path string, body any, out any) error {
	candidates, token, err := resolveRemoteAgentCandidates(deviceHint)
	if err != nil {
		return err
	}
	var bodyJSON []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyJSON = data
	}
	_, status, raw, err := doRemoteAgentRequest(ctx, candidates, token, method, path, bodyJSON, 60*time.Second)
	if err != nil {
		return err
	}
	if status >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(status)
		}
		return fmt.Errorf("remote %s %s failed: HTTP %d: %s", method, path, status, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
