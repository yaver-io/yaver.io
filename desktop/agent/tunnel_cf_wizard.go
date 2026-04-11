package main

// tunnel_cf_wizard.go — `yaver tunnel cloudflare wizard` walks
// the dev through turning their naked Mac mini / Linux box /
// Hetzner VPS into a publicly-reachable agent behind Cloudflare.
//
// No SSH. No inbound ports open. The whole chain is:
//
//   1. Check cloudflared is installed (and explain how if not).
//   2. Run `cloudflared tunnel login` — opens the Cloudflare
//      dashboard in the browser so the dev picks a zone.
//   3. Ask for a hostname (e.g. "mac.example.com") and a tunnel
//      name (derived from the machine hostname by default).
//   4. `cloudflared tunnel create <name>` — produces a tunnel
//      UUID + credentials .json in ~/.cloudflared/.
//   5. Write a yaml at ~/.cloudflared/<uuid>.yml that points at
//      http://127.0.0.1:18080 (the agent's HTTP server) with the
//      TLS verify off (self-signed).
//   6. `cloudflared tunnel route dns <name> <hostname>` — creates
//      the DNS CNAME in the dev's Cloudflare zone.
//   7. Save the CloudflareTunnelConfig into yaver's config.json
//      so the mobile app starts preferring it on the next
//      heartbeat.
//   8. Print instructions for running `cloudflared tunnel run
//      <name>` as a service (brew/systemd) so the tunnel stays up.
//
// The wizard is intentionally idempotent — re-running it should
// notice an already-configured tunnel and offer to reuse it.

