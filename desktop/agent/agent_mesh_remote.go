package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func remoteAgentBaseAndToken(deviceHint string) (string, string, error) {
	if strings.TrimSpace(deviceHint) == "" {
		return "", "", fmt.Errorf("remote device id required")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return "", "", fmt.Errorf("missing auth token")
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return "", "", fmt.Errorf("missing convex site url")
	}

	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return "", "", fmt.Errorf("list devices: %w", err)
	}
	var target *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if strings.HasPrefix(d.DeviceID, deviceHint) ||
			strings.EqualFold(d.Name, deviceHint) ||
			strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(deviceHint)) {
			target = d
			break
		}
	}
	if target == nil {
		return "", "", fmt.Errorf("device %q not found", deviceHint)
	}
	if !target.IsOnline {
		return "", "", fmt.Errorf("device %q is offline", target.Name)
	}

	if relays, err := FetchRelayServers(cfg.ConvexSiteURL); err == nil {
		for _, r := range relays {
			if strings.TrimSpace(r.HttpURL) != "" {
				return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID, cfg.AuthToken, nil
			}
		}
	}
	for _, r := range cfg.RelayServers {
		if strings.TrimSpace(r.HttpURL) != "" {
			return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID, cfg.AuthToken, nil
		}
	}
	for _, r := range cfg.CachedRelayServers {
		if strings.TrimSpace(r.HttpURL) != "" {
			return strings.TrimRight(r.HttpURL, "/") + "/d/" + target.DeviceID, cfg.AuthToken, nil
		}
	}
	if strings.TrimSpace(target.QuicHost) == "" {
		return "", "", fmt.Errorf("device %q has no reachable host", target.Name)
	}
	return fmt.Sprintf("http://%s:18080", target.QuicHost), cfg.AuthToken, nil
}

func relayPasswordForBase(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || !strings.Contains(baseURL, "/d/") {
		return ""
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return ""
	}
	if cfg.RelayPassword != "" {
		return cfg.RelayPassword
	}
	if cfg.CachedRelayPassword != "" {
		return cfg.CachedRelayPassword
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	origin := strings.TrimRight(u.Scheme+"://"+u.Host, "/")
	for _, relay := range cfg.RelayServers {
		if strings.TrimRight(relay.HttpURL, "/") == origin && relay.Password != "" {
			return relay.Password
		}
	}
	for _, relay := range cfg.CachedRelayServers {
		if strings.TrimRight(relay.HttpURL, "/") == origin && relay.Password != "" {
			return relay.Password
		}
	}
	if cfg.ConvexSiteURL != "" {
		if relays, err := FetchRelayServers(cfg.ConvexSiteURL); err == nil {
			for _, relay := range relays {
				if strings.TrimRight(relay.HttpURL, "/") == origin {
					if cfg.CachedRelayPassword != "" {
						return cfg.CachedRelayPassword
					}
					if cfg.RelayPassword != "" {
						return cfg.RelayPassword
					}
				}
			}
		}
	}
	return ""
}

func remoteAgentJSON(ctx context.Context, baseURL, token, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+path, bodyReader)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if relayPassword := relayPasswordForBase(baseURL); relayPassword != "" {
		req.Header.Set("X-Relay-Password", relayPassword)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("remote %s %s failed: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
