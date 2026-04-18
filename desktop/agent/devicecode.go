package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// deviceCodeResponse is the response from POST /auth/device-code.
type deviceCodeResponse struct {
	UserCode   string `json:"userCode"`
	DeviceCode string `json:"deviceCode"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// pollResponse is the response from GET /auth/device-code/poll.
type pollResponse struct {
	Status string `json:"status"` // "pending", "authorized", "expired"
	Token  string `json:"token,omitempty"`
}

// runDeviceCodeAuth performs the device code auth flow for headless machines.
// It requests a device code, displays it (with QR code), and polls until authorized.
func runDeviceCodeAuth(convexURL string) (string, error) {
	// 1. Request a device code
	payload, _ := json.Marshal(buildDeviceCodeRequest())
	resp, err := httpClient.Post(convexURL+"/auth/device-code", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("device code request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var dcResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return "", fmt.Errorf("decode device code response: %w", err)
	}

	// 2. Display the code and URL
	authURL := "https://yaver.io/auth/device"
	authURLWithCode := authURL + "?code=" + dcResp.UserCode

	fmt.Println()
	fmt.Println("  To sign in, scan this QR code or visit the URL below:")
	fmt.Println()

	// Render QR code in terminal
	qrterminal.GenerateWithConfig(authURLWithCode, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 2,
	})

	fmt.Println()
	fmt.Printf("  URL:  %s\n", authURL)
	fmt.Printf("  Code: %s\n", dcResp.UserCode)
	fmt.Println()

	ttl := time.Until(time.UnixMilli(dcResp.ExpiresAt))
	fmt.Printf("  Waiting for authentication... (expires in %d minutes)\n", int(ttl.Minutes()))
	fmt.Println()

	// 3. Poll every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	deadline := time.UnixMilli(dcResp.ExpiresAt)

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("device code expired")
			}

			token, done, err := pollDeviceCode(convexURL, dcResp.DeviceCode)
			if err != nil {
				// Non-fatal poll error, keep trying
				continue
			}
			if done {
				if token == "" {
					return "", fmt.Errorf("device code expired or already used")
				}
				return token, nil
			}
			// status == "pending", keep polling
		}
	}
}

// pollDeviceCode makes a single poll request.
// Returns (token, done, error). done=true means stop polling.
func pollDeviceCode(convexURL, deviceCode string) (string, bool, error) {
	req, err := http.NewRequest("GET", convexURL+"/auth/device-code/poll?device_code="+deviceCode, nil)
	if err != nil {
		return "", false, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("poll failed (status %d)", resp.StatusCode)
	}

	var pr pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", false, err
	}

	switch pr.Status {
	case "authorized":
		return pr.Token, true, nil
	case "expired":
		return "", true, nil
	default:
		return "", false, nil
	}
}

// isHeadless returns true if the environment suggests no display is available.
func isHeadless() bool {
	// SSH session without display forwarding
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return true
		}
	}
	return false
}