import (
	"bufio"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// runTunnelCFWizard is the entry point wired from runTunnel().
func runTunnelCFWizard() {
	r := bufio.NewReader(os.Stdin)

	fmt.Println("Yaver — Cloudflare Tunnel Wizard")
	fmt.Println("--------------------------------")
	fmt.Println()
	fmt.Println("This wizard turns this machine into a publicly-reachable")
	fmt.Println("Yaver endpoint via your Cloudflare account. Requires:")
	fmt.Println("  • a Cloudflare account with at least one domain (zone)")
	fmt.Println("  • the cloudflared binary installed locally")
	fmt.Println()

	if _, err := osexec.LookPath("cloudflared"); err != nil {
		fmt.Println("✗ cloudflared is not installed.")
		fmt.Println()
		switch runtime.GOOS {
		case "darwin":
			fmt.Println("  brew install cloudflared")
		case "linux":
			fmt.Println("  curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared")
		default:
			fmt.Println("  Download from https://github.com/cloudflare/cloudflared/releases")
		}
		fmt.Println()
		fmt.Println("Re-run `yaver tunnel cloudflare wizard` once it's on PATH.")
		return
	}

	home, _ := os.UserHomeDir()
	cfDir := filepath.Join(home, ".cloudflared")
	certFile := filepath.Join(cfDir, "cert.pem")

	// Step 2: cloudflared login (skip if cert already present).
	if _, err := os.Stat(certFile); err != nil {
		fmt.Println("Step 1 of 5 — Cloudflare account login")
		fmt.Println("Running `cloudflared tunnel login` (opens browser)...")
		cmd := osexec.Command("cloudflared", "tunnel", "login")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "cloudflared login failed: %v\n", err)
			return
		}
	} else {
		fmt.Println("Step 1 of 5 — reusing existing cloudflared cert at", certFile)
	}

	// Step 3: pick a hostname and tunnel name.
	hostname := promptWithDefault(r, "Hostname (e.g. mac.mydomain.com)", "")
	if hostname == "" {
		fmt.Println("A hostname is required. Aborting.")
		return
	}
	defaultName, _ := os.Hostname()
	defaultName = strings.ReplaceAll(defaultName, ".", "-")
	if defaultName == "" {
		defaultName = "yaver-agent"
	}
	tunnelName := promptWithDefault(r, "Tunnel name", defaultName)

	// Step 4: create the tunnel (or reuse).
	fmt.Println()
	fmt.Println("Step 2 of 5 — creating tunnel", tunnelName)
	out, err := osexec.Command("cloudflared", "tunnel", "create", tunnelName).CombinedOutput()
	outStr := string(out)
	if err != nil && !strings.Contains(outStr, "already exists") {
		fmt.Fprintf(os.Stderr, "cloudflared create failed:\n%s\n", outStr)
		return
	}
	// Extract tunnel UUID from either "Created tunnel" line or
	// the existing-tunnel error message.
	uuid := extractTunnelUUID(outStr)
	if uuid == "" {
		// Fall back to `tunnel list` to resolve the UUID by name.
		listOut, _ := osexec.Command("cloudflared", "tunnel", "list").Output()
		uuid = lookupTunnelUUID(string(listOut), tunnelName)
	}
	if uuid == "" {
		fmt.Fprintln(os.Stderr, "Could not determine tunnel UUID — check `cloudflared tunnel list` and rerun.")
		return
	}
	fmt.Println("  UUID:", uuid)

	// Step 5: write config.yml pointing at the agent.
	configPath := filepath.Join(cfDir, uuid+".yml")
	credsPath := filepath.Join(cfDir, uuid+".json")
	yml := fmt.Sprintf(`tunnel: %s
credentials-file: %s

ingress:
  - hostname: %s
    service: http://127.0.0.1:18080
    originRequest:
      noTLSVerify: true
  - service: http_status:404
`, uuid, credsPath, hostname)
	if err := os.WriteFile(configPath, []byte(yml), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", configPath, err)
		return
	}
	fmt.Println()
	fmt.Println("Step 3 of 5 — wrote tunnel config:", configPath)

	// Step 6: route DNS.
	fmt.Println()
	fmt.Println("Step 4 of 5 — routing DNS", hostname, "→ tunnel")
	routeOut, err := osexec.Command("cloudflared", "tunnel", "route", "dns", tunnelName, hostname).CombinedOutput()
	if err != nil && !strings.Contains(string(routeOut), "already exists") {
		fmt.Fprintf(os.Stderr, "cloudflared route dns failed:\n%s\n", string(routeOut))
		// Keep going — tunnel is still usable once the dev
		// wires the DNS record manually.
	} else {
		fmt.Println("  DNS record created/verified.")
	}

	// Step 7: save into yaver config.
	cfg, _ := LoadConfig()
	if cfg == nil {
		cfg = &Config{}
	}
	url := "https://" + hostname
	already := false
	for i := range cfg.CloudflareTunnels {
		if cfg.CloudflareTunnels[i].URL == url {
			cfg.CloudflareTunnels[i].Label = tunnelName
			already = true
			break
		}
	}
	if !already {
		cfg.CloudflareTunnels = append(cfg.CloudflareTunnels, CloudflareTunnelConfig{
			ID:       shortHash(uuid),
			URL:      url,
			Label:    tunnelName,
			Priority: len(cfg.CloudflareTunnels) + 1,
		})
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save yaver config: %v\n", err)
		return
	}
	fmt.Println()
	fmt.Println("Step 5 of 5 — saved to ~/.yaver/config.json")

	// Step 8: run instructions.
	fmt.Println()
	fmt.Println("✓ Tunnel is wired. To keep it up across reboots:")
	switch runtime.GOOS {
	case "darwin":
		fmt.Printf("  sudo cloudflared --config %s service install\n", configPath)
		fmt.Println("  sudo launchctl start com.cloudflare.cloudflared")
	case "linux":
		fmt.Printf("  sudo cloudflared --config %s service install\n", configPath)
		fmt.Println("  sudo systemctl enable --now cloudflared")
	default:
		fmt.Printf("  cloudflared --config %s tunnel run %s\n", configPath, tunnelName)
	}
	fmt.Println()
	fmt.Printf("The Yaver mobile app will pick up %s automatically.\n", url)
}

func extractTunnelUUID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		// "Created tunnel <name> with id <uuid>"
		if strings.Contains(line, "with id") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "id" && i+1 < len(parts) {
					return strings.TrimRight(parts[i+1], ".,")
				}
			}
		}
		// "tunnel with name <name> already exists with id <uuid>"
		if strings.Contains(line, "already exists") && strings.Contains(line, "id") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "id" && i+1 < len(parts) {
					return strings.TrimRight(parts[i+1], ".,")
				}
			}
		}
	}
	return ""
}

func lookupTunnelUUID(listOutput, name string) string {
	for _, line := range strings.Split(listOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, f := range fields {
			if f == name {
				return fields[0]
			}
		}
	}
	return ""
}

func promptWithDefault(r *bufio.Reader, prompt, dflt string) string {
	if dflt != "" {
		fmt.Printf("%s [%s]: ", prompt, dflt)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return dflt
	}
	return line
}

func shortHash(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}
