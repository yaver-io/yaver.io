package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
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
	if err := checkRunnerWorkDirWritable(runner, workDir); err != nil {
		return err
	}
	return nil
}

// checkRunnerWorkDirWritable returns a friendly error when the runner's
// sandbox would fail to write into workDir. Codex 0.123.0 wraps every
// `codex exec` in a bwrap (bubblewrap) sandbox that drops
// CAP_DAC_OVERRIDE before invoking the model — so even root inside the
// sandbox is treated as an unprivileged user against the host's DAC,
// and a `chown 501:staff` left over from a Mac → Linux rsync makes
// codex hard-fail mid-task with `bwrap: Can't create file at
// <workDir>/.codex: Permission denied`. The user sees a partial output
// like "blocked: every shell command fails with bwrap…" which doesn't
// point at the actual fix (chown the dir).
//
// We probe by trying to create + remove a dotfile in workDir using the
// agent's effective uid. If the create fails for any reason we surface
// a single readable line that tells the user exactly which path is
// unwritable, who currently owns it, and the command that fixes it.
//
// Skipped for non-sandboxed runners (claude, opencode) — their write
// path is the agent's normal cmd.Dir, so the host's regular DAC rules
// already apply and a normal "permission denied" travels straight to
// the user without sandbox indirection.
func checkRunnerWorkDirWritable(runner RunnerConfig, workDir string) error {
	if normalizeRunnerID(runner.RunnerID) != "codex" {
		return nil
	}
	dir := strings.TrimSpace(workDir)
	if dir == "" {
		return nil // empty workDir = agent uses its own cwd; that path is exercised by SDK token checks
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil // non-existent paths surface a clearer error elsewhere
	}
	probe, probeErr := os.CreateTemp(dir, ".yaver-codex-probe-*")
	if probeErr == nil {
		probe.Close()
		_ = os.Remove(probe.Name())
	}
	// The probe lies when the agent is root: CAP_DAC_OVERRIDE lets the
	// host write succeed even though bwrap (which strips that cap) is
	// going to refuse the same operation. codexBwrapWillFail catches
	// that case by inspecting ownership directly.
	bwrapBlocked := codexBwrapWillFail(info)
	if probeErr == nil && !bwrapBlocked {
		return nil
	}
	owner := workDirOwnerLabel(info)
	if owner == "" {
		owner = "unknown"
	}
	uid := os.Geteuid()
	gid := os.Getegid()
	probeNote := "host probe failed: " + fmt.Sprintf("%v", probeErr)
	if probeErr == nil && bwrapBlocked {
		probeNote = "host probe succeeded via CAP_DAC_OVERRIDE but bwrap will drop that cap"
	}
	return fmt.Errorf(
		"codex sandbox cannot write into %s (dir owner: %s, agent: uid=%d gid=%d). "+
			"Codex's bwrap drops CAP_DAC_OVERRIDE so the project must be owned by the user running yaver. "+
			"Run `sudo chown -R %d:%d %s` on the host and retry. (%s)",
		dir, owner, uid, gid, uid, gid, dir, probeNote,
	)
}

// DetectRunnerRuntimeStatus performs best-effort readiness checks for runners
// whose real usability depends on auth state, local config, or provider policy.
//
// Honors `lastRunnerAuthFailure` — when a recent task with this runner exited
// with an auth-error pattern (401 / Invalid bearer token / Not logged in),
// flips AuthConfigured back to false even if the file/keychain is present.
// File presence is a *necessary* but not *sufficient* signal — without this
// override, DeviceDetailsModal cheerfully renders ✓ signed in while the next
// task instantly 401's. Cleared via runner_auth_browser_http.go's OAuth-
// completion path so a successful re-sign-in reverts the override.
func DetectRunnerRuntimeStatus(runner RunnerConfig, workDir string) RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}

	switch normalizeRunnerID(runner.RunnerID) {
	case "codex":
		status = detectCodexStatus()
	case "opencode":
		status = detectOpenCodeStatus(workDir)
	case "claude":
		status = detectClaudeStatus()
	case "glm":
		status = detectGLMStatus()
	default:
		return status
	}
	if status.AuthConfigured && runnerAuthFailureRecent(normalizeRunnerID(runner.RunnerID)) {
		status.AuthConfigured = false
		status.AuthSource = ""
		if strings.TrimSpace(status.Warning) == "" {
			status.Warning = "Token rejected by API on the last task — sign in again to refresh."
		}
	}
	return status
}

