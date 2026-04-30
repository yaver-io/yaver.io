package main

import (
	"encoding/json"
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

func capabilityForRunner(runnerID, workDir string) CapabilityTargetReadiness {
	cfg := GetRunnerConfig(runnerID)
	if err := CheckRunnerBinary(cfg.Command); err != nil {
		return CapabilityTargetReadiness{
			Enabled:         false,
			ReasonCode:      "runner." + normalizeRunnerID(runnerID) + ".not_installed",
			Reason:          fmt.Sprintf("%s is not installed on this machine.", runnerCapabilityName(runnerID)),
			SuggestedAction: fmt.Sprintf("Install %s on the host machine before using it remotely.", runnerCapabilityName(runnerID)),
		}
	}
	status := DetectRunnerRuntimeStatus(cfg, workDir)
	if code, reason, action, blocked := runnerCapabilityReason(normalizeRunnerID(runnerID), status); blocked {
		return CapabilityTargetReadiness{
			Enabled:         false,
			ReasonCode:      code,
			Reason:          reason,
			SuggestedAction: action,
		}
	}
	notes := []string{}
	if status.AuthConfigured && strings.TrimSpace(status.AuthSource) != "" {
		notes = append(notes, "authenticated via "+strings.TrimSpace(status.AuthSource))
	}
	if strings.TrimSpace(status.Warning) != "" {
		notes = append(notes, strings.TrimSpace(status.Warning))
	}
	return CapabilityTargetReadiness{Enabled: true, Notes: notes}
}

func runnerCapabilityName(runnerID string) string {
	switch normalizeRunnerID(runnerID) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "opencode":
		return "OpenCode"
	default:
		return strings.TrimSpace(runnerID)
	}
}

func runnerCapabilityReason(runnerID string, status RunnerRuntimeStatus) (code, reason, action string, blocked bool) {
	switch runnerID {
	case "codex":
		if strings.Contains(strings.ToLower(status.Error), "not authenticated") {
			return ReasonRunnerCodexNotAuthenticated, "Codex is installed but not authenticated on this machine.", "Run the Codex login flow or provide `OPENAI_API_KEY` before using Codex remotely.", true
		}
		if strings.Contains(strings.ToLower(status.Error), "blocking the sandbox") {
			return ReasonRunnerCodexLinuxSandboxBlocked, "This Linux machine is blocking the sandbox Codex needs for execution.", "Fix the Linux sandbox prerequisites on the host before running Codex.", true
		}
	case "claude":
		if !status.AuthConfigured {
			return ReasonRunnerClaudeAuthRequired, "Claude Code is installed but no usable auth was detected yet.", "Run the Claude browser login flow or save an Anthropic credential on the host machine.", true
		}
	case "opencode":
		if strings.TrimSpace(status.Error) != "" {
			return ReasonRunnerOpenCodeUnusable, strings.TrimSpace(status.Error), "Update the OpenCode provider/auth configuration on the host before using it remotely.", true
		}
	}
	return "", "", "", false
}

