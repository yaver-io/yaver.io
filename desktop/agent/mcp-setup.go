package main

// mcp-setup.go — `yaver mcp setup <client>` registers Yaver as an MCP
// server inside one of yaver's three first-class runner CLIs.
//
// Only claude-code, codex, and opencode are supported. Editor MCP
// clients (Claude Desktop, Cursor, VS Code, Windsurf, Zed) are out
// of scope; if a user wants Yaver in those, they can paste the
// `yaver mcp setup show` output into the editor's config manually.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mdp/qrterminal/v3"
)

func runMCPSetup(args []string) {
	if len(args) == 0 {
		fmt.Print("Yaver MCP Setup — register Yaver as an MCP server\n\n" +
			"Usage:\n" +
			"  yaver mcp setup claude-code  Add Yaver to the Claude Code user MCP config\n" +
			"  yaver mcp setup codex        Add Yaver to the Codex CLI MCP config\n" +
			"  yaver mcp setup opencode     Add Yaver to opencode's MCP config\n" +
			"  yaver mcp setup phone        Print the remote-connector URL + QR for the\n" +
			"                               phone's Claude app (or any remote-MCP client)\n" +
			"  yaver mcp setup show         Print the config JSON (for manual paste)\n\n" +
			"claude-code / codex / opencode register a local stdio server. `phone`\n" +
			"is different: it prints the OAuth'd remote endpoint the Claude app on\n" +
			"your phone (or ChatGPT) adds as a connector, so you can drive this box\n" +
			"by voice from the car. Run `yaver mcp` to inspect the tool list.\n")
		return
	}

	yaverPath := findYaverBinary()

	switch args[0] {
	case "claude", "claude-code":
		setupClaudeCode(yaverPath, false)
	case "codex":
		setupCodex(yaverPath, false)
	case "opencode":
		setupOpenCode(yaverPath, false)
	case "phone", "connector", "mobile":
		runMCPSetupPhone()
	case "show":
		showMCPConfig(yaverPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown MCP client: %s (use claude-code, codex, opencode, or phone)\n", args[0])
		os.Exit(1)
	}
}

// runMCPSetupPhone prints the remote MCP connector URL for this box (the relay
// `/d/<deviceId>/mcp` endpoint) plus a scannable QR, so the Claude app on a
// phone — or ChatGPT, or any remote-MCP client — can add Yaver as a connector.
// Unlike the stdio clients above, nothing is written to a config file: the
// client stores the URL, and OAuth (Yaver's own AS) authenticates it. See
// docs/yaver-phone-mcp-connector.md.
func runMCPSetupPhone() {
	url, deviceID, err := phoneConnectorURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println("Add Yaver to your phone's Claude app (or ChatGPT) as a connector:")
	fmt.Println()
	fmt.Println("  1. In the app: add a custom connector / remote MCP server with this URL:")
	fmt.Println()
	fmt.Printf("       %s\n", url)
	fmt.Println()
	fmt.Println("  2. Sign in when prompted — this is Yaver's own OAuth, not Anthropic's.")
	fmt.Println("  3. On the consent screen, tick \"Full access\" so the connector can run")
	fmt.Println("     ops and drive coding runners (claude/codex/opencode/glm) on this box.")
	fmt.Println("     Leaving it unchecked limits the connector to read-only utility tools.")
	fmt.Println()
	printConnectorQR(url)
	fmt.Printf("Device: %s\n", deviceID)
}

// phoneConnectorURL resolves the remote MCP endpoint a phone connector should
// use: the relay path form `<relayBase>/d/<deviceId>/mcp`. It prefers the relay
// URL the running daemon was assigned, then any relay server in config.
func phoneConnectorURL() (string, string, error) {
	deviceID := localDeviceID()
	if deviceID == "" {
		return "", "", fmt.Errorf("this machine isn't registered yet — run `yaver auth` then `yaver serve` first")
	}
	// The daemon's assigned relay URL is already path-style and device-scoped
	// (`.../d/<deviceId>`); append the resource. Empty in a fresh CLI process,
	// so fall through to config.
	if base := strings.TrimRight(getAssignedRelayURL(), "/"); base != "" {
		return base + "/mcp", deviceID, nil
	}
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		servers := cfg.RelayServers
		if len(servers) == 0 {
			servers = cfg.CachedRelayServers
		}
		for _, srv := range servers {
			if u := strings.TrimRight(strings.TrimSpace(srv.HttpURL), "/"); u != "" {
				return fmt.Sprintf("%s/d/%s/mcp", u, deviceID), deviceID, nil
			}
		}
	}
	return "", deviceID, fmt.Errorf("no relay endpoint known yet — start the agent with `yaver serve` so it registers a relay tunnel, then re-run `yaver mcp setup phone`")
}

