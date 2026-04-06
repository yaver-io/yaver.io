package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func runSandbox(args []string) {
	if len(args) == 0 {
		printSandboxUsage()
		return
	}

	switch args[0] {
	case "build":
		runSandboxBuild()
	case "status":
		runSandboxStatus()
	case "help", "--help", "-h":
		printSandboxUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown sandbox subcommand: %s\n", args[0])
		printSandboxUsage()
		os.Exit(1)
	}
}

func runSandboxBuild() {
	cr := NewContainerRunner()
	if !cr.IsAvailable() {
		fmt.Fprintln(os.Stderr, "Docker is not installed or not running.")
		fmt.Fprintln(os.Stderr, "Install Docker: https://docs.docker.com/get-docker/")
		os.Exit(1)
	}

	fmt.Println("Building yaver-sandbox image...")
	fmt.Println("This may take a few minutes the first time (downloads Node, Go, Python, Claude Code).")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if err := cr.BuildImage(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Sandbox image built successfully.")
	fmt.Println()
	fmt.Println("Enable containerization:")
	fmt.Println("  yaver serve --containerize-guests    # Guest tasks in containers")
	fmt.Println("  yaver serve --containerize-host      # All tasks in containers")
	fmt.Println()
	fmt.Println("Or set in ~/.yaver/config.json:")
	fmt.Println(`  "containerize_guests": true`)
	fmt.Println(`  "containerize_host": true`)
}

func runSandboxStatus() {
	cr := NewContainerRunner()

	fmt.Println("Yaver Sandbox Status")
	fmt.Println()

	if !cr.IsAvailable() {
		fmt.Println("  Docker:    not available")
		fmt.Println()
		fmt.Println("Install Docker to use containerized task execution.")
		return
	}

	fmt.Println("  Docker:    available")
	if cr.IsImageReady() {
		fmt.Println("  Image:     yaver-sandbox (ready)")
	} else {
		fmt.Println("  Image:     not built")
		fmt.Println()
		fmt.Println("  Run 'yaver sandbox build' to build the sandbox image.")
	}

	cfg, err := LoadConfig()
	if err == nil {
		fmt.Println()
		fmt.Printf("  Guest containerization:  %v\n", cfg.ContainerizeGuests)
		fmt.Printf("  Host containerization:   %v\n", cfg.ContainerizeHost)
		networkMode := cfg.ContainerNetwork
		if networkMode == "" {
			networkMode = "host (default)"
		}
		fmt.Printf("  Network mode:            %s\n", networkMode)
		fmt.Printf("  Read-only rootfs:        %v\n", cfg.ContainerReadOnly)
		if cfg.ContainerImage != "" {
			fmt.Printf("  Custom image:            %s\n", cfg.ContainerImage)
		}
		if cfg.ContainerCPU != "" {
			fmt.Printf("  CPU limit:               %s\n", cfg.ContainerCPU)
		}
		if cfg.ContainerMemory != "" {
			fmt.Printf("  Memory limit:            %s\n", cfg.ContainerMemory)
		}
		if len(cfg.ContainerMounts) > 0 {
			fmt.Printf("  Extra mounts:            %d configured\n", len(cfg.ContainerMounts))
		}
	}
}

func printSandboxUsage() {
	fmt.Println(`Usage: yaver sandbox <command>

Run AI agent tasks inside Docker containers for isolation and security.
Containerization is optional and disabled by default.

Commands:
  build       Build the yaver-sandbox Docker image (~2-3 min first time)
  status      Show Docker and sandbox image status

Enabling containerization:
  yaver serve --containerize-guests    # Guest tasks only (security)
  yaver serve --containerize-host      # All tasks (clean builds)

Or in ~/.yaver/config.json:
  "containerize_guests": true
  "containerize_host": true

The sandbox image includes: Node.js, Python, Go, Rust, Java, Ruby,
Claude Code, Aider, Expo CLI, and common build tools. Project dirs
are mounted as volumes — builds use your actual source code.

Custom images per project: place a Dockerfile.yaver in your project
root. The agent will build and use it instead of the default image.`)
}
