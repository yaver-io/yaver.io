// `yaver register-url` — manage this device's auto-provisioned
// <deviceId>.<expose-domain> URL on the public relay.
//
// Subcommands (positional, default = check):
//   yaver register-url            same as `check`
//   yaver register-url provision  ensure registered (idempotent)
//   yaver register-url reprovision  unregister + provision
//   yaver register-url unprovision  remove the registration
//   yaver register-url check      print current state
//
// Provisioning normally happens automatically:
//   1. The relay auto-registers `<deviceId>.<expose-domain>` on
//      every tunnel connect (no agent action needed; relay v0.1.11+).
//   2. The agent caches the AssignedURL from the register response
//      and publishes it as a publicEndpoint on heartbeat.
// The CLI exists for retry / inspection in case the first relay
// register raced (e.g. a fresh relay deploy without the auto-
// provision code) or the user wants to claim a different
// subdomain explicitly.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func runRegisterURLCmd(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printRegisterURLHelp()
		return
	}
	sub := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("register-url", flag.ExitOnError)
	subdomain := fs.String("subdomain", "", "Override subdomain (default = device_id)")
	fs.Parse(rest)

	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		fmt.Fprintf(os.Stderr, "Could not read agent config: %v\n", err)
		os.Exit(1)
	}
	if cfg.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "Agent has no device_id yet. Run `yaver auth` first.")
		os.Exit(1)
	}

	wantSub := strings.ToLower(strings.TrimSpace(*subdomain))
	if wantSub == "" {
		wantSub = strings.ToLower(cfg.DeviceID)
	}

	switch sub {
	case "provision":
		if err := registerURLProvision(cfg, wantSub); err != nil {
			fmt.Fprintf(os.Stderr, "provision failed: %v\n", err)
			os.Exit(1)
		}
	case "unprovision":
		if err := registerURLUnprovision(cfg, wantSub); err != nil {
			fmt.Fprintf(os.Stderr, "unprovision failed: %v\n", err)
			os.Exit(1)
		}
	case "reprovision":
		_ = registerURLUnprovision(cfg, wantSub) // best-effort
		if err := registerURLProvision(cfg, wantSub); err != nil {
			fmt.Fprintf(os.Stderr, "reprovision failed: %v\n", err)
			os.Exit(1)
		}
	case "check", "":
		registerURLCheck(cfg, wantSub)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", sub)
		printRegisterURLHelp()
		os.Exit(1)
	}
}

func printRegisterURLHelp() {
	fmt.Print(`yaver register-url — manage this device's auto-provisioned
public-relay subdomain (e.g. https://<device_id>.dev.yaver.io).

Usage:
  yaver register-url              # same as check
  yaver register-url provision    # ensure registered (idempotent)
  yaver register-url reprovision  # unregister, then re-register
  yaver register-url unprovision  # remove registration
  yaver register-url check        # show current state

Flags:
  --subdomain <name>   Override the subdomain (default = device_id)

Provisioning is normally automatic:
  - The relay auto-registers <deviceId>.<expose-domain> on every
    tunnel connect (relay v0.1.11+).
  - The agent caches the assigned URL and publishes it as a
    publicEndpoint on heartbeat — the dashboard probes that URL
    instead of trying direct LAN IPs from a browser behind NAT.

This CLI exists for retry / inspection when the first register
raced or the user wants a custom subdomain. Talks to the
already-running agent over /127.0.0.1:18080/relay/* — no QUIC.
`)
}

// registerURLCheck prints the current state of this device's
// subdomain registration, queried from the local agent (which
// holds the AssignedURL from the last relay register response)
// AND from the relay's /tunnels endpoint as ground truth.
func registerURLCheck(cfg *Config, wantSub string) {
	fmt.Printf("device_id:        %s\n", cfg.DeviceID)
	fmt.Printf("desired subdomain: %s\n", wantSub)

	// Local agent's view: what URL it cached from the last relay
	// register response. Read via /info — the agent surfaces it
	// in publicEndpoints[].
	local := agentLocalAssignedURL()
	if local != "" {
		fmt.Printf("local agent says:  %s\n", local)
	} else {
		fmt.Println("local agent says:  (no assigned URL cached — restart agent or check it's registered)")
	}

	// Relay's view: hit /tunnels on each configured relay.
	for _, rs := range cfg.RelayServers {
		base := strings.TrimRight(rs.HttpURL, "/")
		if base == "" {
			continue
		}
		fmt.Printf("relay %-12s  ", rs.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/tunnels", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		defer resp.Body.Close()
		var data struct {
			Tunnels []struct {
				DeviceID string `json:"deviceId"`
			} `json:"tunnels"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&data)
		matched := false
		for _, t := range data.Tunnels {
			if strings.HasPrefix(strings.ToLower(t.DeviceID), wantSub[:8]) {
				fmt.Printf("✓ tunnel up — https://%s.<expose-domain> reachable\n", wantSub)
				matched = true
				break
			}
		}
		if !matched {
			fmt.Printf("✗ tunnel not in /tunnels — relay doesn't know about us\n")
		}
	}
}

// registerURLProvision asks the relay (via the local agent) to
// (re)register the deviceId-as-subdomain. Triggers a fresh QUIC
// register handshake by bumping the agent's tunnel; the relay's
// auto-register code path picks up the new tunnel and creates
// the subdomain row.
func registerURLProvision(cfg *Config, sub string) error {
	url := "http://127.0.0.1:18080/relay/reregister"
	body := strings.NewReader(`{"reason":"register-url provision","subdomain":"` + sub + `"}`)
	return postLocalAgent(url, body)
}

func registerURLUnprovision(cfg *Config, sub string) error {
	url := "http://127.0.0.1:18080/relay/expose/unregister"
	body := strings.NewReader(`{"subdomain":"` + sub + `"}`)
	return postLocalAgent(url, body)
}

func postLocalAgent(url string, body *strings.Reader) error {
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.AuthToken == "" {
		return fmt.Errorf("no auth token in agent config")
	}
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	fmt.Println("OK")
	return nil
}

// agentLocalAssignedURL queries the local /info for the
// publicEndpoints[] and picks the *.<expose-domain> entry the
// relay assigned. Returns "" if the agent hasn't cached one.
func agentLocalAssignedURL() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:18080/info", nil)
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var data struct {
		PublicEndpoints []string `json:"publicEndpoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ""
	}
	for _, ep := range data.PublicEndpoints {
		if strings.Contains(ep, ".dev.yaver.io") || strings.Contains(ep, ".yaver.io") {
			return ep
		}
	}
	return ""
}