func runnerDoctorDetail(runner RunnerConfig, workDir, binaryPath, version string) (string, string) {
	status := DetectRunnerRuntimeStatus(runner, workDir)
	detail := strings.TrimSpace(binaryPath)
	if strings.TrimSpace(version) != "" {
		detail = strings.TrimSpace(detail + " (" + strings.TrimSpace(version) + ")")
	}
	switch {
	case strings.TrimSpace(status.Error) != "":
		if detail == "" {
			return "warn", status.Error
		}
		return "warn", detail + " — " + status.Error
	case status.AuthConfigured && strings.TrimSpace(status.AuthSource) != "":
		if detail == "" {
			return "ok", "authenticated via " + status.AuthSource
		}
		return "ok", detail + " — authenticated via " + status.AuthSource
	case strings.TrimSpace(status.Warning) != "":
		if detail == "" {
			return "warn", status.Warning
		}
		return "warn", detail + " — " + status.Warning
	default:
		if detail == "" {
			return "ok", "installed"
		}
		return "ok", detail
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
	case func() bool {
		value, _ := hostSecretValue("ANTHROPIC_API_KEY")
		return value != ""
	}():
		status.AuthConfigured = true
		_, source := hostSecretValue("ANTHROPIC_API_KEY")
		status.AuthSource = source
	case func() bool {
		value, _ := hostSecretValue("ANTHROPIC_AUTH_TOKEN")
		return value != ""
	}():
		status.AuthConfigured = true
		_, source := hostSecretValue("ANTHROPIC_AUTH_TOKEN")
		status.AuthSource = source
	case func() bool {
		value, _ := hostSecretValue("CLAUDE_CODE_OAUTH_TOKEN")
		return value != ""
	}():
		status.AuthConfigured = true
		_, source := hostSecretValue("CLAUDE_CODE_OAUTH_TOKEN")
		status.AuthSource = source
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
	if runtime.GOOS == "linux" {
		if err := codexLinuxSandboxPrereqError(); err != "" {
			status.Ready = false
			status.Error = err
			return status
		}
	}
	switch {
	case func() bool {
		value, _ := hostSecretValue("OPENAI_API_KEY")
		return value != ""
	}():
		status.AuthConfigured = true
		_, source := hostSecretValue("OPENAI_API_KEY")
		status.AuthSource = source
	case runnerFileExists(codexAuthPath()):
		status.AuthConfigured = true
		status.AuthSource = codexAuthPath()
	default:
		status.Ready = false
		status.Error = "Codex is installed but not authenticated. Set `OPENAI_API_KEY` or run the Codex login flow first."
	}
	return status
}

func codexLinuxSandboxPrereqError() string {
	issues := []string{}
	if value, ok := readLinuxKernelParam("/proc/sys/kernel/unprivileged_userns_clone"); ok && value == "0" {
		issues = append(issues, "kernel.unprivileged_userns_clone=0")
	}
	if value, ok := readLinuxKernelParam("/proc/sys/user/max_user_namespaces"); ok && (value == "0" || value == "") {
		issues = append(issues, "user.max_user_namespaces=0")
	}
	if value, ok := readLinuxKernelParam("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); ok && value == "1" {
		issues = append(issues, "kernel.apparmor_restrict_unprivileged_userns=1")
	}
	if len(issues) == 0 {
		return ""
	}
	return "Codex is installed but this Linux host is blocking the sandbox it uses for `codex exec`. Fix host user-namespace support first (`kernel.unprivileged_userns_clone=1`, `user.max_user_namespaces=1048576`, and if present `kernel.apparmor_restrict_unprivileged_userns=0`). Current blockers: " + strings.Join(issues, ", ")
}

func readLinuxKernelParam(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
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
	cfgPath, cfgText := readFirstExistingFile(openCodeConfigPaths(workDir))
	cfg := loadOpenCodeConfigMapText(cfgText)
	providers := openCodeRuntimeProviders(cfg)

	authLower := strings.ToLower(authText)
	cfgLower := strings.ToLower(cfgText)
	openAIValue, _ := hostSecretValue("OPENAI_API_KEY")
	glmValue, glmSource := hostSecretValue("GLM_API_KEY")
	anthropicValue, _ := hostSecretValue("ANTHROPIC_API_KEY")

	hasOpenAIOAuth := strings.Contains(authLower, "openai")
	hasAnthropicOAuth := strings.Contains(authLower, "anthropic")
	hasOpenAIAPI := openAIValue != ""
	hasGLMAPI := glmValue != ""
	hasAnthropicAPI := anthropicValue != ""
	hasLocalProvider := false
	hasConfiguredProvider := false
	for _, p := range providers {
		id := normalizeOpenCodeProvider(p.ID)
		baseLower := strings.ToLower(strings.TrimSpace(p.BaseURL))
		if p.HasAPIKey || strings.TrimSpace(p.BaseURL) != "" {
			hasConfiguredProvider = true
		}
		if id == "openai" && (p.HasAPIKey || openAIValue != "") {
			hasOpenAIAPI = true
		}
		if (id == "glm" || id == "zai" || id == "z-ai") && (p.HasAPIKey || glmValue != "") {
			hasGLMAPI = true
		}
		if id == "anthropic" && (p.HasAPIKey || anthropicValue != "") {
			hasAnthropicAPI = true
		}
		if id == "ollama" || id == "lmstudio" || id == "llama.cpp" {
			hasLocalProvider = hasLocalProvider || p.HasAPIKey || strings.TrimSpace(p.BaseURL) != ""
		}
		if strings.Contains(baseLower, ":11434") || strings.Contains(baseLower, ":1234") {
			hasLocalProvider = true
		}
	}
	if !hasOpenAIAPI {
		hasOpenAIAPI = strings.Contains(cfgLower, "openai_api_key")
	}
	if !hasGLMAPI {
		hasGLMAPI = strings.Contains(cfgLower, "glm_api_key") ||
			strings.Contains(cfgLower, "zai_api_key") ||
			strings.Contains(cfgLower, "\"glm\"") ||
			strings.Contains(cfgLower, "\"z-ai\"") ||
			strings.Contains(cfgLower, "\"zai\"")
	}
	if !hasAnthropicAPI {
		hasAnthropicAPI = strings.Contains(cfgLower, "anthropic_api_key")
	}
	if !hasLocalProvider {
		hasLocalProvider = strings.Contains(cfgLower, "ollama") ||
			strings.Contains(cfgLower, "lmstudio") ||
			strings.Contains(cfgLower, "llama.cpp") ||
			strings.Contains(cfgLower, "localhost:11434") ||
			strings.Contains(cfgLower, "127.0.0.1:11434") ||
			strings.Contains(cfgLower, "127.0.0.1:1234")
	}

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
	case hasGLMAPI:
		status.AuthConfigured = true
		if glmSource != "" {
			status.AuthSource = glmSource
		} else {
			status.AuthSource = "GLM API key"
		}
	case hasAnthropicAPI:
		status.AuthConfigured = true
		status.AuthSource = "Anthropic API key"
	case hasLocalProvider:
		status.AuthConfigured = true
		status.AuthSource = "local provider config"
	case hasConfiguredProvider:
		status.AuthConfigured = true
		if cfgPath != "" {
			status.AuthSource = cfgPath
		} else {
			status.AuthSource = "provider config"
		}
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

type openCodeRuntimeProvider struct {
	ID        string
	BaseURL   string
	HasAPIKey bool
}

func loadOpenCodeConfigMapText(raw string) map[string]any {
	clean := stripJSONC([]byte(raw))
	if strings.TrimSpace(string(clean)) == "" {
		return map[string]any{}
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return map[string]any{}
	}
	return cfg
}

func openCodeRuntimeProviders(cfg map[string]any) []openCodeRuntimeProvider {
	node, _ := cfg["provider"].(map[string]any)
	if len(node) == 0 {
		return nil
	}
	out := make([]openCodeRuntimeProvider, 0, len(node))
	for id, raw := range node {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		options, _ := entry["options"].(map[string]any)
		baseURL, _ := stringFromMap(options, "baseURL")
		if strings.TrimSpace(baseURL) == "" {
			baseURL, _ = stringFromMap(options, "baseUrl")
		}
		apiKey, _ := stringFromMap(options, "apiKey")
		out = append(out, openCodeRuntimeProvider{
			ID:        strings.TrimSpace(id),
			BaseURL:   strings.TrimSpace(baseURL),
			HasAPIKey: strings.TrimSpace(apiKey) != "",
		})
	}
	return out
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
