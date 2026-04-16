package main

// remote_yaver.go — yaver-to-yaver helpers. CLI commands that
// support `--to <device>` (autoideas / autodev / autoinit) route
// through here so they all use the same device resolution +
// transport (P2P or relay), match the auth headers handoff uses,
// and surface a uniform error message.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func remoteYaverTargets(cfg *Config, deviceHint string) []string {
	targets := []string{}
	seen := map[string]bool{}
	add := func(url string) {
		url = strings.TrimSpace(url)
		if url == "" || seen[url] {
			return
		}
		seen[url] = true
		targets = append(targets, url)
	}
	add(resolveDeviceURL(cfg, deviceHint, true))
	add(resolveDeviceURL(cfg, deviceHint, false))
	return targets
}

// remoteYaverPOST fires a JSON POST against the named device's
// daemon at <baseURL><path> and returns the parsed body. Best-
// effort: missing device hint, unauthenticated config, or a 5xx
// response prints to stderr + os.Exit(1) so CLI callers get a
// clean failure surface (matching the handoff CLI shape).
func remoteYaverPOST(deviceHint, path string, body map[string]interface{}) map[string]interface{} {
	if strings.TrimSpace(deviceHint) == "" {
		fmt.Fprintln(os.Stderr, "remote: --to requires a device id / hostname")
		os.Exit(2)
	}
	cfg := mustLoadAuthConfig()
	payload, _ := json.Marshal(body)
	client := &http.Client{Timeout: 60 * time.Second}
	var lastErr string
	for _, target := range remoteYaverTargets(cfg, deviceHint) {
		req, _ := http.NewRequest("POST", target+path, strings.NewReader(string(payload)))
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			lastErr = fmt.Sprintf("HTTP %d — %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			continue
		}
		var out map[string]interface{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if lastErr == "" {
		lastErr = "no reachable target"
	}
	fmt.Fprintf(os.Stderr, "remote %s: %s\n", path, lastErr)
	os.Exit(1)
	return nil
}

// remoteYaverGET is the GET sibling of remoteYaverPOST. Same exit
// semantics; query string already baked into path by caller.
func remoteYaverGET(deviceHint, path string) map[string]interface{} {
	if strings.TrimSpace(deviceHint) == "" {
		fmt.Fprintln(os.Stderr, "remote: --to requires a device id / hostname")
		os.Exit(2)
	}
	cfg := mustLoadAuthConfig()
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr string
	for _, target := range remoteYaverTargets(cfg, deviceHint) {
		req, _ := http.NewRequest("GET", target+path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			lastErr = fmt.Sprintf("HTTP %d — %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			continue
		}
		var out map[string]interface{}
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if lastErr == "" {
		lastErr = "no reachable target"
	}
	fmt.Fprintf(os.Stderr, "remote %s: %s\n", path, lastErr)
	os.Exit(1)
	return nil
}
