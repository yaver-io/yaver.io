package main

// ops_resolve.go — alias resolution for the ops dispatcher.
//
// Today: "primary" → userSettings.primaryDeviceId from Convex.
// Follow-up: per-user aliases table keyed off device tags so users
// can say ops("gpu", ...) or ops("mac-mini", ...).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// resolvePrimaryDeviceID reads the caller's userSettings from Convex
// to find which device the user flagged as primary. Empty string +
// nil error means "no primary set" — caller decides whether to fall
// back to another strategy or surface the missing preference.
func resolvePrimaryDeviceID(ctx context.Context, s *HTTPServer) (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", fmt.Errorf("not signed in")
	}
	convex := s.convexURL
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	req, err := http.NewRequestWithContext(ctx, "GET", convex+"/settings", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch settings: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Settings struct {
			PrimaryDeviceID string `json:"primaryDeviceId"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return strings.TrimSpace(parsed.Settings.PrimaryDeviceID), nil
}
