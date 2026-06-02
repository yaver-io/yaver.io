package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const configDirName = ".yaver"

// Config holds persisted agent configuration.
type Config struct {
	AuthToken string `json:"auth_token,omitempty"`
	// PreviousAuthToken is the most-recently-superseded AuthToken, kept
	// only as a vault-decryption fallback. The vault key is derived from
	// AuthToken via DerivePassphraseFromToken; on every token rotation
	// SetAuthToken tries to rekey the vault under the new token, but if
	// that step is skipped (older code path, partial write, no vault on
	// disk yet) openVault uses this field to recover and trigger a
	// rekey. Cleared as soon as the rekey succeeds.
	PreviousAuthToken string `json:"previous_auth_token,omitempty"`
	// PreviousAuthTokens is a bounded, newest-first chain of superseded
	// AuthTokens (most recent at index 0). PreviousAuthToken alone only
	// survives ONE rotation between vault writes — if the token rotates
	// twice before the vault is rekeyed (e.g. the agent never ran a
	// vault op in between), the original key is lost and vault.enc is
	// permanently undecryptable. openVault walks this whole chain so the
	// vault recovers as long as ANY token that ever encrypted it is
	// still here. Capped at maxPrevAuthTokens; cleared on rekey success.
	PreviousAuthTokens []string            `json:"previous_auth_tokens,omitempty"`
	DeviceID           string              `json:"device_id,omitempty"`
	ConvexSiteURL      string              `json:"convex_site_url,omitempty"`
	WebBaseURL         string              `json:"web_base_url,omitempty"`
	TLSCert            string              `json:"tls_cert,omitempty"`
	TLSKey             string              `json:"tls_key,omitempty"`
	AutoStart          bool                `json:"auto_start,omitempty"`
	AutoUpdate         bool                `json:"auto_update,omitempty"`
	HeadlessKeepAwake  *bool               `json:"headless_keep_awake,omitempty"`
	RelayPassword      string              `json:"relay_password,omitempty"`
	RelayServers       []RelayServerConfig `json:"relay_servers,omitempty"`
	// Cached relay settings come from Convex/user settings and are used as a
	// reboot-safe fallback when the agent's auth token has expired.
	CachedRelayPassword string                   `json:"cached_relay_password,omitempty"`
	CachedRelayServers  []RelayServerConfig      `json:"cached_relay_servers,omitempty"`
	CloudflareTunnels   []CloudflareTunnelConfig `json:"cloudflare_tunnels,omitempty"`
	// ConnectionPreferences is a privacy-safe control-plane summary for
	// Convex/mobile. It lets a user say "this machine should prefer
	// headscale" or "disable relay preference" without publishing extra
	// VPN control-plane details. Concrete IPs/URLs still come from
	// localIps/publicEndpoints/relay_servers.
	ConnectionPreferences []ConnectionPreference `json:"connection_preferences,omitempty"`
	// PublicEndpoints is a manual list of hostnames or URLs that the
	// agent advertises to Convex on top of Cloudflare-tunnel and
	// relay-assigned URLs. Useful for headless boxes with a stable
	// public IP (Hetzner, EC2, …) where you want `yaver ssh @alias`
	// and the dashboard Shell tooltip to resolve to the public host
	// without standing up a Cloudflare tunnel. Each entry can be a
	// bare host (e.g. "198.51.100.20") or an https URL — the
	// resolver strips schemes + trailing slashes for SSH use.
	PublicEndpoints               []string         `json:"public_endpoints,omitempty"`
	MacOSPermissionOnboardingDone bool             `json:"macos_permission_onboarding_done,omitempty"`
	HostShare                     *HostShareConfig `json:"host_share,omitempty"`
	Sandbox                       *SandboxConfig   `json:"sandbox,omitempty"`
	Exec                          *ExecConfig      `json:"exec,omitempty"`
	Email                         *EmailConfig     `json:"email,omitempty"`
	ACLPeers                      []ACLPeerConfig  `json:"acl_peers,omitempty"`
	// Speech is the legacy voice config field. Kept parsable so older
	// config.json files don't error out; new installs should use Voice.
	Speech *SpeechConfig `json:"speech,omitempty"`
	// Voice is the hands-free agent-loop config (revived 2026-05-27).
	// Wake/PTT → Deepgram Flux STT → CreateTaskWithOptions(source="voice-input")
	// → Cartesia Sonic-3 TTS back to caller.
	Voice               *VoiceConfig        `json:"voice,omitempty"`
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
	// DisableAutoPublicIP suppresses the agent's "probe my own external
	// IPv4 at startup and append it to publicEndpoints" behavior.
	// Default OFF (auto-publish IS on) because Yaver's wedge for
	// remote primaries is "user reaches the box from their phone, no
	// SSH" — a device with zero working publicEndpoints is invisible
	// the moment a Cloudflare tunnel rotates or a relay subdomain
	// loses DNS routing. Set true if you route exclusively through
	// Cloudflare and don't want the bare host:port advertised.
	DisableAutoPublicIP bool `json:"disable_auto_public_ip,omitempty"`

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

// SpeechConfig is the legacy struct. Kept so older config.json
// files parse cleanly; the live voice surface uses VoiceConfig.
type SpeechConfig struct {
	Provider   string `json:"provider,omitempty"`
	APIKey     string `json:"api_key,omitempty"`
	TTSEnabled bool   `json:"tts_enabled,omitempty"`
}

// VoiceConfig holds STT + TTS provider creds and per-project bias
// terms for the hands-free agent loop.
//
// Phase 1 picks (project_voice_glasses_revival_2026_05_27.md):
//
//	STT = Deepgram Flux (Nova-3), model-integrated end-of-turn detection
//	TTS = Cartesia Sonic-3, 40ms model latency
//
// Mobile/SDK clients hit GET /voice/status to discover readiness,
// then open a WebSocket to /voice/stream to push 16kHz mono PCM and
// receive transcript + agent result + TTS audio frames.
type VoiceConfig struct {
	Enabled bool `json:"enabled,omitempty"`

	// Provider selection — each user picks their own. Yaver itself
	// does NOT ship a default API key for any provider; everything
	// here is the USER's account / billing relationship. These keys
	// are stored locally in ~/.yaver/config.json and never sync to
	// Convex (enforced by convex_privacy_test.go).
	//
	// STTProvider: "openai" (default) | "deepgram"
	// TTSProvider: "openai" (default) | "cartesia"
	//
	// Empty string → defaults to "openai" since one OpenAI key covers
	// both, lowest friction for solo users.
	STTProvider string `json:"stt_provider,omitempty"`
	TTSProvider string `json:"tts_provider,omitempty"`

	// OpenAI — single key covers both STT (whisper-1 / gpt-4o-transcribe)
	// and TTS (gpt-4o-mini-tts / tts-1). Cheapest setup friction: one
	// signup at platform.openai.com.
	OpenAIAPIKey   string `json:"openai_api_key,omitempty"`
	OpenAISTTModel string `json:"openai_stt_model,omitempty"` // "" = whisper-1
	OpenAITTSModel string `json:"openai_tts_model,omitempty"` // "" = gpt-4o-mini-tts
	OpenAITTSVoice string `json:"openai_tts_voice,omitempty"` // "" = "alloy"

	// Deepgram (alternate STT + TTS) — Flux Nova-3 streaming STT with
	// built-in end-of-turn detection AND Aura-2 streaming TTS (~$30/M
	// chars, ~half Cartesia). One signup + one key covers both legs of
	// the loop — the lowest-friction alt to OpenAI when the user wants
	// snappier latency without juggling two vendors.
	DeepgramAPIKey   string `json:"deepgram_api_key,omitempty"`
	DeepgramTTSModel string `json:"deepgram_tts_model,omitempty"` // "" = aura-2-thalia-en

	// Cartesia (alternate TTS) — Sonic-3 streaming with 40ms TTFA.
	// Worth picking over OpenAI when the user values snappy back-and-
	// forth conversations + premium voice quality.
	CartesiaAPIKey  string `json:"cartesia_api_key,omitempty"`
	CartesiaVoiceID string `json:"cartesia_voice_id,omitempty"` // empty = Cartesia default voice

	// AssemblyAI (alternate STT) — Universal-Streaming v3, 99+ langs
	// including Turkish, ~$0.0025/min (the cheapest mainstream STT).
	// Worth picking over Deepgram for budget + language breadth.
	// New keys are stored in the vault via LookupVoiceCredential; this
	// field is the legacy-fallback path so existing installs don't break.
	AssemblyAIAPIKey   string `json:"assemblyai_api_key,omitempty"`
	AssemblyAILanguage string `json:"assemblyai_language,omitempty"` // "" = auto-detect

	// ElevenLabs (alternate TTS) — Flash v2.5, 32 langs, ~75ms TTFA,
	// top-tier voice quality. Worth picking over Cartesia when voice
	// character matters more than per-character price.
	ElevenLabsAPIKey     string `json:"elevenlabs_api_key,omitempty"`
	ElevenLabsTTSVoiceID string `json:"elevenlabs_tts_voice_id,omitempty"` // empty = "Rachel"
	ElevenLabsTTSModel   string `json:"elevenlabs_tts_model,omitempty"`    // empty = "eleven_flash_v2_5"

	// ProjectKeyterms biases the Deepgram session per-project so that
	// "useState", "Convex", "Hermes", repo names, etc. don't get
	// mangled. Key is the project slug; value is the keyterm list.
	// Only consumed when STTProvider == "deepgram".
	ProjectKeyterms map[string][]string `json:"project_keyterms,omitempty"`
	// DefaultProject is the project slug used when the client doesn't
	// specify one in the WS start frame. Empty = no keyterm bias.
	DefaultProject string `json:"default_project,omitempty"`
	// LaunchProjects maps a spoken slug ("sfmg" / "yaver" / "talos") to
	// the absolute workDir of that project. Voice utterances matching
	// "launch X" / "open X" / "start X" trigger a Hermes smoke-test +
	// Hermes-push to paired phones instead of creating a Claude task.
	// Example: {"sfmg": "/Users/me/Workspace/sfmg", "yaver": "/Users/me/Workspace/yaver.io/mobile"}
	LaunchProjects map[string]string `json:"launch_projects,omitempty"`

	// AssistantName is what the user calls Yaver out loud. Empty = "yaver"
	// (the default). Setting it to "sam" / "feyi" / "kole" makes
	// "Hey Sam, deploy web" route exactly like "Hey Yaver, deploy web" —
	// the spoken name is stripped from the front of an utterance before
	// the remainder is matched against the verb catalogue. This is free
	// today because the wake phrase is transcript *filtering* (any name
	// works with zero training); a future low-power hotword engine would
	// need a trained keyword model, so this stays the source of truth for
	// the spoken name. See assistantWakeWords in voice_control.go.
	AssistantName string `json:"assistant_name,omitempty"`
}

// EffectiveAssistantName returns the spoken wake name, defaulting to
// "yaver" when unset. Lowercased + trimmed so the wake-word match is
// case-insensitive and forgiving of stray whitespace.
func (v *VoiceConfig) EffectiveAssistantName() string {
	if v == nil {
		return defaultAssistantName
	}
	n := strings.ToLower(strings.TrimSpace(v.AssistantName))
	if n == "" {
		return defaultAssistantName
	}
	return n
}

// EffectiveSTTProvider returns the configured STT provider, defaulting
// to the free/offline local whisper engine when unset (no key, no cost).
// Single source of truth so the HTTP handler and status endpoint never drift.
func (v *VoiceConfig) EffectiveSTTProvider() string {
	if v == nil {
		return "local"
	}
	p := strings.ToLower(strings.TrimSpace(v.STTProvider))
	if p == "" {
		return "local"
	}
	return p
}

// EffectiveTTSProvider returns the configured TTS provider, defaulting
// to the free/offline local engine (say/espeak on host; AVSpeech/
// TextToSpeech on mobile) when unset.
func (v *VoiceConfig) EffectiveTTSProvider() string {
	if v == nil {
		return "local"
	}
	p := strings.ToLower(strings.TrimSpace(v.TTSProvider))
	if p == "" {
		return "local"
	}
	return p
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

type ConnectionPreference struct {
	Kind      string `json:"kind"`
	Active    bool   `json:"active"`
	Preferred bool   `json:"preferred"`
	Source    string `json:"source"`
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
//
// Defensive auth-token guard (May 2026): if the in-memory cfg has an
// empty AuthToken but the existing on-disk file has a non-empty one,
// we restore the on-disk token before writing instead of silently
// wiping it. This prevents accidental sign-out from `cfg = &Config{}`
// fallbacks after a transient LoadConfig failure (e.g. the user lost
// auth after a `yaver primary status` round-trip — root cause was a
// SaveConfig elsewhere that started from an empty cfg). Explicit
// sign-out paths (`runSignout`, `runAuthFactoryReset`, MCP
// `authLogout`) call SaveConfigClearingAuth instead, which bypasses
// the guard.
func SaveConfig(cfg *Config) error {
	if cfg != nil && strings.TrimSpace(cfg.AuthToken) == "" {
		if onDisk, lerr := LoadConfig(); lerr == nil && onDisk != nil && strings.TrimSpace(onDisk.AuthToken) != "" {
			log.Printf("[config] WARNING: SaveConfig called with empty AuthToken but on-disk file has one — preserving the existing token. Use SaveConfigClearingAuth to wipe deliberately.")
			cfg.AuthToken = onDisk.AuthToken
		}
	}
	return saveConfigUnchecked(cfg)
}

// SaveConfigClearingAuth bypasses the auth-token preservation guard
// in SaveConfig. Used only by sign-out / factory-reset / MCP logout
// paths that genuinely intend to leave AuthToken empty on disk.
func SaveConfigClearingAuth(cfg *Config) error {
	return saveConfigUnchecked(cfg)
}

func saveConfigUnchecked(cfg *Config) error {
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
