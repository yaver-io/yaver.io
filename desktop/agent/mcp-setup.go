package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func runMCPSetup(args []string) {
	if len(args) == 0 {
		fmt.Print(`Yaver MCP Setup — configure AI editors to use Yaver as an MCP server

Usage:
  yaver mcp setup claude       Add to Claude Desktop config
  yaver mcp setup claude-code  Add to Claude Code user MCP config
  yaver mcp setup cursor       Add to Cursor MCP config
  yaver mcp setup vscode       Add to VS Code MCP config
  yaver mcp setup windsurf     Add to Windsurf MCP config
  yaver mcp setup zed          Add to Zed settings
  yaver mcp setup show         Show config JSON (copy/paste manually)

Each command adds Yaver as an MCP server to the editor's config file.
Yaver exposes 48 tools: task management, file search, git, exec, screenshots, and more.
`)
		return
	}

	yaverPath := findYaverBinary()

	switch args[0] {
	case "claude":
		setupMCPEditor("Claude Desktop", claudeDesktopConfigPath(), yaverPath)
	case "claude-code":
		setupClaudeCode(yaverPath, false)
	case "cursor":
		setupMCPEditor("Cursor", cursorConfigPath(), yaverPath)
	case "vscode":
		setupMCPEditor("VS Code", vscodeConfigPath(), yaverPath)
	case "windsurf":
		setupMCPEditor("Windsurf", windsurfConfigPath(), yaverPath)
	case "zed":
		setupZed(yaverPath)
	case "show":
		showMCPConfig(yaverPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown editor: %s\n", args[0])
		os.Exit(1)
	}
}

func setupClaudeCode(yaverPath string, quiet bool) {
	if _, err := exec.LookPath("claude"); err != nil {
		if !quiet {
			fmt.Println("Claude Code CLI not found on PATH; skipping Claude Code MCP setup.")
		}
		return
	}

	getCmd := exec.Command("claude", "mcp", "get", "yaver")
	if err := getCmd.Run(); err == nil {
		if !quiet {
			fmt.Println("Yaver is already configured in Claude Code.")
		}
		return
	}

	addCmd := exec.Command("claude", "mcp", "add", "--scope", "user", "yaver", "--", yaverPath, "mcp")
	out, err := addCmd.CombinedOutput()
	if err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "Claude Code MCP setup failed: %v\n%s\n", err, string(out))
		}
		return
	}

	if !quiet {
		fmt.Println("  MCP: Added Yaver to Claude Code user MCP config")
	}
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

func claudeDesktopConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(os.Getenv("HOME"), ".config", "Claude", "claude_desktop_config.json")
	}
}

func cursorConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), ".cursor", "mcp.json")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Cursor", "mcp.json")
	default:
		return filepath.Join(os.Getenv("HOME"), ".cursor", "mcp.json")
	}
}

func vscodeConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Code", "User", "settings.json")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Code", "User", "settings.json")
	default:
		return filepath.Join(os.Getenv("HOME"), ".config", "Code", "User", "settings.json")
	}
}

func windsurfConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), ".windsurf", "mcp.json")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Windsurf", "mcp.json")
	default:
		return filepath.Join(os.Getenv("HOME"), ".windsurf", "mcp.json")
	}
}

func mcpServerEntry(yaverPath string) map[string]interface{} {
	return map[string]interface{}{
		"command": yaverPath,
		"args":    []string{"mcp"},
	}
}

func setupMCPEditor(name string, configPath string, yaverPath string) {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create config directory: %v\n", err)
		return
	}

	config := make(map[string]interface{})

	data, err := os.ReadFile(configPath)
	if err == nil {
		json.Unmarshal(data, &config)
	}

	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		servers = make(map[string]interface{})
	}

	if _, exists := servers["yaver"]; exists {
		fmt.Printf("Yaver is already configured in %s.\n", name)
		fmt.Printf("Config: %s\n", configPath)
		return
	}

	servers["yaver"] = mcpServerEntry(yaverPath)
	config["mcpServers"] = servers

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON error: %v\n", err)
		return
	}

	if err := os.WriteFile(configPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write config: %v\n", err)
		return
	}

	fmt.Printf("  MCP: Added Yaver to %s\n", name)
	fmt.Printf("\nRestart %s to activate.\n", name)
}

func setupZed(yaverPath string) {
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "zed", "settings.json")
	if runtime.GOOS == "darwin" {
		configPath = filepath.Join(os.Getenv("HOME"), ".config", "zed", "settings.json")
	}

	config := make(map[string]interface{})
	data, err := os.ReadFile(configPath)
	if err == nil {
		json.Unmarshal(data, &config)
	}

	lmConfig, ok := config["language_models"].(map[string]interface{})
	if !ok {
		lmConfig = make(map[string]interface{})
	}

	mcpServers, ok := lmConfig["mcp_servers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}

	if _, exists := mcpServers["yaver"]; exists {
		fmt.Println("Yaver is already configured in Zed.")
		fmt.Printf("Config: %s\n", configPath)
		return
	}

	mcpServers["yaver"] = map[string]interface{}{
		"command": yaverPath,
		"args":    []string{"mcp"},
	}
	lmConfig["mcp_servers"] = mcpServers
	config["language_models"] = lmConfig

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON error: %v\n", err)
		os.Exit(1)
	}

	os.MkdirAll(filepath.Dir(configPath), 0755)
	if err := os.WriteFile(configPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Added Yaver MCP server to Zed.")
	fmt.Printf("Config: %s\n", configPath)
	fmt.Println("\nRestart Zed to activate.")
}

// autoSetupMCP detects installed editors and configures MCP for any that
// aren't already configured. Runs silently during `yaver serve`.
func autoSetupMCP() {
	yaverPath := findYaverBinary()
	setupClaudeCode(yaverPath, true)

	type editor struct {
		name       string
		configPath string
		isZed      bool
	}

	editors := []editor{
		{"Claude Desktop", claudeDesktopConfigPath(), false},
		{"Cursor", cursorConfigPath(), false},
		{"Windsurf", windsurfConfigPath(), false},
	}
	if runtime.GOOS == "darwin" {
		editors = append(editors, editor{"Zed", filepath.Join(os.Getenv("HOME"), ".config", "zed", "settings.json"), true})
	}

	configured := 0
	for _, e := range editors {
		// Check if config dir exists (editor is installed)
		dir := filepath.Dir(e.configPath)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// Check if already configured
		data, err := os.ReadFile(e.configPath)
		if err == nil {
			var config map[string]interface{}
			json.Unmarshal(data, &config)
			if servers, ok := config["mcpServers"].(map[string]interface{}); ok {
				if _, exists := servers["yaver"]; exists {
					continue // Already configured
				}
			}
			if e.isZed {
				if lm, ok := config["language_models"].(map[string]interface{}); ok {
					if servers, ok := lm["mcp_servers"].(map[string]interface{}); ok {
						if _, exists := servers["yaver"]; exists {
							continue
						}
					}
				}
			}
		}

		// Configure silently
		if e.isZed {
			setupZed(yaverPath)
		} else {
			setupMCPEditor(e.name, e.configPath, yaverPath)
		}
		configured++
	}

	if configured > 0 {
		fmt.Printf("  MCP configured for %d editor(s). Restart them to activate.\n", configured)
	}
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
