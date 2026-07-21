package main

// launch_gcp.go — provisions a GCE instance from the public Yaver custom
// image. Uses the `gcloud` CLI; default project/zone resolution = same
// chain that `gcloud config get-value` produces.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func launchGCP(ctx context.Context, opts *launchOptions) error {
	if _, err := exec.LookPath("gcloud"); err != nil {
		return fmt.Errorf("gcloud CLI not found on PATH. Install: https://cloud.google.com/sdk/docs/install")
	}

	project, err := resolveGCPProject()
	if err != nil {
		return err
	}
	zone := readGCPZone(opts.Manifest)
	machineType := readGCPMachineType(opts.Manifest, opts.Arch)

	imageRef, err := readGCPImage(opts.Manifest, opts.Arch)
	usingFallback := false
	if err != nil {
		imageRef = readGCPUbuntuImageFamily(opts.Arch)
		usingFallback = true
	}

	dc, err := requestLaunchDeviceCode(opts.SourceConfig)
	if err != nil {
		return fmt.Errorf("request device-code: %w", err)
	}
	if err := authorizeOwnDeviceCode(opts.SourceConfig, dc.UserCode); err != nil {
		return fmt.Errorf("authorize device-code: %w", err)
	}

	userData := buildCloudInitUserData(dc, opts.SourceConfig)
	if usingFallback {
		userData = buildCloudInitUserDataWithInstall(dc, opts.SourceConfig)
	}
	udFile, err := os.CreateTemp("", "yaver-launch-gcp-*.yaml")
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
	// GCP instance names must be lowercase + DNS-safe; the auto-generated
	// timestamp form is already fine, but a user --name might not be.
	name = strings.ToLower(name)

	if usingFallback {
		fmt.Printf("Provisioning GCP instance %q (project=%s, zone=%s, type=%s, image-family=%s + cloud-init install)...\n", name, project, zone, machineType, imageRef)
		fmt.Println("  No published Yaver image for this arch — first boot installs yaver-cli from npm.")
	} else {
		fmt.Printf("Provisioning GCP instance %q (project=%s, zone=%s, type=%s)...\n", name, project, zone, machineType)
	}

	createArgs := []string{"compute", "instances", "create", name,
		"--project=" + project,
		"--zone=" + zone,
		"--machine-type=" + machineType,
		"--metadata-from-file=user-data=" + udFile.Name(),
		"--labels=managed-by=yaver-launch",
		"--format=json"}
	if usingFallback {
		createArgs = append(createArgs, "--image-family="+imageRef, "--image-project=ubuntu-os-cloud")
	} else {
		createArgs = append(createArgs, "--image="+imageRef)
	}
	createOut, err := exec.CommandContext(ctx, "gcloud", createArgs...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("gcloud compute instances create failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("gcloud compute instances create failed: %w", err)
	}
	var createParsed []struct {
		NetworkInterfaces []struct {
			AccessConfigs []struct {
				NatIP string `json:"natIP"`
			} `json:"accessConfigs"`
		} `json:"networkInterfaces"`
	}
	if err := json.Unmarshal(createOut, &createParsed); err != nil || len(createParsed) == 0 {
		return fmt.Errorf("parse gcloud create output: %w", err)
	}
	ip := ""
	if nis := createParsed[0].NetworkInterfaces; len(nis) > 0 && len(nis[0].AccessConfigs) > 0 {
		ip = nis[0].AccessConfigs[0].NatIP
	}
	if ip == "" {
		return fmt.Errorf("gcloud create returned no external IP (private VPC?)")
	}
	fmt.Printf("  Instance created: %s  ip=%s\n", name, ip)

	fmt.Println("Waiting for first-boot to consume device-code...")
	if _, err := pollDeviceForOnline(ctx, dc); err != nil {
		return fmt.Errorf("box never came online: %w (delete: gcloud compute instances delete %s --zone=%s)", err, name, zone)
	}
	fmt.Println("  Box is online and authenticated as your user.")

	if !opts.NoMirror {
		boxBase := fmt.Sprintf("http://%s:18080", ip)
		fmt.Println("Mirroring runner credentials to the box:")
		mirrorRunnersToBox(ctx, opts, boxBase)
	}

	fmt.Println()
	fmt.Println("✓ Done.")
	fmt.Printf("  Name:        %s\n", name)
	fmt.Printf("  Project:     %s\n", project)
	fmt.Printf("  Zone:        %s\n", zone)
	fmt.Printf("  IP:          %s\n", ip)
	fmt.Printf("  SSH:         gcloud compute ssh %s --zone=%s\n", name, zone)
	return nil
}

func resolveGCPProject() (string, error) {
	if env := strings.TrimSpace(os.Getenv("YAVER_GCP_PROJECT")); env != "" {
		return env, nil
	}
	out, err := exec.Command("gcloud", "config", "get-value", "project", "--quiet").Output()
	if err == nil {
		v := strings.TrimSpace(string(out))
		if v != "" && v != "(unset)" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no GCP project configured. Set one of:\n" +
		"  YAVER_GCP_PROJECT=<project-id>\n" +
		"  gcloud config set project <project-id>")
}

func readGCPImage(m *cloudImagesManifest, arch string) (string, error) {
	gcp, ok := m.Providers["gcp"]
	if !ok {
		return "", fmt.Errorf("manifest has no gcp section")
	}
	images, _ := gcp["images"].(map[string]any)
	if images == nil {
		return "", fmt.Errorf("manifest has no gcp.images map")
	}
	v, _ := images[arch].(string)
	if v == "" {
		return "", fmt.Errorf("no GCP image published for arch=%s yet.\n"+
			"  Build + publish one:\n"+
			"    ./scripts/build-cloud-image.sh --provider gcp --arch %s", arch, arch)
	}
	return v, nil
}

func readGCPZone(m *cloudImagesManifest) string {
	gcp, ok := m.Providers["gcp"]
	if !ok {
		return "europe-west4-a"
	}
	if v, _ := gcp["defaultZone"].(string); v != "" {
		return v
	}
	return "europe-west4-a"
}

func readGCPMachineType(m *cloudImagesManifest, arch string) string {
	gcp, ok := m.Providers["gcp"]
	if !ok {
		if arch == "arm64" {
			return "t2a-standard-1"
		}
		return "e2-small"
	}
	defaults, _ := gcp["defaultMachineType"].(map[string]any)
	if defaults != nil {
		if v, _ := defaults[arch].(string); v != "" {
			return v
		}
	}
	if arch == "arm64" {
		return "t2a-standard-1"
	}
	return "e2-small"
}

func readGCPUbuntuImageFamily(arch string) string {
	if arch == "arm64" {
		return "ubuntu-2404-lts-arm64"
	}
	return "ubuntu-2404-lts-amd64"
}