// lastRunnerAuthFailure tracks runners whose most recent task spawn exited
// with an auth-error pattern in stdout/stderr. tasks.go/watchProcess writes
// here on detection; DetectRunnerRuntimeStatus reads to override the
// presence-based AuthConfigured. Cleared when a fresh OAuth completes.
//
// Why a TTL at all: if the user signs back in via a path we can't observe
// (e.g. they SSH'd to the box and ran `claude /login` themselves), the
// override would otherwise stick forever. 30 min lets the next status poll
// re-check via DetectRunnerRuntimeStatus's normal file/keychain probe.
var (
	lastRunnerAuthFailure = struct {
		sync.Mutex
		at map[string]time.Time
	}{at: make(map[string]time.Time)}
	runnerAuthFailureTTL = 30 * time.Minute
)

// MarkRunnerAuthInvalid records that the named runner just produced an
// auth-error on a task spawn. Called from tasks.go/watchProcess. Safe to
// call from any goroutine.
func MarkRunnerAuthInvalid(runnerID string) {
	id := normalizeRunnerID(runnerID)
	if id == "" {
		return
	}
	lastRunnerAuthFailure.Lock()
	defer lastRunnerAuthFailure.Unlock()
	lastRunnerAuthFailure.at[id] = time.Now()
}

// ClearRunnerAuthInvalid removes the override for a runner. Called from the
// browser-auth flow when a fresh OAuth completes successfully.
func ClearRunnerAuthInvalid(runnerID string) {
	id := normalizeRunnerID(runnerID)
	if id == "" {
		return
	}
	lastRunnerAuthFailure.Lock()
	defer lastRunnerAuthFailure.Unlock()
	delete(lastRunnerAuthFailure.at, id)
}

func runnerAuthFailureRecent(runnerID string) bool {
	lastRunnerAuthFailure.Lock()
	defer lastRunnerAuthFailure.Unlock()
	at, ok := lastRunnerAuthFailure.at[runnerID]
	if !ok {
		return false
	}
	if time.Since(at) > runnerAuthFailureTTL {
		delete(lastRunnerAuthFailure.at, runnerID)
		return false
	}
	return true
}

