package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type runnerAuthSetupRequest struct {
	Runner           string `json:"runner"`
	OpenAIAPIKey     string `json:"openai_api_key,omitempty"`
	AnthropicAPIKey  string `json:"anthropic_api_key,omitempty"`
	GLMAPIKey        string `json:"glm_api_key,omitempty"`
	ZAIAPIKey        string `json:"zai_api_key,omitempty"`
	Notes            string `json:"notes,omitempty"`
	InstallIfMissing *bool  `json:"install_if_missing,omitempty"`
	CodexLogin       *bool  `json:"codex_login,omitempty"`
	SetupMCP         *bool  `json:"setup_mcp,omitempty"`
	AllowInstallOnly *bool  `json:"allow_install_only,omitempty"`
}

type runnerAuthSetupResult struct {
	OK             bool     `json:"ok"`
	Runner         string   `json:"runner"`
	DeviceID       string   `json:"device_id,omitempty"`
	Installed      bool     `json:"installed"`
	InstallAttempt bool     `json:"installAttempt,omitempty"`
	VaultKeys      []string `json:"vaultKeys,omitempty"`
	LoginAttempt   bool     `json:"loginAttempt,omitempty"`
	MCPConfigured  []string `json:"mcpConfigured,omitempty"`
	Ready          bool     `json:"ready"`
	AuthConfigured bool     `json:"authConfigured"`
	AuthSource     string   `json:"authSource,omitempty"`
	Detail         string   `json:"detail,omitempty"`
	Warning        string   `json:"warning,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

func runRunnerAuthSetup(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver runner-auth setup <runner> [flags]")
		os.Exit(1)
	}
	runner := normalizeRunnerAuthName(args[0])
	fs := flag.NewFlagSet("runner-auth setup", flag.ExitOnError)
	target := fs.String("target", "", "remote device ID to update")
	openAIKey := fs.String("openai-api-key", "", "OpenCode provider credential")
	anthropicKey := fs.String("anthropic-api-key", "", "OpenCode provider credential")
	glmKey := fs.String("glm-api-key", "", "GLM API key")
	zaiKey := fs.String("zai-api-key", "", "ZAI API key")
	notes := fs.String("notes", "", "optional vault note")
	noInstall := fs.Bool("no-install", false, "skip installing the runner if missing")
	noLogin := fs.Bool("no-login", false, "skip Codex headless login")
	noMCP := fs.Bool("no-mcp", false, "skip registering Yaver as an MCP server in the runner")
	fs.Parse(args[1:])

	installIfMissing := !*noInstall
	codexLogin := !*noLogin
	setupMCP := !*noMCP
	req := runnerAuthSetupRequest{
		Runner:           runner,
		OpenAIAPIKey:     *openAIKey,
		AnthropicAPIKey:  *anthropicKey,
		GLMAPIKey:        *glmKey,
		ZAIAPIKey:        *zaiKey,
		Notes:            *notes,
		InstallIfMissing: &installIfMissing,
		CodexLogin:       &codexLogin,
		SetupMCP:         &setupMCP,
	}

	var (
		result runnerAuthSetupResult
		err    error
	)
	if strings.TrimSpace(*target) != "" {
		result, err = applyRunnerAuthSetupRemote(strings.TrimSpace(*target), req)
	} else {
		result, err = applyRunnerAuthSetupLocal(context.Background(), req)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth setup: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s ready=%t auth=%t installed=%t\n", result.Runner, result.Ready, result.AuthConfigured, result.Installed)
	if len(result.VaultKeys) > 0 {
		fmt.Printf("vault: %s\n", strings.Join(result.VaultKeys, ", "))
	}
	if len(result.MCPConfigured) > 0 {
		fmt.Printf("mcp: %s\n", strings.Join(result.MCPConfigured, ", "))
	}
	if result.AuthSource != "" {
		fmt.Printf("auth: %s\n", result.AuthSource)
	}
	if result.Detail != "" {
		fmt.Printf("detail: %s\n", result.Detail)
	}
	if result.Warning != "" {
		fmt.Printf("warning: %s\n", result.Warning)
	}
	for _, note := range result.Notes {
		if strings.TrimSpace(note) != "" {
			fmt.Printf("note: %s\n", note)
		}
	}
}

func runnerAuthValueProvided(req runnerAuthSetupRequest) bool {
	switch normalizeRunnerAuthName(req.Runner) {
	case "claude":
		return false
	case "codex":
		return false
	case "opencode":
		return strings.TrimSpace(req.OpenAIAPIKey) != "" ||
			strings.TrimSpace(req.AnthropicAPIKey) != "" ||
			strings.TrimSpace(req.GLMAPIKey) != "" ||
			strings.TrimSpace(req.ZAIAPIKey) != ""
	case "glm":
		return strings.TrimSpace(req.GLMAPIKey) != "" ||
			strings.TrimSpace(req.ZAIAPIKey) != ""
	default:
		return false
	}
}

func runnerStatusRowFor(rows []runnerAuthStatusRow, runner string) runnerAuthStatusRow {
	runner = normalizeRunnerAuthName(runner)
	for _, row := range rows {
		if normalizeRunnerAuthName(row.ID) == runner {
			return row
		}
	}
	return runnerAuthStatusRow{ID: runner}
}

func installNodeGlobalPackage(ctx context.Context, pkg string) error {
	if runtime.GOOS == "linux" {
		ensureLinuxRunnerSandboxSupport()
	}
	nodeBin, err := installNodeRuntime(ctx, nil)
	if err != nil {
		return err
	}
	npmPath := filepath.Join(nodeBin, "npm")
	if runtime.GOOS == "windows" {
		npmPath += ".cmd"
	}
	cmd := exec.CommandContext(ctx, npmPath, "install", "-g", pkg)
	cmd.Env = append(os.Environ(), "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install -g %s: %v: %s", pkg, err, strings.TrimSpace(string(out)))
	}
	augmentAgentPATH()
	return nil
}

func ensureLinuxRunnerSandboxSupport() {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return
	}
	const path = "/etc/sysctl.d/99-yaver-runner-sandbox.conf"
	var b strings.Builder
	b.WriteString("kernel.unprivileged_userns_clone=1\n")
	b.WriteString("user.max_user_namespaces=1048576\n")
	if _, err := os.Stat("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); err == nil {
		b.WriteString("kernel.apparmor_restrict_unprivileged_userns=0\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return
	}
	cmd := exec.Command("sysctl", "--system")
	_ = cmd.Run()
}

func ensureRunnerInstalled(ctx context.Context, runner string) error {
	cmd := GetRunnerConfig(runner).Command
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("unsupported runner %q", runner)
	}
	if resolveRunnerBinary(cmd) != "" {
		return nil
	}
	switch normalizeRunnerAuthName(runner) {
	case "claude", "glm":
		return installNodeGlobalPackage(ctx, "@anthropic-ai/claude-code")
	case "codex":
		return installNodeGlobalPackage(ctx, "@openai/codex")
	case "opencode":
		return installNodeGlobalPackage(ctx, "opencode-ai")
	default:
		return fmt.Errorf("runner %q does not have an auto-install recipe yet", runner)
	}
}

func setupRunnerMCP(runner string) ([]string, error) {
	yaverPath := findYaverBinary()
	switch normalizeRunnerAuthName(runner) {
	case "claude", "glm":
		// glm runs the `claude` binary (pointed at a gateway), so its MCP
		// config is the same Claude Code user config.
		if _, err := ensureClaudeCodeMCPConfig(yaverPath); err != nil {
			return nil, err
		}
		return []string{"claude-code"}, nil
	case "codex":
		if _, err := ensureCodexMCPConfig(yaverPath); err != nil {
			return nil, err
		}
		return []string{"codex"}, nil
	case "opencode":
		// The $19 included-model tier runs opencode; without this it had NO
		// yaver_* tools on the box. Registers Yaver in opencode.json so the
		// managed runner gets the full yaver_* tool surface automatically.
		if _, err := ensureOpenCodeMCPConfig(yaverPath); err != nil {
			return nil, err
		}
		return []string{"opencode"}, nil
	default:
		return nil, nil
	}
}

func applyRunnerAuthSetupLocal(ctx context.Context, req runnerAuthSetupRequest) (runnerAuthSetupResult, error) {
	req.Runner = normalizeRunnerAuthName(req.Runner)
	result := runnerAuthSetupResult{OK: true, Runner: req.Runner}
	if req.Runner != "claude" && req.Runner != "codex" && req.Runner != "opencode" && req.Runner != "glm" {
		return result, fmt.Errorf("unsupported runner %q (want claude, codex, opencode, or glm)", req.Runner)
	}

	installIfMissing := boolOrDefault(req.InstallIfMissing, true)
	cmdName := GetRunnerConfig(req.Runner).Command
	if installIfMissing {
		if resolveRunnerBinary(cmdName) == "" {
			if err := ensureRunnerInstalled(ctx, req.Runner); err != nil {
				return result, err
			}
			result.InstallAttempt = true
		}
	} else if resolveRunnerBinary(cmdName) == "" {
		return result, fmt.Errorf("%s is not installed and --no-install was set", req.Runner)
	}

	if runnerAuthValueProvided(req) {
		entries, err := buildRunnerAuthEntries(
			req.Runner,
			req.OpenAIAPIKey,
			req.AnthropicAPIKey,
			"",
			"",
			req.GLMAPIKey,
			req.ZAIAPIKey,
			req.Notes,
		)
		if err != nil {
			return result, err
		}
		if err := setRunnerAuthEntriesLocal(entries); err != nil {
			return result, err
		}
		for _, entry := range entries {
			result.VaultKeys = append(result.VaultKeys, entry.Name)
		}
	}

	if req.Runner == "codex" && boolOrDefault(req.CodexLogin, true) {
		// Codex auth MUST use ChatGPT Plus OAuth via `codex login
		// --device-auth` (see runner_auth_browser_http.go). The API-key
		// path was deleted 2026-05-27 per
		// feedback_no_api_keys_subscription_only — double-bills + breaks
		// "all agents on same plan" promise.
		result.Notes = append(result.Notes, "Codex requires ChatGPT Plus OAuth. Open Yaver mobile or run `codex login --device-auth`.")
	}

	if boolOrDefault(req.SetupMCP, true) {
		configured, err := setupRunnerMCP(req.Runner)
		if err != nil {
			return result, err
		}
		result.MCPConfigured = configured
	}

	rows, err := collectRunnerAuthStatusRows()
	if err != nil {
		return result, err
	}
	row := runnerStatusRowFor(rows, req.Runner)
	result.Installed = row.Installed
	result.Ready = row.Ready
	result.AuthConfigured = row.AuthConfigured
	result.AuthSource = row.AuthSource
	result.Warning = row.Warning
	result.Detail = row.Detail

	if req.Runner == "claude" && !row.AuthConfigured {
		if boolOrDefault(req.AllowInstallOnly, false) && row.Installed {
			result.Warning = "Claude Code was installed, but authentication is still required."
			if strings.TrimSpace(result.Detail) == "" {
				result.Detail = "Open the browser/device login flow to finish Claude Code setup."
			}
			return result, nil
		}
		return result, fmt.Errorf("Claude Code is installed but no auth was configured. Finish Claude plan OAuth with the browser login flow or import subscription credentials from an already-signed-in user-owned device")
	}
	if req.Runner == "codex" && !row.AuthConfigured {
		if boolOrDefault(req.AllowInstallOnly, false) && row.Installed {
			result.Warning = "Codex was installed, but authentication is still required."
			if strings.TrimSpace(result.Detail) == "" {
				result.Detail = "Open the browser/device login flow to finish ChatGPT Plus/Pro plan OAuth for Codex."
			}
			return result, nil
		}
		return result, fmt.Errorf("Codex is installed but no auth was configured. Finish ChatGPT Plus/Pro plan OAuth with the browser login flow or import subscription credentials from an already-signed-in user-owned device")
	}
	if req.Runner == "claude" {
		result.Notes = append(result.Notes, "Yaver can use the saved Claude credentials from its vault. Direct `claude` shell sessions outside Yaver may still require exported env vars or Claude's own native login.")
	}
	return result, nil
}

func applyRunnerAuthSetupRemote(target string, req runnerAuthSetupRequest) (runnerAuthSetupResult, error) {
	out, err := proxyToDeviceJSON(context.Background(), "runner-auth-setup", target, http.MethodPost, "/runner-auth/setup", req)
	if err != nil {
		return runnerAuthSetupResult{}, err
	}
	var result runnerAuthSetupResult
	raw, err := json.Marshal(out)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	result.DeviceID = strings.TrimSpace(target)
	return result, nil
}

func mcpRunnerAuthSetup(deviceID string, req runnerAuthSetupRequest) interface{} {
	var (
		result runnerAuthSetupResult
		err    error
	)
	if strings.TrimSpace(deviceID) != "" {
		result, err = applyRunnerAuthSetupRemote(strings.TrimSpace(deviceID), req)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err = applyRunnerAuthSetupLocal(ctx, req)
	}
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return result
}
