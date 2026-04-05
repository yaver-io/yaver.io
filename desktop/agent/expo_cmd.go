package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

func runExpo(args []string) {
	if len(args) == 0 {
		printExpoUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "setup":
		runExpoSetup(args[1:])
	case "start":
		runExpoStart(args[1:])
	case "build":
		runExpoBuild(args[1:])
	case "status":
		runExpoStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown expo subcommand: %s\n\n", args[0])
		printExpoUsage()
		os.Exit(1)
	}
}

func printExpoUsage() {
	fmt.Print(`Usage:
  yaver expo setup [--dir <path>]                Inject Yaver Feedback SDK into Expo project
  yaver expo start [--dir <path>] [--port 8081]  Start Expo Metro + P2P tunnel for hot reload
  yaver expo build android [--dir <path>]        Build Android (local Expo)
  yaver expo build ios [--dir <path>]            Build iOS (local Expo)
  yaver expo build android --eas [--dir <path>]  Build Android via EAS Build (cloud)
  yaver expo build ios --eas [--dir <path>]      Build iOS via EAS Build (no Mac needed)
  yaver expo status                              Show running Expo session + tunnels

Vibe coding workflow:
  1. yaver expo setup        → adds feedback SDK + Expo config plugin
  2. yaver expo start        → Metro bundler tunneled to your phone via P2P
  3. Shake phone → send visual feedback → AI fixes code → hot reload

Works on any machine (Linux, Mac, Yaver Cloud). No Mac needed for iOS via --eas.
Credentials (EAS tokens, Apple certs) stay on your machine — never sent to Yaver servers.
`)
}

// isExpoProject checks if the given directory is an Expo project.
func isExpoProject(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	// Check both dependencies and devDependencies for "expo"
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[key].(map[string]interface{}); ok {
			if _, found := deps["expo"]; found {
				return true
			}
		}
	}
	return false
}

