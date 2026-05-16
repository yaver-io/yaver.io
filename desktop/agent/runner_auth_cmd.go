package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type runnerAuthEntry struct {
	Name     string
	Value    string
	Notes    string
	Provider string
}

func runRunnerAuth(args []string) {
	if len(args) == 0 {
		printRunnerAuthUsage()
		return
	}
	if target, runner, extra, ok := parseRunnerAuthQuickFlow(args); ok {
		runRunnerAuthQuickFlow(target, runner, extra)
		return
	}
	switch args[0] {
	case "status", "ls", "list":
		runRunnerAuthStatus(args[1:])
	case "set":
		runRunnerAuthSet(args[1:])
	case "setup":
		runRunnerAuthSetup(args[1:])
	case "help", "-h", "--help":
		printRunnerAuthUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown runner-auth subcommand: %s\n\n", args[0])
		printRunnerAuthUsage()
		os.Exit(1)
	}
}

func printRunnerAuthUsage() {
	fmt.Print(`yaver runner-auth — headless runner/provider auth setup

Usage:
  yaver runner-auth status [--target <deviceId>]
  yaver runner-auth <deviceId|name|alias> <claude|claude-code|codex>
  yaver runner-auth set claude [--target <deviceId>] [--anthropic-api-key <key> | --anthropic-auth-token <token> | --claude-code-oauth-token <token>]
  yaver runner-auth set codex [--target <deviceId>] --openai-api-key <key>
  yaver runner-auth set opencode [--target <deviceId>] [--openai-api-key <key>] [--anthropic-api-key <key>] [--glm-api-key <key>] [--zai-api-key <key>]
  yaver runner-auth setup claude [--target <deviceId>] [--anthropic-api-key <key>] [--anthropic-auth-token <token>] [--claude-code-oauth-token <token>]
  yaver runner-auth setup codex [--target <deviceId>] [--openai-api-key <key>] [--no-install] [--no-login] [--no-mcp]

Examples:
  yaver runner-auth test codex
  yaver runner-auth test claude-code
  yaver runner-auth set codex --openai-api-key $OPENAI_API_KEY
  yaver runner-auth setup codex --target cloud-12345678 --openai-api-key $OPENAI_API_KEY
  yaver runner-auth set opencode --glm-api-key $GLM_API_KEY --target cloud-12345678
  yaver runner-auth status --target cloud-12345678

Notes:
  - <device> <runner> is the interactive remote auth shortcut: it checks local Yaver auth, checks the target machine's Yaver auth, runs remote 'yaver auth --headless' over 'yaver ssh' if needed, then starts the remote Claude/Codex browser auth flow and prints the link/code.
  - Values are stored in the target machine's Yaver vault.
  - setup also installs the runner when missing and wires Yaver into the runner's MCP config when supported.
  - --target uses the existing Yaver remote-agent channel; it does not require SSH.
`)
}

func parseRunnerAuthQuickFlow(args []string) (target string, runner string, extra []string, ok bool) {
	if len(args) < 2 {
		return "", "", nil, false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status", "ls", "list", "set", "setup", "help", "-h", "--help":
		return "", "", nil, false
	}
	runner = normalizeRunnerAuthName(args[1])
	if runner != "claude" && runner != "codex" {
		return "", "", nil, false
	}
	return strings.TrimSpace(args[0]), runner, args[2:], true
}

func normalizeRunnerAuthName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude-code":
		return "claude"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func buildRunnerAuthEntries(runner string, openAIKey string, anthropicKey string, anthropicAuthToken string, claudeOAuthToken string, glmKey string, zaiKey string, notes string) ([]runnerAuthEntry, error) {
	runner = normalizeRunnerAuthName(runner)
	add := func(out []runnerAuthEntry, name, value, provider string) []runnerAuthEntry {
		value = strings.TrimSpace(value)
		if value == "" {
			return out
		}
		entryNotes := strings.TrimSpace(notes)
		if entryNotes == "" {
			entryNotes = fmt.Sprintf("Set by yaver runner-auth for %s (%s).", runner, provider)
		}
		return append(out, runnerAuthEntry{Name: name, Value: value, Provider: provider, Notes: entryNotes})
	}

	var out []runnerAuthEntry
	switch runner {
	case "claude":
		out = add(out, "ANTHROPIC_API_KEY", anthropicKey, "anthropic-api-key")
		out = add(out, "ANTHROPIC_AUTH_TOKEN", anthropicAuthToken, "anthropic-auth-token")
		out = add(out, "CLAUDE_CODE_OAUTH_TOKEN", claudeOAuthToken, "claude-code-oauth-token")
	case "codex":
		out = add(out, "OPENAI_API_KEY", openAIKey, "openai-api-key")
	case "opencode":
		out = add(out, "OPENAI_API_KEY", openAIKey, "openai-api-key")
		out = add(out, "ANTHROPIC_API_KEY", anthropicKey, "anthropic-api-key")
		out = add(out, "GLM_API_KEY", glmKey, "glm-api-key")
		out = add(out, "ZAI_API_KEY", zaiKey, "zai-api-key")
	default:
		return nil, fmt.Errorf("unsupported runner %q (want claude, codex, or opencode)", runner)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no auth values provided for %s", runner)
	}
	return out, nil
}

