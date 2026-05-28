package main

import (
	"fmt"
	"os"
	"strings"
)

type talosACLSetup struct {
	Mode       string
	URL        string
	Auth       string
	LicenseKey string
	Command    string
}

func runACLTalos(args []string, cfg *Config) error {
	setup, err := parseTalosACLSetup(args)
	if err != nil {
		return err
	}
	peer, err := buildTalosACLPeer(setup)
	if err != nil {
		return err
	}
	cfg.ACLPeers = upsertACLPeer(cfg.ACLPeers, peer)
	if err := SaveConfig(cfg); err != nil {
		return err
	}
	target := peer.URL
	if peer.Type == "stdio" {
		target = "stdio: " + peer.Command
	}
	fmt.Printf("Configured Talos MCP peer: %s\n", target)
	return nil
}

func parseTalosACLSetup(args []string) (talosACLSetup, error) {
	setup := talosACLSetup{
		Mode:       firstNonEmptyTalosACL(os.Getenv("TALOS_MCP_MODE"), "auto"),
		URL:        os.Getenv("TALOS_MCP_URL"),
		Auth:       os.Getenv("TALOS_MCP_AUTH"),
		LicenseKey: firstNonEmptyTalosACL(os.Getenv("TALOS_LICENSE_KEY"), os.Getenv("TALOS_MCP_LICENSE")),
		Command:    firstNonEmptyTalosACL(os.Getenv("TALOS_TALCLI_PATH"), "talcli"),
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--mode":
			i++
			if i >= len(args) {
				return setup, fmt.Errorf("--mode requires a value")
			}
			setup.Mode = args[i]
		case "--url":
			i++
			if i >= len(args) {
				return setup, fmt.Errorf("--url requires a value")
			}
			setup.URL = args[i]
		case "--auth":
			i++
			if i >= len(args) {
				return setup, fmt.Errorf("--auth requires a value")
			}
			setup.Auth = args[i]
		case "--license":
			i++
			if i >= len(args) {
				return setup, fmt.Errorf("--license requires a value")
			}
			setup.LicenseKey = args[i]
		case "--command":
			i++
			if i >= len(args) {
				return setup, fmt.Errorf("--command requires a value")
			}
			setup.Command = args[i]
		case "--help", "-h":
			return setup, fmt.Errorf("usage: yaver acl talos [--mode local|remote|auto] [--url MCP_URL] [--auth TOKEN] [--license TALOS_KEY] [--command talcli]")
		default:
			return setup, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	setup.Mode = strings.ToLower(strings.TrimSpace(setup.Mode))
	if setup.Mode == "" {
		setup.Mode = "auto"
	}
	return setup, nil
}

func buildTalosACLPeer(setup talosACLSetup) (ACLPeerConfig, error) {
	if setup.Mode != "auto" && setup.Mode != "local" && setup.Mode != "remote" {
		return ACLPeerConfig{}, fmt.Errorf("unsupported mode %q (want local, remote, or auto)", setup.Mode)
	}
	url := strings.TrimSpace(setup.URL)
	if setup.Mode == "remote" || (setup.Mode == "auto" && url != "") {
		if url == "" {
			return ACLPeerConfig{}, fmt.Errorf("remote mode requires --url or TALOS_MCP_URL")
		}
		return ACLPeerConfig{
			ID:   "talos",
			Name: "Talos",
			Type: "http",
			URL:  url,
			Auth: strings.TrimSpace(setup.Auth),
		}, nil
	}

	cmd := strings.TrimSpace(setup.Command)
	if cmd == "" {
		cmd = "talcli"
	}
	parts := []string{cmd, "mcp", "serve"}
	if key := strings.TrimSpace(setup.LicenseKey); key != "" {
		parts = append(parts, "--license", key)
	}
	return ACLPeerConfig{
		ID:      "talos",
		Name:    "Talos",
		Type:    "stdio",
		Command: strings.Join(parts, " "),
	}, nil
}

func upsertACLPeer(peers []ACLPeerConfig, peer ACLPeerConfig) []ACLPeerConfig {
	for i := range peers {
		if peers[i].ID == peer.ID {
			peers[i] = peer
			return peers
		}
	}
	return append(peers, peer)
}

func firstNonEmptyTalosACL(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
