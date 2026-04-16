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
	target := resolveDeviceURL(cfg, deviceHint, true)
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", target+path, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote %s: %v\n", path, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "remote %s: HTTP %d — %s\n", path, resp.StatusCode, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// remoteYaverGET is the GET sibling of remoteYaverPOST. Same exit
// semantics; query string already baked into path by caller.
func remoteYaverGET(deviceHint, path string) map[string]interface{} {
	if strings.TrimSpace(deviceHint) == "" {
		fmt.Fprintln(os.Stderr, "remote: --to requires a device id / hostname")
		os.Exit(2)
	}
	cfg := mustLoadAuthConfig()
	target := resolveDeviceURL(cfg, deviceHint, true)
	req, _ := http.NewRequest("GET", target+path, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote %s: %v\n", path, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "remote %s: HTTP %d — %s\n", path, resp.StatusCode, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}
