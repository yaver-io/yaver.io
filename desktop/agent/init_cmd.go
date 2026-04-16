package main

// init_cmd.go — `yaver init` first-run wizard.
//
// Fresh machine → running, publicly-reachable, auto-updating
// agent in one walkthrough. Each step is already implemented
// elsewhere in the binary; this file is just the glue that
// asks "do you want X?" and runs the existing command.
//
// Steps:
//   1. Sign in (yaver auth) if not already signed in
//   2. Pick a default AI runner
//   3. Set the bootstrap secret (for remote /auth/recover)
//   4. Optional: Cloudflare Tunnel wizard for public access
//   5. Optional: systemd/launchd install for boot-up
//   6. Optional: enable auto-update
//   7. Run doctor for a final sanity pass

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"os"
	"runtime"
	"strings"
)

func runInit(args []string) {
	_ = args
	r := bufio.NewReader(os.Stdin)

	fmt.Println("Yaver — first-run wizard")
	fmt.Println("------------------------")
	fmt.Println("This walks you through everything a fresh machine needs.")
	fmt.Println("Press Enter to accept the default for each step.")
	fmt.Println()

	// Step 1 — sign in
	cfg, _ := LoadConfig()
	fmt.Println("Step 1 of 7 — sign in")
	if cfg != nil && cfg.AuthToken != "" {
		fmt.Println("  ✓ Already signed in (token present in ~/.yaver/config.json)")
	} else {
		if promptYes(r, "Open browser to sign in now?", true) {
			runAuth(nil)
			cfg, _ = LoadConfig()
		} else {
			fmt.Println("  Skipping — you can run `yaver auth` later.")
		}
	}
	fmt.Println()

	// Step 2 — default runner
	fmt.Println("Step 2 of 7 — default AI runner")
	fmt.Println("  Options: claude, codex, aider, goose, amp, opencode, ollama")
	runner := prompt(r, "Default runner", "claude")
	if runner != "" && cfg != nil {
		runSetRunner([]string{runner})
	}
	fmt.Println()

	// Step 3 — bootstrap secret
	fmt.Println("Step 3 of 7 — bootstrap secret")
	fmt.Println("  Used by the mobile app to recover this agent from outside")
	fmt.Println("  the LAN if auth expires. Store the plaintext in your password")
	fmt.Println("  manager and paste it into Yaver mobile → Settings → Recovery.")
	if promptYes(r, "Generate + save a new bootstrap secret?", true) {
		secret := generateBootstrapSecret()
		if err := SetBootstrapSecret(secret); err != nil {
			fmt.Println("  error:", err)
		} else {
			fmt.Println()
			fmt.Println("  Your bootstrap secret (save this now — it won't be shown again):")
			fmt.Println("  ", secret)
			fmt.Println()
		}
	}
	fmt.Println()

	// Step 4 — Cloudflare tunnel
	fmt.Println("Step 4 of 7 — public tunnel (optional)")
	fmt.Println("  For headless machines you'll reach from outside the LAN,")
	fmt.Println("  a Cloudflare Tunnel is the simplest choice.")
	if promptYes(r, "Run the Cloudflare Tunnel wizard?", false) {
		runTunnelCFWizard()
	} else {
		fmt.Println("  Skipping — alternatives: `yaver relay add`, Tailscale, SSH port-forward.")
	}
	fmt.Println()

	// Step 5 — service install
	fmt.Println("Step 5 of 7 — run on boot")
	if runtime.GOOS == "linux" {
		if promptYes(r, "Install systemd user service so yaver starts on login?", true) {
			runServe([]string{"--install-systemd"})
		}
	} else if runtime.GOOS == "darwin" {
		if promptYes(r, "Install launchd agent so yaver starts on login?", true) {
			// Desktop installer handles this; show instructions.
			fmt.Println("  macOS: launch the Yaver desktop installer or run `yaver serve` to fork.")
		}
	} else {
		fmt.Println("  (Manual: start `yaver serve` at boot.)")
	}
	fmt.Println()

	if runtime.GOOS == "darwin" {
		maybeRunMacOSPermissionOnboarding("init")
	}

	// Step 6 — auto update
	fmt.Println("Step 6 of 7 — auto-update")
	if promptYes(r, "Auto-update agent from GitHub releases?", true) {
		runConfigSet("auto-update", "true")
	}
	fmt.Println()

	// Step 7 — doctor
	fmt.Println("Step 7 of 7 — doctor")
	if promptYes(r, "Run `yaver doctor` now?", true) {
		fmt.Println()
		runDoctor()
	}
	fmt.Println()
	fmt.Println("Done! Start the agent with `yaver serve`.")
}

func prompt(r *bufio.Reader, label, dflt string) string {
	if dflt != "" {
		fmt.Printf("> %s [%s]: ", label, dflt)
	} else {
		fmt.Printf("> %s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return dflt
	}
	return line
}

func promptYes(r *bufio.Reader, label string, dflt bool) bool {
	d := "y/N"
	if dflt {
		d = "Y/n"
	}
	fmt.Printf("> %s [%s]: ", label, d)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return dflt
	}
	return line == "y" || line == "yes"
}

// generateBootstrapSecret returns a random 24-char alphanumeric
// passphrase. Not a word list — the mobile app copies/pastes it,
// and a 24-char base36-ish string has ~124 bits of entropy.
func generateBootstrapSecret() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 24)
	buf := make([]byte, 24)
	_, _ = randomRead(buf)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// randomRead is an indirection so tests could replace it; the
// default uses crypto/rand.
var randomRead = func(b []byte) (int, error) {
	return rand.Read(b)
}
