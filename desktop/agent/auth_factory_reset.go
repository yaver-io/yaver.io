package main

import (
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
)

func runAuthFactoryReset(args []string) {
	fs := flag.NewFlagSet("auth factory-reset", flag.ExitOnError)
	convexURL := fs.String("convex-url", defaultConvexSiteURL, "Convex site URL")
	headless := fs.Bool("headless", false, "Use device code flow after reset")
	skipNPM := fs.Bool("skip-npm", false, "Skip npm refresh and reuse the current yaver binary")
	fs.Parse(args)

	fmt.Println("Factory-resetting Yaver auth state...")

	cfg, loadErr := LoadConfig()
	if loadErr == nil && cfg.AuthToken != "" {
		runSignout()
	} else if pid, running := isAgentRunning(); running {
		if proc, findErr := os.FindProcess(pid); findErr == nil {
			terminateProcess(proc)
			fmt.Println("Stopped running Yaver agent.")
		}
	}

	// Preserve relay reachability + Convex URL so the daemon stays
	// reachable from the dashboard / mobile app after the reset.
	// Wiping the entire config used to leave the box stranded:
	//   • no relay_password → tunnel registration fails
	//   • no relay_servers  → nowhere to connect outbound
	//   • no device_id      → ephemeral, but that's fine
	// The dashboard's pair flow needs a reachable agent. The right
	// recovery shape is "agent stays up in claim mode" — meaning a
	// running daemon that maintains its tunnel and exposes
	// /auth/pair endpoints, just without an auth_token. So we do
	// load → wipe-only-user-bound-fields → save instead of `rm`.
	preserved := &Config{}
	if loadErr == nil && cfg != nil {
		preserved.ConvexSiteURL = cfg.ConvexSiteURL
		preserved.RelayServers = cfg.RelayServers
		preserved.RelayPassword = cfg.RelayPassword
		preserved.CachedRelayPassword = cfg.CachedRelayPassword
		preserved.CachedRelayServers = cfg.CachedRelayServers
	}
	if saveErr := SaveConfig(preserved); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write reset config: %v\n", saveErr)
		// Fall through — try the brute-force remove path too so
		// the next start at least sees no stale auth.
		if path, pathErr := ConfigPath(); pathErr == nil && path != "" {
			_ = os.Remove(path)
		}
	} else {
		fmt.Println("Wiped auth state; preserved relay credentials so the dashboard can re-pair this box.")
	}

	// pendingAuthPath / pairedTokensPath are user-bound and should
	// always be removed. They don't carry relay info.
	for _, resolver := range []func() (string, error){pendingAuthPath, pairedTokensPath} {
		path, pathErr := resolver()
		if pathErr != nil || path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", path, err)
		}
	}

	nextBinary := ""
	if !*skipNPM {
		if path, refreshErr := refreshNpmYaverCLI(); refreshErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: npm refresh failed: %v\n", refreshErr)
			fmt.Fprintln(os.Stderr, "Continuing with the current yaver binary.")
		} else {
			nextBinary = path
		}
	}

	// Always prefer restarting the same binary path that launched this
	// process. Falling back to PATH here caused /usr/bin vs
	// /usr/local/bin drift on systemd hosts.
	if current := preferredYaverBinaryPath(); current != "" {
		nextBinary = current
	}
	if nextBinary == "" {
		fmt.Fprintln(os.Stderr, "Error: could not locate yaver binary for restart")
		os.Exit(1)
	}

	// Restart shape depends on whether we're recovering a running
	// daemon (dashboard-driven, headless) vs. an interactive CLI
	// invocation (developer typed `yaver auth factory-reset` at a
	// prompt). The dashboard / repair-ephemeral-auth path passes
	// --headless, in which case we re-launch `yaver serve` so the
	// daemon stays up and the dashboard can re-pair through the
	// preserved relay tunnel. Interactive runs fall back to the
	// classic browser sign-in flow.
	var nextArgs []string
	if *headless {
		// Keep the daemon listening. Bootstrap mode auth_token=""
		// + preserved relay creds means the relay tunnel still
		// registers, /auth/pair endpoints are reachable from the
		// dashboard, and the user can claim the box from the web
		// UI without touching SSH.
		nextArgs = []string{"serve"}
		fmt.Println()
		fmt.Printf("Restarting daemon with %s serve so the dashboard can re-pair...\n", filepath.Base(nextBinary))
	} else {
		nextArgs = []string{"auth", "--convex-url", *convexURL}
		fmt.Println()
		fmt.Printf("Restarting sign-in with %s...\n", filepath.Base(nextBinary))
	}
	if err := restartAuthBinary(nextBinary, nextArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: restart agent: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func refreshNpmYaverCLI() (string, error) {
	augmentAgentPATH()
	npmPath, err := osexec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found in PATH")
	}
	fmt.Println("Refreshing npm-installed Yaver CLI...")
	cmd := osexec.Command(npmPath, "install", "-g", "yaver-cli@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return "", err
	}
	augmentAgentPATH()
	yaverPath := preferredYaverBinaryPath()
	if yaverPath == "" {
		return "", fmt.Errorf("npm refresh succeeded but no yaver binary could be resolved")
	}
	return yaverPath, nil
}

func restartAuthBinary(binaryPath string, args []string) error {
	cmd := osexec.Command(binaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	return cmd.Run()
}

func spawnAuthFactoryReset(headless bool) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"auth", "factory-reset", "--skip-npm"}
	if headless {
		args = append(args, "--headless")
	}
	cmd := osexec.Command(execPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	applyDetachSysProcAttr(cmd)
	return cmd.Start()
}