// printConnectorQR renders the connector URL as a terminal QR (unless the user
// opted out of pairing QRs), mirroring printPairURLAndQR.
func printConnectorQR(url string) {
	if strings.TrimSpace(url) == "" || pairQROptOut() {
		return
	}
	qrterminal.GenerateWithConfig(url, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 2,
	})
	fmt.Println()
}

func runMCPUnregister(args []string) {
	if len(args) == 0 {
		args = []string{"claude-code", "codex", "opencode"}
	}
	for _, target := range args {
		switch target {
		case "claude", "claude-code":
			changed, err := removeClaudeCodeMCPConfig()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Claude Code MCP unregister failed: %v\n", err)
				continue
			}
			if changed {
				fmt.Println("  MCP: Removed Yaver from Claude Code user MCP config")
			}
		case "codex":
			changed, err := removeCodexMCPConfig()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Codex MCP unregister failed: %v\n", err)
				continue
			}
			if changed {
				fmt.Println("  MCP: Removed Yaver from Codex CLI MCP config")
			}
		case "opencode":
			changed, err := removeOpenCodeMCPConfig()
			if err != nil {
				fmt.Fprintf(os.Stderr, "opencode MCP unregister failed: %v\n", err)
				continue
			}
			if changed {
				fmt.Println("  MCP: Removed Yaver from opencode MCP config")
			}
		default:
			fmt.Fprintf(os.Stderr, "Unknown MCP client: %s (use claude-code, codex, or opencode)\n", target)
		}
	}
}

func setupClaudeCode(yaverPath string, quiet bool) {
	changed, err := ensureClaudeCodeMCPConfig(yaverPath)
	if err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "Claude Code MCP setup failed: %v\n", err)
		}
		return
	}
	if quiet {
		return
	}
	if changed {
		fmt.Println("  MCP: Added Yaver to Claude Code user MCP config")
		return
	}
	fmt.Println("Yaver is already configured in Claude Code.")
}

func ensureClaudeCodeMCPConfig(yaverPath string) (bool, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return false, fmt.Errorf("claude not found on PATH")
	}

	getCmd := exec.Command("claude", "mcp", "get", "yaver")
	if err := getCmd.Run(); err == nil {
		removeCmd := exec.Command("claude", "mcp", "remove", "--scope", "user", "yaver")
		if out, err := removeCmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("refresh existing Claude Code MCP entry: %v: %s", err, string(out))
		}
	}

	addCmd := exec.Command("claude", "mcp", "add", "--scope", "user", "yaver", "--", yaverPath, "mcp")
	out, err := addCmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, string(out))
	}
	return true, nil
}

func removeClaudeCodeMCPConfig() (bool, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return false, nil
	}
	getCmd := exec.Command("claude", "mcp", "get", "yaver")
	if err := getCmd.Run(); err != nil {
		return false, nil
	}
	removeCmd := exec.Command("claude", "mcp", "remove", "--scope", "user", "yaver")
	out, err := removeCmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, string(out))
	}
	return true, nil
}

func setupCodex(yaverPath string, quiet bool) {
	changed, err := ensureCodexMCPConfig(yaverPath)
	if err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "Codex MCP setup failed: %v\n", err)
		}
		return
	}
	if quiet {
		return
	}
	if changed {
		fmt.Println("  MCP: Added Yaver to Codex CLI MCP config")
		return
	}
	fmt.Println("Yaver is already configured in Codex.")
}

