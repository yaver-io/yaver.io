package main

// managed.go — thin client for the Convex-backed per-subsystem
// managed toggle. Backs the `managed_get`/`managed_set` MCP tools
// and the `yaver managed` CLI subcommand. The actual source of truth
// is userSettings.managed in Convex (see backend/convex/schema.ts);
// this file is the shim that lets agent code reach it without
// reimplementing the /settings HTTP handshake.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ValidManagedSubsystems is the single source of truth for which
// subsystems participate in the toggle. Keep in sync with
// backend/convex/schema.ts userSettings.managed + the MCP schema's
// enum. Adding a new subsystem is intentionally a two-line change
// here + in schema.ts so the CLI/UI pick it up automatically.
var ValidManagedSubsystems = []string{
	"relay", "dns", "analytics", "storage", "email", "ci", "voice", "llm",
}

func isValidManagedSubsystem(name string) bool {
	for _, s := range ValidManagedSubsystems {
		if s == name {
			return true
		}
	}
	return false
}

// fetchManagedSettings returns the managed toggle block from Convex
// as a pretty JSON payload suitable for MCP/CLI echo. Missing
// subsystems mean "not set" (legacy behaviour). We don't unmarshal
// into a typed struct here because the set of keys evolves with
// the schema — passing through verbatim keeps the CLI correct
// without a rebuild every time a new subsystem is added.
func fetchManagedSettings(ctx context.Context, convexURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/settings", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/settings: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Settings struct {
			Managed map[string]interface{} `json:"managed"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"managed":    parsed.Settings.Managed,
		"subsystems": ValidManagedSubsystems,
		"hint":       "missing subsystems are not set — the feature uses its legacy default. true=Yaver-managed, false=self-hosted.",
	}
	return json.MarshalIndent(out, "", "  ")
}

// setManagedSubsystem patches one subsystem's managed flag.
// rawValue is the raw JSON for the value: `true`, `false`, or `null`
// (any other shape is rejected). Null clears the subsystem's
// preference; booleans set it explicitly.
func setManagedSubsystem(ctx context.Context, convexURL, token, subsystem string, rawValue json.RawMessage) error {
	if !isValidManagedSubsystem(subsystem) {
		return fmt.Errorf("unknown subsystem %q (valid: %v)", subsystem, ValidManagedSubsystems)
	}
	// Validate the value shape. Accept empty to mean null.
	trimmed := bytesTrim(rawValue)
	var value interface{}
	switch {
	case len(trimmed) == 0 || string(trimmed) == "null":
		value = nil
	case string(trimmed) == "true":
		value = true
	case string(trimmed) == "false":
		value = false
	default:
		// Also accept JSON-encoded strings for CLI ergonomics:
		// {"managed":"true"} behaves like {"managed":true}.
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			switch s {
			case "true", "yes", "on":
				value = true
			case "false", "no", "off":
				value = false
			case "null", "clear", "unset", "":
				value = nil
			default:
				return fmt.Errorf("managed must be true, false, or null (got %q)", s)
			}
		} else {
			return fmt.Errorf("managed must be true, false, or null (got %s)", string(trimmed))
		}
	}

	patch := map[string]interface{}{
		"managed": map[string]interface{}{subsystem: value},
	}
	body, _ := json.Marshal(patch)
	req, err := http.NewRequestWithContext(ctx, "POST", convexURL+"/settings", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /settings: HTTP %d — %s", resp.StatusCode, string(out))
	}
	return nil
}

func bytesTrim(b []byte) []byte {
	// TrimSpace on a json.RawMessage without importing strings/bytes helpers.
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}