// IsRunnerAuthFailureOutput matches stdout/stderr from a runner spawn
// against patterns indicating the OAuth token was rejected. Mirrors mobile
// ErrorMessage.tsx::detectRunnerAuthFailure so server- and client-side
// detections stay in sync — if a pattern triggers the mobile failure card
// CTA, the server-side override should also fire.
//
// Returns the runner id ("claude" / "codex") on match, "" otherwise.
func IsRunnerAuthFailureOutput(output string) string {
	m := strings.ToLower(output)
	if m == "" {
		return ""
	}
	looksLikeClaude := (strings.Contains(m, "not logged in") &&
		(strings.Contains(m, "/login") || strings.Contains(m, "please run"))) ||
		strings.Contains(m, "invalid bearer token") ||
		strings.Contains(m, "invalid authentication credentials") ||
		strings.Contains(m, "claude code-credentials")
	if looksLikeClaude {
		return "claude"
	}
	looksLikeCodex := (strings.Contains(m, "sign in required") &&
		(strings.Contains(m, "codex") || strings.Contains(m, "chatgpt"))) ||
		strings.Contains(m, "codex login --device-auth") ||
		(strings.Contains(m, "not authenticated") && strings.Contains(m, "codex")) ||
		(strings.Contains(m, "model is not supported") && strings.Contains(m, "chatgpt account")) ||
		strings.Contains(m, "refresh_token_reused") ||
		strings.Contains(m, "token_expired")
	if looksLikeCodex {
		return "codex"
	}
	looksLikeOpenCode := strings.Contains(m, "opencode") && (strings.Contains(m, "ai_apicallerror") ||
		strings.Contains(m, "failedtoopensocket") ||
		strings.Contains(m, "stream error") ||
		strings.Contains(m, "providerid="))
	if looksLikeOpenCode {
		return "opencode"
	}
	return ""
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
	case "glm":
		return "GLM (z.ai)"
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
	case "glm":
		if !status.AuthConfigured {
			return "runner.glm.auth_required", "GLM (z.ai) needs a z.ai API key.", "Save a ZAI_API_KEY in the vault (or runner-provider/API_KEY__glm) before using GLM remotely.", true
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

// normalizeRunnerID maps a user-facing runner id to the agent's
// internal canonical id. User-facing ("claude-code") collapses onto
// the internal id ("claude") so the agent's spawn / case tables don't
// need to be re-threaded. Switch this if the internal canonical ever
// flips — the rest of the agent reads through this single function.
func normalizeRunnerID(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "claude-code":
		return "claude"
	case "zai", "z.ai", "z-ai", "glm-4.6", "glm-4.7":
		// GLM/z.ai user-facing aliases collapse onto the internal "glm" id.
		return "glm"
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
		} else if runtime.GOOS == "darwin" && claudeMacKeychainHasCreds() {
			// macOS subscription users have no env var and no
			// ~/.claude/.credentials.json — Claude Code stores the
			// OAuth token in the system Keychain under the service
			// name "Claude Code-credentials". `security find-generic-
			// password` exits 0 iff the entry exists, so we use it
			// as a cheap presence check (cached for 60s to keep the
			// /runner-auth/status poll loop free of fork overhead).
			status.AuthConfigured = true
			status.AuthSource = "macOS Keychain · Claude Code-credentials"
		}
	}
	return status
}

// detectGLMStatus reports auth readiness for the GLM (z.ai) runner. GLM runs
// on the claude binary pointed at z.ai's Anthropic endpoint, so "authenticated"
// means a z.ai credential resolves — either a bare ZAI_API_KEY / GLM_API_KEY
// (env or vault) or an explicit runner-provider config (API_KEY__glm). No
// Anthropic OAuth / Keychain path applies here.
func detectGLMStatus() RunnerRuntimeStatus {
	status := RunnerRuntimeStatus{Ready: true}
	if cfg := runnerProviderConfigFor("glm"); cfg.apiKey != "" {
		status.AuthConfigured = true
		status.AuthSource = "z.ai key (" + cfg.baseURL + ")"
		return status
	}
	if value, source := hostSecretValue("ZAI_API_KEY"); value != "" {
		status.AuthConfigured = true
		status.AuthSource = source
		return status
	}
	status.Warning = "No z.ai credential found — set ZAI_API_KEY (or vault runner-provider/API_KEY__glm) to use GLM."
	return status
}

var (
	claudeMacKeychainCache = struct {
		sync.Mutex
		ok bool
		at time.Time
	}{}
	claudeMacKeychainTTL = 60 * time.Second
)

