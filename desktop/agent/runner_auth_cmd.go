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
	"text/tabwriter"
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
  yaver runner-auth set claude [--target <deviceId>] [--anthropic-api-key <key> | --anthropic-auth-token <token> | --claude-code-oauth-token <token>]
  yaver runner-auth set codex [--target <deviceId>] --openai-api-key <key>
  yaver runner-auth set opencode [--target <deviceId>] [--openai-api-key <key>] [--anthropic-api-key <key>] [--glm-api-key <key>] [--zai-api-key <key>]
  yaver runner-auth setup claude [--target <deviceId>] [--anthropic-api-key <key>] [--anthropic-auth-token <token>] [--claude-code-oauth-token <token>]
  yaver runner-auth setup codex [--target <deviceId>] [--openai-api-key <key>] [--no-install] [--no-login] [--no-mcp]

Examples:
  yaver runner-auth set codex --openai-api-key $OPENAI_API_KEY
  yaver runner-auth setup codex --target cloud-12345678 --openai-api-key $OPENAI_API_KEY
  yaver runner-auth set opencode --glm-api-key $GLM_API_KEY --target cloud-12345678
  yaver runner-auth status --target cloud-12345678

Notes:
  - Values are stored in the target machine's Yaver vault.
  - setup also installs the runner when missing and wires Yaver into the runner's MCP config when supported.
  - --target uses the existing Yaver remote-agent channel; it does not require SSH.
`)
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
	ID             string
	Name           string
	Installed      bool
	Ready          bool
	AuthConfigured bool
	AuthSource     string
	Warning        string
	Error          string
	Path           string
	Detail         string
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
	rows := make([]runnerAuthStatusRow, 0, len(runners))
	for _, runner := range runners {
		path, err := osexec.LookPath(runner.Cmd)
		row := runnerAuthStatusRow{
			ID:        runner.ID,
			Name:      runner.Name,
			Installed: err == nil,
			Path:      path,
		}
		if err != nil {
			row.Warning = "Not installed"
			row.Detail = "Not installed"
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
		version := ""
		if out, verr := osexec.Command(runner.Cmd, "--version").CombinedOutput(); verr == nil {
			version = strings.TrimSpace(strings.Split(string(out), "\n")[0])
			if len(version) > 60 {
				version = version[:60]
			}
		}
		_, row.Detail = runnerDoctorDetail(cfg, wd, path, version)
		rows = append(rows, row)
	}
	return rows, nil
}

func fetchRunnerAuthStatusRowsRemote(target string) ([]runnerAuthStatusRow, error) {
	out, err := proxyToDeviceJSON(context.Background(), "runner-auth-status", target, http.MethodGet, "/agent/runners", nil)
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
