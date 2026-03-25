package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const configDirName = ".yaver"

// Config holds persisted agent configuration.
type Config struct {
	AuthToken     string              `json:"auth_token,omitempty"`
	DeviceID      string              `json:"device_id,omitempty"`
	ConvexSiteURL string              `json:"convex_site_url,omitempty"`
	TLSCert       string              `json:"tls_cert,omitempty"`
	TLSKey        string              `json:"tls_key,omitempty"`
	AutoStart     bool                `json:"auto_start,omitempty"`
	AutoUpdate    bool                `json:"auto_update,omitempty"`
	RelayPassword string              `json:"relay_password,omitempty"`
	RelayServers      []RelayServerConfig    `json:"relay_servers,omitempty"`
	CloudflareTunnels []CloudflareTunnelConfig `json:"cloudflare_tunnels,omitempty"`
	Sandbox       *SandboxConfig      `json:"sandbox,omitempty"`
	Exec          *ExecConfig         `json:"exec,omitempty"`
	Email         *EmailConfig        `json:"email,omitempty"`
	ACLPeers      []ACLPeerConfig     `json:"acl_peers,omitempty"`
	Speech        *SpeechConfig       `json:"speech,omitempty"`
	Voice         *VoiceConfig        `json:"voice,omitempty"`
	Notifications *NotificationConfig `json:"notifications,omitempty"`
	WebhookSecret string              `json:"webhook_secret,omitempty"`
	HAURL         string              `json:"ha_url,omitempty"`
	HAToken       string              `json:"ha_token,omitempty"`
	AllowedIPs    []string            `json:"allowed_ips,omitempty"`     // IP allowlist CIDRs
	TLSFingerprint string            `json:"tls_fingerprint,omitempty"` // SHA256 of TLS cert
	TLSPort       int                 `json:"tls_port,omitempty"`       // HTTPS port (default 18443)
}

// ExecConfig controls remote command execution settings.
type ExecConfig struct {
	Enabled        bool   `json:"enabled"`            // default: true
	MaxConcurrent  int    `json:"max_concurrent,omitempty"`  // default: 10
	DefaultTimeout int    `json:"default_timeout_s,omitempty"` // default: 300
	Shell          string `json:"shell,omitempty"`    // default: "sh"
}

// SpeechConfig holds speech-to-text and text-to-speech settings for CLI voice input.
type SpeechConfig struct {
	Provider  string `json:"provider,omitempty"`   // "whisper" (local), "openai", "deepgram", "assemblyai"
	APIKey    string `json:"api_key,omitempty"`    // API key for cloud providers
	TTSEnabled bool  `json:"tts_enabled,omitempty"` // read responses aloud (macOS `say`, linux `espeak`)
}

// EmailConfig holds email provider credentials.
type EmailConfig struct {
	Provider          string `json:"provider,omitempty"`           // "office365" or "gmail"
	AzureTenantID     string `json:"azure_tenant_id,omitempty"`
	AzureClientID     string `json:"azure_client_id,omitempty"`
	AzureClientSecret string `json:"azure_client_secret,omitempty"`
	GoogleClientID     string `json:"google_client_id,omitempty"`
	GoogleClientSecret string `json:"google_client_secret,omitempty"`
	GoogleRefreshToken string `json:"google_refresh_token,omitempty"`
	SenderEmail       string `json:"sender_email,omitempty"`
	SenderName        string `json:"sender_name,omitempty"`
}

// ACLPeerConfig describes a connected MCP peer for the Agent Communication Layer.
type ACLPeerConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`      // HTTP MCP endpoint
	Type    string `json:"type"`               // "http" or "stdio"
	Command string `json:"command,omitempty"`   // for stdio transport
	Auth    string `json:"auth,omitempty"`      // bearer token
}

// RelayServerConfig describes a relay server configured in config.json.
type RelayServerConfig struct {
	ID       string `json:"id"`
	QuicAddr string `json:"quic_addr"`
	HttpURL  string `json:"http_url,omitempty"`
	Password string `json:"password,omitempty"`
	Region   string `json:"region,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Label    string `json:"label,omitempty"`
}

// CloudflareTunnelConfig describes a Cloudflare Tunnel endpoint in config.json.
type CloudflareTunnelConfig struct {
	ID                   string `json:"id"`
	URL                  string `json:"url"`                           // e.g. "https://my-tunnel.example.com"
	CFAccessClientId     string `json:"cf_access_client_id,omitempty"`
	CFAccessClientSecret string `json:"cf_access_client_secret,omitempty"`
	Label                string `json:"label,omitempty"`
	Priority             int    `json:"priority,omitempty"`
}

// ConfigDir returns the path to ~/.yaver/, creating it if needed.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// ConfigPath returns the full path to the config JSON file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfig reads the config file from disk. Returns a zero-value Config if
// the file does not exist.
func LoadConfig() (*Config, error) {
	p, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the config to disk.
func SaveConfig(cfg *Config) error {
	p, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(p, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// TLSCertPath returns the path where the self-signed TLS certificate is stored.
func TLSCertPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cert.pem"), nil
}

// TLSKeyPath returns the path where the TLS private key is stored.
func TLSKeyPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "key.pem"), nil
}
