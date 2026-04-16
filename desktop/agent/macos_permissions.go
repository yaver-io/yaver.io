package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
)

type macOSPermissionStep struct {
	Title       string
	Description string
	URL         string
}

var macOSPermissionSteps = []macOSPermissionStep{
	{
		Title:       "Accessibility",
		Description: "Needed for deeper desktop control and input automation flows.",
		URL:         "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
	},
	{
		Title:       "Screen Recording",
		Description: "Needed for screenshots, live previews, and screen capture driven tooling.",
		URL:         "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
	},
	{
		Title:       "Automation",
		Description: "Needed when Yaver controls other apps via Apple Events.",
		URL:         "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation",
	},
	{
		Title:       "Microphone",
		Description: "Needed for voice input and dictation-driven task submission.",
		URL:         "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone",
	},
}

func stdinLooksInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func maybeRunMacOSPermissionOnboarding(source string) {
	if runtime.GOOS != "darwin" || !stdinLooksInteractive() {
		return
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.MacOSPermissionOnboardingDone {
		return
	}
	runMacOSPermissionOnboarding(cfg, source, false)
}

func runMacOSPermissions(args []string) {
	fs := flag.NewFlagSet("permissions", flag.ExitOnError)
	reset := fs.Bool("reset", false, "Forget the one-time onboarding flag before opening the checklist")
	fs.Parse(args)

	if runtime.GOOS != "darwin" {
		fmt.Println("macOS permission onboarding is only relevant on macOS.")
		return
	}

	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	if *reset {
		cfg.MacOSPermissionOnboardingDone = false
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "failed to reset onboarding flag: %v\n", err)
		}
	}
	runMacOSPermissionOnboarding(cfg, "permissions", true)
}

func runMacOSPermissionOnboarding(cfg *Config, source string, force bool) {
	if runtime.GOOS != "darwin" {
		return
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.MacOSPermissionOnboardingDone && !force {
		return
	}
	if !stdinLooksInteractive() {
		fmt.Println("macOS permission onboarding requires an interactive terminal.")
		fmt.Println("Run `yaver permissions` from Terminal when you're ready.")
		return
	}

	r := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("macOS Permissions")
	fmt.Println("-----------------")
	fmt.Println("Yaver can use a few macOS permissions depending on the features you turn on.")
	fmt.Println("This one-time checklist opens the common System Settings panes now so later")
	fmt.Println("serve / remote re-auth / web UI triggers do not keep nagging from Yaver's side.")
	fmt.Println()
	if source == "serve" {
		fmt.Println("Triggered from first interactive `yaver serve`.")
	} else if source == "init" {
		fmt.Println("Triggered from `yaver init`.")
	} else {
		fmt.Println("Manual run via `yaver permissions`.")
	}
	fmt.Println()

	if !force {
		if !promptYes(r, "Run the macOS permission checklist now?", true) {
			cfg.MacOSPermissionOnboardingDone = true
			if err := SaveConfig(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not persist onboarding flag: %v\n", err)
			}
			fmt.Println("Skipped. Yaver will stay quiet about this automatically now.")
			fmt.Println("Reopen it any time with `yaver permissions`.")
			fmt.Println()
			return
		}
	}

	for i, step := range macOSPermissionSteps {
		fmt.Printf("%d. %s\n", i+1, step.Title)
		fmt.Printf("   %s\n", step.Description)
		if promptYes(r, "   Open this System Settings pane now?", true) {
			if err := openMacOSSettingsURL(step.URL); err != nil {
				fmt.Printf("   Could not open System Settings automatically: %v\n", err)
				fmt.Printf("   Open it manually with: %s\n", step.URL)
			}
		}
		fmt.Print("   Press Enter to continue...")
		_, _ = r.ReadString('\n')
	}

	cfg.MacOSPermissionOnboardingDone = true
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not persist onboarding flag: %v\n", err)
	}

	fmt.Println()
	fmt.Println("macOS permission onboarding marked complete.")
	fmt.Println("Yaver will not show this checklist automatically again.")
	fmt.Println("Reopen it any time with `yaver permissions`.")
	fmt.Println("macOS may still show a one-time system prompt when a feature runs for the first")
	fmt.Println("time, or if the app/binary identity changes.")
	fmt.Println()
}

func openMacOSSettingsURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("empty URL")
	}
	return osexec.Command("open", rawURL).Start()
}
