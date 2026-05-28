package main

// launch_aws.go — provisions an EC2 instance from the public Yaver AMI.
// Uses the `aws` CLI; default region/creds resolution = same chain that
// `aws configure` produces (env, profile, IAM role).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func launchAWS(ctx context.Context, opts *launchOptions) error {
	if _, err := exec.LookPath("aws"); err != nil {
		return fmt.Errorf("aws CLI not found on PATH. Install: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html")
	}
	region := opts.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = os.Getenv("AWS_DEFAULT_REGION")
		}
	}
	if region == "" {
		return fmt.Errorf("--region is required for `yaver launch aws` (or set AWS_REGION). Example: --region us-east-1")
	}

	amiID, err := readAWSAMI(opts.Manifest, region, opts.Arch)
	if err != nil {
		return err
	}
	instanceType := readAWSInstanceType(opts.Manifest, opts.Arch)

	dc, err := requestLaunchDeviceCode(opts.SourceConfig)
	if err != nil {
		return fmt.Errorf("request device-code: %w", err)
	}
	if err := authorizeOwnDeviceCode(opts.SourceConfig, dc.UserCode); err != nil {
		return fmt.Errorf("authorize device-code: %w", err)
	}

	userData := buildCloudInitUserData(dc, opts.SourceConfig)
	udFile, err := os.CreateTemp("", "yaver-launch-aws-*.yaml")
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

	fmt.Printf("Provisioning AWS instance %q (region=%s, type=%s, ami=%s)...\n", name, region, instanceType, amiID)

	runArgs := []string{
		"ec2", "run-instances",
		"--region", region,
		"--image-id", amiID,
		"--instance-type", instanceType,
		"--user-data", "file://" + udFile.Name(),
		"--tag-specifications",
		fmt.Sprintf("ResourceType=instance,Tags=[{Key=Name,Value=%s},{Key=managed-by,Value=yaver-launch}]", name),
		"--query", "Instances[0].InstanceId",
		"--output", "text",
	}
	if keyName := os.Getenv("AWS_SSH_KEY_NAME"); keyName != "" {
		runArgs = append(runArgs, "--key-name", keyName)
	}
	out, err := exec.CommandContext(ctx, "aws", runArgs...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("aws ec2 run-instances failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("aws ec2 run-instances failed: %w", err)
	}
	instanceID := strings.TrimSpace(string(out))
	if instanceID == "" {
		return fmt.Errorf("aws ec2 run-instances returned empty instance id")
	}
	fmt.Printf("  Instance created: %s\n", instanceID)

	fmt.Println("  Waiting for instance-running state...")
	if err := exec.CommandContext(ctx, "aws", "ec2", "wait", "instance-running",
		"--region", region, "--instance-ids", instanceID).Run(); err != nil {
		return fmt.Errorf("aws ec2 wait instance-running failed: %w", err)
	}

	descOut, err := exec.CommandContext(ctx, "aws", "ec2", "describe-instances",
		"--region", region,
		"--instance-ids", instanceID,
		"--query", "Reservations[0].Instances[0].PublicIpAddress",
		"--output", "text",
	).Output()
	if err != nil {
		return fmt.Errorf("aws ec2 describe-instances failed: %w", err)
	}
	ip := strings.TrimSpace(string(descOut))
	if ip == "" || ip == "None" {
		return fmt.Errorf("instance %s has no public IP (private-subnet launch?)", instanceID)
	}
	fmt.Printf("  Public IP: %s\n", ip)

	fmt.Println("Waiting for first-boot to consume device-code...")
	if _, err := pollDeviceForOnline(ctx, dc); err != nil {
		return fmt.Errorf("box never came online: %w (terminate: aws ec2 terminate-instances --region %s --instance-ids %s)", err, region, instanceID)
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
	fmt.Printf("  Instance:    %s\n", instanceID)
	fmt.Printf("  Public IP:   %s\n", ip)
	if os.Getenv("AWS_SSH_KEY_NAME") != "" {
		fmt.Printf("  SSH:         ssh ubuntu@%s\n", ip)
	} else {
		fmt.Println("  SSH:         no AWS_SSH_KEY_NAME set — use SSM Session Manager or relaunch with a key")
	}
	return nil
}

func readAWSAMI(m *cloudImagesManifest, region, arch string) (string, error) {
	aws, ok := m.Providers["aws"]
	if !ok {
		return "", fmt.Errorf("manifest has no aws section")
	}
	amis, _ := aws["amis"].(map[string]any)
	if amis == nil {
		return "", fmt.Errorf("manifest has no aws.amis map")
	}
	regionMap, _ := amis[region].(map[string]any)
	if regionMap == nil {
		return "", fmt.Errorf("no Yaver AMIs published for region %q yet.\n"+
			"  Build + publish one:\n"+
			"    AWS_REGION=%s ./scripts/build-cloud-image.sh --provider aws --arch %s\n"+
			"  Or pick a region we've already published — see cloud-images.json", region, region, arch)
	}
	id, _ := regionMap[arch].(string)
	if id == "" {
		return "", fmt.Errorf("no Yaver AMI published for region=%s arch=%s yet.\n"+
			"  AWS_REGION=%s ./scripts/build-cloud-image.sh --provider aws --arch %s", region, arch, region, arch)
	}
	return id, nil
}

func readAWSInstanceType(m *cloudImagesManifest, arch string) string {
	aws, ok := m.Providers["aws"]
	if !ok {
		if arch == "arm64" {
			return "t4g.small"
		}
		return "t3.small"
	}
	defaults, _ := aws["defaultInstanceType"].(map[string]any)
	if defaults != nil {
		if v, _ := defaults[arch].(string); v != "" {
			return v
		}
	}
	if arch == "arm64" {
		return "t4g.small"
	}
	return "t3.small"
}
