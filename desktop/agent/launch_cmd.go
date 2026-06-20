package main

// launch_cmd.go — `yaver launch <hetzner|aws|gcp|ssh>` top-level verb.
//
// The launch verb is a thin conductor on top of existing primitives:
//
//   1. Fetch cloud-images.json from the GH raw URL to learn the public
//      image-id for the chosen provider+arch. The manifest is a static
//      JSON file at the repo root, updated by release-cli.yml on each
//      cli/v* tag after build-cloud-image.sh + release-cloud-image.sh
//      flip the new snapshot/AMI/image to public.
//
//   2. Request a fresh device-code from Convex via requestDeviceCode().
//      The code is pre-injected into the box's cloud-init so the box's
//      first-boot consumes it and exchanges it for its own (auth_token,
//      device_id) pair under the SAME user_id as the launching device.
//      Zero re-OAuth for the user.
//
//   3. Provision the box (provider-specific; see launch_*.go).
//
//   4. Poll Convex for the new device row to appear online.
//
//   5. For each runner (claude-code, codex, opencode) that the launching
//      device has signed-in locally, call PushMirrorToPeer to copy the
//      credential to the new box. The user gets `claude` / `codex` /
//      `opencode` already authed when they SSH in. This is the
//      runner_auth_mirror flow already shipped for the glass-OAuth path.
//
// Provider files (launch_hetzner.go, launch_aws.go, launch_gcp.go,
// launch_ssh.go) implement step 3 only. Everything else is shared here.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const cloudImagesManifestURL = "https://raw.githubusercontent.com/kivanccakmak/yaver.io/main/cloud-images.json"

type cloudImagesManifest struct {
	SchemaVersion int                              `json:"schemaVersion"`
	UpdatedAt     *string                          `json:"updatedAt"`
	YaverVersion  *string                          `json:"yaverVersion"`
	Providers     map[string]map[string]any        `json:"providers"`
	Artifacts     map[string]map[string]*string    `json:"artifacts"`
}

// launchOptions are the per-invocation knobs every provider takes.
// Source = the launching device's local config (auth token + convex URL
// to scope the device-code + runner mirror). Target = the box being born.
type launchOptions struct {
	Provider     string
	Arch         string
	Region       string // aws-only; empty for others
	Name         string // user-visible label for the box; auto-generated if empty
	SSHTarget    string // ssh-only: user@host
	Manifest     *cloudImagesManifest
	SourceConfig *Config
	NoMirror     bool          // skip runner mirror (debug)
	Timeout      time.Duration // overall launch timeout (default 10m)
}

func runLaunch(args []string) {
	if len(args) == 0 {
		printLaunchUsage()
		return
	}

	switch args[0] {
	case "help", "--help", "-h":
		printLaunchUsage()
		return
	case "hetzner", "aws", "gcp", "ssh":
		// fall through to provider dispatch
	default:
		fmt.Fprintf(os.Stderr, "Unknown launch provider: %s\n\n", args[0])
		printLaunchUsage()
		os.Exit(1)
	}

	opts := parseLaunchArgs(args)
	if err := prepareLaunch(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "launch: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	var err error
	switch opts.Provider {
	case "hetzner":
		err = launchHetzner(ctx, &opts)
	case "aws":
		err = launchAWS(ctx, &opts)
	case "gcp":
		err = launchGCP(ctx, &opts)
	case "ssh":
		err = launchSSH(ctx, &opts)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch %s: %v\n", opts.Provider, err)
		os.Exit(1)
	}
}

func parseLaunchArgs(args []string) launchOptions {
	opts := launchOptions{
		Provider: args[0],
		Arch:     "arm64", // default — cheapest tier on Hetzner + Graviton on AWS
		Timeout:  10 * time.Minute,
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--arch":
			if i+1 < len(rest) {
				opts.Arch = rest[i+1]
				i++
			}
		case "--region":
			if i+1 < len(rest) {
				opts.Region = rest[i+1]
				i++
			}
		case "--name":
			if i+1 < len(rest) {
				opts.Name = rest[i+1]
				i++
			}
		case "--no-mirror":
			opts.NoMirror = true
		case "--timeout":
			if i+1 < len(rest) {
				if d, err := time.ParseDuration(rest[i+1]); err == nil {
					opts.Timeout = d
				}
				i++
			}
		default:
			// For ssh, the first positional is user@host
			if opts.Provider == "ssh" && opts.SSHTarget == "" && !strings.HasPrefix(a, "-") {
				opts.SSHTarget = a
			}
		}
	}
	return opts
}

