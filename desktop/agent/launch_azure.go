package main

// launch_azure.go — provisions an Azure VM with the same first-boot
// device-code bootstrap as every other cloud launch provider. Uses the `az`
// CLI so credentials, subscription selection, and tenant policy stay in Azure's
// normal auth chain.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func launchAzure(ctx context.Context, opts *launchOptions) error {
	if _, err := exec.LookPath("az"); err != nil {
		return fmt.Errorf("Azure CLI not found on PATH. Install: https://learn.microsoft.com/cli/azure/install-azure-cli")
	}
	resourceGroup := resolveAzureResourceGroup()
	if resourceGroup == "" {
		return fmt.Errorf("Azure resource group is required. Set YAVER_AZURE_RESOURCE_GROUP or AZURE_RESOURCE_GROUP")
	}
	location := opts.Region
	if location == "" {
		location = os.Getenv("YAVER_AZURE_LOCATION")
	}
	if location == "" {
		location = os.Getenv("AZURE_LOCATION")
	}
	if location == "" {
		location = readAzureDefaultLocation(opts.Manifest)
	}
	vmSize := readAzureVMSize(opts.Manifest, opts.Arch)
	imageRef := readAzureVMImage(opts.Manifest, opts.Arch)

	dc, err := requestLaunchDeviceCode(opts.SourceConfig)
	if err != nil {
		return fmt.Errorf("request device-code: %w", err)
	}
	if err := authorizeOwnDeviceCode(opts.SourceConfig, dc.UserCode); err != nil {
		return fmt.Errorf("authorize device-code: %w", err)
	}

	userData := buildCloudInitUserDataWithInstall(dc, opts.SourceConfig)
	udFile, err := os.CreateTemp("", "yaver-launch-azure-*.yaml")
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
	name = strings.ToLower(name)

	fmt.Printf("Provisioning Azure VM %q (group=%s, location=%s, size=%s, image=%s + cloud-init install)...\n", name, resourceGroup, location, vmSize, imageRef)
	createArgs := []string{
		"vm", "create",
		"--resource-group", resourceGroup,
		"--name", name,
		"--location", location,
		"--image", imageRef,
		"--size", vmSize,
		"--admin-username", "yaver",
		"--generate-ssh-keys",
		"--custom-data", udFile.Name(),
		"--public-ip-sku", "Standard",
		"--tags", "managed-by=yaver-launch",
		"--output", "json",
	}
	if subscription := strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID")); subscription != "" {
		createArgs = append(createArgs, "--subscription", subscription)
	}
	out, err := exec.CommandContext(ctx, "az", createArgs...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("az vm create failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("az vm create failed: %w", err)
	}
	var created struct {
		ID               string `json:"id"`
		PublicIPAddress  string `json:"publicIpAddress"`
		PrivateIPAddress string `json:"privateIpAddress"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return fmt.Errorf("parse az vm create output: %w", err)
	}
	ip := strings.TrimSpace(created.PublicIPAddress)
	if ip == "" {
		return fmt.Errorf("az vm create returned no public IP")
	}
	fmt.Printf("  VM created: %s  ip=%s\n", firstNonEmpty(created.ID, name), ip)

	fmt.Println("Waiting for first-boot to consume device-code...")
	if _, err := pollDeviceForOnline(ctx, dc); err != nil {
		return fmt.Errorf("box never came online: %w (delete: az vm delete -g %s -n %s --yes)", err, resourceGroup, name)
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
	fmt.Printf("  Group:       %s\n", resourceGroup)
	fmt.Printf("  Location:    %s\n", location)
	fmt.Printf("  IP:          %s\n", ip)
	fmt.Printf("  SSH:         ssh yaver@%s\n", ip)
	return nil
}

func resolveAzureResourceGroup() string {
	for _, key := range []string{"YAVER_AZURE_RESOURCE_GROUP", "AZURE_RESOURCE_GROUP"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func readAzureVMImage(m *cloudImagesManifest, arch string) string {
	if m != nil {
		if azure, ok := m.Providers["azure"]; ok {
			images, _ := azure["images"].(map[string]any)
			if images != nil {
				if v, _ := images[arch].(string); v != "" {
					return v
				}
			}
		}
	}
	if arch == "arm64" {
		return "Canonical:ubuntu-24_04-lts:server-arm64:latest"
	}
	return "Canonical:ubuntu-24_04-lts:server:latest"
}

func readAzureDefaultLocation(m *cloudImagesManifest) string {
	if m != nil {
		if azure, ok := m.Providers["azure"]; ok {
			if v, _ := azure["defaultLocation"].(string); v != "" {
				return v
			}
		}
	}
	return "westeurope"
}

func readAzureVMSize(m *cloudImagesManifest, arch string) string {
	if m != nil {
		if azure, ok := m.Providers["azure"]; ok {
			defaults, _ := azure["defaultVMSize"].(map[string]any)
			if defaults != nil {
				if v, _ := defaults[arch].(string); v != "" {
					return v
				}
			}
		}
	}
	if arch == "arm64" {
		return "Standard_D2ps_v5"
	}
	return "Standard_D2s_v5"
}