// addPluginToAppJSON idempotently adds the feedback SDK config plugin to app.json.
func addPluginToAppJSON(appJSONPath string) error {
	data, err := os.ReadFile(appJSONPath)
	if err != nil {
		return err
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("invalid JSON in %s: %w", appJSONPath, err)
	}

	const pluginName = "yaver-feedback-react-native"

	// Navigate to expo.plugins
	expo, ok := config["expo"].(map[string]interface{})
	if !ok {
		// No "expo" key — might be a flat config
		expo = config
	}

	plugins, _ := expo["plugins"].([]interface{})

	// Check if already present
	for _, p := range plugins {
		if s, ok := p.(string); ok && s == pluginName {
			return nil // already added
		}
	}

	plugins = append(plugins, pluginName)
	expo["plugins"] = plugins

	if _, hasExpo := config["expo"]; hasExpo {
		config["expo"] = expo
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(appJSONPath, append(out, '\n'), 0644)
}

func runExpoSetup(args []string) {
	fs := flag.NewFlagSet("expo setup", flag.ExitOnError)
	dir := fs.String("dir", ".", "Expo project directory")
	fs.Parse(args)

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	// Verify Expo project
	if !isExpoProject(absDir) {
		fmt.Fprintln(os.Stderr, "Not an Expo project (no 'expo' in package.json dependencies).")
		fmt.Fprintln(os.Stderr, "Run this in an Expo project directory, or use --dir <path>.")
		os.Exit(1)
	}

	pm := detectPackageManager(absDir)
	fmt.Printf("Expo project detected (%s)\n\n", pm)

	// Install feedback SDK
	var installCmd *osexec.Cmd
	switch pm {
	case "yarn":
		installCmd = osexec.Command("yarn", "add", "yaver-feedback-react-native")
	case "pnpm":
		installCmd = osexec.Command("pnpm", "add", "yaver-feedback-react-native")
	default:
		installCmd = osexec.Command("npm", "install", "yaver-feedback-react-native")
	}
	installCmd.Dir = absDir
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	fmt.Printf("Installing yaver-feedback-react-native via %s...\n", pm)
	if err := installCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()

	// Add config plugin to app.json
	appJSON := filepath.Join(absDir, "app.json")
	appConfigJS := filepath.Join(absDir, "app.config.js")
	appConfigTS := filepath.Join(absDir, "app.config.ts")

	if _, err := os.Stat(appJSON); err == nil {
		if err := addPluginToAppJSON(appJSON); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update app.json: %v\n", err)
		} else {
			fmt.Println("Added yaver-feedback-react-native to app.json plugins.")
		}
	} else if _, err := os.Stat(appConfigJS); err == nil {
		fmt.Println("Detected app.config.js — add the plugin manually:")
		fmt.Println(`  plugins: [...existingPlugins, "yaver-feedback-react-native"]`)
	} else if _, err := os.Stat(appConfigTS); err == nil {
		fmt.Println("Detected app.config.ts — add the plugin manually:")
		fmt.Println(`  plugins: [...existingPlugins, "yaver-feedback-react-native"]`)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println()
	fmt.Println("  1. Add to your root component (App.tsx or app/_layout.tsx):")
	fmt.Println()
	fmt.Println("     import { initExpo, FeedbackModal } from 'yaver-feedback-react-native';")
	fmt.Println("     initExpo(); // auto-discovers your dev machine")
	fmt.Println()
	fmt.Println("     // In your root component JSX:")
	fmt.Println("     <>")
	fmt.Println("       <YourApp />")
	fmt.Println("       <FeedbackModal />")
	fmt.Println("     </>")
	fmt.Println()
	fmt.Println("  2. Create a dev build (required for full feedback features):")
	fmt.Println("     npx expo prebuild")
	fmt.Println("     npx expo run:android   # or: yaver expo build android")
	fmt.Println("     npx expo run:ios       # or: yaver expo build ios --eas")
	fmt.Println()
	fmt.Println("  3. Start hot reload session:")
	fmt.Println("     yaver expo start")
	fmt.Println()
	fmt.Println("  Shake your phone to send visual feedback → AI agent fixes → hot reload.")
	fmt.Println()
	fmt.Println("  Note: Expo Go has limited native module support. Use a dev build for")
	fmt.Println("  screen recording and voice annotations. Screenshots + shake work everywhere.")
}

func runExpoStart(args []string) {
	fs := flag.NewFlagSet("expo start", flag.ExitOnError)
	dir := fs.String("dir", "", "Expo project directory")
	port := fs.Int("port", 8081, "Metro bundler port")
	fs.Parse(args)

	// Start Metro via build system
	body := map[string]interface{}{
		"platform": "custom",
		"workDir":  *dir,
		"args":     []string{fmt.Sprintf("npx expo start --port %d", *port)},
	}
	resp, err := localAgentRequest("POST", "/builds", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting Expo: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the agent running? Start with 'yaver serve'.")
		os.Exit(1)
	}

	var build Build
	remarshal(resp, &build)
	fmt.Printf("Expo Metro started (build %s)\n", build.ID)

	// Create tunnel for Metro
	createTunnel(*port, "expo-metro")

	fmt.Println()
	fmt.Println("Expo dev session active:")
	fmt.Printf("  Build output: yaver build status %s\n", build.ID)
	fmt.Printf("  Tunnel: localhost:%d → your phone via P2P\n", *port)
	fmt.Println()
	fmt.Println("  Your phone connects through Yaver — Metro hot reload just works.")
	fmt.Println("  Shake to send feedback → AI agent fixes → changes hot reload.")
	fmt.Println()
	fmt.Println("  yaver logs       View Metro output")
	fmt.Println("  yaver expo status  Check session status")
}

func runExpoBuild(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver expo build <android|ios> [--dir <path>] [--eas]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("expo build", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	eas := fs.Bool("eas", false, "Build via EAS Build (cloud — no Mac needed for iOS)")
	fs.Parse(args[1:])

	if *eas {
		// EAS Build — cloud build, credentials stay local in ~/.expo/
		easPlatform := target
		if easPlatform != "android" && easPlatform != "ios" {
			fmt.Fprintf(os.Stderr, "Unknown target: %s (use android or ios)\n", target)
			os.Exit(1)
		}
		fmt.Println("Starting EAS Build (cloud)...")
		fmt.Println("  Credentials are read from your local machine (~/.expo/, env vars).")
		fmt.Println("  They are sent directly to EAS — never to Yaver servers.")
		fmt.Println()
		cmd := fmt.Sprintf("npx eas-cli build --platform %s --non-interactive", easPlatform)
		startBuildViaAgent(PlatformCustom, *dir, []string{cmd})
		return
	}

	// Local Expo build
	var platform BuildPlatform
	switch target {
	case "android":
		platform = PlatformExpoAndroid
	case "ios":
		platform = PlatformExpoIOS
	default:
		fmt.Fprintf(os.Stderr, "Unknown target: %s (use android or ios)\n", target)
		os.Exit(1)
	}

	startBuildViaAgent(platform, *dir, fs.Args())
}

func runExpoStatus() {
	// Query tunnels
	tunnelResp, tunnelErr := localAgentRequest("GET", "/tunnels", nil)
	// Query builds
	buildResp, buildErr := localAgentRequest("GET", "/builds", nil)

	if tunnelErr != nil && buildErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", tunnelErr)
		fmt.Fprintln(os.Stderr, "Is the agent running? Start with 'yaver serve'.")
		os.Exit(1)
	}

	// Filter expo tunnels
	fmt.Println("Expo Tunnels:")
	if tunnelErr == nil {
		var tunnels []TunnelSession
		remarshal(tunnelResp, &tunnels)

		expoTunnels := 0
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, t := range tunnels {
			if strings.Contains(t.Protocol, "expo") {
				if expoTunnels == 0 {
					fmt.Fprintln(w, "  ID\tPORT\tPROTOCOL\tACTIVE")
				}
				fmt.Fprintf(w, "  %s\t%d\t%s\t%v\n", t.ID, t.LocalPort, t.Protocol, t.Active)
				expoTunnels++
			}
		}
		w.Flush()
		if expoTunnels == 0 {
			fmt.Println("  (none)")
		}
	}

	fmt.Println()
	fmt.Println("Expo Builds:")
	if buildErr == nil {
		var builds []BuildSummary
		remarshal(buildResp, &builds)

		expoBuilds := 0
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, b := range builds {
			if strings.Contains(string(b.Platform), "expo") || strings.Contains(b.ArtifactName, "expo") {
				if expoBuilds == 0 {
					fmt.Fprintln(w, "  ID\tPLATFORM\tSTATUS\tARTIFACT")
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", b.ID, b.Platform, b.Status, b.ArtifactName)
				expoBuilds++
			}
		}
		w.Flush()
		if expoBuilds == 0 {
			fmt.Println("  (none)")
		}
	}
}
