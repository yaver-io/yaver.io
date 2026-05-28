package main

// launch_hetzner.go — provisions a Hetzner Cloud server from the public
// Yaver snapshot. Uses the `hcloud` CLI rather than the SDK to match
// scripts/build-cloud-image.sh + ci/hcloud/*.sh and avoid a new go.mod
// dependency.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func launchHetzner(ctx context.Context, opts *launchOptions) error {
	if _, err := exec.LookPath("hcloud"); err != nil {
		return fmt.Errorf("hcloud CLI not found on PATH. Install: brew install hcloud  (macOS)  /  https://github.com/hetznercloud/cli")
	}
	if os.Getenv("HCLOUD_TOKEN") == "" {
		return fmt.Errorf("HCLOUD_TOKEN env var is not set. Get a token from https://console.hetzner.cloud/ → Security → API Tokens")
	}

	// Hetzner snapshots are private to the project that owns the
	// HCLOUD_TOKEN, so cloud-images.json may not have one for this user
	// (only the repo owner's project gets a snapshot from the CI build).
	// Fall back to the vanilla Ubuntu 24.04 image — first boot takes a
	// few extra minutes while cloud-init installs yaver-cli from npm,
	// but the user-data path is identical (the pending-auth.json gets
	// consumed the same way once `yaver serve` starts).
	snapshotID, snapshotErr := readHetznerSnapshot(opts.Manifest, opts.Arch)
	imageRef := snapshotID
	usingFallback := false
	if snapshotErr != nil {
		imageRef = "ubuntu-24.04"
		usingFallback = true
	}
	serverType := readHetznerServerType(opts.Manifest, opts.Arch)
	location := readHetznerLocation(opts.Manifest)

	dc, err := requestLaunchDeviceCode(opts.SourceConfig)
	if err != nil {
		return fmt.Errorf("request device-code: %w", err)
	}
	if err := authorizeOwnDeviceCode(opts.SourceConfig, dc.UserCode); err != nil {
		return fmt.Errorf("authorize device-code: %w", err)
	}

	var userData string
	if usingFallback {
		userData = buildCloudInitUserDataWithInstall(dc, opts.SourceConfig)
	} else {
		userData = buildCloudInitUserData(dc, opts.SourceConfig)
	}
	udFile, err := os.CreateTemp("", "yaver-launch-user-data-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(udFile.Name())
	if _, err := udFile.WriteString(userData); err != nil {
		return err
	}
	udFile.Close()

	name := opts.Name
	if name == "" {
		name = fmt.Sprintf("yaver-%s", time.Now().Format("20060102-150405"))
	}

	if usingFallback {
		fmt.Printf("Provisioning Hetzner box %q (%s, %s, image=ubuntu-24.04 + cloud-init install)\n", name, serverType, location)
		fmt.Println("  No published Yaver snapshot for this Hetzner project — first boot installs yaver-cli from npm (~3 min extra).")
	} else {
		fmt.Printf("Provisioning Hetzner box %q (%s, %s, snapshot=%s)...\n", name, serverType, location, snapshotID)
	}
	out, err := exec.CommandContext(ctx, "hcloud", "server", "create",
		"--name", name,
		"--type", serverType,
		"--location", location,
		"--image", imageRef,
		"--user-data-from-file", udFile.Name(),
		"--label", "managed-by=yaver-launch",
		"-o", "json",
	).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("hcloud server create failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("hcloud server create failed: %w", err)
	}

	var createResp struct {
		Server struct {
			ID         int `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
	}
	if err := json.Unmarshal(out, &createResp); err != nil {
		return fmt.Errorf("parse hcloud create output: %w", err)
	}
	ip := createResp.Server.PublicNet.IPv4.IP
	if ip == "" {
		return fmt.Errorf("hcloud create returned no IPv4 address")
	}
	fmt.Printf("  Box created: id=%d  ip=%s\n", createResp.Server.ID, ip)

	fmt.Println("Waiting for first-boot to consume device-code...")
	if _, err := pollDeviceForOnline(ctx, dc); err != nil {
		return fmt.Errorf("box never came online: %w (delete with: hcloud server delete %s)", err, name)
	}
	fmt.Println("  Box is online and authenticated as your user.")

	if !opts.NoMirror {
		boxBase := fmt.Sprintf("http://%s:18080", ip)
		fmt.Println("Mirroring runner credentials to the box:")
		mirrorRunnersToBox(ctx, opts, boxBase)
	}

	fmt.Println()
	fmt.Println("✓ Done.")
	fmt.Printf("  Name:    %s\n", name)
	fmt.Printf("  IP:      %s\n", ip)
	fmt.Printf("  SSH:     ssh root@%s\n", ip)
	fmt.Printf("  Status:  yaver ping %s   (after the device row settles)\n", name)
	return nil
}

func readHetznerSnapshot(m *cloudImagesManifest, arch string) (string, error) {
	hetzner, ok := m.Providers["hetzner"]
	if !ok {
		return "", fmt.Errorf("manifest has no hetzner section")
	}
	snaps, _ := hetzner["snapshots"].(map[string]any)
	if snaps == nil {
		return "", fmt.Errorf("manifest has no hetzner.snapshots map")
	}
	v, _ := snaps[arch].(string)
	if v == "" {
		return "", fmt.Errorf("no Hetzner snapshot published yet for arch=%s\n"+
			"  Build one and update cloud-images.json:\n"+
			"    HCLOUD_TOKEN=… ./scripts/build-cloud-image.sh --provider hetzner --arch %s", arch, arch)
	}
	return v, nil
}

func readHetznerServerType(m *cloudImagesManifest, arch string) string {
	hetzner, ok := m.Providers["hetzner"]
	if !ok {
		// Defaults if manifest is missing the field.
		if arch == "amd64" {
			return "cpx21"
		}
		return "cax21"
	}
	defaults, _ := hetzner["defaultServerType"].(map[string]any)
	if defaults != nil {
		if v, _ := defaults[arch].(string); v != "" {
			return v
		}
	}
	if arch == "amd64" {
		return "cpx21"
	}
	return "cax21"
}

func readHetznerLocation(m *cloudImagesManifest) string {
	hetzner, ok := m.Providers["hetzner"]
	if !ok {
		return "hel1"
	}
	if v, _ := hetzner["defaultLocation"].(string); v != "" {
		return v
	}
	return "hel1"
}
