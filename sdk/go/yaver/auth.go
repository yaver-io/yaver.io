package yaver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DefaultConvexURL is the production Convex backend.
const DefaultConvexURL = "https://perceptive-minnow-557.eu-west-1.convex.site"

// User represents an authenticated Yaver user.
type User struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	FullName        string `json:"fullName"`
	Provider        string `json:"provider"` // "google", "apple", "github", "microsoft"
	SurveyCompleted bool   `json:"surveyCompleted"`
}

// Device represents a registered device.
type Device struct {
	DeviceID      string    `json:"deviceId"`
	Name          string    `json:"name"`
	Platform      string    `json:"platform"` // "macos", "linux", "windows"
	Host          string    `json:"quicHost"`
	Port          int       `json:"quicPort"`
	IsOnline      bool      `json:"isOnline"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}

// AuthClient handles authentication and device management against the Convex backend.
type AuthClient struct {
	ConvexURL  string
	AuthToken  string
	HTTPClient *http.Client
}

// NewAuthClient creates a new auth client.
func NewAuthClient(convexURL, authToken string) *AuthClient {
	if convexURL == "" {
		convexURL = DefaultConvexURL
	}
	return &AuthClient{
		ConvexURL:  convexURL,
		AuthToken:  authToken,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ValidateToken checks if the auth token is valid and returns the user.
func (a *AuthClient) ValidateToken() (*User, error) {
	req, err := http.NewRequest("GET", a.ConvexURL+"/auth/validate", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.AuthToken)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("token expired")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("validate token: HTTP %d", resp.StatusCode)
	}

	var result struct {
		User User `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.User, nil
}

// ListDevices returns all registered devices for the authenticated user.
func (a *AuthClient) ListDevices() ([]Device, error) {
	req, err := http.NewRequest("GET", a.ConvexURL+"/devices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.AuthToken)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list devices: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Devices []Device `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Devices, nil
}

// GetSettings retrieves user settings from the backend.
func (a *AuthClient) GetSettings() (*UserSettings, error) {
	req, err := http.NewRequest("GET", a.ConvexURL+"/settings", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.AuthToken)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("get settings: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Settings UserSettings `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Settings, nil
}

// SaveSettings saves user settings to the backend.
func (a *AuthClient) SaveSettings(settings *UserSettings) error {
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", a.ConvexURL+"/settings", jsonReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	resp.Body.Close()
	return nil
}

// UserSettings holds user preferences synced via Convex.
type UserSettings struct {
	ForceRelay          *bool   `json:"forceRelay,omitempty"`
	RunnerID            string  `json:"runnerId,omitempty"`
	CustomRunnerCommand string  `json:"customRunnerCommand,omitempty"`
	SpeechProvider      string  `json:"speechProvider,omitempty"`
	SpeechAPIKey        string  `json:"speechApiKey,omitempty"`
	TTSEnabled          *bool   `json:"ttsEnabled,omitempty"`
	Verbosity           *int    `json:"verbosity,omitempty"`
}
