package main

import (
	"os"
	"runtime"
	"strings"
)

type deviceCodeRequest struct {
	MachineName    string `json:"machineName,omitempty"`
	Platform       string `json:"platform,omitempty"`
	Arch           string `json:"arch,omitempty"`
	Shell          string `json:"shell,omitempty"`
	Environment    string `json:"environment,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	PreferredProvider string `json:"preferredProvider,omitempty"`
	IsWSL          bool   `json:"isWsl,omitempty"`
}

func buildDeviceCodeRequest() deviceCodeRequest {
	host, _ := os.Hostname()
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	rt := detectWSLRuntime()

	platform := runtime.GOOS
	environment := ""
	if rt.IsWSL {
		if rt.Version == 1 {
			platform = "wsl1"
			environment = "WSL1"
		} else {
			platform = "wsl2"
			environment = "WSL2"
		}
	}

	return deviceCodeRequest{
		MachineName:    strings.TrimSpace(host),
		Platform:       platform,
		Arch:           runtime.GOARCH,
		Shell:          shell,
		Environment:    environment,
		RuntimeVersion: version,
		PreferredProvider: detectPreferredAuthProvider(),
		IsWSL:          rt.IsWSL,
	}
}

func detectPreferredAuthProvider() string {
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_AUTH_PROVIDER"))); raw != "" {
		switch raw {
		case "apple", "google", "microsoft":
			return raw
		}
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return ""
	}
	info, err := ValidateTokenInfo(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil || info == nil {
		return ""
	}
	raw := strings.ToLower(strings.TrimSpace(info.Provider))
	switch raw {
	case "apple", "google", "microsoft":
		return raw
	default:
		return ""
	}
}
