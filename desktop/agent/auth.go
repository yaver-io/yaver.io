package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RefreshToken extends the session expiry by 30 days.
// Returns nil on success. Returns an error with status 401 if the session is expired.
func RefreshToken(baseURL, token string) error {
	req, err := newBearerRequest("POST", baseURL+"/auth/refresh", token, nil)
	if err != nil {
		return fmt.Errorf("create refresh request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("refresh token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("session expired (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh token failed (status %d)", resp.StatusCode)
	}
	return nil
}

// RunnerInfo describes an active runner process for heartbeat reporting.
type RunnerInfo struct {
	TaskID   string `json:"taskId"`
	RunnerID string `json:"runnerId"`
	Model    string `json:"model,omitempty"`
	PID      int    `json:"pid"`
	Status   string `json:"status"` // "running" or "idle"
	Title    string `json:"title"`
}

// newBearerRequest creates an HTTP request with Authorization: Bearer header.
func newBearerRequest(method, url, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

const (
	httpTimeout = 10 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

// ValidateToken checks the auth token against the Convex backend.
// Returns nil on success, an error otherwise.
func ValidateToken(baseURL, token string) error {
	req, err := newBearerRequest("GET", baseURL+"/auth/validate", token, nil)
	if err != nil {
		return fmt.Errorf("create validate request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("validate token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validate token failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// UserInfo contains user profile information from Convex.
type UserInfo struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	FullName string `json:"fullName"`
	Provider string `json:"provider"`
}

// ValidateTokenInfo checks the auth token against Convex and returns full user info.
func ValidateTokenInfo(baseURL, token string) (*UserInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/auth/validate", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create validate request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("validate token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("validate token failed (status %d)", resp.StatusCode)
	}

	var result struct {
		User UserInfo `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode validate response: %w", err)
	}
	return &result.User, nil
}

// ValidateTokenUser checks the auth token against Convex and returns the userId.
func ValidateTokenUser(baseURL, token string) (string, error) {
	info, err := ValidateTokenInfo(baseURL, token)
	if err != nil {
		return "", err
	}
	return info.UserID, nil
}

// RegisterDeviceRequest contains the fields sent when registering a device.
type RegisterDeviceRequest struct {
	Token     string `json:"-"`
	DeviceID  string `json:"deviceId"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	PublicKey string `json:"publicKey"`
	QuicHost  string `json:"quicHost"`
	QuicPort  int    `json:"quicPort"`
}

// RelayServerInfo describes a relay server from platform config.
type RelayServerInfo struct {
	ID       string `json:"id"`
	QuicAddr string `json:"quicAddr"` // e.g. "relay.example.com:4433"
	HttpURL  string `json:"httpUrl"`  // e.g. "https://connect.yaver.io"
	Region   string `json:"region"`
	Priority int    `json:"priority"`
}

// PlatformConfig holds all platform-level config fetched from Convex /config.
type PlatformConfig struct {
	RelayServers []RelayServerInfo   `json:"relayServers"`
	Runners      []backendRunnerFull `json:"runners"`
	Models       []BackendModel      `json:"models"`
}

// BackendModel mirrors the Convex aiModels table.
type BackendModel struct {
	ModelID     string `json:"modelId"`
	RunnerID    string `json:"runnerId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
	SortOrder   int    `json:"sortOrder"`
}

// FetchPlatformConfig fetches all platform config from Convex (relays, runners, models).
func FetchPlatformConfig(baseURL string) (*PlatformConfig, error) {
	req, err := http.NewRequest("GET", baseURL+"/config", nil)
	if err != nil {
		return nil, fmt.Errorf("create config request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("config request failed (status %d)", resp.StatusCode)
	}

	var result PlatformConfig
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &result, nil
}

// FetchRelayServers fetches just relay servers (convenience wrapper).
func FetchRelayServers(baseURL string) ([]RelayServerInfo, error) {
	cfg, err := FetchPlatformConfig(baseURL)
	if err != nil {
		return nil, err
	}
	return cfg.RelayServers, nil
}

// RegisterDevice registers this desktop agent with the Convex backend.
func RegisterDevice(baseURL string, r RegisterDeviceRequest) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal register request: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/register", r.Token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create register request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("register device request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register device failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ErrAuthExpired is returned when a 401 response indicates the token has expired.
var ErrAuthExpired = fmt.Errorf("auth token expired (401)")

// SendHeartbeat sends a heartbeat to the Convex backend so the device stays
// marked as online. Includes active runner info if any.
// Returns ErrAuthExpired if the server returns 401.
func SendHeartbeat(baseURL, token, deviceID string, runners []RunnerInfo, quicHost string) error {
	payload := map[string]interface{}{
		"deviceId": deviceID,
		"runners":  runners,
	}
	if quicHost != "" {
		payload["quicHost"] = quicHost
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/heartbeat", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create heartbeat request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ReportMetrics sends CPU/RAM metrics to Convex.
func ReportMetrics(baseURL, token, deviceID string, cpuPercent, memUsedMB, memTotalMB float64) error {
	payload := map[string]interface{}{
		"deviceId":      deviceID,
		"cpuPercent":    cpuPercent,
		"memoryUsedMb":  memUsedMB,
		"memoryTotalMb": memTotalMB,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/metrics", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create metrics request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("metrics request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("metrics failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// SendDevLog sends a developer log to Convex (only stored for developer emails).
func SendDevLog(baseURL, token, email, tag, message string, data map[string]interface{}) {
	payload := map[string]interface{}{
		"email":   email,
		"source":  "agent",
		"level":   "debug",
		"tag":     tag,
		"message": message,
	}
	if data != nil {
		d, _ := json.Marshal(data)
		payload["data"] = string(d)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := newBearerRequest("POST", baseURL+"/dev/log", token, bytes.NewReader(body))
	if err != nil {
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ReportDeviceEvent sends a lifecycle event (crash, restart, etc.) to Convex.
func ReportDeviceEvent(baseURL, token, deviceID, event, details string) error {
	payload := map[string]interface{}{
		"deviceId": deviceID,
		"event":    event,
		"details":  details,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/event", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create event request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("event request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("event report failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// SetRunnerDown updates the runnerDown flag on the device in Convex.
func SetRunnerDown(baseURL, token, deviceID string, down bool) error {
	payload := map[string]interface{}{
		"deviceId":   deviceID,
		"runnerDown": down,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal runner-down: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/runner-down", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create runner-down request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("runner-down request: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// ReportRunnerUsage records how long a runner ran for a task.
func ReportRunnerUsage(baseURL, token, deviceID, taskID, runner, model, source string, durationSec float64, startedAt, finishedAt int64) error {
	payload := map[string]interface{}{
		"deviceId":    deviceID,
		"taskId":      taskID,
		"runner":      runner,
		"model":       model,
		"durationSec": durationSec,
		"startedAt":   startedAt,
		"finishedAt":  finishedAt,
		"source":      source,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/usage/record", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create usage request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("usage report failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// UserSettings holds the user's account-level settings from Convex.
type UserSettings struct {
	ForceRelay          bool   `json:"forceRelay"`
	RelayUrl            string `json:"relayUrl"`
	RelayPassword       string `json:"relayPassword"`
	TunnelUrl           string `json:"tunnelUrl"`
	RunnerID            string `json:"runnerId"`
	CustomRunnerCommand string `json:"customRunnerCommand"`
}

// FetchUserSettings fetches the user's settings from Convex GET /settings.
func FetchUserSettings(baseURL, token string) (*UserSettings, error) {
	req, err := newBearerRequest("GET", baseURL+"/settings", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create settings request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settings request failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Settings UserSettings `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return &result.Settings, nil
}

// MarkOffline tells the backend this device is going offline.
func MarkOffline(baseURL, token, deviceID string) error {
	payload := map[string]string{"deviceId": deviceID}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal offline: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/offline", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create offline request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("offline request: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
