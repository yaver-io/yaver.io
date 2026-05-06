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
	"sync"
	"time"
)

// resolvePrimaryDeviceID reads the caller's userSettings from Convex
// to find which device the user flagged as primary. Empty string +
// nil error means "no primary set" — caller decides whether to fall
// back to another strategy or surface the missing preference.
func resolvePrimaryDeviceID(ctx context.Context, s *HTTPServer) (string, error) {
	row, err := fetchUserSettings(ctx, s)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(row.PrimaryDeviceID), nil
}

// userSettingsRow mirrors the subset of userSettings fields the agent
// reads from Convex's /settings GET. Add new fields here as more
// agent-side flows need them — keep the struct narrow so unrelated
// shape changes don't require recompiling the parser.
type userSettingsRow struct {
	PrimaryDeviceID        string `json:"primaryDeviceId"`
	PrimaryRunnerByDevice  []struct {
		DeviceID string `json:"deviceId"`
		RunnerID string `json:"runnerId"`
		Model    string `json:"model,omitempty"`
		Mode     string `json:"mode,omitempty"`
		Provider string `json:"provider,omitempty"`
	} `json:"primaryRunnerByDevice"`
}

type primaryRunnerPreference struct {
	RunnerID string
	Model    string
	Mode     string
	Provider string
}

var (
	userSettingsCacheMu     sync.Mutex
	userSettingsCacheVal    *userSettingsRow
	userSettingsCacheExpiry time.Time
)

const userSettingsCacheTTL = 30 * time.Second

func fetchUserSettings(ctx context.Context, s *HTTPServer) (*userSettingsRow, error) {
	userSettingsCacheMu.Lock()
	if userSettingsCacheVal != nil && time.Now().Before(userSettingsCacheExpiry) {
		row := userSettingsCacheVal
		userSettingsCacheMu.Unlock()
		return row, nil
	}
	userSettingsCacheMu.Unlock()

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not signed in")
	}
	convex := ""
	if s != nil {
		convex = s.convexURL
	}
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	req, err := http.NewRequestWithContext(ctx, "GET", convex+"/settings", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch settings: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Settings userSettingsRow `json:"settings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	row := parsed.Settings
	userSettingsCacheMu.Lock()
	userSettingsCacheVal = &row
	userSettingsCacheExpiry = time.Now().Add(userSettingsCacheTTL)
	userSettingsCacheMu.Unlock()
	return &row, nil
}

// resolvePrimaryRunnerForSelf returns the runner+model the current
// user has pinned for THIS device in Convex's userSettings.
// primaryRunnerByDevice. Used by the feedback flow when the inbound
// /tasks payload doesn't carry an explicit runner — Convex is the
// authoritative source, the mobile UserDefault is only a hint that
// can be stale or absent.
func resolvePrimaryRunnerPrefForSelf(ctx context.Context, s *HTTPServer) primaryRunnerPreference {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
		return primaryRunnerPreference{}
	}
	row, err := fetchUserSettings(ctx, s)
	if err != nil || row == nil {
		return primaryRunnerPreference{}
	}
	for _, e := range row.PrimaryRunnerByDevice {
		if strings.TrimSpace(e.DeviceID) == cfg.DeviceID {
			return primaryRunnerPreference{
				RunnerID: strings.TrimSpace(e.RunnerID),
				Model:    strings.TrimSpace(e.Model),
				Mode:     strings.TrimSpace(e.Mode),
				Provider: strings.TrimSpace(e.Provider),
			}
		}
	}
	return primaryRunnerPreference{}
}

func resolvePrimaryRunnerForSelf(ctx context.Context, s *HTTPServer) (string, string) {
	pref := resolvePrimaryRunnerPrefForSelf(ctx, s)
	return pref.RunnerID, pref.Model
}
