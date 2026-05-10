package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const version = "0.1.17"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := os.Args[1]
	switch cmd {
	case "serve":
		runServe(os.Args[2:])
	case "tunnel":
		runTunnel(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "set-password":
		runSetPassword(os.Args[2:])
	case "logs":
		runLogs(os.Args[2:])
	case "tunnels":
		runTunnels(os.Args[2:])
	case "restart":
		runRestart(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("yaver-relay %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`yaver-relay — P2P relay server for Yaver

Usage:
  yaver-relay serve          Run the relay server (deploy on public VPS)
  yaver-relay tunnel         Connect local agent to relay (run on dev machine)
  yaver-relay status         Check if the relay is running and show status
  yaver-relay tunnels        Show active tunnels
  yaver-relay set-password   Update the relay password
  yaver-relay logs           Show relay logs (Docker or systemd)
  yaver-relay restart        Restart the relay (Docker or systemd)
  yaver-relay version        Print version
  yaver-relay help           Show this help

Serve flags:
  --quic-port      QUIC port for agent tunnels (default 4433)
  --http-port      HTTP port for mobile clients (default 8443)
  --password       Shared password for relay authentication (env: RELAY_PASSWORD)
  --convex-url     Convex backend URL for per-user password validation (env: CONVEX_URL)
  --expose-domain  Base domain for subdomain expose routing (default yaver.io, env: EXPOSE_DOMAIN)

Tunnel flags:
  --relay        Relay server address (e.g. relay.yaver.io:4433)
  --agent        Local agent HTTP address (default 127.0.0.1:18080)
  --device-id    Device ID (from yaver config)
  --token        Auth token (from yaver config)
  --password     Shared relay password (env: RELAY_PASSWORD)

Status / Tunnels flags:
  --port         HTTP port to query (default 8443)

Set-password flags:
  --env-file     Path to .env file to update (default: .env in current dir)

Logs flags:
  --tail         Number of lines to show (default 50)
  -f, --follow   Stream logs continuously

Architecture:
  Mobile App ──HTTPS──► Relay Server ──QUIC tunnel──► Desktop Agent
  (roaming)             (Hetzner VPS)                 (behind NAT)

  • Mobile makes short HTTP requests — IP changes don't matter
  • Agent maintains persistent QUIC tunnel — stable on ethernet
  • No TUN/TAP, no VPN — pure application-layer proxy
  • Auto-reconnect with exponential backoff on disconnect
`)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	quicPort := fs.Int("quic-port", 4433, "QUIC port for agent tunnels")
	httpPort := fs.Int("http-port", 8443, "HTTP port for mobile clients")
	password := fs.String("password", "", "Shared password for relay authentication (env: RELAY_PASSWORD)")
	convexURL := fs.String("convex-url", "", "Convex backend URL for per-user password validation (env: CONVEX_URL)")
	// Default empty → no auto-subdomain feature. The previous default of
	// "yaver.io" was a hard-coded operator-of-the-public-relay assumption
	// that broke every self-hoster (they don't own that zone) and quietly
	// published unroutable publicUrls into Convex device rows. Empty is
	// safe; the official deployment opts in via systemd unit or env.
	exposeDomain := fs.String("expose-domain", "", "Base domain for subdomain expose routing — e.g. dev.yaver.io. Empty disables auto-subdomain. (env: EXPOSE_DOMAIN)")
	allowOpen := fs.Bool("allow-open", false, "Explicitly allow running with no password and no Convex URL (open mode). Refuses to start otherwise. C-9 audit.")
	// Phase 7 — colocated TURN. Disabled by default (port=0) so existing
	// docker-compose deployments don't suddenly bind a new port. Operators
	// flip it on by passing --turn-port=3478 (or via env). The TURN auth
	// secret defaults to the same RELAY_PASSWORD; override with
	// TURN_AUTH_SECRET if you want them to rotate independently.
	turnPort := fs.Int("turn-port", 0, "UDP port for the colocated TURN server. 0 = disabled (default). 3478 is the IANA-assigned port. (env: TURN_PORT)")
	turnPublicIP := fs.String("turn-public-ip", "", "WAN-reachable IP for TURN candidates. Required when --turn-port is set. (env: TURN_PUBLIC_IP)")
	turnRealm := fs.String("turn-realm", "yaver-relay", "TURN authentication realm")
	fs.Parse(args)

	pw := *password
	if pw == "" {
		pw = os.Getenv("RELAY_PASSWORD")
	}
	if pw == "" {
		if data, err := os.ReadFile(".relay-password"); err == nil {
			pw = strings.TrimSpace(string(data))
			if pw != "" {
				log.Printf("  Password loaded from .relay-password file")
			}
		}
	}

	cURL := *convexURL
	if cURL == "" {
		cURL = os.Getenv("CONVEX_URL")
	}
	// Optional opt-in to Yaver's production Convex deployment for
	// per-user `__rp=` validation. ONLY public.yaver.io's official
	// deployment should set this — self-hosted relays must not phone
	// home to Yaver's backend. The official systemd unit
	// (relay/deploy/yaver-relay.service) sets both this var AND an
	// explicit Environment=CONVEX_URL=... so this fallback is just
	// belt + suspenders. Self-hosters who clone this repo and run
	// docker-compose up get an empty CONVEX_URL → shared-password-
	// only mode (or open mode if no shared password set), with no
	// outbound calls to perceptive-minnow-557.
	if cURL == "" && os.Getenv("YAVER_RELAY_OFFICIAL") == "1" {
		cURL = "https://perceptive-minnow-557.eu-west-1.convex.site"
		log.Printf("  Convex URL: %s (YAVER_RELAY_OFFICIAL=1 — using Yaver-cloud default)", cURL)
	}

	eDomain := *exposeDomain
	if eDomain == "" {
		if ed := os.Getenv("EXPOSE_DOMAIN"); ed != "" {
			eDomain = ed
		}
	}

	// C-9 (audit 2026-05-02): refuse to start in fully-open mode unless
	// the operator explicitly opted in with --allow-open. Open mode means
	// validatePassword() returns true for any password, so anyone reaching
	// the relay can register tunnels, hijack /admin/set-password, and
	// proxy /d/<id>/... — equivalent to giving the public an unrestricted
	// shell into every connected agent. The legacy default silently
	// became "open" when neither --password nor --convex-url was set;
	// that footgun has now been closed off.
	if pw == "" && cURL == "" && !*allowOpen {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "ERROR: relay refusing to start without authentication.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "No password set and no Convex URL configured. The relay would")
		fmt.Fprintln(os.Stderr, "accept any registration from anyone on the public internet.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Choose one of:")
		fmt.Fprintln(os.Stderr, "  1. Set a shared password (recommended for self-hosted relays):")
		fmt.Fprintln(os.Stderr, "       export RELAY_PASSWORD=<your-secret>")
		fmt.Fprintln(os.Stderr, "       yaver-relay serve")
		fmt.Fprintln(os.Stderr, "     or:")
		fmt.Fprintln(os.Stderr, "       yaver-relay serve --password=<your-secret>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  2. Configure per-user password validation via Convex:")
		fmt.Fprintln(os.Stderr, "       yaver-relay serve --convex-url=https://<deployment>.convex.site")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  3. If you really want an open relay (NOT recommended for any")
		fmt.Fprintln(os.Stderr, "     internet-reachable host), pass --allow-open explicitly.")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(2)
	}

	log.Printf("yaver-relay %s starting...", version)
	log.Printf("  QUIC tunnel port: %d", *quicPort)
	log.Printf("  HTTP proxy port:  %d", *httpPort)
	if pw != "" {
		log.Printf("  Password auth:    enabled (shared)")
	} else if cURL != "" {
		log.Printf("  Password auth:    enabled (per-user via Convex)")
	} else {
		// We only get here when --allow-open was set explicitly (see C-9
		// guard above). Log a loud warning so it shows up in operator
		// logs and dashboards.
		log.Printf("  Password auth:    DISABLED (open mode — --allow-open was set; do NOT expose this relay to the public internet)")
	}
	if cURL != "" {
		log.Printf("  Convex backend:   %s", cURL)
	}
	if eDomain != "" {
		log.Printf("  Expose domain:    %s", eDomain)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received %s, shutting down...", sig)
		cancel()
	}()

	// Resolve TURN configuration. Env vars override flags only when
	// the flag wasn't explicitly set, mirroring how RELAY_PASSWORD is
	// handled above.
	turnP := *turnPort
	if turnP == 0 {
		if v := os.Getenv("TURN_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				turnP = p
			}
		}
	}
	turnIP := *turnPublicIP
	if turnIP == "" {
		turnIP = os.Getenv("TURN_PUBLIC_IP")
	}
	turnSecret := os.Getenv("TURN_AUTH_SECRET")
	if turnSecret == "" {
		// Reuse the relay password by default — same secret, no extra
		// key material to distribute.
		turnSecret = pw
	}

	if turnP > 0 {
		switch {
		case turnIP == "":
			log.Printf("  TURN server:      DISABLED — --turn-port=%d set but TURN_PUBLIC_IP / --turn-public-ip is empty", turnP)
		case turnSecret == "":
			log.Printf("  TURN server:      DISABLED — --turn-port=%d set but no auth secret available (set RELAY_PASSWORD or TURN_AUTH_SECRET)", turnP)
		default:
			go func(port int, ip, realm, secret string) {
				if err := StartTURN(ctx, ip, realm, port, secret); err != nil {
					log.Printf("TURN server error: %v", err)
				}
			}(turnP, turnIP, *turnRealm, turnSecret)
		}
	}

	server := NewRelayServer(*quicPort, *httpPort, pw, cURL, eDomain)
	if err := server.Start(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// detectRuntime checks whether the relay is running under Docker or systemd.
func detectRuntime() string {
	// Check Docker
	if err := exec.Command("docker", "inspect", "yaver-relay").Run(); err == nil {
		return "docker"
	}
	// Check systemd
	if err := exec.Command("systemctl", "is-active", "--quiet", "yaver-relay").Run(); err == nil {
		return "systemd"
	}
	return ""
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	port := fs.Int("port", 8443, "HTTP port to query")
	fs.Parse(args)

	// H-14 (audit 2026-05-02): /health is now slim — only {ok, version}.
	// Detail comes from the auth-gated /admin/status. The CLI auths
	// against the local relay via RELAY_ADMIN_TOKEN or RELAY_PASSWORD
	// env (operator-only context, since this command is meant for the
	// machine running the relay).
	healthURL := fmt.Sprintf("http://localhost:%d/health", *port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		fmt.Println("Relay is DOWN")
		fmt.Printf("  Could not reach %s\n", healthURL)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Println("Relay is DOWN (invalid response)")
		os.Exit(1)
	}

	fmt.Println("Relay is UP")
	if v, ok := data["version"]; ok {
		fmt.Printf("  Version:  %v\n", v)
	}

	// Try the auth-gated admin endpoint for the rich data. Best-effort:
	// if no admin token / password is in the env, just skip the detail.
	statusURL := fmt.Sprintf("http://localhost:%d/admin/status", *port)
	req, _ := http.NewRequest("GET", statusURL, nil)
	if tok := strings.TrimSpace(os.Getenv("RELAY_ADMIN_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if pw := strings.TrimSpace(os.Getenv("RELAY_PASSWORD")); pw != "" {
		req.Header.Set("X-Relay-Password", pw)
	}
	if resp2, err := client.Do(req); err == nil {
		defer resp2.Body.Close()
		if resp2.StatusCode == 200 {
			var detail map[string]interface{}
			if err := json.NewDecoder(resp2.Body).Decode(&detail); err == nil {
				if t, ok := detail["tunnels"]; ok {
					fmt.Printf("  Tunnels:  %v active\n", t)
				}
				if u, ok := detail["uptime"]; ok {
					fmt.Printf("  Uptime:   %v\n", u)
				}
			}
		} else if resp2.StatusCode == 401 {
			fmt.Println("  (set RELAY_ADMIN_TOKEN or RELAY_PASSWORD for tunnel/uptime detail)")
		}
	}
}

func runSetPassword(args []string) {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	envFile := fs.String("env-file", "", "Path to .env file to update")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver-relay set-password <new-password> [--env-file <path>]")
		os.Exit(1)
	}
	newPassword := remaining[0]

	// Write to /etc/yaver-relay/password if possible, otherwise ./relay-password.txt
	passwordFile := "/etc/yaver-relay/password"
	dir := filepath.Dir(passwordFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		passwordFile = "./relay-password.txt"
	}

	if err := os.WriteFile(passwordFile, []byte(newPassword+"\n"), 0600); err != nil {
		// Fall back to local file
		passwordFile = "./relay-password.txt"
		if err2 := os.WriteFile(passwordFile, []byte(newPassword+"\n"), 0600); err2 != nil {
			fmt.Fprintf(os.Stderr, "Error writing password file: %v\n", err2)
			os.Exit(1)
		}
	}
	fmt.Printf("Password written to %s\n", passwordFile)

	// Update .env file
	envPath := *envFile
	if envPath == "" {
		envPath = ".env"
	}

	if data, err := os.ReadFile(envPath); err == nil {
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "RELAY_PASSWORD=") {
				lines[i] = "RELAY_PASSWORD=" + newPassword
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, "RELAY_PASSWORD="+newPassword)
		}
		if err := os.WriteFile(envPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update %s: %v\n", envPath, err)
		} else {
			fmt.Printf("Updated %s\n", envPath)
		}
	}

	fmt.Println("Password updated. Restart the relay for the change to take effect.")
}

func runLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	tail := fs.Int("tail", 50, "Number of lines to show")
	follow := fs.Bool("follow", false, "Stream logs continuously")
	followShort := fs.Bool("f", false, "Stream logs continuously (shorthand)")
	fs.Parse(args)

	streaming := *follow || *followShort
	runtime := detectRuntime()

	switch runtime {
	case "docker":
		cmdArgs := []string{"logs", "--tail", fmt.Sprintf("%d", *tail)}
		if streaming {
			cmdArgs = append(cmdArgs, "-f")
		}
		cmdArgs = append(cmdArgs, "yaver-relay")
		cmd := exec.Command("docker", cmdArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "systemd":
		cmdArgs := []string{"-u", "yaver-relay", "-n", fmt.Sprintf("%d", *tail)}
		if streaming {
			cmdArgs = append(cmdArgs, "-f")
		}
		cmd := exec.Command("journalctl", cmdArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Println("Could not find relay process. Check manually.")
		fmt.Println("  Docker:  docker logs -f yaver-relay")
		fmt.Println("  Systemd: journalctl -u yaver-relay -f")
		os.Exit(1)
	}
}

func runTunnels(args []string) {
	fs := flag.NewFlagSet("tunnels", flag.ExitOnError)
	port := fs.Int("port", 8443, "HTTP port to query")
	fs.Parse(args)

	url := fmt.Sprintf("http://localhost:%d/tunnels", *port)
	client := &http.Client{Timeout: 3 * time.Second}
	// H-14 (audit 2026-05-02): /tunnels now requires admin auth. Read
	// credentials from env so the operator can run `yaver-relay tunnels`
	// from the same shell where they run `yaver-relay serve`.
	req, _ := http.NewRequest("GET", url, nil)
	if tok := strings.TrimSpace(os.Getenv("RELAY_ADMIN_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if pw := strings.TrimSpace(os.Getenv("RELAY_PASSWORD")); pw != "" {
		req.Header.Set("X-Relay-Password", pw)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not reach relay at %s\n", url)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		fmt.Fprintln(os.Stderr, "Error: /tunnels is admin-only — set RELAY_ADMIN_TOKEN or RELAY_PASSWORD in env")
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	var data struct {
		OK      bool `json:"ok"`
		Tunnels []struct {
			DeviceID    string `json:"deviceId"`
			PeerAddr    string `json:"peerAddr"`
			ConnectedAt string `json:"connectedAt"`
			Uptime      string `json:"uptime"`
		} `json:"tunnels"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	if len(data.Tunnels) == 0 {
		fmt.Println("No active tunnels.")
		return
	}

	fmt.Printf("Active tunnels (%d):\n\n", len(data.Tunnels))
	fmt.Printf("  %-14s %-24s %-22s %s\n", "DEVICE ID", "PEER ADDRESS", "CONNECTED AT", "UPTIME")
	fmt.Printf("  %-14s %-24s %-22s %s\n", "---------", "------------", "------------", "------")
	for _, t := range data.Tunnels {
		connTime := t.ConnectedAt
		if parsed, err := time.Parse(time.RFC3339, t.ConnectedAt); err == nil {
			connTime = parsed.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  %-14s %-24s %-22s %s\n", t.DeviceID, t.PeerAddr, connTime, t.Uptime)
	}
}

func runRestart(args []string) {
	_ = args // no flags needed
	runtime := detectRuntime()

	switch runtime {
	case "docker":
		fmt.Println("Restarting relay (Docker)...")
		cmd := exec.Command("docker", "restart", "yaver-relay")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Relay restarted.")

	case "systemd":
		fmt.Println("Restarting relay (systemd)...")
		cmd := exec.Command("systemctl", "restart", "yaver-relay")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Relay restarted.")

	default:
		fmt.Println("Could not find relay process.")
		fmt.Println("  Docker:  docker restart yaver-relay")
		fmt.Println("  Systemd: systemctl restart yaver-relay")
		os.Exit(1)
	}
}

func runTunnel(args []string) {
	fs := flag.NewFlagSet("tunnel", flag.ExitOnError)
	relayAddr := fs.String("relay", "", "Relay server address (host:port)")
	agentAddr := fs.String("agent", "127.0.0.1:18080", "Local agent HTTP address")
	deviceID := fs.String("device-id", "", "Device ID")
	token := fs.String("token", "", "Auth token")
	password := fs.String("password", "", "Shared relay password (env: RELAY_PASSWORD)")
	fs.Parse(args)

	if *relayAddr == "" {
		fmt.Fprintln(os.Stderr, "Error: --relay is required")
		fmt.Fprintln(os.Stderr, "Example: yaver-relay tunnel --relay=relay.yaver.io:4433 --device-id=abc123 --token=mytoken")
		os.Exit(1)
	}
	if *deviceID == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "Error: --device-id and --token are required")
		os.Exit(1)
	}

	pw := *password
	if pw == "" {
		pw = os.Getenv("RELAY_PASSWORD")
	}

	log.Printf("Connecting to relay %s...", *relayAddr)
	log.Printf("  Local agent: %s", *agentAddr)
	log.Printf("  Device ID:   %s", (*deviceID)[:8])

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	client := NewTunnelClient(*relayAddr, *agentAddr, *deviceID, *token, pw)
	if err := client.Run(ctx); err != nil {
		log.Fatalf("tunnel error: %v", err)
	}
}
