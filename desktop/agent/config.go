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
	AuthToken         string              `json:"auth_token,omitempty"`
	DeviceID          string              `json:"device_id,omitempty"`
	ConvexSiteURL     string              `json:"convex_site_url,omitempty"`
	WebBaseURL        string              `json:"web_base_url,omitempty"`
	TLSCert           string              `json:"tls_cert,omitempty"`
	TLSKey            string              `json:"tls_key,omitempty"`
	AutoStart         bool                `json:"auto_start,omitempty"`
	AutoUpdate        bool                `json:"auto_update,omitempty"`
	HeadlessKeepAwake *bool               `json:"headless_keep_awake,omitempty"`
	RelayPassword     string              `json:"relay_password,omitempty"`
	RelayServers      []RelayServerConfig `json:"relay_servers,omitempty"`
	// Cached relay settings come from Convex/user settings and are used as a
	// reboot-safe fallback when the agent's auth token has expired.
	CachedRelayPassword           string                   `json:"cached_relay_password,omitempty"`
	CachedRelayServers            []RelayServerConfig      `json:"cached_relay_servers,omitempty"`
	CloudflareTunnels             []CloudflareTunnelConfig `json:"cloudflare_tunnels,omitempty"`
	// PublicEndpoints is a manual list of hostnames or URLs that the
	// agent advertises to Convex on top of Cloudflare-tunnel and
	// relay-assigned URLs. Useful for headless boxes with a stable
	// public IP (Hetzner, EC2, …) where you want `yaver ssh @alias`
	// and the dashboard Shell tooltip to resolve to the public host
	// without standing up a Cloudflare tunnel. Each entry can be a
	// bare host (e.g. "198.51.100.20") or an https URL — the
	// resolver strips schemes + trailing slashes for SSH use.
	PublicEndpoints []string `json:"public_endpoints,omitempty"`
	MacOSPermissionOnboardingDone bool                     `json:"macos_permission_onboarding_done,omitempty"`
	HostShare                     *HostShareConfig         `json:"host_share,omitempty"`
	Sandbox                       *SandboxConfig           `json:"sandbox,omitempty"`
	Exec                          *ExecConfig              `json:"exec,omitempty"`
	Email                         *EmailConfig             `json:"email,omitempty"`
	ACLPeers                      []ACLPeerConfig          `json:"acl_peers,omitempty"`
	// Speech is an inert JSON field. The voice surface was killed
	// 2026-04-28 (project_lean_stack_2026_04_28.md). This field is
	// preserved only so a concurrent thread / older config.json
	// that still serializes it doesn't break parsing.
	Speech              *SpeechConfig       `json:"speech,omitempty"`
	Notifications       *NotificationConfig `json:"notifications,omitempty"`
	WebhookSecret       string              `json:"webhook_secret,omitempty"`
	AnalyticsWebhookURL string              `json:"analytics_webhook_url,omitempty"`
	// DeployWebhookURL is POST'd a JSON body summarising each finished
	// /deploy/ship run. Empty = disabled. Intended use: point at a
	// Slack / Discord / Zapier inbound URL so overnight guest-deploy
	// failures can page a human.
	DeployWebhookURL string `json:"deploy_webhook_url,omitempty"`
	// DeployWebhookOn filters which events fire. Values: "success",
	// "failure", "all" (default). Empty also means "all".
	DeployWebhookOn string `json:"deploy_webhook_on,omitempty"`
	// DeployWebhookSecret enables HMAC-SHA256 signing of the webhook
	// body. When set, every POST carries:
	//
	//   X-Yaver-Timestamp: <unix-seconds>
	//   X-Yaver-Signature: sha256=<hex HMAC of "{timestamp}.{body}">
	//
	// Downstream receivers reject a POST whose timestamp is outside
	// their acceptable drift window + whose HMAC doesn't recompute.
	// Empty = no signing (deploy_webhook_url still works).
	DeployWebhookSecret string `json:"deploy_webhook_secret,omitempty"`
	// DeployWebhookOnByTarget overrides DeployWebhookOn on a per-target
	// basis. e.g. {"testflight": "failure", "cloudflare": "all"} fires
	// the webhook only on testflight failures + every cloudflare
	// result. Targets absent from the map fall back to DeployWebhookOn
	// (or "all" when that's also empty). Values same as
	// DeployWebhookOn: "all", "success", "failure".
	DeployWebhookOnByTarget map[string]string `json:"deploy_webhook_on_by_target,omitempty"`
	RateLimit               *RateLimitConfig  `json:"rate_limit,omitempty"`

	// Machine-level monitors (disk-health, peer heartbeat)
	// run on every serve by default. Each can be individually
	// disabled in config for devs who don't want extra
	// goroutines or who've wired the same checks elsewhere.
	DisableDiskHealth       bool `json:"disable_disk_health,omitempty"`
	DisableHeartbeatWatcher bool `json:"disable_heartbeat_watcher,omitempty"`

	// BootstrapSecretHash is the SHA-256 of a pre-shared
	// secret used by the unauthenticated /auth/recover
	// endpoint. Empty = recovery disabled. The dev sets this
	// once via `yaver config set bootstrap-secret <value>`
	// and stores the plaintext in their password manager so
	// they can unlock a headless agent that's lost auth
	// without SSH'ing in.
	BootstrapSecretHash string `json:"bootstrap_secret_hash,omitempty"`
	// RequirePrivateRecoveryTransport blocks /auth/recover on direct public
	// internet ingress. When false (default), recovery stays reachable on
	// the main HTTP listener. When true, recovery is limited to LAN/loopback,
	// Tailscale, private relay, or an HTTPS Cloudflare Tunnel.
	RequirePrivateRecoveryTransport bool     `json:"require_private_recovery_transport,omitempty"`
	HAURL                           string   `json:"ha_url,omitempty"`
	HAToken                         string   `json:"ha_token,omitempty"`
	AllowedIPs                      []string `json:"allowed_ips,omitempty"`        // IP allowlist CIDRs (applies to owner + guest; guests can also use AllowedGuestIPs)
	AllowedGuestIPs                 []string `json:"allowed_guest_ips,omitempty"`  // Extra CIDRs admitted ONLY when the request carries a valid guest/SDK bearer (e.g. relay/Tailscale IPs that should not permit anonymous access)
	TLSFingerprint                  string   `json:"tls_fingerprint,omitempty"`    // SHA256 of TLS cert
	TLSPort                         int      `json:"tls_port,omitempty"`           // HTTPS port (default 18443)
	IOSInstallMethod                string   `json:"ios_install_method,omitempty"` // "auto" (default), "native", "bundle"

	// Container isolation — run tasks inside Docker containers
	ContainerizeGuests bool                   `json:"containerize_guests,omitempty"` // run guest tasks in containers (default: false)
	ContainerizeHost   bool                   `json:"containerize_host,omitempty"`   // run host tasks in containers (default: false)
	ContainerImage     string                 `json:"container_image,omitempty"`     // custom image (default: yaver-sandbox)
	ContainerCPU       string                 `json:"container_cpu,omitempty"`       // CPU limit e.g. "2.0"
	ContainerMemory    string                 `json:"container_memory,omitempty"`    // Memory limit e.g. "4g"
	ContainerNetwork   string                 `json:"container_network,omitempty"`   // Network mode: "host" (default), "bridge", "none"
	ContainerReadOnly  bool                   `json:"container_read_only,omitempty"` // Read-only root filesystem (writes only to /workspace, /tmp)
	ContainerMounts    []string               `json:"container_mounts,omitempty"`    // Extra volume mounts e.g. ["/opt/android-sdk:/opt/android-sdk:ro"]
	SharedStorage      []SharedStorageProfile `json:"shared_storage,omitempty"`
	Code               *CodeCLIConfig         `json:"code,omitempty"`
}

