package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func runDev(args []string) {
	if len(args) == 0 {
		printDevUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "start":
		runDevStart(args[1:])
	case "stop":
		runDevStop()
	case "status":
		runDevStatus()
	case "reload":
		runDevReload()
	case "rebuild":
		runDevRebuild(args[1:])
	case "builds":
		runDevBuilds()
	default:
		fmt.Fprintf(os.Stderr, "Unknown dev subcommand: %s\n\n", args[0])
		printDevUsage()
		os.Exit(1)
	}
}

func runDevStart(args []string) {
	fs := flag.NewFlagSet("dev start", flag.ExitOnError)
	framework := fs.String("framework", "", "Framework (expo, flutter, vite, nextjs). Auto-detect if omitted.")
	port := fs.Int("port", 0, "Dev server port (framework default if 0)")
	platform := fs.String("platform", "ios", "Target platform (ios, android, web)")
	workDir := fs.String("dir", ".", "Project directory")
	standalone := fs.Bool("standalone", false, "Run standalone (not through agent)")
	fs.Parse(args)

	if *standalone {
		// Run dev server directly without agent
		mgr := NewDevServerManager()
		if err := mgr.Start(*framework, *workDir, *platform, *port); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		status := mgr.Status()
		fmt.Printf("Dev server running:\n")
		fmt.Printf("  Framework: %s\n", status.Framework)
		fmt.Printf("  Port:      %d\n", status.Port)
		fmt.Printf("  Bundle:    %s\n", status.BundleURL)
		fmt.Printf("\nPress Ctrl+C to stop.\n")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
			fmt.Println("\nStopping dev server...")
			mgr.Stop()
		case <-ctx.Done():
		}
		return
	}

	// Send to running agent
	body := map[string]interface{}{
		"framework": *framework,
		"workDir":   *workDir,
		"platform":  *platform,
		"port":      *port,
	}
	resp, err := localAgentRequest("POST", "/dev/start", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Printf("Dev server started:\n%s\n", data)
	fmt.Printf("\nAccessible at /dev/* through the agent and relay.\n")
}

func runDevStop() {
	resp, err := localAgentRequest("POST", "/dev/stop", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if resp["ok"] == "true" {
		fmt.Println("Dev server stopped.")
	} else {
		fmt.Fprintf(os.Stderr, "Failed: %v\n", resp["error"])
		os.Exit(1)
	}
}

func runDevStatus() {
	resp, err := localAgentRequest("GET", "/dev/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(data))
}

func runDevReload() {
	resp, err := localAgentRequest("POST", "/dev/reload", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if resp["ok"] == "true" {
		fmt.Println("Hot reload triggered.")
	} else {
		fmt.Fprintf(os.Stderr, "Failed: %v\n", resp["error"])
		os.Exit(1)
	}
}

func runDevRebuild(args []string) {
	project := ""
	if len(args) > 0 {
		project = args[0]
	}

	if project != "" {
		// Clear specific build marker
		marker := fmt.Sprintf("%s/%s.built", yaverBuildsDir(), project)
		if err := os.Remove(marker); err != nil {
			fmt.Fprintf(os.Stderr, "No build marker for %s\n", project)
			os.Exit(1)
		}
		fmt.Printf("Cleared build marker for %s — next hot reload will rebuild.\n", project)
	} else {
		// Clear all
		entries, _ := os.ReadDir(yaverBuildsDir())
		count := 0
		for _, e := range entries {
			if fmt.Sprintf("%s", e.Name()) != "" {
				os.Remove(fmt.Sprintf("%s/%s", yaverBuildsDir(), e.Name()))
				count++
			}
		}
		fmt.Printf("Cleared %d build marker(s) — next hot reload will rebuild all.\n", count)
	}
}

func runDevBuilds() {
	entries, err := os.ReadDir(yaverBuildsDir())
	if err != nil || len(entries) == 0 {
		fmt.Println("No build markers found.")
		return
	}
	fmt.Println("Build markers (next hot reload skips build for these):")
	for _, e := range entries {
		info, _ := e.Info()
		builtAt := "unknown"
		if info != nil {
			builtAt = info.ModTime().Format("2006-01-02 15:04:05")
		}
		name := e.Name()
		if len(name) > 6 {
			name = name[:len(name)-6] // strip ".built"
		}
		fmt.Printf("  %s  (built %s)\n", name, builtAt)
	}
}

func printDevUsage() {
	fmt.Println(`Usage: yaver dev <command>

Commands:
  start       Start a dev server (auto-detect or specify framework)
  stop        Stop the running dev server
  status      Show dev server status
  reload      Trigger hot reload
  rebuild     Clear build marker (force rebuild on next hot reload)
  builds      List build markers

Start options:
  --framework expo|flutter|vite|nextjs  Framework (auto-detect if omitted)
  --port N                              Dev server port (framework default)
  --platform ios|android|web            Target platform (default: ios)
  --dir /path                           Project directory (default: .)
  --standalone                          Run without agent (direct mode)

The dev server is proxied through the agent at /dev/*
and accessible via relay at https://<relay>/d/<deviceId>/dev/*

Examples:
  yaver dev start                           # auto-detect framework
  yaver dev start --framework expo          # force Expo/Metro
  yaver dev start --dir ./demo/AcmeStore    # specify project dir
  yaver dev reload                          # trigger hot reload
  yaver dev stop`)
}
