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
	Runner string `json:"runner"`
	// Deprecated credential/setup fields are retained only so rolling clients
	// receive an explicit refusal instead of silently losing secrets.
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
	noInstall := fs.Bool("no-install", false, "skip installing the runner if missing")
	fs.Parse(args[1:])

	installIfMissing := !*noInstall
	req := runnerAuthSetupRequest{
		Runner:           runner,
		InstallIfMissing: &installIfMissing,
		AllowInstallOnly: boolPtr(true),
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
	case "claude":
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
	case "claude":
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
	if reason, retired := retiredRunnerReason(req.Runner); retired {
		return result, fmt.Errorf("%s", reason)
	}
	if req.Runner != "claude" && req.Runner != "codex" && req.Runner != "opencode" {
		return result, fmt.Errorf("unsupported runner %q (want claude, codex, or opencode)", req.Runner)
	}
	if runnerAuthValueProvided(req) || strings.TrimSpace(req.Notes) != "" {
		return result, fmt.Errorf("Yaver does not accept runner credentials; open an encrypted PTY and authenticate with the native %s CLI", req.Runner)
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

	if !row.AuthConfigured {
		result.Warning = fmt.Sprintf("%s is installed, but its native CLI is not authenticated.", row.Name)
		result.Detail = fmt.Sprintf("Open an encrypted Yaver PTY on this device and run the native %s sign-in command. Yaver does not broker or copy runner credentials.", req.Runner)
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
