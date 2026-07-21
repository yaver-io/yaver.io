package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type launchProviderCandidate struct {
	provider string
	ready    bool
	reason   string
}

func launchCloudAuto(ctx context.Context, opts *launchOptions) error {
	candidates := launchCloudCandidates(opts)
	for _, c := range candidates {
		if !c.ready {
			continue
		}
		selected := *opts
		selected.Provider = c.provider
		fmt.Printf("Yaver selected a cloud workspace location (%s). Provider choice is automatic.\n", cloudLaunchLabel(c.provider))
		switch c.provider {
		case "hetzner":
			return launchHetzner(ctx, &selected)
		case "gcp":
			return launchGCP(ctx, &selected)
		case "aws":
			return launchAWS(ctx, &selected)
		case "azure":
			return launchAzure(ctx, &selected)
		}
	}
	var reasons []string
	for _, c := range candidates {
		reasons = append(reasons, fmt.Sprintf("%s: %s", cloudLaunchLabel(c.provider), c.reason))
	}
	return fmt.Errorf("no cloud workspace provider is ready for automatic launch. %s", strings.Join(reasons, "; "))
}

func launchCloudCandidates(opts *launchOptions) []launchProviderCandidate {
	// Product placement prefers Yaver-owned paid capacity and credit-aware
	// backend policy. This local dev fallback only chooses among credentials
	// already configured on this machine, without exposing a provider picker.
	return []launchProviderCandidate{
		hetznerLaunchCandidate(),
		gcpLaunchCandidate(),
		awsLaunchCandidate(opts),
		azureLaunchCandidate(),
	}
}

func hetznerLaunchCandidate() launchProviderCandidate {
	if _, err := exec.LookPath("hcloud"); err != nil {
		return launchProviderCandidate{provider: "hetzner", reason: "hcloud CLI missing"}
	}
	if os.Getenv("HCLOUD_TOKEN") == "" {
		return launchProviderCandidate{provider: "hetzner", reason: "HCLOUD_TOKEN missing"}
	}
	return launchProviderCandidate{provider: "hetzner", ready: true}
}

func gcpLaunchCandidate() launchProviderCandidate {
	if _, err := exec.LookPath("gcloud"); err != nil {
		return launchProviderCandidate{provider: "gcp", reason: "gcloud CLI missing"}
	}
	if project, err := resolveGCPProject(); err != nil || strings.TrimSpace(project) == "" {
		return launchProviderCandidate{provider: "gcp", reason: "GCP project not configured"}
	}
	return launchProviderCandidate{provider: "gcp", ready: true}
}

func awsLaunchCandidate(opts *launchOptions) launchProviderCandidate {
	if _, err := exec.LookPath("aws"); err != nil {
		return launchProviderCandidate{provider: "aws", reason: "aws CLI missing"}
	}
	region := opts.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return launchProviderCandidate{provider: "aws", reason: "AWS region missing"}
	}
	return launchProviderCandidate{provider: "aws", ready: true}
}

func azureLaunchCandidate() launchProviderCandidate {
	if _, err := exec.LookPath("az"); err != nil {
		return launchProviderCandidate{provider: "azure", reason: "az CLI missing"}
	}
	if resolveAzureResourceGroup() == "" {
		return launchProviderCandidate{provider: "azure", reason: "Azure resource group missing"}
	}
	return launchProviderCandidate{provider: "azure", ready: true}
}

func cloudLaunchLabel(provider string) string {
	switch provider {
	case "hetzner":
		return "EU/US cloud"
	case "gcp":
		return "Google Cloud region"
	case "aws":
		return "AWS region"
	case "azure":
		return "Azure region"
	default:
		return "cloud"
	}
}