// CodeCLIConfig persists the deterministic `yaver code` control-plane state so
// machine/runner/repo defaults survive across separate CLI invocations.
type CodeCLIConfig struct {
	WorkMode           string `json:"work_mode,omitempty"` // local | attached
	AttachedDeviceID   string `json:"attached_device_id,omitempty"`
	AttachedDeviceName string `json:"attached_device_name,omitempty"`
	Runner             string `json:"runner,omitempty"`
	Model              string `json:"model,omitempty"`
	Mode               string `json:"mode,omitempty"`
	Provider           string `json:"provider,omitempty"`
	BaseURL            string `json:"base_url,omitempty"`
	RepoPath           string `json:"repo_path,omitempty"`
	RepoRemote         bool   `json:"repo_remote,omitempty"`
	// OrchestrationMode is preserved as an inert JSON field so a
	// concurrent thread that's still pushing it through Convex /
	// patches doesn't break the build. The CLI / MCP no longer
	// honors it — see project_lean_stack_2026_04_28.md (yaver code
	// Phase 5 was dropped 2026-04-28).
	OrchestrationMode string `json:"orchestration_mode,omitempty"`
}

// ExecConfig controls remote command execution settings.
type ExecConfig struct {
	Enabled        bool   `json:"enabled"`                     // default: true
	MaxConcurrent  int    `json:"max_concurrent,omitempty"`    // default: 10
	DefaultTimeout int    `json:"default_timeout_s,omitempty"` // default: 300
	Shell          string `json:"shell,omitempty"`             // default: "sh"
}

