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
)

func runMCPSetup(args []string) {
	if len(args) == 0 {
		fmt.Print("Yaver MCP Setup — register Yaver as an MCP server\n\n" +
			"Usage:\n" +
			"  yaver mcp setup claude-code  Add Yaver to the Claude Code user MCP config\n" +
			"  yaver mcp setup codex        Add Yaver to the Codex CLI MCP config\n" +
			"  yaver mcp setup opencode     Add Yaver to opencode's MCP config\n" +
			"  yaver mcp setup show         Print the config JSON (for manual paste)\n\n" +
			"Yaver exposes a curated set of tools (task management, file search,\n" +
			"git, exec, screenshots, and more) over stdio. Run `yaver mcp` to\n" +
			"inspect the tool list.\n")
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
	case "show":
		showMCPConfig(yaverPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown MCP client: %s (use claude-code, codex, or opencode)\n", args[0])
		os.Exit(1)
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
		return false, nil
	}

	addCmd := exec.Command("claude", "mcp", "add", "--scope", "user", "yaver", "--", yaverPath, "mcp")
	out, err := addCmd.CombinedOutput()
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
		return false, nil
	}

	addCmd := exec.Command("codex", "mcp", "add", "yaver", "--", yaverPath, "mcp")
	out, err := addCmd.CombinedOutput()
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
	if _, exists := mcp["yaver"]; exists {
		return false, nil
	}
	mcp["yaver"] = map[string]interface{}{
		"type":    "local",
		"command": []string{yaverPath, "mcp"},
		"enabled": true,
	}
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
