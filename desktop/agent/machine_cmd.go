package main

// machine_cmd.go — `yaver machine ...` CLI surface for the
// headless-hardware ops story. Right now this is a thin
// façade over diskhealth.go (SMART + disk space), but it's
// the landing spot for future remote power, sleep scheduling,
// temperature monitoring, and the "your Mac mini hasn't
// checked in for 10 minutes" heartbeat alert.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func runMachine(args []string) {
	if len(args) == 0 {
		printMachineUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "health":
		runMachineHealthCmd()
	case "scan":
		runDiskHealthScan()
		runMachineHealthCmd()
	case "onboarding":
		runMachineOnboarding(args[1:])
	case "edge-loop":
		runMachineEdgeLoop(args[1:])
	case "companion":
		emitCompanionManifest(args[1:])
	case "help", "--help", "-h":
		printMachineUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown machine subcommand: %s\n\n", args[0])
		printMachineUsage()
		os.Exit(1)
	}
}

func printMachineUsage() {
	fmt.Print(`Yaver machine — headless hardware management.

Usage:
  yaver machine health            Print the latest disk + SMART snapshot
  yaver machine scan              Force a fresh scan now
  yaver machine onboarding ...    Configure OpenAI / GitHub / GitLab on this or a remote machine
  yaver machine edge-loop ...     Durable poll→understand→Talos-sync loop (RTU/TCP); run as a companion service
  yaver machine companion ...     Print a yaver.companion.yaml that runs edge-loop as a reboot-durable service

When the agent is running as a daemon, it scans every 10
minutes and fires a notification whenever a filesystem crosses
85% / 95% or a drive reports a SMART failure. No vendor account
— state lives under ~/.yaver/ and alerts go through the
existing push channel.
`)
}

func runMachineOnboarding(args []string) {
	if len(args) == 0 {
		printMachineOnboardingUsage()
		return
	}
	switch args[0] {
	case "status", "ls", "list":
		runMachineOnboardingStatus(args[1:])
	case "apply", "set":
		runMachineOnboardingApply(args[1:])
	case "help", "-h", "--help":
		printMachineOnboardingUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown machine onboarding subcommand: %s\n\n", args[0])
		printMachineOnboardingUsage()
		os.Exit(1)
	}
}

func printMachineOnboardingUsage() {
	fmt.Print(`yaver machine onboarding — provider/bootstrap setup for dev machines

Usage:
  yaver machine onboarding status [--target <deviceId>]
  yaver machine onboarding apply [--target <deviceId>] [--openai-api-key <key>] [--github-token <pat>] [--gitlab-token <pat>] [--gitlab-host <host>] [--clone=true|false] [--ci=true|false]

Examples:
  yaver machine onboarding status --target cloud-1234
  yaver machine onboarding apply --target cloud-1234 --openai-api-key $OPENAI_API_KEY
  yaver machine onboarding apply --target cloud-1234 --github-token $GITHUB_TOKEN --gitlab-token $GITLAB_TOKEN
`)
}

func runMachineOnboardingStatus(args []string) {
	fs := flag.NewFlagSet("machine onboarding status", flag.ExitOnError)
	target := fs.String("target", "", "remote device ID to inspect")
	fs.Parse(args)

	var (
		status machineOnboardingStatus
		err    error
	)
	if strings.TrimSpace(*target) != "" {
		status, err = fetchMachineOnboardingStatusRemote(strings.TrimSpace(*target))
	} else {
		status = collectMachineOnboardingStatus()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine onboarding status: %v\n", err)
		os.Exit(1)
	}
	if len(status.Providers) == 0 {
		fmt.Println("No onboarding status available.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tREADY\tCONFIGURED\tDETAIL")
	for _, provider := range status.Providers {
		ready := "no"
		if provider.Ready {
			ready = "yes"
		}
		configured := "no"
		if provider.Configured {
			configured = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", provider.ID, ready, configured, firstNonEmpty(provider.Detail, provider.Warning, "-"))
	}
	w.Flush()
}

func runMachineOnboardingApply(args []string) {
	fs := flag.NewFlagSet("machine onboarding apply", flag.ExitOnError)
	target := fs.String("target", "", "remote device ID to update")
	openAIKey := fs.String("openai-api-key", "", "OpenAI API key")
	githubToken := fs.String("github-token", "", "GitHub personal access token")
	gitlabToken := fs.String("gitlab-token", "", "GitLab personal access token")
	gitlabHost := fs.String("gitlab-host", "", "GitLab host (default gitlab.com)")
	clone := fs.Bool("clone", true, "configure git clone/pull credentials")
	ci := fs.Bool("ci", true, "configure vault CI/deploy token")
	notes := fs.String("notes", "", "optional notes for the OpenAI vault entry")
	fs.Parse(args)

	req := machineOnboardingApplyRequest{
		OpenAIAPIKey: *openAIKey,
		GitHubToken:  *githubToken,
		GitLabToken:  *gitlabToken,
		GitLabHost:   *gitlabHost,
		ApplyClone:   clone,
		ApplyCIToken: ci,
		Notes:        *notes,
	}

	var (
		result map[string]any
		err    error
	)
	if strings.TrimSpace(*target) != "" {
		result, err = applyMachineOnboardingRemote(strings.TrimSpace(*target), req)
	} else {
		result, err = applyMachineOnboardingLocal(req)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "machine onboarding apply: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(pretty))
}