// SpeechConfig is preserved as an inert struct after the voice
// surface was removed 2026-04-28. No code path consumes it any
// more. Kept only to satisfy stale references and JSON parsing.
type SpeechConfig struct {
	Provider   string `json:"provider,omitempty"`
	APIKey     string `json:"api_key,omitempty"`
	TTSEnabled bool   `json:"tts_enabled,omitempty"`
}

// EmailConfig holds email provider credentials.
type EmailConfig struct {
	Provider           string `json:"provider,omitempty"` // "office365" or "gmail"
	AzureTenantID      string `json:"azure_tenant_id,omitempty"`
	AzureClientID      string `json:"azure_client_id,omitempty"`
	AzureClientSecret  string `json:"azure_client_secret,omitempty"`
	GoogleClientID     string `json:"google_client_id,omitempty"`
	GoogleClientSecret string `json:"google_client_secret,omitempty"`
	GoogleRefreshToken string `json:"google_refresh_token,omitempty"`
	SenderEmail        string `json:"sender_email,omitempty"`
	SenderName         string `json:"sender_name,omitempty"`
	// Transactional SMTP relay — used by yaver email send for
	// outbound-only "send password reset" style mail. Lives
	// alongside the inbox-sync fields above because they share
	// the same underlying EmailConfig record; at runtime they
	// target different use cases.
	SMTPHost     string `json:"smtp_host,omitempty"`
	SMTPPort     int    `json:"smtp_port,omitempty"`
	SMTPUsername string `json:"smtp_username,omitempty"`
	SMTPPassword string `json:"smtp_password,omitempty"`
	SMTPFrom     string `json:"smtp_from,omitempty"`
	SMTPStartTLS bool   `json:"smtp_starttls,omitempty"` // RFC 3207 STARTTLS on port 587
}

// Attach RateLimit on the top-level Config — declared here so
// config.go is the one source of truth for config shape.

// ACLPeerConfig describes a connected MCP peer for the Agent Communication Layer.
type ACLPeerConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`     // HTTP MCP endpoint
	Type    string `json:"type"`              // "http" or "stdio"
	Command string `json:"command,omitempty"` // for stdio transport
	Auth    string `json:"auth,omitempty"`    // bearer token
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
	URL                  string `json:"url"` // e.g. "https://my-tunnel.example.com"
	CFAccessClientId     string `json:"cf_access_client_id,omitempty"`
	CFAccessClientSecret string `json:"cf_access_client_secret,omitempty"`
	Label                string `json:"label,omitempty"`
	Priority             int    `json:"priority,omitempty"`
}

// SharedStorageProfile defines a machine-level shared storage target that any
// authenticated Yaver client on this machine may browse.
type SharedStorageProfile struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Type               string `json:"type"` // local, smb, webdav, storagebox, s3
	Path               string `json:"path,omitempty"`
	MountPath          string `json:"mount_path,omitempty"`
	Remote             string `json:"remote,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
	Bucket             string `json:"bucket,omitempty"`
	Region             string `json:"region,omitempty"`
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	AccessKey          string `json:"access_key,omitempty"`
	SecretKey          string `json:"secret_key,omitempty"`
	ReadOnly           bool   `json:"read_only,omitempty"`
	Notes              string `json:"notes,omitempty"`
	ContainerMountMode string `json:"container_mount_mode,omitempty"` // none, host, guests, all
	ContainerPath      string `json:"container_path,omitempty"`       // in-container mount path
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

// SaveConfig writes the config to disk ATOMICALLY.
//
// Token rotation (Apr 2026) made this hot path: every daily refresh
// replaces the agent's bearer token, and if the process dies between
// os.WriteFile's truncate and its fsync, we'd lose the new token AND
// the old one (the server has already rotated). A truncated config
// means the next boot sees an empty token and forces re-auth — exactly
// the "signed in forever" contract we're protecting.
//
// Write-tmp-rename-fsync pattern survives power loss and SIGKILL:
//
//  1. Marshal to memory.
//  2. Write to <path>.tmp with 0600.
//  3. fsync the tmp file (bytes hit the platter).
//  4. Atomic rename over <path>.
//  5. fsync the parent directory so the rename is durable.
//
// If anything below step 4 fails, the original file is untouched;
// if step 4 succeeds, the new file is on disk. Either way, the
// config is never in a half-written state.
func SaveConfig(cfg *Config) error {
	p, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, ".config.json.tmp-")
	if err != nil {
		return fmt.Errorf("create tmp config: %w", err)
	}
	tmpPath := tmp.Name()
	// If we bail before rename, don't leave the tmp file behind.
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp config: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("chmod tmp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("fsync tmp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp config: %w", err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		cleanup()
		return fmt.Errorf("rename tmp config: %w", err)
	}
	// fsync the directory so the rename survives a power loss. Best-
	// effort: some filesystems (tmpfs on Linux, some Darwin overlays)
	// return ENOSYS — ignore those, the rename itself is still atomic.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
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