func ensureCodexMCPConfig(yaverPath string) (bool, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return false, fmt.Errorf("codex not found on PATH")
	}

	getCmd := exec.Command("codex", "mcp", "get", "yaver")
	if err := getCmd.Run(); err == nil {
		removeCmd := exec.Command("codex", "mcp", "remove", "yaver")
		if out, err := removeCmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("refresh existing Codex MCP entry: %v: %s", err, string(out))
		}
	}

	addCmd := exec.Command("codex", "mcp", "add", "yaver", "--", yaverPath, "mcp")
	out, err := addCmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, string(out))
	}
	return true, nil
}

func removeCodexMCPConfig() (bool, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return false, nil
	}
	getCmd := exec.Command("codex", "mcp", "get", "yaver")
	if err := getCmd.Run(); err != nil {
		return false, nil
	}
	removeCmd := exec.Command("codex", "mcp", "remove", "yaver")
	out, err := removeCmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, string(out))
	}
	return true, nil
}

func setupOpenCode(yaverPath string, quiet bool) {
	changed, err := ensureOpenCodeMCPConfig(yaverPath)
	if err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "opencode MCP setup failed: %v\n", err)
		}
		return
	}
	if quiet {
		return
	}
	if changed {
		fmt.Println("  MCP: Added Yaver to opencode MCP config")
		return
	}
	fmt.Println("Yaver is already configured in opencode.")
}

// ensureOpenCodeMCPConfig adds Yaver to ~/.config/opencode/opencode.json
// under the `mcp.yaver` key. opencode reads this on startup; it does
// not have a `mcp add` subcommand so we patch the JSON directly.
func ensureOpenCodeMCPConfig(yaverPath string) (bool, error) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return false, fmt.Errorf("opencode not found on PATH")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return false, err
	}

	cfg := make(map[string]interface{})
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	mcp, _ := cfg["mcp"].(map[string]interface{})
	if mcp == nil {
		mcp = make(map[string]interface{})
	}
	entry := map[string]interface{}{
		"type":    "local",
		"command": []string{yaverPath, "mcp"},
		"enabled": true,
	}
	if existing, exists := mcp["yaver"]; exists {
		existingJSON, _ := json.Marshal(existing)
		entryJSON, _ := json.Marshal(entry)
		if string(existingJSON) == string(entryJSON) {
			return false, nil
		}
	}
	mcp["yaver"] = entry
	cfg["mcp"] = mcp

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return false, err
	}
	return true, nil
}

func removeOpenCodeMCPConfig() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	cfg := make(map[string]interface{})
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, err
	}
	mcp, _ := cfg["mcp"].(map[string]interface{})
	if mcp == nil {
		return false, nil
	}
	if _, exists := mcp["yaver"]; !exists {
		return false, nil
	}
	delete(mcp, "yaver")
	if len(mcp) == 0 {
		delete(cfg, "mcp")
	} else {
		cfg["mcp"] = mcp
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return false, err
	}
	return true, nil
}

func findYaverBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "yaver"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	return resolved
}

func mcpServerEntry(yaverPath string) map[string]interface{} {
	return map[string]interface{}{
		"command": yaverPath,
		"args":    []string{"mcp"},
	}
}

// autoSetupMCP runs silently during `yaver serve` and registers Yaver
// in any of the three first-class runner CLIs that are installed and
// not yet configured. Editor MCP clients are intentionally not
// auto-configured — yaver only first-classes claude-code, codex, and
// opencode.
func autoSetupMCP() {
	yaverPath := findYaverBinary()
	setupClaudeCode(yaverPath, true)
	setupCodex(yaverPath, true)
	setupOpenCode(yaverPath, true)
}

func showMCPConfig(yaverPath string) {
	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"yaver": mcpServerEntry(yaverPath),
		},
	}
	out, _ := json.MarshalIndent(config, "", "  ")
	fmt.Println(string(out))
}
