package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RunnerRuntimeStatus describes whether a runner is usable on this machine,
// including runner-specific auth/config checks that plain LookPath misses.
type RunnerRuntimeStatus struct {
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

// CheckRunnerReady verifies the binary exists and that any runner-specific
// auth/config policy checks pass for the current workspace.
func CheckRunnerReady(runner RunnerConfig, workDir string) error {
	if err := CheckRunnerBinary(runner.Command); err != nil {
		return err
	}
	status := DetectRunnerRuntimeStatus(runner, workDir)
	if !status.Ready && strings.TrimSpace(status.Error) != "" {
		return fmt.Errorf("%s", status.Error)
	}
	return nil
}

// DetectRunnerRuntimeStatus performs best-effort readiness checks for runners
// whose real usability depends on auth state, local config, or provider policy.
func DetectRunnerRuntimeStatus(runner RunnerConfig, workDir string) RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}

	switch normalizeRunnerID(runner.RunnerID) {
	case "codex":
		return detectCodexStatus()
	case "opencode":
		return detectOpenCodeStatus(workDir)
	case "claude":
		return detectClaudeStatus()
	default:
		return status
	}
}

func normalizeRunnerID(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "claude-code":
		return "claude"
	default:
		return strings.ToLower(strings.TrimSpace(id))
	}
}

func detectClaudeStatus() RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}
	switch {
	case strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "":
		status.AuthConfigured = true
		status.AuthSource = "ANTHROPIC_API_KEY"
	case strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")) != "":
		status.AuthConfigured = true
		status.AuthSource = "ANTHROPIC_AUTH_TOKEN"
	case strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")) != "":
		status.AuthConfigured = true
		status.AuthSource = "CLAUDE_CODE_OAUTH_TOKEN"
	default:
		if path, ok := claudeCredentialsPath(); ok {
			status.AuthConfigured = true
			if runtime.GOOS == "darwin" {
				status.AuthSource = "macOS Keychain / Claude login"
			} else {
				status.AuthSource = path
			}
		}
	}
	return status
}

func claudeCredentialsPath() (string, bool) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		path := filepath.Join(dir, ".credentials.json")
		if runnerFileExists(path) {
			return path, true
		}
	}
	if runtime.GOOS == "darwin" {
		// Claude Code stores subscription credentials in the encrypted Keychain.
		// We cannot cheaply probe Keychain access here, so leave auth as unknown.
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	return path, runnerFileExists(path)
}

func detectCodexStatus() RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}
	switch {
	case strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "":
		status.AuthConfigured = true
		status.AuthSource = "OPENAI_API_KEY"
	case runnerFileExists(codexAuthPath()):
		status.AuthConfigured = true
		status.AuthSource = codexAuthPath()
	default:
		status.Ready = false
		status.Error = "Codex is installed but not authenticated. Set `OPENAI_API_KEY` or run the Codex login flow first."
	}
	return status
}

func codexAuthPath() string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func detectOpenCodeStatus(workDir string) RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}
	authPath, authText := readFirstExistingFile(openCodeAuthPaths())
	_, cfgText := readFirstExistingFile(openCodeConfigPaths(workDir))

	authLower := strings.ToLower(authText)
	cfgLower := strings.ToLower(cfgText)

	hasOpenAIOAuth := strings.Contains(authLower, "openai")
	hasAnthropicOAuth := strings.Contains(authLower, "anthropic")
	hasOpenAIAPI := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" || strings.Contains(cfgLower, "openai_api_key")
	hasAnthropicAPI := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" || strings.Contains(cfgLower, "anthropic_api_key")
	hasLocalProvider := strings.Contains(cfgLower, "ollama") ||
		strings.Contains(cfgLower, "lmstudio") ||
		strings.Contains(cfgLower, "llama.cpp") ||
		strings.Contains(cfgLower, "localhost:11434") ||
		strings.Contains(cfgLower, "127.0.0.1:11434") ||
		strings.Contains(cfgLower, "127.0.0.1:1234")

	// Anthropic explicitly forbids third-party apps from routing Claude.ai
	// subscription OAuth credentials on behalf of users. If OpenCode is the
	// only detected auth source and it points at Anthropic, make the dev use
	// Yaver's direct Claude Code integration instead.
	if hasAnthropicOAuth && !hasAnthropicAPI && !hasOpenAIOAuth && !hasOpenAIAPI && !hasLocalProvider {
		status.Ready = false
		status.AuthConfigured = true
		status.AuthSource = authPath
		status.Error = "OpenCode appears to be configured with Anthropic OAuth credentials. Anthropic does not allow third-party wrappers to route Claude.ai login on behalf of users. Use Yaver's direct `claude` runner instead, or configure OpenCode with an Anthropic API key."
		return status
	}

	switch {
	case hasOpenAIOAuth:
		status.AuthConfigured = true
		status.AuthSource = authPath
	case hasOpenAIAPI:
		status.AuthConfigured = true
		status.AuthSource = "OpenAI API key"
	case hasAnthropicAPI:
		status.AuthConfigured = true
		status.AuthSource = "Anthropic API key"
	case hasLocalProvider:
		status.AuthConfigured = true
		status.AuthSource = "local provider config"
	case strings.TrimSpace(authText) != "":
		status.AuthConfigured = true
		status.AuthSource = authPath
	case strings.TrimSpace(cfgText) != "":
		status.Warning = "OpenCode config found but no explicit auth was detected; environment-based providers may still work."
	default:
		status.Warning = "OpenCode auth was not detected. If tasks fail, run `opencode auth list` or `/connect`."
	}
	return status
}

func openCodeAuthPaths() []string {
	var out []string
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dir != "" {
		out = append(out, filepath.Join(dir, "auth.json"))
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		out = append(out, filepath.Join(xdg, "opencode", "auth.json"))
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".local", "share", "opencode", "auth.json"),
			filepath.Join(home, ".opencode", "auth.json"),
		)
	}
	return uniqStrings(out)
}

func openCodeConfigPaths(workDir string) []string {
	var out []string
	if workDir != "" {
		out = append(out,
			filepath.Join(workDir, "opencode.json"),
			filepath.Join(workDir, "opencode.jsonc"),
			filepath.Join(workDir, ".opencode", "opencode.json"),
			filepath.Join(workDir, ".opencode", "opencode.jsonc"),
		)
	}
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); dir != "" {
		out = append(out,
			filepath.Join(dir, "opencode.json"),
			filepath.Join(dir, "opencode.jsonc"),
		)
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		out = append(out,
			filepath.Join(xdg, "opencode", "opencode.json"),
			filepath.Join(xdg, "opencode", "opencode.jsonc"),
		)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".config", "opencode", "opencode.json"),
			filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
			filepath.Join(home, ".opencode.json"),
			filepath.Join(home, ".opencode.jsonc"),
		)
	}
	return uniqStrings(out)
}

func readFirstExistingFile(paths []string) (string, string) {
	for _, path := range paths {
		if !runnerFileExists(path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			return path, string(data)
		}
	}
	return "", ""
}

func runnerFileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func uniqStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
