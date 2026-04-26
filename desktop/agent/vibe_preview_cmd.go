package main

// vibe_preview_cmd.go — `yaver vibe preview ...` CLI subcommands.
//
// Talks to the local agent on :18080 via localAgentRequest; the daemon does
// the actual capture work. Supports start/stop/status/snapshot for Phase 1.
// Stream tailing + summary scrubbing land alongside Phase 2/4.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func runVibe(args []string) {
	if len(args) == 0 {
		printVibeUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "preview":
		runVibePreview(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown vibe subcommand: %s\n\n", args[0])
		printVibeUsage()
		os.Exit(1)
	}
}

func runVibePreview(args []string) {
	if len(args) == 0 {
		printVibePreviewUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "start":
		runVibePreviewStart(args[1:])
	case "stop":
		runVibePreviewStop(args[1:])
	case "status":
		runVibePreviewStatus()
	case "snapshot":
		runVibePreviewSnapshot(args[1:])
	case "clip":
		runVibePreviewClip(args[1:])
	case "clips":
		runVibePreviewClips(args[1:])
	case "events":
		runVibePreviewEvents(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown vibe preview subcommand: %s\n\n", args[0])
		printVibePreviewUsage()
		os.Exit(1)
	}
}

func runVibePreviewClip(args []string) {
	fs := flag.NewFlagSet("vibe preview clip", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	source := fs.String("source", "", "Capture source: sim-ios | sim-android | phone (auto-detect if empty)")
	duration := fs.Int("duration", 0, "Recording duration in seconds (default 12, max 30)")
	hint := fs.String("hint", "", "Free-form exercise hint (used by Phase 7 driver)")
	fs.Parse(args)
	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		fs.Usage()
		os.Exit(1)
	}
	body := map[string]interface{}{
		"project":        *project,
		"source":         *source,
		"durationMaxSec": *duration,
		"exerciseHint":   *hint,
	}
	resp, err := localAgentRequest("POST", "/vibing/preview/clip/start", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(pretty))
}

func runVibePreviewClips(args []string) {
	fs := flag.NewFlagSet("vibe preview clips", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	fs.Parse(args)
	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		fs.Usage()
		os.Exit(1)
	}
	resp, err := localAgentRequest("GET", "/vibing/preview/clips?project="+*project, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(pretty))
}

func runVibePreviewEvents(args []string) {
	fs := flag.NewFlagSet("vibe preview events", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	fs.Parse(args)
	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		fs.Usage()
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "vibe preview events: not authenticated — run `yaver auth`")
		os.Exit(1)
	}
	url := fmt.Sprintf("http://127.0.0.1:18080/vibing/preview/events?project=%s", *project)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "vibe preview events: server returned %d\n", resp.StatusCode)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "── vibe-preview events for %s (Ctrl-C to exit) ──\n", *project)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		fmt.Println(payload)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "vibe preview events: connection closed: %v\n", err)
	}
}

func runVibePreviewStart(args []string) {
	fs := flag.NewFlagSet("vibe preview start", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required, used as session key)")
	target := fs.String("target", "", "Dev server URL to capture (e.g. http://127.0.0.1:3000)")
	mode := fs.String("mode", "live", "Capture mode: live | change-only | summary-only")
	profile := fs.String("profile", "", "Profile: live-direct | live-relay-wifi | live-relay-cell | change-only | summary-only")
	netMode := fs.String("net", "", "Network hint: direct | relay-wifi | relay-cell")
	fs.Parse(args)

	if *project == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "Error: --project and --target are required")
		fs.Usage()
		os.Exit(1)
	}

	body := map[string]interface{}{
		"project":   *project,
		"targetUrl": *target,
		"mode":      *mode,
		"profile":   *profile,
		"netMode":   *netMode,
	}
	resp, err := localAgentRequest("POST", "/vibing/preview/start", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(pretty))
}

func runVibePreviewStop(args []string) {
	fs := flag.NewFlagSet("vibe preview stop", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	fs.Parse(args)

	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		fs.Usage()
		os.Exit(1)
	}
	resp, err := localAgentRequest("POST", "/vibing/preview/stop", map[string]interface{}{"project": *project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if resp["ok"] == true {
		fmt.Printf("Preview for %s stopped.\n", *project)
		return
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(os.Stderr, string(pretty))
	os.Exit(1)
}

func runVibePreviewStatus() {
	resp, err := localAgentRequest("GET", "/vibing/preview/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(pretty))
}

func runVibePreviewSnapshot(args []string) {
	fs := flag.NewFlagSet("vibe preview snapshot", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	fs.Parse(args)

	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		fs.Usage()
		os.Exit(1)
	}
	resp, err := localAgentRequest("POST", "/vibing/preview/snapshot", map[string]interface{}{"project": *project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	pretty, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(pretty))
}

func printVibeUsage() {
	fmt.Print(`yaver vibe — vibe-coding subcommands

Usage:
  yaver vibe preview <start|stop|status|snapshot> [flags]

See ` + "`yaver vibe preview`" + ` for the live-preview screenshot stream of a
remote dev server, viewable from the mobile app while vibing.
`)
}

func printVibePreviewUsage() {
	fmt.Print(`yaver vibe preview — live screenshot stream of a dev server

Usage:
  yaver vibe preview start --project <name> --target <url> [flags]
  yaver vibe preview stop --project <name>
  yaver vibe preview status
  yaver vibe preview snapshot --project <name>

start flags:
  --project string    project name (used as session key) (required)
  --target  string    dev server URL to capture, e.g. http://127.0.0.1:3000 (required)
  --mode    string    live | change-only | summary-only            (default "live")
  --profile string    explicit profile (overrides --net):
                        live-direct | live-relay-wifi | live-relay-cell
                        | change-only | summary-only
  --net     string    network hint: direct | relay-wifi | relay-cell

The agent must be running (` + "`yaver serve`" + `) and have Chrome/Chromium installed.
`)
}