func prepareLaunch(opts *launchOptions) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load local config: %w", err)
	}
	if cfg.AuthToken == "" {
		return fmt.Errorf("not signed in — run `yaver auth` first (the launch flow mirrors your auth to the new box)")
	}
	if cfg.ConvexSiteURL == "" {
		return fmt.Errorf("local config missing convex_site_url; run `yaver auth` to repair")
	}
	opts.SourceConfig = cfg

	if opts.Arch != "amd64" && opts.Arch != "arm64" {
		return fmt.Errorf("--arch must be amd64 or arm64, got %q", opts.Arch)
	}

	if opts.Provider == "ssh" {
		if opts.SSHTarget == "" {
			return fmt.Errorf("ssh provider requires a user@host argument: `yaver launch ssh user@host`")
		}
		// SSH adoption doesn't need the cloud-images manifest — we install
		// the agent from the npm/release path directly on the target box.
		return nil
	}

	manifest, err := fetchCloudImagesManifest(opts.Timeout)
	if err != nil {
		return fmt.Errorf("fetch cloud-images.json: %w", err)
	}
	opts.Manifest = manifest
	return nil
}

func fetchCloudImagesManifest(timeout time.Duration) (*cloudImagesManifest, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(cloudImagesManifestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, cloudImagesManifestURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var m cloudImagesManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// requestLaunchDeviceCode wraps requestDeviceCode (auth_recover.go) and
// returns the bits the cloud-init seed needs to consume the code on
// first-boot.
type launchDeviceCode struct {
	DeviceCode string
	UserCode   string
	ConvexURL  string
}

func requestLaunchDeviceCode(cfg *Config) (*launchDeviceCode, error) {
	dc, err := requestDeviceCodeFn(cfg.ConvexSiteURL)
	if err != nil {
		return nil, err
	}
	return &launchDeviceCode{
		DeviceCode: dc.DeviceCode,
		UserCode:   dc.UserCode,
		ConvexURL:  cfg.ConvexSiteURL,
	}, nil
}

// authorizeOwnDeviceCode calls POST /auth/device-code/authorize with the
// launching device's auth token. This binds the freshly minted device-code
// to the launching user's identity WITHOUT a browser round-trip — the new
// box can then trade the code for a token via the standard /poll endpoint.
// Reuses the existing handler at backend/convex/http.ts /auth/device-code/
// authorize.
func authorizeOwnDeviceCode(cfg *Config, userCode string) error {
	body, _ := json.Marshal(map[string]string{"userCode": userCode})
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(cfg.ConvexSiteURL, "/")+"/auth/device-code/authorize",
		strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authorize device-code: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	return nil
}

// buildCloudInitUserData generates a #cloud-config YAML that pre-injects
// the (already authorized) device-code into /home/yaver/.yaver/pending-
// auth.json, then runs `yaver auth --headless --background-wait` so the
// agent picks up the token without any human interaction.
//
// The cloud-image's yaver-cloud-firstboot.service hands off to
// cloud-firstboot.sh, which detects this pending-auth file and bypasses
// the usual "show URL + wait for browser" branch.
func buildCloudInitUserData(dc *launchDeviceCode, cfg *Config) string {
	now := time.Now().UnixMilli()
	expires := now + int64(15*time.Minute/time.Millisecond)
	pending := map[string]any{
		"deviceCode": dc.DeviceCode,
		"userCode":   dc.UserCode,
		"url":        "(authorized via yaver launch)",
		"convexUrl":  dc.ConvexURL,
		"expiresAt":  expires,
		"createdAt":  now,
	}
	pendingJSON, _ := json.Marshal(pending)
	// Single heredoc-style writefile + a runcmd that drops it under the
	// yaver user's home (cloud-firstboot.sh chowns /home/yaver/.yaver to
	// yaver:yaver after writing the file).
	return fmt.Sprintf(`#cloud-config
write_files:
  - path: /etc/yaver/pending-auth.json
    permissions: '0600'
    owner: root:root
    content: |
      %s
runcmd:
  - mkdir -p /home/yaver/.yaver
  - install -m 0600 /etc/yaver/pending-auth.json /home/yaver/.yaver/pending-auth.json
  - chown -R yaver:yaver /home/yaver/.yaver 2>/dev/null || true
`, string(pendingJSON))
}

// buildCloudInitUserDataWithInstall is the fallback used when no public
// Yaver image exists for the provider (e.g. Hetzner, whose snapshots
// aren't world-shareable). Same pending-auth-injection as the snapshot
// path, plus: apt-installs Node + tmux, npm-installs yaver-cli (which
// pulls the per-platform binary), creates a yaver user, and starts
// yaver auth --headless --background-wait + yaver serve in tmux. First
// boot is ~3 min slower than the snapshot path; post-boot expectations
// (device-code consumed → heartbeat → runner mirror) are identical.
func buildCloudInitUserDataWithInstall(dc *launchDeviceCode, _ *Config) string {
	now := time.Now().UnixMilli()
	expires := now + int64(15*time.Minute/time.Millisecond)
	pending := map[string]any{
		"deviceCode": dc.DeviceCode,
		"userCode":   dc.UserCode,
		"url":        "(authorized via yaver launch fallback)",
		"convexUrl":  dc.ConvexURL,
		"expiresAt":  expires,
		"createdAt":  now,
	}
	pendingJSON, _ := json.Marshal(pending)

	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - curl
  - git
  - jq
  - ca-certificates
  - tmux

write_files:
  - path: /etc/yaver/pending-auth.json
    permissions: '0600'
    owner: root:root
    content: |
      %s
  - path: /usr/local/lib/yaver/cloud-install.sh
    permissions: '0755'
    owner: root:root
    content: |
      #!/usr/bin/env bash
      set -euo pipefail
      if ! id yaver >/dev/null 2>&1; then
        useradd --system --create-home --home-dir /home/yaver --shell /bin/bash --comment "Yaver agent" yaver
      fi
      if ! command -v node >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
        apt-get install -y nodejs
      fi
      if ! command -v yaver >/dev/null 2>&1; then
        npm install -g yaver-cli
      fi
      install -d -m 0700 -o yaver -g yaver /home/yaver/.yaver
      install -m 0600 -o yaver -g yaver /etc/yaver/pending-auth.json \
        /home/yaver/.yaver/pending-auth.json
      sudo -iu yaver bash -lc \
        'tmux kill-session -t yaver 2>/dev/null || true; \
         tmux new-session -d -s yaver "yaver auth --headless --background-wait --convex-url %q && exec yaver serve --port 18080 --debug"'

runcmd:
  - bash /usr/local/lib/yaver/cloud-install.sh
`, string(pendingJSON), dc.ConvexURL)
}

// pollDeviceForOnline blocks until the device-code is consumed by the new
// box and the box reports a heartbeat. Returns the new box's deviceId on
// success, or an error if the timeout expires.
func pollDeviceForOnline(ctx context.Context, dc *launchDeviceCode) (string, error) {
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Now().Add(10 * time.Minute)
	}
	for time.Now().Before(deadline) {
		token, done, err := pollDeviceCode(dc.ConvexURL, dc.DeviceCode)
		if err == nil && done && token != "" {
			// The box has consumed the code → it now has its own auth_token
			// + device_id. We don't see the deviceId from this poll path,
			// only the token. But we don't need it: PushMirrorToPeer
			// addresses the box by its LAN/Tailscale/relay URL, not its
			// device id. The token presence is a strong "box is alive".
			return token, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return "", fmt.Errorf("device-code never consumed — box may not have booted")
}

// mirrorRunnersToBox iterates the three known runners and pushes any
// credential the launching device has locally. Failures are reported but
// don't fail the launch (the user can re-mirror later via runner_auth).
func mirrorRunnersToBox(ctx context.Context, opts *launchOptions, boxBaseURL string) {
	runners := []string{"claude", "codex", "opencode"}
	httpDo := http.DefaultClient.Do
	for _, runner := range runners {
		if _, err := ReadLocalRunnerCredential(runner); err != nil {
			fmt.Printf("  [%s] no local credential to mirror — skipped\n", runner)
			continue
		}
		_, err := PushMirrorToPeer(ctx, runner, boxBaseURL, opts.SourceConfig.AuthToken, httpDo)
		if err != nil {
			fmt.Printf("  [%s] mirror failed: %v\n", runner, err)
			continue
		}
		fmt.Printf("  [%s] mirrored ✓\n", runner)
	}
}

func printLaunchUsage() {
	fmt.Println(`Usage: yaver launch <hetzner|aws|gcp|ssh> [options]

Provisions or adopts a Yaver-ready box on the chosen target. Auth + the
three coding runners (claude-code, codex, opencode) are mirrored from
THIS device automatically — the new box comes up signed in to your
subscriptions without any re-OAuth.

Providers:
  hetzner            Hetzner Cloud  — uses $HCLOUD_TOKEN, public snapshot
  aws                AWS EC2        — uses default AWS CLI creds, public AMI
  gcp                Google Cloud   — uses default gcloud creds, public image
  ssh user@host      Adopt an existing Linux box you can SSH to (NAS,
                     VPS, homelab, on-prem). No cloud image — installs the
                     agent in-place and registers it as your device.

Options:
  --arch <amd64|arm64>    Target architecture (default: arm64; cheaper +
                          available on every provider tier we use)
  --region <region>       Cloud region. AWS-only; Hetzner+GCP have defaults
                          in cloud-images.json
  --name <label>          Friendly label for the box (default: auto)
  --no-mirror             Skip runner-credential mirror (debug)
  --timeout <duration>    Overall launch timeout (default: 10m)

Examples:
  yaver launch hetzner
  yaver launch hetzner --arch amd64
  yaver launch aws --region us-east-1
  yaver launch gcp
  yaver launch ssh kivanc@homelab.lan

Image discovery:
  Public image IDs live in cloud-images.json at the repo root and are
  fetched at launch time. If the manifest has no image for your provider
  + arch yet (early in the rollout, or a new region), build one with
  scripts/build-cloud-image.sh.`)
}