// claudeMacKeychainHasCreds returns true if the macOS Keychain has the
// "Claude Code-credentials" generic password entry that Claude Code 2.x
// uses for subscription OAuth. Result cached for 60s — the underlying
// `security` invocation triggers a 1-time auth prompt the very first
// time the calling process accesses the entry, but subsequent reads
// from the same daemon are silent.
func claudeMacKeychainHasCreds() bool {
	claudeMacKeychainCache.Lock()
	defer claudeMacKeychainCache.Unlock()
	if !claudeMacKeychainCache.at.IsZero() && time.Since(claudeMacKeychainCache.at) < claudeMacKeychainTTL {
		return claudeMacKeychainCache.ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials")
	err := cmd.Run()
	ok := err == nil
	claudeMacKeychainCache.ok = ok
	claudeMacKeychainCache.at = time.Now()
	return ok
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
	if value, source := hostSecretValue("OPENAI_API_KEY"); value != "" {
		status.AuthConfigured = true
		status.AuthSource = source
		return status
	}
	// Check every path the codex CLI is known to drop credentials at,
	// across versions / install methods. The original detector only
	// looked at ~/.codex/auth.json and missed installs that write
	// credentials.json (newer device-auth) or store the OAuth payload
	// under ~/.codex/sessions/. When the file is missing, the dashboard
	// re-prompts for sign-in even though the user just completed the
	// flow — that surfaced as the "Test → Sign In Codex" loop in #19.
	for _, path := range codexAuthCandidatePaths() {
		if runnerFileExists(path) {
			status.AuthConfigured = true
			status.AuthSource = path
			return status
		}
	}
	// Final fallback: ask the codex CLI itself. `codex login status`
	// exits 0 with account info when authenticated, non-zero with a
	// "run codex login" message otherwise. Only useful when the binary
	// resolves on PATH — when it doesn't we leave the existing "no
	// credentials" error in place. Cached so the poll loop doesn't
	// fork codex every 1.5s.
	if codexLoginStatusOK() {
		status.AuthConfigured = true
		status.AuthSource = "codex login status"
		return status
	}
	status.Ready = false
	status.Error = "Codex is installed but no credentials were found. Set `OPENAI_API_KEY` or run `codex login --device-auth` (and complete it in the browser). Checked: " + strings.Join(codexAuthCandidatePaths(), ", ") + "."
	return status
}

var (
	codexLoginStatusCache = struct {
		sync.Mutex
		ok bool
		at time.Time
	}{}
	codexLoginStatusTTL = 60 * time.Second
)

func codexLoginStatusOK() bool {
	codexLoginStatusCache.Lock()
	defer codexLoginStatusCache.Unlock()
	if !codexLoginStatusCache.at.IsZero() && time.Since(codexLoginStatusCache.at) < codexLoginStatusTTL {
		return codexLoginStatusCache.ok
	}
	bin := resolveRunnerBinary("codex")
	if bin == "" {
		codexLoginStatusCache.ok = false
		codexLoginStatusCache.at = time.Now()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	cmd := osexec.CommandContext(ctx, bin, "login", "status")
	err := cmd.Run()
	ok := err == nil
	codexLoginStatusCache.ok = ok
	codexLoginStatusCache.at = time.Now()
	return ok
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

// codexAuthCandidatePaths returns every plausible location the codex
// CLI may drop credentials at, in priority order. Different versions
// of the codex CLI use different file names — older cuts wrote
// `auth.json`, the device-auth flow we shell out to writes
// `credentials.json`, and the OAuth-session variant stashes a
// directory of session JSON under `sessions/`. We treat any of them
// existing as "authenticated" so the readiness probe stops re-asking
// the user to sign in after they've already completed the flow.
//
// Honors CODEX_HOME, then $HOME/.codex/* and $HOME/.openai/codex/*
// (the latter is what the OpenAI CLI defaults to when codex is
// installed as part of the unified `openai` Python package).
func codexAuthCandidatePaths() []string {
	out := []string{}
	add := func(parent string) {
		if parent == "" {
			return
		}
		out = append(out,
			filepath.Join(parent, "auth.json"),
			filepath.Join(parent, "credentials.json"),
			filepath.Join(parent, "session.json"),
			filepath.Join(parent, "sessions"),
		)
	}
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		add(dir)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		add(filepath.Join(home, ".codex"))
		add(filepath.Join(home, ".openai", "codex"))
		add(filepath.Join(home, ".config", "codex"))
	}
	// De-dup while preserving order so the AuthSource we report is the
	// first match the user is most likely to recognise.
	seen := map[string]bool{}
	dedup := make([]string, 0, len(out))
	for _, p := range out {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		dedup = append(dedup, p)
	}
	return dedup
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
