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
		runSandboxBuild(args[1:])
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

func runSandboxBuild(args []string) {
	variant := SandboxVariantFat
	for _, a := range args {
		switch a {
		case "--slim":
			variant = SandboxVariantSlim
		case "--fat":
			variant = SandboxVariantFat
		case "-h", "--help":
			printSandboxUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown sandbox build flag: %s\n", a)
			printSandboxUsage()
			os.Exit(1)
		}
	}

	cr := NewContainerRunner()
	if !cr.IsAvailable() {
		fmt.Fprintln(os.Stderr, "Docker is not installed or not running.")
		fmt.Fprintln(os.Stderr, "Install Docker: https://docs.docker.com/get-docker/")
		os.Exit(1)
	}

	if variant == SandboxVariantSlim {
		fmt.Println("Building yaver-sandbox-slim (distroless) image...")
		fmt.Println("Smaller + faster cold-start. No Java/Ruby/Rust/Go/Python — runners + git only.")
	} else {
		fmt.Println("Building yaver-sandbox image...")
		fmt.Println("This may take a few minutes the first time (downloads Node, Go, Python, Claude Code).")
	}
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if err := cr.BuildImageVariant(ctx, variant); err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Sandbox image built successfully.")
	fmt.Println()
	if variant == SandboxVariantSlim {
		fmt.Println("Use the slim image:")
		fmt.Println("  yaver serve --containerize-guests --container-image yaver-sandbox-slim")
		fmt.Println()
		fmt.Println("Or set in ~/.yaver/config.json:")
		fmt.Println(`  "container_image": "yaver-sandbox-slim"`)
	} else {
		fmt.Println("Enable containerization:")
		fmt.Println("  yaver serve --containerize-guests    # Guest tasks in containers")
		fmt.Println("  yaver serve --containerize-host      # All tasks in containers")
		fmt.Println()
		fmt.Println("Or set in ~/.yaver/config.json:")
		fmt.Println(`  "containerize_guests": true`)
		fmt.Println(`  "containerize_host": true`)
	}
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
		fmt.Println("  Image (fat):   yaver-sandbox (ready)")
	} else {
		fmt.Println("  Image (fat):   not built — run 'yaver sandbox build'")
	}
	if cr.IsImageReadyVariant(SandboxVariantSlim) {
		fmt.Println("  Image (slim):  yaver-sandbox-slim (ready)")
	} else {
		fmt.Println("  Image (slim):  not built — run 'yaver sandbox build --slim'")
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
  build           Build the fat sandbox image (~2-3 min first time)
  build --slim    Build the distroless slim image (~250MB, runners-only)
  status          Show Docker and sandbox image status

Enabling containerization:
  yaver serve --containerize-guests    # Guest tasks only (security)
  yaver serve --containerize-host      # All tasks (clean builds)

Or in ~/.yaver/config.json:
  "containerize_guests": true
  "containerize_host": true

Images:
  yaver-sandbox       (fat, ~2 GB)  Node.js, Python, Go, Rust, Java,
                                    Ruby, Claude Code, Aider, Expo CLI,
                                    and common build tools. Use this
                                    for native Android/Cargo/etc. builds.
  yaver-sandbox-slim  (~250 MB)     Distroless base + Node + git + the
                                    three coding runners (claude-code,
                                    codex, opencode). Faster cold-start
                                    for tasks that only edit + run the
                                    project's own scripts. Select via
                                    "container_image": "yaver-sandbox-slim".

Project dirs are mounted as volumes — builds use your actual source code.

Custom images per project: place a Dockerfile.yaver in your project
root. The agent will build and use it instead of the default image.`)
}