func setRunnerAuthEntriesLocal(entries []runnerAuthEntry) error {
	vs := openVault()
	for _, entry := range entries {
		if err := vs.Set(VaultEntry{
			Name:     entry.Name,
			Category: "api-key",
			Value:    entry.Value,
			Notes:    entry.Notes,
		}); err != nil {
			return err
		}
	}
	return nil
}

func setRunnerAuthEntriesRemote(target string, entries []runnerAuthEntry) error {
	for _, entry := range entries {
		_, err := proxyToDeviceJSON(
			context.Background(),
			"runner-auth-set",
			target,
			http.MethodPost,
			"/vault/set",
			map[string]any{
				"name":     entry.Name,
				"category": "api-key",
				"value":    entry.Value,
				"notes":    entry.Notes,
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

type runnerAuthStatusRow struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
	Path           string `json:"path,omitempty"`
	Detail         string `json:"detail,omitempty"`
	// Version is the first line of `<bin> --version` output (capped at
	// 80 chars). Surfaced in the mobile Coding Agents pane so the user
	// can see "Claude Code 2.1.126" / "codex-cli 0.122" / "opencode 1.4.0"
	// at a glance and tell whether their install is current.
	Version string `json:"version,omitempty"`
}

var installedRunnerInventoryCache = struct {
	mu       sync.Mutex
	ids      []string
	probedAt time.Time
	dirty    bool
	ttl      time.Duration
}{
	dirty: true,
	ttl:   30 * time.Minute,
}

func collectRunnerAuthStatusRows() ([]runnerAuthStatusRow, error) {
	wd, _ := os.Getwd()
	runners := []struct {
		ID   string
		Name string
		Cmd  string
	}{
		{ID: "claude", Name: "Claude Code", Cmd: "claude"},
		{ID: "codex", Name: "OpenAI Codex", Cmd: "codex"},
		{ID: "opencode", Name: "OpenCode", Cmd: "opencode"},
	}
	rows := make([]runnerAuthStatusRow, 0, len(runners)+8)
	for _, runner := range runners {
		path := resolveRunnerBinary(runner.Cmd)
		row := runnerAuthStatusRow{
			ID:        runner.ID,
			Name:      runner.Name,
			Installed: path != "",
			Path:      path,
		}
		if path == "" {
			row.Warning = "Not installed"
			row.Detail = "Not installed"
			rows = append(rows, row)
			continue
		}
		// Sanity-check the binary actually IS the runner we asked for.
		// Without this, a same-named shim (e.g. user has a `~/code/`
		// directory containing a `codex` binary unrelated to OpenAI's
		// CLI, or a shell wrapper) would register Installed=true and
		// the feedback SDK / tasks picker would falsely advertise it
		// as a usable runner. The sigOK probe runs `<path> --version`
		// with a 1.5s timeout (cached for 5 min so the poll loop stays
		// cheap) and matches the banner against a per-runner signature.
		sigOK, sigVersion := verifyRunnerBinarySignature(runner.ID, path)
		if !sigOK {
			row.Installed = false
			row.Warning = "Binary at " + path + " does not match the expected " + runner.Name + " signature."
			row.Detail = row.Warning
			rows = append(rows, row)
			continue
		}
		cfg := GetRunnerConfig(runner.ID)
		status := DetectRunnerRuntimeStatus(cfg, wd)
		row.Ready = status.Ready
		row.AuthConfigured = status.AuthConfigured
		row.AuthSource = status.AuthSource
		row.Warning = status.Warning
		row.Error = status.Error
		version := sigVersion
		if version == "" {
			// Fallback to the original probe in case sigVersion was empty
			// (verifyRunnerBinarySignature returns version="" for
			// unknown runners, even though it returns ok=true).
			if out, verr := osexec.Command(path, "--version").CombinedOutput(); verr == nil {
				version = strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(version) > 60 {
					version = version[:60]
				}
			}
		}
		row.Version = version
		_, row.Detail = runnerDoctorDetail(cfg, wd, path, version)
		rows = append(rows, row)
	}
	rows = append(rows, ollamaRunnerStatusRow())
	rows = append(rows, opencodeProviderStatusRows(rows)...)
	return rows, nil
}

func collectInstalledRunnerIDsFresh() []string {
	runners := []struct {
		taskID  string
		auditID string
		cmd     string
	}{
		{taskID: "claude-code", auditID: "claude", cmd: "claude"},
		{taskID: "codex", auditID: "codex", cmd: "codex"},
		{taskID: "opencode", auditID: "opencode", cmd: "opencode"},
	}
	installed := make([]string, 0, len(runners))
	for _, runner := range runners {
		path := resolveRunnerBinary(runner.cmd)
		if path == "" {
			continue
		}
		ok, _ := verifyRunnerBinarySignature(runner.auditID, path)
		if ok {
			installed = append(installed, runner.taskID)
		}
	}
	return installed
}

func collectInstalledRunnerIDs() []string {
	installedRunnerInventoryCache.mu.Lock()
	if !installedRunnerInventoryCache.dirty &&
		len(installedRunnerInventoryCache.ids) >= 0 &&
		!installedRunnerInventoryCache.probedAt.IsZero() &&
		time.Since(installedRunnerInventoryCache.probedAt) < installedRunnerInventoryCache.ttl {
		cached := append([]string(nil), installedRunnerInventoryCache.ids...)
		installedRunnerInventoryCache.mu.Unlock()
		return cached
	}
	installedRunnerInventoryCache.mu.Unlock()

	fresh := collectInstalledRunnerIDsFresh()

	installedRunnerInventoryCache.mu.Lock()
	installedRunnerInventoryCache.ids = append([]string(nil), fresh...)
	installedRunnerInventoryCache.probedAt = time.Now()
	installedRunnerInventoryCache.dirty = false
	installedRunnerInventoryCache.mu.Unlock()
	return fresh
}

func markInstalledRunnerInventoryDirty() {
	installedRunnerInventoryCache.mu.Lock()
	installedRunnerInventoryCache.dirty = true
	installedRunnerInventoryCache.mu.Unlock()
}

// ollamaRunnerStatusRow probes the local Ollama daemon. Ollama needs no
// auth — "ready" means the daemon is up and listening on its default
// port. We surface it as a peer runner because it's a first-class
// option for any opencode build/plan target and the user wants
// `yaver primary status` to reflect it alongside claude / codex.
func ollamaRunnerStatusRow() runnerAuthStatusRow {
	row := runnerAuthStatusRow{ID: "ollama", Name: "Ollama"}
	row.Path = resolveRunnerBinary("ollama")
	row.Installed = row.Path != ""

	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		host = "http://127.0.0.1:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	url := strings.TrimRight(host, "/") + "/api/tags"
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(url)
	daemonUp := err == nil && resp != nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}
	switch {
	case daemonUp:
		row.Ready = true
		row.AuthConfigured = true // Ollama needs no API key
		row.AuthSource = host
		row.Detail = "ready · " + host
	case row.Installed:
		row.Detail = "installed but daemon not reachable on " + host + " — run `ollama serve`"
		row.Warning = "Ollama daemon not running"
	default:
		row.Detail = "Not installed"
		row.Warning = "Not installed"
	}
	return row
}

// opencodeProviderStatusRows expands the user's opencode.json providers
// into one synthetic row per provider so the user can see at a glance
// which BYOK providers (anthropic / openai / glm / zai / openrouter /
// ollama / …) are wired up. Skipped when opencode itself is not
// installed — the existing OpenCode row already says "not installed"
// in that case and we don't want to confuse with a tree of phantom
// providers underneath it.
func opencodeProviderStatusRows(existing []runnerAuthStatusRow) []runnerAuthStatusRow {
	hasOpenCode := false
	for _, r := range existing {
		if r.ID == "opencode" && r.Installed {
			hasOpenCode = true
			break
		}
	}
	if !hasOpenCode {
		return nil
	}
	cfg, err := loadOpenCodeConfigSummary()
	if err != nil || !cfg.Exists || len(cfg.Providers) == 0 {
		return nil
	}
	out := make([]runnerAuthStatusRow, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		row := runnerAuthStatusRow{
			ID:        "opencode/" + p.ID,
			Name:      "  ↳ " + firstNonEmpty(p.Name, p.ID),
			Installed: true,
			Path:      cfg.Path,
		}
		if envVar, hasKey := opencodeProviderEnvKey(p.ID); hasKey {
			present := strings.TrimSpace(os.Getenv(envVar)) != ""
			row.AuthConfigured = present
			if present {
				row.Ready = true
				row.AuthSource = "$" + envVar
			} else {
				row.AuthSource = "$" + envVar + " (not set)"
				row.Warning = "API key env var not set"
			}
		} else {
			// Provider has no canonical Yaver-tracked env var —
			// trust the opencode config (BYOK fields like baseUrl
			// imply the user wired it themselves).
			row.AuthConfigured = true
			row.Ready = true
			row.AuthSource = "opencode.json"
		}
		details := []string{}
		if strings.TrimSpace(p.BaseURL) != "" {
			details = append(details, p.BaseURL)
		}
		if len(p.Models) > 0 {
			details = append(details, fmt.Sprintf("%d model%s", len(p.Models), pluralS(len(p.Models))))
		}
		row.Detail = strings.Join(details, " · ")
		if row.Detail == "" {
			row.Detail = row.AuthSource
		}
		out = append(out, row)
	}
	return out
}

func opencodeProviderEnvKey(providerID string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "anthropic":
		return "ANTHROPIC_API_KEY", true
	case "openai":
		return "OPENAI_API_KEY", true
	case "glm", "z-ai", "zhipu":
		return "GLM_API_KEY", true
	case "zai":
		return "ZAI_API_KEY", true
	case "openrouter":
		return "OPENROUTER_API_KEY", true
	case "groq":
		return "GROQ_API_KEY", true
	case "mistral":
		return "MISTRAL_API_KEY", true
	case "deepseek":
		return "DEEPSEEK_API_KEY", true
	case "google", "gemini":
		return "GEMINI_API_KEY", true
	case "ollama":
		// Ollama is daemon-based, no API key. Caller treats absence
		// of an env-key mapping as "BYOK via opencode.json".
		return "", false
	}
	return "", false
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func fetchRunnerAuthStatusRowsRemote(target string) ([]runnerAuthStatusRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := proxyToDeviceJSON(ctx, "runner-auth-status", target, http.MethodGet, "/agent/runners", nil)
	if err != nil {
		return nil, err
	}
	raw, _ := out["runners"].([]any)
	rows := make([]runnerAuthStatusRow, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if id != "claude" && id != "codex" && id != "opencode" {
			continue
		}
		row := runnerAuthStatusRow{
			ID:             id,
			Name:           stringValue(m["name"]),
			Installed:      boolValue(m["installed"]),
			Ready:          boolValue(m["ready"]),
			AuthConfigured: boolValue(m["authConfigured"]),
			AuthSource:     stringValue(m["authSource"]),
			Path:           stringValue(m["path"]),
		}
		if !row.Installed {
			row.Detail = "Not installed"
		} else if !row.Ready && stringValue(m["error"]) != "" {
			row.Error = stringValue(m["error"])
			row.Detail = row.Error
		} else if row.AuthConfigured && row.AuthSource != "" {
			row.Detail = "authenticated via " + row.AuthSource
		} else if stringValue(m["warning"]) != "" {
			row.Warning = stringValue(m["warning"])
			row.Detail = row.Warning
		} else {
			row.Detail = "installed"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func ensureRunnerAuthLocalConfig() (*Config, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	if strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		fmt.Println("Yaver is not signed in locally. Starting `yaver auth` first...")
		runAuth(nil)
		cfg, err = LoadConfig()
		if err != nil {
			return nil, err
		}
	}
	if cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("local Yaver auth did not complete")
	}
	return cfg, nil
}

func resolveRunnerAuthTarget(targetHint string) (*Config, *DeviceInfo, error) {
	cfg, err := ensureRunnerAuthLocalConfig()
	if err != nil {
		return nil, nil, err
	}
	devices, err := listDevicesEnsuringAuth(cfg)
	if err != nil {
		return nil, nil, err
	}
	target, err := resolveDevice(targetHint, devices)
	if err != nil {
		return nil, nil, err
	}
	return cfg, target, nil
}

func summarizeRunnerAuthRow(row runnerAuthStatusRow) string {
	if !row.Installed {
		return "not installed"
	}
	if row.Ready {
		if strings.TrimSpace(row.AuthSource) != "" {
			return "ready via " + strings.TrimSpace(row.AuthSource)
		}
		return "ready"
	}
	if strings.TrimSpace(row.Detail) != "" {
		return strings.TrimSpace(row.Detail)
	}
	if strings.TrimSpace(row.Error) != "" {
		return strings.TrimSpace(row.Error)
	}
	if strings.TrimSpace(row.Warning) != "" {
		return strings.TrimSpace(row.Warning)
	}
	return "installed but not ready"
}

func findRunnerAuthStatusRow(rows []runnerAuthStatusRow, runner string) (runnerAuthStatusRow, bool) {
	runner = normalizeRunnerAuthName(runner)
	for _, row := range rows {
		if normalizeRunnerAuthName(row.ID) == runner {
			return row, true
		}
	}
	return runnerAuthStatusRow{}, false
}

func describeDeviceReauthProbe(probe deviceReauthProbe) string {
	switch probe.State {
	case "healthy":
		return "signed in"
	case "ready-to-connect":
		return "signed in (agent heartbeat catching up)"
	case "bootstrap":
		return "not signed in yet"
	case "yaver-auth-expired":
		return "signed out / token expired"
	case "offline":
		return "offline"
	case "unreachable":
		return "online but unreachable"
	default:
		if strings.TrimSpace(probe.Error) != "" {
			return probe.State + ": " + strings.TrimSpace(probe.Error)
		}
		return probe.State
	}
}

func runRemoteHeadlessYaverAuthOverSSH(targetHint string) error {
	fmt.Printf("Target Yaver auth is missing. Running remote `yaver auth --headless` over `yaver ssh %s`...\n\n", targetHint)
	yaverPath := findYaverBinary()
	cmd := osexec.Command(yaverPath, "ssh", targetHint, "--", "yaver", "auth", "--headless")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runRemoteHeadlessRunnerAuthOverSSH is the SSH-based fallback for
// `yaver primary codex` / `yaver primary claude` when the remote agent's
// HTTP /runner-auth/browser/start path won't work — usually because the
// agent's PATH (under systemd / launchd) doesn't include where the user's
// `claude` or `codex` actually lives.
//
// We `yaver ssh` into the target, ask bash to load the user's login
// environment (`bash -lc`), and run the runner CLI directly. stdio is
// piped straight through to the local terminal — the runner itself
// prints the OAuth URL or the device-code, waits for the user, and (for
// claude) prompts for the pasted-back token. No URL/code parsing on our
// side; we are just a transparent pipe over SSH.
//
// This intentionally bypasses the agent: it's the recovery path when the
// agent can't see the binary AT ALL. The HTTP-based flow is still the
// preferred path when the resolver succeeds (mobile uses it, desktop
// dashboards use it, and it works without a TTY).
func runRemoteHeadlessRunnerAuthOverSSH(targetHint, runner string) error {
	runner = normalizeRunnerAuthName(runner)
	var remoteCmd string
	switch runner {
	case "claude":
		remoteCmd = "claude auth login --console"
	case "codex":
		remoteCmd = "codex login --device-auth"
	default:
		return fmt.Errorf("ssh-based runner auth: unsupported runner %q (want claude or codex)", runner)
	}

	fmt.Printf("Falling back to SSH: spawning `%s` on %s under your login shell.\n", remoteCmd, targetHint)
	fmt.Println("(The runner will print its OAuth URL/code below — open it in any browser to finish sign-in.)")
	fmt.Println()

	yaverPath := findYaverBinary()
	// `bash -lc` loads .bash_profile / .profile / .bashrc so PATH picks up
	// ~/.npm-global/bin, ~/.bun/bin, /opt/homebrew/bin, /usr/local/bin, etc.
	// — wherever the user actually installed claude/codex. CI=1+NO_COLOR=1
	// keeps the runner from emitting cursor controls that confuse a
	// non-interactive ssh stream.
	wrapped := "CI=1 NO_COLOR=1 TERM=dumb " + remoteCmd
	cmd := osexec.Command(yaverPath, "ssh", targetHint, "--", "bash", "-lc", wrapped)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForRemoteYaverAuth(cfg *Config, target *DeviceInfo, timeout time.Duration) (deviceReauthProbe, error) {
	deadline := time.Now().Add(timeout)
	for {
		probe := probeOwnedDeviceReauth(cfg, target)
		switch probe.State {
		case "healthy", "ready-to-connect":
			return probe, nil
		}
		if time.Now().After(deadline) {
			return probe, fmt.Errorf("remote Yaver auth still not ready: %s", describeDeviceReauthProbe(probe))
		}
		time.Sleep(3 * time.Second)
	}
}

func runRunnerAuthQuickFlow(targetHint, runner string, extra []string) {
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "runner-auth: unexpected extra arguments after %s %s: %s\n", targetHint, runner, strings.Join(extra, " "))
		os.Exit(1)
	}
	cfg, target, err := resolveRunnerAuthTarget(targetHint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Machine: %s (%s)\n", target.Name, target.DeviceID[:8])
	if strings.TrimSpace(target.Alias) != "" {
		fmt.Printf("Alias:   %s\n", target.Alias)
	}

	probe := probeOwnedDeviceReauth(cfg, target)
	fmt.Printf("Yaver:   %s\n", describeDeviceReauthProbe(probe))
	if strings.TrimSpace(probe.Error) != "" {
		fmt.Printf("Detail:  %s\n", strings.TrimSpace(probe.Error))
	}

	switch probe.State {
	case "bootstrap", "yaver-auth-expired":
		if err := runRemoteHeadlessYaverAuthOverSSH(targetHint); err != nil {
			fmt.Fprintf(os.Stderr, "runner-auth: remote yaver auth failed: %v\n", err)
			os.Exit(1)
		}
		probe, err = waitForRemoteYaverAuth(cfg, target, 2*time.Minute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runner-auth: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nYaver:   %s\n", describeDeviceReauthProbe(probe))
	case "offline", "unreachable":
		fmt.Fprintf(os.Stderr, "runner-auth: target machine is %s\n", describeDeviceReauthProbe(probe))
		os.Exit(1)
	}

	rows, err := fetchRunnerAuthStatusRowsRemote(target.DeviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth: fetch remote runner status: %v\n", err)
		os.Exit(1)
	}
	row, ok := findRunnerAuthStatusRow(rows, runner)
	if !ok {
		fmt.Fprintf(os.Stderr, "runner-auth: remote machine did not report %s status\n", runner)
		os.Exit(1)
	}
	fmt.Printf("%s:  %s\n\n", runner, summarizeRunnerAuthRow(row))
	if !row.Installed {
		// Same false-negative fallback as runRunnerQuickFlow — agent's
		// PATH may not include ~/.npm-global/bin so claude/codex appear
		// missing even when the user has them. Try the SSH path before
		// giving up.
		fmt.Fprintf(os.Stderr, "runner-auth: agent reports %s as not installed on %s. Trying SSH fallback under your login shell.\n", runner, target.Name)
		if err := runRemoteHeadlessRunnerAuthOverSSH(targetHint, runner); err != nil {
			fmt.Fprintf(os.Stderr, "runner-auth: SSH fallback failed: %v\n", err)
			os.Exit(1)
		}
		rows, err = fetchRunnerAuthStatusRowsRemote(target.DeviceID)
		if err == nil {
			row, _ = findRunnerAuthStatusRow(rows, runner)
			fmt.Printf("%s:  %s\n", runner, summarizeRunnerAuthRow(row))
		}
		return
	}

	if err := runCodeBrowserAuthFlow(target.DeviceID, runner); err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth: HTTP browser-auth failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "runner-auth: trying SSH fallback under your login shell.")
		if sshErr := runRemoteHeadlessRunnerAuthOverSSH(targetHint, runner); sshErr != nil {
			fmt.Fprintf(os.Stderr, "runner-auth: SSH fallback also failed: %v\n", sshErr)
			os.Exit(1)
		}
	}

	rows, err = fetchRunnerAuthStatusRowsRemote(target.DeviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth: recheck remote runner status: %v\n", err)
		os.Exit(1)
	}
	row, _ = findRunnerAuthStatusRow(rows, runner)
	fmt.Printf("%s:  %s\n", runner, summarizeRunnerAuthRow(row))
}

func runRunnerQuickFlow(targetHint, runner string, extra []string) {
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "runner: unexpected extra arguments after %s %s: %s\n", targetHint, runner, strings.Join(extra, " "))
		os.Exit(1)
	}
	cfg, target, err := resolveRunnerAuthTarget(targetHint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Machine: %s (%s)\n", target.Name, target.DeviceID[:8])
	if strings.TrimSpace(target.Alias) != "" {
		fmt.Printf("Alias:   %s\n", target.Alias)
	}

	probe := probeOwnedDeviceReauth(cfg, target)
	fmt.Printf("Yaver:   %s\n", describeDeviceReauthProbe(probe))
	if strings.TrimSpace(probe.Error) != "" {
		fmt.Printf("Detail:  %s\n", strings.TrimSpace(probe.Error))
	}

	switch probe.State {
	case "bootstrap", "yaver-auth-expired":
		if err := runRemoteHeadlessYaverAuthOverSSH(targetHint); err != nil {
			fmt.Fprintf(os.Stderr, "runner: remote yaver auth failed: %v\n", err)
			os.Exit(1)
		}
		probe, err = waitForRemoteYaverAuth(cfg, target, 2*time.Minute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runner: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nYaver:   %s\n", describeDeviceReauthProbe(probe))
	case "offline", "unreachable":
		fmt.Fprintf(os.Stderr, "runner: target machine is %s\n", describeDeviceReauthProbe(probe))
		os.Exit(1)
	}

	rows, err := fetchRunnerAuthStatusRowsRemote(target.DeviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner: fetch remote runner status: %v\n", err)
		os.Exit(1)
	}
	row, ok := findRunnerAuthStatusRow(rows, runner)
	if !ok {
		fmt.Fprintf(os.Stderr, "runner: remote machine did not report %s status\n", runner)
		os.Exit(1)
	}
	fmt.Printf("%s:  %s\n\n", runner, summarizeRunnerAuthRow(row))
	if !row.Installed {
		// Agent says "not installed" but it's commonly a false negative: the
		// systemd/launchd PATH the agent inherits doesn't include
		// ~/.npm-global/bin etc. where the user actually has claude/codex.
		// Fall back to running the runner CLI over SSH under the user's
		// login shell — that picks up the right PATH.
		fmt.Fprintf(os.Stderr, "runner: agent reports %s as not installed on %s. Trying SSH fallback under your login shell.\n", runner, target.Name)
		if err := runRemoteHeadlessRunnerAuthOverSSH(targetHint, runner); err != nil {
			fmt.Fprintf(os.Stderr, "runner: SSH fallback failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "runner: install the CLI on %s (e.g. `npm i -g @anthropic-ai/claude-code` / `@openai/codex`) and try again.\n", target.Name)
			os.Exit(1)
		}
		// SSH path doesn't go through the agent so the row won't update;
		// re-fetch and trust whatever the next status snapshot says.
		rows, err = fetchRunnerAuthStatusRowsRemote(target.DeviceID)
		if err == nil {
			row, _ = findRunnerAuthStatusRow(rows, runner)
			fmt.Printf("%s:  %s\n", runner, summarizeRunnerAuthRow(row))
		}
	} else if !row.AuthConfigured {
		if err := runCodeBrowserAuthFlow(target.DeviceID, runner); err != nil {
			fmt.Fprintf(os.Stderr, "runner: HTTP browser-auth failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "runner: trying SSH fallback under your login shell.")
			if sshErr := runRemoteHeadlessRunnerAuthOverSSH(targetHint, runner); sshErr != nil {
				fmt.Fprintf(os.Stderr, "runner: SSH fallback also failed: %v\n", sshErr)
				os.Exit(1)
			}
		}
		rows, err = fetchRunnerAuthStatusRowsRemote(target.DeviceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runner: recheck remote runner status: %v\n", err)
			os.Exit(1)
		}
		row, _ = findRunnerAuthStatusRow(rows, runner)
		fmt.Printf("%s:  %s\n", runner, summarizeRunnerAuthRow(row))
	}
	if !row.AuthConfigured {
		fmt.Fprintf(os.Stderr, "runner: %s still is not authenticated on %s\n", runner, target.Name)
		os.Exit(1)
	}

	if err := codeSwitchRunner(target.DeviceID, runner); err != nil {
		fmt.Fprintf(os.Stderr, "runner: switch remote runner to %s: %v\n", runner, err)
		os.Exit(1)
	}
	fmt.Printf("Active coding runner on %s: %s\n", target.Name, runner)
}

func runRunnerAuthStatus(args []string) {
	fs := flag.NewFlagSet("runner-auth status", flag.ExitOnError)
	target := fs.String("target", "", "remote device ID to inspect")
	fs.Parse(args)

	var (
		rows []runnerAuthStatusRow
		err  error
	)
	if strings.TrimSpace(*target) != "" {
		rows, err = fetchRunnerAuthStatusRowsRemote(strings.TrimSpace(*target))
	} else {
		rows, err = collectRunnerAuthStatusRows()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth status: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("No runner auth status available.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUNNER\tINSTALLED\tREADY\tAUTH\tDETAIL")
	for _, row := range rows {
		auth := "no"
		if row.AuthConfigured {
			auth = "yes"
		}
		ready := "no"
		if row.Ready {
			ready = "yes"
		}
		installed := "no"
		if row.Installed {
			installed = "yes"
		}
		detail := row.Detail
		if detail == "" {
			if row.Path != "" {
				detail = row.Path
			} else {
				detail = "-"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.ID, installed, ready, auth, detail)
	}
	w.Flush()
}

func runRunnerAuthSet(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver runner-auth set <runner> [flags]")
		os.Exit(1)
	}
	runner := normalizeRunnerAuthName(args[0])
	fs := flag.NewFlagSet("runner-auth set", flag.ExitOnError)
	target := fs.String("target", "", "remote device ID to update")
	openAIKey := fs.String("openai-api-key", "", "OpenAI API key")
	anthropicKey := fs.String("anthropic-api-key", "", "Anthropic API key")
	anthropicAuthToken := fs.String("anthropic-auth-token", "", "Anthropic auth token")
	claudeOAuthToken := fs.String("claude-code-oauth-token", "", "Claude Code OAuth token")
	glmKey := fs.String("glm-api-key", "", "GLM API key")
	zaiKey := fs.String("zai-api-key", "", "ZAI API key")
	notes := fs.String("notes", "", "optional vault note")
	fs.Parse(args[1:])

	entries, err := buildRunnerAuthEntries(runner, *openAIKey, *anthropicKey, *anthropicAuthToken, *claudeOAuthToken, *glmKey, *zaiKey, *notes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth set: %v\n", err)
		os.Exit(1)
	}

	if strings.TrimSpace(*target) != "" {
		err = setRunnerAuthEntriesRemote(strings.TrimSpace(*target), entries)
	} else {
		err = setRunnerAuthEntriesLocal(entries)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner-auth set: %v\n", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if strings.TrimSpace(*target) != "" {
		fmt.Printf("Saved %s for %s on %s.\n", strings.Join(names, ", "), runner, strings.TrimSpace(*target))
		return
	}
	fmt.Printf("Saved %s for %s.\n", strings.Join(names, ", "), runner)
}

func mcpRunnerAuthStatus(deviceID string) interface{} {
	if strings.TrimSpace(deviceID) != "" {
		rows, err := fetchRunnerAuthStatusRowsRemote(strings.TrimSpace(deviceID))
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"runners": rows, "device_id": strings.TrimSpace(deviceID)}
	}
	rows, err := collectRunnerAuthStatusRows()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"runners": rows}
}

func mcpRunnerAuthSet(deviceID, runner, openAIKey, anthropicKey, anthropicAuthToken, claudeOAuthToken, glmKey, zaiKey, notes string) interface{} {
	entries, err := buildRunnerAuthEntries(runner, openAIKey, anthropicKey, anthropicAuthToken, claudeOAuthToken, glmKey, zaiKey, notes)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	if strings.TrimSpace(deviceID) != "" {
		if err := setRunnerAuthEntriesRemote(strings.TrimSpace(deviceID), entries); err != nil {
			return map[string]any{"error": err.Error()}
		}
	} else {
		if err := setRunnerAuthEntriesLocal(entries); err != nil {
			return map[string]any{"error": err.Error()}
		}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return map[string]any{
		"ok":        true,
		"runner":    normalizeRunnerAuthName(runner),
		"device_id": strings.TrimSpace(deviceID),
		"vaultKeys": names,
	}
}
